package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"example.com/sandbox-demo/internal/model"
	"example.com/sandbox-demo/internal/network"
	runcoptions "github.com/containerd/containerd/api/types/runc/options"
	containerd "github.com/containerd/containerd/v2/client"
	seccomp "github.com/containerd/containerd/v2/contrib/seccomp"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func (s *Service) createContainer(ctx context.Context, sandboxID, id, name, image string, args, env []string, workDir string, lim model.ResourceLimits, resource model.ResourceSpec, netnsPath string) (model.ContainerState, error) {
	s.dbg("container create call id=%s image=%s", id, image)
	ref := normalizeImage(image)

	baseSpecOpts := []oci.SpecOpts{
		seccomp.WithDefaultProfile(),
		oci.WithHostHostsFile,
		s.withSandboxResolvConf(),
		oci.WithMaskedPaths([]string{"/proc/acpi", "/proc/kcore", "/proc/keys", "/proc/timer_list", "/sys/firmware"}),
		oci.WithReadonlyPaths([]string{"/proc/bus", "/proc/fs", "/proc/irq", "/proc/sys", "/proc/sysrq-trigger"}),
		oci.WithMemoryLimit(uint64(lim.MemoryBytes)),
		oci.WithPidsLimit(lim.PidsLimit),
		oci.WithCPUCFS(lim.CPUQuota, lim.CPUPeriod),
	}

	if len(args) > 0 {
		baseSpecOpts = append(baseSpecOpts, oci.WithProcessArgs(args...))
	}

	if len(env) > 0 {
		baseSpecOpts = append(baseSpecOpts, oci.WithEnv(env))
	}

	if workDir != "" {
		baseSpecOpts = append(baseSpecOpts, oci.WithProcessCwd(workDir))
	}

	if netnsPath != "" {
		baseSpecOpts = append(baseSpecOpts, oci.WithLinuxNamespace(specs.LinuxNamespace{Type: specs.NetworkNamespace, Path: netnsPath}))
	}

	const snapshotter = "overlayfs"
	const runtimeName = "io.containerd.runc.v2"
	runtimeOpts := &runcoptions.Options{BinaryName: s.runtimeBinary}

	img, err := s.client.GetImage(ctx, ref)
	if err != nil {
		img, err = s.client.Pull(ctx, ref, containerd.WithPullSnapshotter(snapshotter))
		if err != nil {
			return model.ContainerState{}, fmt.Errorf("pull image %q: %w", ref, err)
		}
	}

	if err := img.Unpack(ctx, snapshotter); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return model.ContainerState{}, fmt.Errorf("unpack image %q with snapshotter %q: %w", ref, snapshotter, err)
	}

	specOpts := append([]oci.SpecOpts{oci.WithImageConfig(img)}, baseSpecOpts...)
	snap := id + "-snapshot"

	for attempt := 1; attempt <= 4; attempt++ {
		s.dbg("container create attempt id=%s snapshotter=%s runtime=%s attempt=%d", id, snapshotter, runtimeName, attempt)
		ctr, err := s.client.NewContainer(ctx, id,
			containerd.WithImage(img),
			containerd.WithSnapshotter(snapshotter),
			containerd.WithNewSnapshot(snap, img),
			containerd.WithRuntime(runtimeName, runtimeOpts),
			containerd.WithNewSpec(specOpts...),
		)

		if err != nil {
			return model.ContainerState{}, fmt.Errorf("new container %q: %w", id, err)
		}

		logPath, err := s.containerLogPath(sandboxID, name)
		if err != nil {
			_ = ctr.Delete(ctx)
			return model.ContainerState{}, fmt.Errorf("invalid log path: %w", err)
		}

		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			_ = ctr.Delete(ctx)
			return model.ContainerState{}, fmt.Errorf("create log directory: %w", err)
		}

		task, err := ctr.NewTask(ctx, cio.LogFile(logPath))
		if err != nil {
			_ = ctr.Delete(ctx)
			if isTransientRuntimeErr(err) {
				time.Sleep(time.Duration(120*attempt) * time.Millisecond)
				continue
			}

			return model.ContainerState{}, fmt.Errorf("new task %q: %w", id, err)
		}

		if err := task.Start(ctx); err != nil {
			_, _ = task.Delete(ctx, containerd.WithProcessKill)
			_ = ctr.Delete(ctx)
			if isTransientRuntimeErr(err) {
				time.Sleep(time.Duration(120*attempt) * time.Millisecond)
				continue
			}

			return model.ContainerState{}, fmt.Errorf("start task %q: %w", id, err)
		}

		s.dbg("container create success id=%s pid=%d", id, task.Pid())
		return model.ContainerState{ID: id, Name: name, Phase: ContainerPhaseRunning, Image: ref, Args: args, Env: env, Resource: resource, SnapshotKey: snap, TaskPID: task.Pid(), Runtime: s.runtimeBinary, TaskStatus: "running"}, nil
	}

	return model.ContainerState{}, fmt.Errorf("start task %q: exceeded retry attempts", id)
}

func isTransientRuntimeErr(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "cannot start a container that has stopped") || strings.Contains(msg, "failed to dial") || strings.Contains(msg, "connection refused")
}

func (s *Service) stopAndDeleteContainer(ctx context.Context, id string) error {
	ctr, err := s.client.LoadContainer(ctx, id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil
		}

		return err
	}

	if task, err := ctr.Task(ctx, nil); err == nil {
		_, _ = task.Delete(ctx, containerd.WithProcessKill)
	}

	if err := ctr.Delete(ctx, containerd.WithSnapshotCleanup); err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		if strings.Contains(strings.ToLower(err.Error()), "running task") || strings.Contains(strings.ToLower(err.Error()), "failed precondition") {
			if task, terr := ctr.Task(ctx, nil); terr == nil {
				_ = task.Kill(ctx, syscall.SIGKILL)
				_, _ = task.Delete(ctx, containerd.WithProcessKill)
			}

			if derr := ctr.Delete(ctx, containerd.WithSnapshotCleanup); derr != nil && !strings.Contains(strings.ToLower(derr.Error()), "not found") {
				return derr
			}

			return nil
		}

		if derr := ctr.Delete(ctx); derr == nil || strings.Contains(strings.ToLower(derr.Error()), "not found") {
			return nil
		}

		return err
	}
	return nil
}

func (s *Service) deleteSandboxFromState(ctx context.Context, sbx *model.Sandbox) error {
	err := s.deleteSandboxRuntimeArtifacts(ctx, sbx)
	_ = s.store.Delete(sbx.ID)
	return err
}

func (s *Service) deleteSandboxRuntimeArtifacts(ctx context.Context, sbx *model.Sandbox) error {
	s.dbg("cleanup runtime artifacts sandbox=%s", sbx.ID)
	var errs []error
	for _, name := range sortedContainerNames(sbx.Containers) {
		if e := s.stopAndDeleteContainer(ctx, sbx.Containers[name].ID); e != nil {
			errs = append(errs, e)
		}
	}

	s.cleanupHostPortPublish(sbx)
	s.cleanupSandboxNetworkPolicy(sbx)
	s.cleanupSandboxCNI(ctx, sbx.ID)
	s.dbg("cleanup runtime artifacts done sandbox=%s err_count=%d", sbx.ID, len(errs))
	return errors.Join(errs...)
}

func (s *Service) cleanupCNICache(sandboxID string) error {
	if sandboxID == "" {
		return nil
	}

	_ = os.RemoveAll("/var/lib/cni/" + sandboxID)
	return nil
}

func (s *Service) resolveSandboxIP(ctx context.Context, sandboxID string) (string, error) {
	deadline := time.Now().Add(12 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("resolve sandbox ip canceled: %w", ctx.Err())
		default:
		}

		ip, err := network.LookupSandboxIPv4FromResultCache(sandboxID)
		if err == nil {
			return ip, nil
		}

		lastErr = err
		time.Sleep(150 * time.Millisecond)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("timeout resolving sandbox ip")
	}

	return "", lastErr
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
	inCreateGrace := sbx.Phase == SandboxPhaseCreating && time.Since(sbx.CreatedAt) < DefaultReadyTimeout
	for name, st := range sbx.Containers {
		next := s.fillContainerRuntimeState(ctx, st)
		if inCreateGrace && next.TaskStatus == "not_found" {
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
	ctr, err := s.client.LoadContainer(ctx, st.ID)
	if err != nil {
		st.TaskStatus = "not_found"
		st.Phase = ContainerPhaseError
		st.Error = "container not found"
		return st
	}

	task, err := ctr.Task(ctx, nil)
	if err != nil {
		st.TaskStatus = "stopped"
		st.Phase = ContainerPhaseStopped
		st.Error = ""
		return st
	}

	status, err := task.Status(ctx)
	if err != nil {
		st.TaskStatus = "unknown"
		st.Phase = ContainerPhaseError
		st.Error = "failed to read task status"
		return st
	}

	st.TaskStatus = string(status.Status)
	st.Error = ""
	st.Phase = taskStatusToContainerPhase(st.TaskStatus)
	st.ExitStatus = status.ExitStatus
	if !status.ExitTime.IsZero() {
		st.ExitTime = status.ExitTime.UTC().Format(time.RFC3339)
	}

	st.TaskPID = task.Pid()
	return st
}

func taskStatusToContainerPhase(taskStatus string) string {
	switch strings.ToLower(taskStatus) {
	case "running":
		return ContainerPhaseRunning
	case "created":
		return ContainerPhaseCreating
	case "stopped", "paused", "pausing":
		return ContainerPhaseStopped
	case "":
		return ContainerPhaseUnknown
	default:
		return ContainerPhaseError
	}
}

func sortedContainerNames(m map[string]model.ContainerState) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}

	sort.Strings(names)
	return names
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

func (s *Service) withSandboxResolvConf() oci.SpecOpts {
	src := "/etc/resolv.conf"
	// On systemd-resolved hosts, /etc/resolv.conf often points to 127.0.0.53,
	// which is unreachable from isolated netns. Use the uplink resolver list.
	if _, err := os.Stat("/run/systemd/resolve/resolv.conf"); err == nil {
		src = "/run/systemd/resolve/resolv.conf"
	}

	return oci.WithMounts([]specs.Mount{{
		Destination: "/etc/resolv.conf",
		Type:        "bind",
		Source:      src,
		Options:     []string{"rbind", "ro"},
	}})
}
