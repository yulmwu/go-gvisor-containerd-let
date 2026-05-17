package sandbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"sandboxd-o/pkg/configutil"
	"sandboxd-o/sandboxd-let/config"
	"sandboxd-o/sandboxd-let/model"
	"sandboxd-o/sandboxd-let/network"
	"sandboxd-o/sandboxd-let/store"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/coreos/go-iptables/iptables"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type Service struct {
	cri            *criClient
	ipt            *iptables.IPTables
	store          *store.FileStore
	portMu         sync.Mutex
	reservedPorts  map[string]string
	reconcileMu    sync.Mutex
	unhealthySince map[string]time.Time
	unhealthyHits  map[string]int
	cfg            config.Config
	namespace      string
	bridgeIF       string
	cidr           string
	containerdAddr string
	cniConfPath    string
	runtimeBinary  string
	lockDir        string
}

// Note: `cfg.Namespace` / `SANDBOX_NAMESPACE` was removed. This service now runs only on the CRI path, and containerd runtime objects are managed in the CRI containerd namespace (`k8s.io`). The old project-level namespace setting did not control CRI runtime placement and was removed to avoid confusion.
const criContainerdNamespace = "k8s.io"

func New(ctx context.Context, cfg config.Config) (*Service, error) {
	cfg = config.WithConfigDefaults(cfg)

	cri, err := newCRIClient(ctx, cfg.ContainerdAddress)
	if err != nil {
		return nil, err
	}

	ipt, err := iptables.New(iptables.Timeout(5))
	if err != nil {
		_ = cri.Close()
		return nil, err
	}

	if err := network.EnsureBridgeNetfilter(); err != nil {
		_ = cri.Close()
		return nil, err
	}

	st, err := store.NewFileStore(cfg.StateBaseDir)
	if err != nil {
		_ = cri.Close()
		return nil, err
	}

	if err := network.EnsureGlobalChains(ipt, configutil.CSVEnv("SANDBOX_FORWARD_HOOK_CHAINS", []string{"FORWARD", "DOCKER-USER"})); err != nil {
		_ = cri.Close()
		return nil, err
	}

	s := &Service{
		cri:            cri,
		ipt:            ipt,
		store:          st,
		reservedPorts:  map[string]string{},
		unhealthySince: map[string]time.Time{},
		unhealthyHits:  map[string]int{},
		cfg:            cfg,
		namespace:      criContainerdNamespace,
		bridgeIF:       cfg.BridgeInterface,
		cidr:           cfg.SubnetCIDR,
		containerdAddr: cfg.ContainerdAddress,
		cniConfPath:    cfg.CNIConfPath,
		runtimeBinary:  cfg.RuntimeBinary,
		lockDir:        cfg.LockDir,
	}
	network.SetDebugLogger(s.dbg)

	if err := os.MkdirAll(s.lockDir, 0o755); err != nil {
		_ = cri.Close()
		return nil, err
	}

	return s, nil
}

func (s *Service) Close() error { return s.cri.Close() }

func (s *Service) dbg(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...))
}

func (s *Service) CreateSandbox(ctx context.Context, req model.CreateSandboxRequest) (*model.Sandbox, error) {
	return s.createSandbox(ctx, req, false)
}

func (s *Service) CreateSandboxAsync(ctx context.Context, req model.CreateSandboxRequest) (*model.Sandbox, error) {
	sbx, err := s.createSandbox(ctx, req, true)
	if err != nil {
		return nil, err
	}

	return sbx, nil
}

func (s *Service) createSandbox(ctx context.Context, req model.CreateSandboxRequest, async bool) (*model.Sandbox, error) {
	s.dbg("create request sandbox=%s async=%t containers=%d ports=%d egress=%t", req.ID, async, len(req.Containers), len(req.Ports), req.Egress)
	// Async create only needs request/preflight window.
	// Sync create must allow configured provisioning/pull windows.
	opTimeout := 90 * time.Second
	if !async {
		opTimeout = s.cfg.ProvisionTimeout + s.cfg.ImagePullTimeout + s.cfg.ContainerCreateTimeout
	}

	opCtx, opCancel := context.WithTimeout(ctx, opTimeout)
	defer opCancel()
	ctx = opCtx

	unlock, err := s.acquireSandboxLock(req.ID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	if err := req.Validate(); err != nil {
		return nil, err
	}

	if _, err := s.store.Load(req.ID); err == nil {
		return nil, fmt.Errorf("sandbox already exists: %s", req.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	ctx = namespaces.WithNamespace(ctx, s.namespace)
	if s.hasRuntimeArtifacts(req.ID) {
		_ = s.cleanupOrphanRuntimeSandbox(ctx, req.ID)
		_ = s.cleanupCNICache(req.ID)
	}

	unlockPorts, err := s.acquireSandboxLock("_ports")
	if err != nil {
		return nil, err
	}

	if err := s.ensureHostPortsAvailable(req.ID, req.Ports); err != nil {
		unlockPorts()
		return nil, err
	}

	releasePorts := s.reserveRequestedPorts(req.ID, req.Ports)
	unlockPorts()
	defer releasePorts()

	// Serialize admission decision + state save to avoid race where concurrent
	// creates both pass admission before either writes creating state.
	unlockAdmission, err := s.acquireSandboxLock("_admission")
	if err != nil {
		return nil, err
	}
	defer unlockAdmission()

	if err := s.enforceAdmission(req); err != nil {
		return nil, err
	}

	if err := s.ensureIPCapacity(req.ID); err != nil {
		return nil, err
	}

	sbx := s.newSandboxState(req)
	if err := s.store.Save(sbx); err != nil {
		return nil, err
	}
	s.dbg("state saved sandbox=%s phase=%s", sbx.ID, sbx.Phase)

	if async {
		// Provision in background; caller can poll via GET/LIST.
		s.dbg("start async provisioning sandbox=%s", req.ID)
		go s.provisionSandbox(req.ID, req)
		return sbx, nil
	}

	return s.provisionSandboxSync(ctx, sbx, req)
}

func (s *Service) provisionSandbox(sandboxID string, req model.CreateSandboxRequest) {
	s.dbg("provision begin sandbox=%s", sandboxID)
	bgCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ProvisionTimeout)
	defer cancel()
	bgCtx = namespaces.WithNamespace(bgCtx, s.namespace)

	unlock, err := s.acquireSandboxLock(sandboxID)
	if err != nil {
		s.markSandboxError(sandboxID, err)
		return
	}
	defer unlock()

	sbx, err := s.store.Load(sandboxID)
	if err != nil {
		return
	}

	if _, err := s.provisionSandboxSync(bgCtx, sbx, req); err != nil {
		s.dbg("provision failed sandbox=%s err=%v", sandboxID, err)
		s.markSandboxError(sandboxID, err)
		return
	}
	s.dbg("provision done sandbox=%s", sandboxID)
}

func (s *Service) provisionSandboxSync(ctx context.Context, sbx *model.Sandbox, req model.CreateSandboxRequest) (*model.Sandbox, error) {
	ctx = namespaces.WithNamespace(ctx, s.namespace)
	created := false
	defer func() {
		if created {
			return
		}

		setSandboxPhase(sbx, SandboxPhaseError, sbx.Error)
		if sbx.Error == "" {
			sbx.Error = "provision failed"
		}

		_ = s.store.Save(sbx)
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.deleteSandboxRuntimeArtifacts(namespaces.WithNamespace(cctx, s.namespace), sbx)
	}()

	pauseCtx, pauseCancel := context.WithTimeout(ctx, s.cfg.ContainerCreateTimeout)
	podID, assignedIP, podCfg, err := s.createPodSandboxCRI(pauseCtx, sbx)
	pauseCancel()
	if err != nil {
		sbx.Error = err.Error()
		return nil, err
	}
	sbx.PauseID = podID
	sbx.IP = assignedIP

	for _, c := range req.Containers {
		s.dbg("create container sandbox=%s name=%s image=%s", sbx.ID, c.Name, c.Image)
		parsedRes, err := parseContainerResource(c.Resource)
		if err != nil {
			err = fmt.Errorf("container %s: %w", c.Name, err)
			sbx.Error = err.Error()
			return nil, err
		}

		lim := parsedResourceToLimits(parsedRes)

		pullCtx, pullCancel := context.WithTimeout(ctx, s.cfg.ImagePullTimeout)
		pullErr := s.cri.pullImage(pullCtx, normalizeImage(c.Image))
		pullCancel()
		if pullErr != nil {
			err = fmt.Errorf("pull image %q: %w", normalizeImage(c.Image), pullErr)
			sbx.Error = err.Error()
			return nil, err
		}

		ctrCtx, ctrCancel := context.WithTimeout(ctx, s.cfg.ContainerCreateTimeout)
		st, err := s.createAndStartCRIContainer(ctrCtx, sbx, podID, podCfg, c, lim)
		ctrCancel()
		if err != nil {
			sbx.Error = err.Error()
			return nil, err
		}

		sbx.Containers[c.Name] = st
	}

	if sbx.IP == "" {
		return nil, fmt.Errorf("sandbox ip not assigned by cni")
	}
	s.dbg("resolved ip sandbox=%s ip=%s", sbx.ID, sbx.IP)

	if err := s.applySandboxNetworkPolicy(sbx); err != nil {
		return nil, err
	}
	s.dbg("network policy applied sandbox=%s", sbx.ID)

	readyCtx, readyCancel := context.WithTimeout(ctx, 18*time.Second)
	defer readyCancel()
	if err := s.waitSandboxReady(readyCtx, sbx); err != nil {
		return nil, err
	}

	if err := s.applyHostPortPublish(sbx); err != nil {
		return nil, err
	}

	s.dbg("hostport publish applied sandbox=%s", sbx.ID)
	// Published TCP readiness is best-effort; runtime task readiness is the
	// primary success signal. Some images open ports slightly after task start.
	_ = s.waitPublishedTCPReady(sbx)

	s.refreshSandboxRuntimeState(ctx, sbx)
	setSandboxPhase(sbx, SandboxPhaseRunning, "")
	if err := s.store.Save(sbx); err != nil {
		return nil, err
	}
	s.dbg("sandbox running saved sandbox=%s", sbx.ID)

	created = true
	return sbx, nil
}

func (s *Service) markSandboxError(sandboxID string, err error) {
	sbx, loadErr := s.store.Load(sandboxID)
	if loadErr != nil {
		return
	}

	msg := ""
	if err != nil {
		msg = err.Error()
	}

	setSandboxPhase(sbx, SandboxPhaseError, msg)
	_ = s.store.Save(sbx)
	s.dbg("sandbox error saved sandbox=%s err=%s", sandboxID, msg)
}

func (s *Service) DeleteSandbox(ctx context.Context, sandboxID string) error {
	unlock, err := s.acquireSandboxLock(sandboxID)
	if err != nil {
		return err
	}
	defer unlock()

	ctx = namespaces.WithNamespace(ctx, s.namespace)
	sbx, err := s.store.Load(sandboxID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.releaseReservedPorts(sandboxID)
			return nil
		}

		return err
	}

	setSandboxPhase(sbx, SandboxPhaseDeleting, "")

	_ = s.store.Save(sbx)
	err = s.deleteSandboxFromState(ctx, sbx)
	s.releaseReservedPorts(sandboxID)
	s.clearUnhealthy(sandboxID)

	return err
}

func (s *Service) ListSandboxes(_ context.Context) ([]*model.Sandbox, error) {
	ctx := namespaces.WithNamespace(context.Background(), s.namespace)
	all, err := s.store.List()
	if err != nil {
		return nil, err
	}

	out := make([]*model.Sandbox, 0, len(all))
	for _, sbx := range all {
		cp := copySandbox(sbx)
		s.refreshSandboxRuntimeState(ctx, cp)
		out = append(out, cp)
	}

	return out, nil
}

func (s *Service) ListSandboxesPage(_ context.Context, cursor string, limit int) ([]*model.Sandbox, string, error) {
	ctx := namespaces.WithNamespace(context.Background(), s.namespace)
	all, err := s.store.List()
	if err != nil {
		return nil, "", err
	}

	if limit <= 0 {
		limit = 20
	}

	if limit > 200 {
		limit = 200
	}

	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	start := 0
	if cursor != "" {
		for i := range all {
			if all[i].ID > cursor {
				start = i
				break
			}

			start = len(all)
		}
	}

	page := make([]*model.Sandbox, 0, limit)
	for i := start; i < len(all) && len(page) < limit; i++ {
		cp := copySandbox(all[i])
		s.refreshSandboxRuntimeState(ctx, cp)
		page = append(page, cp)
	}

	nextCursor := ""
	if start+len(page) < len(all) && len(page) > 0 {
		nextCursor = page[len(page)-1].ID
	}

	return page, nextCursor, nil
}

func (s *Service) GetSandbox(ctx context.Context, id string) (*model.Sandbox, error) {
	sbx, err := s.store.Load(id)
	if err != nil {
		return nil, err
	}

	cp := copySandbox(sbx)
	s.refreshSandboxRuntimeState(namespaces.WithNamespace(ctx, s.namespace), cp)

	return cp, nil
}

func (s *Service) ensureHostPortsAvailable(sandboxID string, requested []model.PortMapping) error {
	if len(requested) == 0 {
		return nil
	}

	used := map[int]struct {
		sandboxID string
		proto     string
	}{}
	all, err := s.store.List()
	if err != nil {
		return err
	}

	for _, sb := range all {
		if sb.ID == sandboxID {
			continue
		}

		for _, p := range sb.Ports {
			proto := normalizeProto(p.Protocol)
			used[p.HostPort] = struct {
				sandboxID string
				proto     string
			}{sandboxID: sb.ID, proto: proto}
		}
	}

	for _, p := range requested {
		proto := normalizeProto(p.Protocol)
		if o, ok := used[p.HostPort]; ok && o.proto == proto {
			return fmt.Errorf("host port already in use: %d/%s (sandbox %s)", p.HostPort, proto, o.sandboxID)
		}

		if owner, ok := s.reservedPortOwner(p.HostPort, proto); ok && owner != sandboxID {
			return fmt.Errorf("host port already reserved: %d/%s (sandbox %s)", p.HostPort, proto, owner)
		}

		if err := ensureLocalPortFree(p.HostPort, proto); err != nil {
			return fmt.Errorf("host port already in use: %d/%s (%v)", p.HostPort, proto, err)
		}
	}

	return nil
}

func (s *Service) reserveRequestedPorts(sandboxID string, requested []model.PortMapping) func() {
	keys := make([]string, 0, len(requested))
	s.portMu.Lock()
	for _, p := range requested {
		k := portKey(p.HostPort, normalizeProto(p.Protocol))
		s.reservedPorts[k] = sandboxID
		keys = append(keys, k)
	}
	s.portMu.Unlock()

	return func() {
		s.portMu.Lock()
		for _, k := range keys {
			if s.reservedPorts[k] == sandboxID {
				delete(s.reservedPorts, k)
			}
		}

		s.portMu.Unlock()
	}
}

func (s *Service) releaseReservedPorts(sandboxID string) {
	s.portMu.Lock()
	for k, v := range s.reservedPorts {
		if v == sandboxID {
			delete(s.reservedPorts, k)
		}
	}

	s.portMu.Unlock()
}

func (s *Service) reservedPortOwner(port int, proto string) (string, bool) {
	s.portMu.Lock()
	defer s.portMu.Unlock()
	v, ok := s.reservedPorts[portKey(port, proto)]

	return v, ok
}

func portKey(port int, proto string) string {
	return fmt.Sprintf("%d/%s", port, normalizeProto(proto))
}

func ensureLocalPortFree(port int, proto string) error {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	if proto == "udp" {
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			return err
		}
		_ = pc.Close()

		return nil
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	_ = ln.Close()
	return nil
}

func (s *Service) waitSandboxReady(ctx context.Context, sbx *model.Sandbox) error {
	deadline := time.Now().Add(config.DefaultReadyTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("sandbox %s readiness canceled: %w", sbx.ID, ctx.Err())
		default:
		}

		if _, err := s.cri.podSandboxStatus(ctx, sbx.PauseID); err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		allRunning := true
		for _, c := range sbx.Containers {
			st, err := s.cri.containerStatus(ctx, c.ID)
			if err != nil || st.State != runtimeapi.ContainerState_CONTAINER_RUNNING {
				allRunning = false
				break
			}
		}

		if allRunning {
			return nil
		}

		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("sandbox %s did not become ready before timeout", sbx.ID)
}

func normalizeProto(proto string) string {
	p := strings.ToLower(strings.TrimSpace(proto))
	if p == "" {
		return "tcp"
	}

	return p
}

func (s *Service) waitPublishedTCPReady(sbx *model.Sandbox) error {
	deadline := time.Now().Add(10 * time.Second)
	consecutive := 0
	for time.Now().Before(deadline) {
		allReady := true
		for _, p := range sbx.Ports {
			if normalizeProto(p.Protocol) != "tcp" {
				continue
			}

			addr := net.JoinHostPort(sbx.IP, fmt.Sprintf("%d", p.ContainerPort))
			conn, err := net.DialTimeout("tcp", addr, 800*time.Millisecond)
			if err != nil {
				allReady = false
				break
			}

			_ = conn.Close()
		}

		if allReady {
			consecutive++
			if consecutive >= 4 {
				return nil
			}
		} else {
			consecutive = 0
		}

		time.Sleep(150 * time.Millisecond)
	}

	return fmt.Errorf("sandbox %s tcp ports not ready before timeout", sbx.ID)
}

func (s *Service) hasRuntimeArtifacts(sandboxID string) bool {
	if sandboxID == "" {
		return false
	}

	if _, err := os.Stat(filepath.Join("/var/lib/cni", sandboxID)); err == nil {
		return true
	}

	ctx := namespaces.WithNamespace(context.Background(), s.namespace)
	if items, err := s.cri.listPodSandboxes(ctx); err == nil {
		for _, it := range items {
			md := it.GetMetadata()
			if md != nil && md.GetName() == sandboxID {
				return true
			}
		}
	}

	return false
}

func copySandbox(in *model.Sandbox) *model.Sandbox {
	if in == nil {
		return nil
	}

	out := *in
	out.Ports = append([]model.PortMapping(nil), in.Ports...)
	out.Containers = make(map[string]model.ContainerState, len(in.Containers))
	for k, v := range in.Containers {
		cp := v
		cp.Args = append([]string(nil), v.Args...)
		cp.Env = append([]string(nil), v.Env...)
		out.Containers[k] = cp
	}

	return &out
}
