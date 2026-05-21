package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sandboxd-o/sandboxd-let/model"
	"sandboxd-o/sandboxd-let/network"

	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

var safeCgroupNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func (s *Service) createPodSandboxCRI(ctx context.Context, sbx *model.Sandbox, reqContainers []model.CreateContainerRequest) (string, string, *runtimeapi.PodSandboxConfig, error) {
	if podID, ok := s.findManagedPodSandboxID(ctx, sbx.ID); ok {
		s.cri.stopAndRemovePodSandbox(ctx, podID)
	}

	podRes, err := podSandboxResourcesFromRequests(reqContainers)
	if err != nil {
		return "", "", nil, err
	}

	cgroupParent, err := ensureSandboxParentCgroup(s.cfg.CgroupParent, sbx.ID, podRes)
	if err != nil {
		return "", "", nil, err
	}

	cfg := &runtimeapi.PodSandboxConfig{
		Metadata: &runtimeapi.PodSandboxMetadata{
			Name:      sbx.ID,
			Namespace: "default",
			Uid:       sbx.ID,
			Attempt:   1,
		},
		Hostname:     sbx.ID,
		LogDirectory: fmt.Sprintf("%s/%s/logs", s.cfg.StateBaseDir, sbx.ID),
		Labels: map[string]string{
			"sandbox-id": sbx.ID,
		},
		Linux: &runtimeapi.LinuxPodSandboxConfig{
			CgroupParent:    cgroupParent,
			SecurityContext: &runtimeapi.LinuxSandboxSecurityContext{},
			Resources:       podRes,
		},
	}

	cfg.DnsConfig = &runtimeapi.DNSConfig{
		Servers: sandboxDNSServers(),
	}

	podID, err := s.cri.runPodSandbox(ctx, cfg, s.runtimeBinary)
	if err != nil {
		return "", "", nil, err
	}

	if err := enforceCgroupV2Limits(podID, podRes); err != nil {
		return "", "", nil, err
	}

	status, err := s.cri.podSandboxStatus(ctx, podID)
	if err != nil {
		return "", "", nil, err
	}

	return podID, status.GetNetwork().GetIp(), cfg, nil
}

func ensureSandboxParentCgroup(baseParent, sandboxID string, lim *runtimeapi.LinuxContainerResources) (string, error) {
	if lim == nil {
		return "", fmt.Errorf("nil linux container resources for sandbox %s", sandboxID)
	}

	if err := validateSandboxIDCgroupSafe(sandboxID); err != nil {
		return "", err
	}

	parent := strings.TrimSpace(baseParent)
	if parent == "" {
		parent = "/k8s.io"
	}

	if !strings.HasPrefix(parent, "/") {
		parent = "/" + parent
	}

	parent = filepath.Clean(parent)
	if parent == "/" {
		return "", fmt.Errorf("invalid cgroup parent %q: root is not allowed", baseParent)
	}

	parentFsPath := filepath.Clean(filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(parent, "/")))
	if !strings.HasPrefix(parentFsPath, "/sys/fs/cgroup/") {
		return "", fmt.Errorf("invalid cgroup parent %q: escapes /sys/fs/cgroup", baseParent)
	}

	if err := os.MkdirAll(parentFsPath, 0o755); err != nil {
		return "", fmt.Errorf("create cgroup parent %s: %w", parentFsPath, err)
	}

	if err := ensureCgroupSubtreeControllers(parentFsPath, []string{"memory", "cpu", "pids"}); err != nil {
		return "", err
	}

	leaf := "sbx-" + sandboxID
	cgroupPath := filepath.Clean(parent + "/" + leaf)
	fsPath := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(cgroupPath, "/"))
	if err := os.MkdirAll(fsPath, 0o755); err != nil {
		return "", fmt.Errorf("create cgroup parent %s: %w", fsPath, err)
	}

	if lim.MemoryLimitInBytes > 0 {
		if err := writeCgroupValue(filepath.Join(fsPath, "memory.max"), strconv.FormatInt(lim.MemoryLimitInBytes, 10)); err != nil {
			return "", err
		}
	}

	if lim.CpuPeriod > 0 && lim.CpuQuota > 0 {
		if err := writeCgroupValue(filepath.Join(fsPath, "cpu.max"), fmt.Sprintf("%d %d", lim.CpuQuota, lim.CpuPeriod)); err != nil {
			return "", err
		}
	}

	if pidsMax, ok := lim.Unified["pids.max"]; ok && strings.TrimSpace(pidsMax) != "" {
		if err := writeCgroupValue(filepath.Join(fsPath, "pids.max"), pidsMax); err != nil {
			return "", err
		}
	}

	return cgroupPath, nil
}

func ensureCgroupSubtreeControllers(cgroupPath string, controllers []string) error {
	availableRaw, err := os.ReadFile(filepath.Join(cgroupPath, "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("read %s/cgroup.controllers: %w", cgroupPath, err)
	}

	enabledRaw, err := os.ReadFile(filepath.Join(cgroupPath, "cgroup.subtree_control"))
	if err != nil {
		return fmt.Errorf("read %s/cgroup.subtree_control: %w", cgroupPath, err)
	}

	available := map[string]struct{}{}
	for _, c := range strings.Fields(string(availableRaw)) {
		available[c] = struct{}{}
	}

	enabled := map[string]struct{}{}
	for _, c := range strings.Fields(string(enabledRaw)) {
		enabled[c] = struct{}{}
	}

	for _, c := range controllers {
		if _, ok := available[c]; !ok {
			continue
		}

		if _, ok := enabled[c]; ok {
			continue
		}

		if err := writeCgroupValue(filepath.Join(cgroupPath, "cgroup.subtree_control"), "+"+c); err != nil {
			return fmt.Errorf("enable cgroup controller %q under %s: %w", c, cgroupPath, err)
		}
	}

	return nil
}

func podSandboxResourcesFromRequests(containers []model.CreateContainerRequest) (*runtimeapi.LinuxContainerResources, error) {
	total := parsedResource{}
	for _, c := range containers {
		r, err := parseContainerResource(c.Resource)
		if err != nil {
			return nil, fmt.Errorf("container %s: %w", c.Name, err)
		}

		total.CPUMilli += r.CPUMilli
		total.MemoryBytes += r.MemoryBytes
	}

	lim := parsedResourceToLimits(total)
	return &runtimeapi.LinuxContainerResources{
		MemoryLimitInBytes: lim.MemoryBytes,
		CpuPeriod:          int64(lim.CPUPeriod),
		CpuQuota:           lim.CPUQuota,
		Unified:            map[string]string{"pids.max": fmt.Sprintf("%d", lim.PidsLimit)},
	}, nil
}

func enforceCgroupV2Limits(id string, lim *runtimeapi.LinuxContainerResources) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}

	if lim == nil {
		return nil
	}

	base, err := findCgroupV2PathByID(id)
	if err != nil {
		return err
	}
	if base == "" {
		// Best effort only when cgroup path does not exist yet.
		return nil
	}

	if lim.MemoryLimitInBytes > 0 {
		if err := writeCgroupValue(filepath.Join(base, "memory.max"), strconv.FormatInt(lim.MemoryLimitInBytes, 10)); err != nil {
			return err
		}
	}

	if lim.MemorySwapLimitInBytes > 0 {
		if err := writeCgroupValue(filepath.Join(base, "memory.swap.max"), strconv.FormatInt(lim.MemorySwapLimitInBytes, 10)); err != nil {
			return err
		}
	}

	if lim.CpuPeriod > 0 && lim.CpuQuota > 0 {
		if err := writeCgroupValue(filepath.Join(base, "cpu.max"), fmt.Sprintf("%d %d", lim.CpuQuota, lim.CpuPeriod)); err != nil {
			return err
		}
	}

	if pidsMax, ok := lim.Unified["pids.max"]; ok && strings.TrimSpace(pidsMax) != "" {
		if err := writeCgroupValue(filepath.Join(base, "pids.max"), pidsMax); err != nil {
			return err
		}
	}

	return nil
}

func writeCgroupValue(path, value string) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return nil
	}

	var lastErr error
	for i := 0; i < 10; i++ {
		if err := os.WriteFile(path, []byte(v), 0o644); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}

	return fmt.Errorf("write %s=%q failed: %w", path, v, lastErr)
}

func cleanupSandboxParentCgroup(baseParent, sandboxID string) {
	if err := validateSandboxIDCgroupSafe(sandboxID); err != nil {
		return
	}

	parent := strings.TrimSpace(baseParent)
	if parent == "" {
		parent = "/k8s.io"
	}

	if !strings.HasPrefix(parent, "/") {
		parent = "/" + parent
	}

	parent = strings.TrimSuffix(parent, "/")
	cgroupPath := filepath.Clean(parent + "/sbx-" + sandboxID)
	_ = os.Remove(filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(cgroupPath, "/")))
}

func validateSandboxIDCgroupSafe(sandboxID string) error {
	if strings.TrimSpace(sandboxID) == "" {
		return fmt.Errorf("sandbox id is empty")
	}

	if strings.Contains(sandboxID, "/") || strings.Contains(sandboxID, "..") {
		return fmt.Errorf("sandbox id %q is not a safe cgroup name", sandboxID)
	}

	if !safeCgroupNameRe.MatchString(sandboxID) {
		return fmt.Errorf("sandbox id %q is not a safe cgroup name", sandboxID)
	}

	return nil
}

func findCgroupV2PathByID(id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", nil
	}

	direct := filepath.Join("/sys/fs/cgroup/k8s.io", id)
	if _, err := os.Stat(direct); err == nil {
		return direct, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat cgroup path %s: %w", direct, err)
	}

	matches, err := filepath.Glob(filepath.Join("/sys/fs/cgroup/k8s.io", "sbx-*", id))
	if err != nil {
		return "", fmt.Errorf("glob cgroup path for %s: %w", id, err)
	}

	for _, p := range matches {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat cgroup path %s: %w", p, err)
		}
	}

	return "", nil
}

func (s *Service) createAndStartCRIContainer(ctx context.Context, sbx *model.Sandbox, podID string, sbxCfg *runtimeapi.PodSandboxConfig, c model.CreateContainerRequest, lim model.ResourceLimits) (model.ContainerState, error) {
	envs := make([]*runtimeapi.KeyValue, 0, len(c.Env))
	for _, kv := range c.Env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}

		envs = append(envs, &runtimeapi.KeyValue{Key: parts[0], Value: parts[1]})
	}

	ctrCfg := &runtimeapi.ContainerConfig{
		Metadata: &runtimeapi.ContainerMetadata{Name: c.Name},
		Image:    &runtimeapi.ImageSpec{Image: normalizeImage(c.Image)},
		Command:  c.Args,
		Envs:     envs,
		LogPath:  c.Name + ".log",
		Labels: map[string]string{
			"sandbox-id": sbx.ID,
			"container":  c.Name,
		},
		Linux: &runtimeapi.LinuxContainerConfig{
			Resources: &runtimeapi.LinuxContainerResources{
				MemoryLimitInBytes: lim.MemoryBytes,
				CpuPeriod:          int64(lim.CPUPeriod),
				CpuQuota:           lim.CPUQuota,
				Unified:            map[string]string{"pids.max": fmt.Sprintf("%d", lim.PidsLimit)},
			},
			SecurityContext: &runtimeapi.LinuxContainerSecurityContext{
				Privileged:     false,
				ReadonlyRootfs: false,
				NoNewPrivs:     true,
				Seccomp: &runtimeapi.SecurityProfile{
					ProfileType: runtimeapi.SecurityProfile_RuntimeDefault,
				},
				MaskedPaths: []string{
					"/proc/acpi",
					"/proc/kcore",
					"/proc/keys",
					"/proc/timer_list",
					"/sys/firmware",
				},
				ReadonlyPaths: []string{
					"/proc/bus",
					"/proc/fs",
					"/proc/irq",
					"/proc/sys",
					"/proc/sysrq-trigger",
				},
			},
		},
	}
	if c.WorkDir != "" {
		ctrCfg.WorkingDir = c.WorkDir
	}

	containerID, err := s.cri.createContainer(ctx, podID, ctrCfg, sbxCfg)
	if err != nil {
		return model.ContainerState{}, err
	}

	if err := s.cri.startContainer(ctx, containerID); err != nil {
		return model.ContainerState{}, err
	}

	if err := enforceCgroupV2Limits(containerID, ctrCfg.Linux.Resources); err != nil {
		return model.ContainerState{}, err
	}

	details, err := s.cri.containerStatus(ctx, containerID)
	if err != nil {
		return model.ContainerState{}, err
	}

	return model.ContainerState{
		ID:         details.ID,
		Name:       c.Name,
		Phase:      ContainerPhaseRunning,
		Image:      normalizeImage(c.Image),
		Args:       c.Args,
		Env:        c.Env,
		Resource:   c.Resource,
		TaskPID:    details.PID,
		Runtime:    s.runtimeBinary,
		TaskStatus: "running",
	}, nil
}

func (s *Service) stopAndDeleteCRISandbox(ctx context.Context, podID string) {
	s.cri.stopAndRemovePodSandbox(ctx, podID)
}

func (s *Service) listCRISandboxIDs(ctx context.Context) ([]string, error) {
	items, err := s.cri.listPodSandboxes(ctx)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(items))
	for _, it := range items {
		if it.GetId() == "" {
			continue
		}
		ids = append(ids, it.GetId())
	}

	return ids, nil
}

func (s *Service) findManagedPodSandboxID(ctx context.Context, sandboxID string) (string, bool) {
	items, err := s.cri.listPodSandboxes(ctx)
	if err != nil {
		return "", false
	}

	for _, it := range items {
		if it.GetId() == "" {
			continue
		}

		labels := it.GetLabels()
		if labels["sandbox-id"] == sandboxID {
			return it.GetId(), true
		}

		md := it.GetMetadata()
		if md != nil && md.GetUid() == sandboxID && md.GetName() == sandboxID {
			return it.GetId(), true
		}
	}

	return "", false
}

func (s *Service) deleteSandboxFromState(ctx context.Context, sbx *model.Sandbox) error {
	err := s.deleteSandboxRuntimeArtifacts(ctx, sbx)
	_ = s.store.Delete(sbx.ID)
	return err
}

func (s *Service) deleteSandboxRuntimeArtifacts(ctx context.Context, sbx *model.Sandbox) error {
	s.dbg("cleanup runtime artifacts sandbox=%s", sbx.ID)
	if sbx.PauseID != "" {
		s.stopAndDeleteCRISandbox(ctx, sbx.PauseID)
	}

	s.cleanupHostPortPublish(sbx)
	s.cleanupSandboxNetworkPolicy(sbx)
	s.cleanupSandboxCNI(ctx, sbx.ID)
	cleanupSandboxParentCgroup(s.cfg.CgroupParent, sbx.ID)
	s.dbg("cleanup runtime artifacts done sandbox=%s", sbx.ID)

	return nil
}

func (s *Service) cleanupCNICache(sandboxID string) error {
	if sandboxID == "" {
		return nil
	}

	_ = os.RemoveAll("/var/lib/cni/" + sandboxID)
	return nil
}

func (s *Service) cleanupSandboxCNI(_ context.Context, sandboxID string) {
	_ = s.cleanupCNICache(sandboxID)
}

func (s *Service) applySandboxNetworkPolicy(sbx *model.Sandbox) error {
	s.dbg("apply sandbox firewall sandbox=%s ip=%s", sbx.ID, sbx.IP)
	return network.ApplySandboxRules(s.ipt, sbx.ID, sbx.IP, s.cidr, s.bridgeIF, sbx.Egress, toPublishedPorts(sbx.Ports))
}

func (s *Service) applyHostPortPublish(sbx *model.Sandbox) error {
	s.dbg("apply hostport dnat sandbox=%s ip=%s ports=%v", sbx.ID, sbx.IP, sbx.Ports)
	return network.ApplyHostPortDNAT(s.ipt, sbx.ID, sbx.IP, toHostPortForwards(sbx.Ports))
}

func (s *Service) cleanupHostPortPublish(sbx *model.Sandbox) {
	s.dbg("cleanup hostport dnat sandbox=%s ip=%s ports=%v", sbx.ID, sbx.IP, sbx.Ports)
	if sbx.IP != "" {
		network.DeleteHostPortDNAT(s.ipt, sbx.ID, sbx.IP, toHostPortForwards(sbx.Ports))
	}
	network.DeleteHostPortDNATBySandbox(s.ipt, sbx.ID)
}

func (s *Service) cleanupSandboxNetworkPolicy(sbx *model.Sandbox) {
	if sbx.IP == "" {
		return
	}
	network.DeleteSandboxRules(s.ipt, sbx.ID, sbx.IP, sbx.BridgeName, toPublishedPorts(sbx.Ports))
}

func (s *Service) refreshSandboxRuntimeState(ctx context.Context, sbx *model.Sandbox) {
	if sbx.Phase == SandboxPhaseError && sbx.Error != "" {
		sbx.UpdatedAt = time.Now().UTC()
		return
	}

	if len(sbx.Containers) == 0 {
		sbx.UpdatedAt = time.Now().UTC()
		return
	}

	hasError := false
	sandboxErr := ""
	allRunning := true
	if sbx.PauseID != "" {
		if _, err := s.cri.podSandboxStatus(ctx, sbx.PauseID); err != nil {
			hasError = true
			allRunning = false
			if sandboxErr == "" {
				sandboxErr = "pause container is not running"
			}
		}
	}

	for name, st := range sbx.Containers {
		next := s.fillContainerRuntimeState(ctx, st)
		// Placeholder IDs are initialized before CRI create/start completes.
		// While sandbox phase is still creating, treat not_found on placeholder
		// as transient instead of promoting to error.
		if sbx.Phase == SandboxPhaseCreating && next.TaskStatus == "not_found" && isPlaceholderContainerID(sbx.ID, next.Name, next.ID) {
			next.Phase = ContainerPhaseCreating
			next.Error = ""
			next.TaskStatus = "creating"
		}

		sbx.Containers[name] = next
		if next.Phase == ContainerPhaseError {
			hasError = true
			if sandboxErr == "" && next.Error != "" {
				sandboxErr = next.Error
			}
		}

		if next.Phase != ContainerPhaseRunning {
			allRunning = false
		}
	}

	switch {
	case sbx.Phase == SandboxPhaseDeleting:
	case hasError:
		sbx.Phase = SandboxPhaseError
		sbx.Error = sandboxErr
	case allRunning:
		sbx.Phase = SandboxPhaseRunning
		sbx.Error = ""
	default:
		sbx.Phase = SandboxPhaseCreating
	}

	sbx.UpdatedAt = time.Now().UTC()
}

func (s *Service) fillContainerRuntimeState(ctx context.Context, st model.ContainerState) model.ContainerState {
	details, err := s.cri.containerStatus(ctx, st.ID)
	if err != nil {
		st.TaskStatus = "not_found"
		st.Phase = ContainerPhaseError
		st.Error = "container not found"
		return st
	}

	st.TaskPID = details.PID
	switch details.State {
	case runtimeapi.ContainerState_CONTAINER_RUNNING:
		st.TaskStatus = "running"
		st.Phase = ContainerPhaseRunning
		st.Error = ""
	case runtimeapi.ContainerState_CONTAINER_CREATED:
		st.TaskStatus = "created"
		st.Phase = ContainerPhaseCreating
		st.Error = ""
	case runtimeapi.ContainerState_CONTAINER_EXITED, runtimeapi.ContainerState_CONTAINER_UNKNOWN:
		st.TaskStatus = strings.ToLower(details.State.String())
		st.Phase = ContainerPhaseStopped
		st.Error = ""
	default:
		st.TaskStatus = "unknown"
		st.Phase = ContainerPhaseError
		st.Error = "failed to read container state"
	}

	return st
}

func toPublishedPorts(pm []model.PortMapping) []network.PublishedPort {
	out := make([]network.PublishedPort, 0, len(pm))
	for _, p := range pm {
		out = append(out, network.PublishedPort{ContainerPort: p.ContainerPort, Protocol: normalizeProto(p.Protocol)})
	}

	return out
}

func toHostPortForwards(pm []model.PortMapping) []network.HostPortForward {
	out := make([]network.HostPortForward, 0, len(pm))
	for _, p := range pm {
		out = append(out, network.HostPortForward{HostPort: p.HostPort, ContainerPort: p.ContainerPort, Protocol: normalizeProto(p.Protocol)})
	}

	return out
}

func normalizeImage(image string) string {
	if strings.Contains(image, "/") {
		if strings.Contains(image, ".") || strings.HasPrefix(image, "localhost/") {
			return image
		}

		return "docker.io/" + image
	}

	return "docker.io/library/" + image
}

func isPlaceholderContainerID(sandboxID, containerName, containerID string) bool {
	if sandboxID == "" || containerName == "" || containerID == "" {
		return false
	}

	return containerID == sandboxID+"-"+containerName
}

var nameserverRe = regexp.MustCompile(`^\s*nameserver\s+([0-9a-fA-F\.:]+)\s*$`)

func sandboxDNSServers() []string {
	// Prefer uplink resolvers on systemd-resolved hosts to avoid stub
	// resolver 127.0.0.53 that is unreachable from isolated netns.
	candidates := []string{
		"/run/systemd/resolve/resolv.conf",
		"/etc/resolv.conf",
	}
	for _, p := range candidates {
		servers := parseNameServers(p)
		if len(servers) > 0 {
			return servers
		}
	}

	return nil
}

func parseNameServers(path string) []string {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil
	}
	defer f.Close()

	out := make([]string, 0, 3)
	seen := map[string]struct{}{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m := nameserverRe.FindStringSubmatch(sc.Text())
		if len(m) != 2 {
			continue
		}

		ip := strings.TrimSpace(m[1])
		// Skip local stub resolvers in sandbox netns.
		if strings.HasPrefix(ip, "127.") || ip == "::1" {
			continue
		}

		if _, ok := seen[ip]; ok {
			continue
		}
		seen[ip] = struct{}{}
		out = append(out, ip)
	}

	return out
}
