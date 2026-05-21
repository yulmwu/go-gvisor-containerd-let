package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"sandboxd-o/sandboxd-let/model"
	"sandboxd-o/sandboxd-orch/client"
	"sandboxd-o/sandboxd-orch/repo"
	"sandboxd-o/sandboxd-orch/types"

	k8sresource "k8s.io/apimachinery/pkg/api/resource"
)

func (s *Service) CreateSandbox(ctx context.Context, req types.CreateSandboxObjectRequest) (*types.Sandbox, error) {
	if err := validateSandboxCreate(req); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	sbx := types.Sandbox{
		ID:   strings.TrimSpace(req.ID),
		Spec: req.Spec,
		Status: types.SandboxStatus{
			Phase: types.SandboxPhasePending,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if req.Spec.TTLSeconds > 0 {
		exp := now.Add(time.Duration(req.Spec.TTLSeconds) * time.Second)
		sbx.Status.ExpireAt = &exp
	}

	if err := s.sbxRepo.CreateSandbox(ctx, sbx); err != nil {
		return nil, err
	}

	return s.sbxRepo.GetSandbox(ctx, sbx.ID)
}

func (s *Service) GetSandbox(ctx context.Context, id string) (*types.Sandbox, error) {
	return s.sbxRepo.GetSandbox(ctx, strings.TrimSpace(id))
}

func (s *Service) ListSandboxes(ctx context.Context) ([]types.Sandbox, error) {
	return s.sbxRepo.ListSandboxes(ctx)
}

func (s *Service) DeleteSandbox(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: sandbox id is required", ErrInvalidInput)
	}

	sbx, err := s.sbxRepo.GetSandbox(ctx, id)
	if err != nil {
		return err
	}

	if sbx.Status.Phase == types.SandboxPhaseDeleting {
		return nil
	}

	sbx.Status.Phase = types.SandboxPhaseDeleting
	sbx.Status.LastError = ""
	if err := s.sbxRepo.UpdateSandboxStatus(ctx, id, sbx.Status); err != nil {
		return err
	}

	return s.finalizeSandboxDelete(ctx, *sbx)
}

func (s *Service) finalizeSandboxDelete(ctx context.Context, sbx types.Sandbox) error {
	if sbx.Status.NodeName != "" {
		client, _, err := s.SandboxOpClientForNode(ctx, sbx.Status.NodeName)
		if err != nil {
			// Node may be removed dynamically; still allow control-plane cleanup.
			if !errors.Is(err, sql.ErrNoRows) {
				sbx.Status.LastError = err.Error()
				_ = s.sbxRepo.UpdateSandboxStatus(ctx, sbx.ID, sbx.Status)
				return err
			}
		}

		if client != nil {
			if _, err := client.DeleteSandbox(ctx, sbx.ID); err != nil {
				sbx.Status.LastError = err.Error()
				_ = s.sbxRepo.UpdateSandboxStatus(ctx, sbx.ID, sbx.Status)
				return err
			}
		}
	}

	if err := s.sbxRepo.ReleaseSandboxPorts(ctx, sbx.ID); err != nil {
		sbx.Status.LastError = err.Error()
		_ = s.sbxRepo.UpdateSandboxStatus(ctx, sbx.ID, sbx.Status)
		return err
	}

	if sbx.Status.NodeName != "" {
		nodeName := sbx.Status.NodeName
		if err := s.sbxRepo.DeleteSandbox(ctx, sbx.ID); err != nil {
			return err
		}

		if err := s.adjustNodeUsageForSandbox(ctx, nodeName, sbx.Spec, -1); err != nil {
			slog.Warn("logical resource decrement failed", slog.String("sandbox", sbx.ID), slog.String("node", nodeName), slog.Any("error", err))
		}

		return nil
	}

	return s.sbxRepo.DeleteSandbox(ctx, sbx.ID)
}

func validateSandboxCreate(req types.CreateSandboxObjectRequest) error {
	if strings.TrimSpace(req.ID) == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidInput)
	}

	if len(req.Spec.Containers) == 0 {
		return fmt.Errorf("%w: at least one container is required", ErrInvalidInput)
	}

	if req.Spec.TTLSeconds < 0 {
		return fmt.Errorf("%w: ttl_seconds must be >= 0", ErrInvalidInput)
	}

	for _, c := range req.Spec.Containers {
		if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Image) == "" {
			return fmt.Errorf("%w: container name and image are required", ErrInvalidInput)
		}

		if strings.TrimSpace(c.Resource.CPU) == "" || strings.TrimSpace(c.Resource.Memory) == "" {
			return fmt.Errorf("%w: container resource cpu/memory are required", ErrInvalidInput)
		}

		if _, err := parseCPUMilli(c.Resource.CPU); err != nil {
			return fmt.Errorf("%w: invalid cpu for container %s", ErrInvalidInput, c.Name)
		}

		if _, err := parseMemoryBytes(c.Resource.Memory); err != nil {
			return fmt.Errorf("%w: invalid memory for container %s", ErrInvalidInput, c.Name)
		}
	}

	for _, p := range req.Spec.Ports {
		if p.ContainerPort < 1 || p.ContainerPort > 65535 {
			return fmt.Errorf("%w: invalid container port", ErrInvalidInput)
		}

		proto := strings.ToLower(strings.TrimSpace(p.Protocol))
		if proto == "" {
			continue
		}

		if proto != "tcp" && proto != "udp" {
			return fmt.Errorf("%w: unsupported protocol", ErrInvalidInput)
		}
	}

	return nil
}

func (s *Service) runSchedulerOnce(ctx context.Context) {
	sandboxes, err := s.sbxRepo.ListSandboxes(ctx)
	if err != nil {
		slog.Warn("scheduler list sandboxes failed", slog.Any("error", err))
		return
	}

	pending := 0
	total := len(sandboxes)
	for _, sbx := range sandboxes {
		if sbx.Status.Phase != types.SandboxPhasePending {
			continue
		}

		pending++
		s.scheduleOne(ctx, sbx)
	}

	slog.Info("scheduler tick completed", slog.Int("sandbox_total", total), slog.Int("pending_count", pending))
}

func (s *Service) scheduleOne(ctx context.Context, sbx types.Sandbox) {
	nodes, err := s.repo.ListNodes(ctx)
	if err != nil {
		slog.Warn("scheduler list nodes failed", slog.String("sandbox", sbx.ID), slog.Any("error", err))
		return
	}

	needCPU, needMem, err := sandboxResourceRequest(sbx.Spec)
	if err != nil {
		sbx.Status.Phase = types.SandboxPhaseFailed
		sbx.Status.LastError = err.Error()
		_ = s.sbxRepo.UpdateSandboxStatus(ctx, sbx.ID, sbx.Status)
		return
	}

	type candidate struct {
		node  types.Node
		ports []types.SandboxPortAssign
	}

	cands := make([]candidate, 0)
	for _, n := range nodes {
		if n.State != types.NodeStateReady {
			continue
		}

		if n.Resources.AvailableCPUMilli < needCPU || n.Resources.AvailableMemory < needMem {
			continue
		}

		used, err := s.sbxRepo.NodeUsedHostPorts(ctx, n.ID)
		if err != nil {
			continue
		}

		ports, ok := allocateHostPorts(sbx.Spec.Ports, used, s.cfg.HostPortMin, s.cfg.HostPortMax)
		if !ok {
			continue
		}

		cands = append(cands, candidate{node: n, ports: ports})
	}

	if len(cands) == 0 {
		sbx.Status.Phase = types.SandboxPhaseFailed
		sbx.Status.LastError = "no feasible node for resources/ports"
		_ = s.sbxRepo.UpdateSandboxStatus(ctx, sbx.ID, sbx.Status)

		slog.Info("scheduler sandbox failed no candidate", slog.String("sandbox", sbx.ID), slog.Int64("need_cpu_milli", needCPU), slog.Int64("need_memory_bytes", needMem))
		return
	}

	sort.Slice(cands, func(i, j int) bool {
		lhs := cands[i].node.Resources.AvailableCPUMilli - needCPU
		rhs := cands[j].node.Resources.AvailableCPUMilli - needCPU
		if lhs == rhs {
			return cands[i].node.ID < cands[j].node.ID
		}

		return lhs > rhs
	})
	chosen := cands[0]
	slog.Info("scheduler selected node", slog.String("sandbox", sbx.ID), slog.String("node", chosen.node.ID), slog.Int("port_count", len(chosen.ports)), slog.Int64("need_cpu_milli", needCPU), slog.Int64("need_memory_bytes", needMem))

	if err := s.sbxRepo.ReserveSandboxPortsAndSchedule(ctx, sbx.ID, chosen.node.ID, chosen.ports); err != nil {
		if errors.Is(err, repo.ErrPortReservationConflict) {
			slog.Info("scheduler reserve ports conflict; will retry next tick", slog.String("sandbox", sbx.ID), slog.String("node", chosen.node.ID), slog.Any("error", err))
			return
		}

		sbx.Status.Phase = types.SandboxPhaseFailed
		sbx.Status.LastError = err.Error()
		_ = s.sbxRepo.UpdateSandboxStatus(ctx, sbx.ID, sbx.Status)

		slog.Warn("scheduler reserve ports failed", slog.String("sandbox", sbx.ID), slog.String("node", chosen.node.ID), slog.Any("error", err))
		return
	}

	fresh, err := s.sbxRepo.GetSandbox(ctx, sbx.ID)
	if err != nil {
		slog.Warn("scheduler reload sandbox failed", slog.String("sandbox", sbx.ID), slog.Any("error", err))
		return
	}

	fresh.Status.External = strings.TrimSpace(chosen.node.Resources.External)
	if fresh.Status.External == "" {
		fresh.Status.External = "(none)"
	}
	if err := s.createSandboxOnNode(ctx, *fresh); err != nil {
		fresh.Status.Phase = types.SandboxPhaseFailed
		fresh.Status.LastError = err.Error()
		_ = s.sbxRepo.UpdateSandboxStatus(ctx, fresh.ID, fresh.Status)
		if relErr := s.sbxRepo.ReleaseSandboxPorts(ctx, fresh.ID); relErr != nil {
			slog.Warn("release sandbox ports failed after create failure", slog.String("sandbox", fresh.ID), slog.Any("error", relErr))
		}

		slog.Warn("scheduler create sandbox on node failed", slog.String("sandbox", fresh.ID), slog.String("node", fresh.Status.NodeName), slog.Any("error", err))
		return
	}

	// Refresh external from repository right after create so callers can see it immediately.
	// (ListNodes snapshot used for scheduling can be stale relative to External object updates.)
	if latestNode, err := s.repo.GetNode(ctx, fresh.Status.NodeName); err == nil {
		if ext := strings.TrimSpace(latestNode.Resources.External); ext != "" {
			fresh.Status.External = ext
		} else {
			fresh.Status.External = "(none)"
		}
	}

	fresh.Status.Phase = types.SandboxPhaseRunning
	fresh.Status.LastError = ""
	if ip := s.fetchSandboxIPOnce(ctx, fresh.Status.NodeName, fresh.ID); ip != "" {
		fresh.Status.IP = ip
	}

	_ = s.sbxRepo.UpdateSandboxStatus(ctx, fresh.ID, fresh.Status)
	if err := s.adjustNodeUsageForSandbox(ctx, fresh.Status.NodeName, fresh.Spec, 1); err != nil {
		slog.Warn("scheduler logical resource increment failed", slog.String("sandbox", fresh.ID), slog.String("node", fresh.Status.NodeName), slog.Any("error", err))
	}

	go s.refreshSandboxIPSoon(fresh.ID, fresh.Status.NodeName)
	slog.Info("scheduler sandbox running", slog.String("sandbox", fresh.ID), slog.String("node", fresh.Status.NodeName))
}

func (s *Service) fetchSandboxIPOnce(ctx context.Context, nodeName, sandboxID string) string {
	c, _, err := s.SandboxOpClientForNode(ctx, nodeName)
	if err != nil {
		return ""
	}

	st, err := c.SandboxStatuses(ctx, []string{sandboxID})
	if err != nil || len(st.Items) == 0 {
		return ""
	}

	return strings.TrimSpace(st.Items[0].IP)
}

func (s *Service) refreshSandboxIPSoon(sandboxID, nodeName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, _, err := s.SandboxOpClientForNode(ctx, nodeName)
	if err != nil {
		return
	}

	for i := 0; i < 10; i++ {
		st, err := c.SandboxStatuses(ctx, []string{sandboxID})
		if err != nil {
			return
		}

		if len(st.Items) > 0 {
			if ip := strings.TrimSpace(st.Items[0].IP); ip != "" {
				latest, err := s.sbxRepo.GetSandbox(ctx, sandboxID)
				if err != nil {
					return
				}

				if latest.Status.IP == ip || latest.Status.Phase == types.SandboxPhaseDeleting {
					return
				}

				next := latest.Status
				next.IP = ip
				_, _ = s.sbxRepo.UpdateSandboxStatusIfUnchanged(ctx, sandboxID, next, latest.UpdatedAt)
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (s *Service) createSandboxOnNode(ctx context.Context, sbx types.Sandbox) error {
	client, _, err := s.SandboxOpClientForNode(ctx, sbx.Status.NodeName)
	if err != nil {
		return err
	}

	req := model.CreateSandboxRequest{ID: sbx.ID, Egress: sbx.Spec.Egress}
	for _, p := range sbx.Status.AssignedPorts {
		req.Ports = append(req.Ports, model.PortMapping{HostPort: p.HostPort, ContainerPort: p.ContainerPort, Protocol: p.Protocol})
	}

	for _, c := range sbx.Spec.Containers {
		req.Containers = append(req.Containers, model.CreateContainerRequest{
			Name:     c.Name,
			Image:    c.Image,
			Args:     append([]string(nil), c.Args...),
			Env:      append([]string(nil), c.Env...),
			WorkDir:  c.WorkDir,
			Resource: model.ResourceSpec{CPU: c.Resource.CPU, Memory: c.Resource.Memory},
		})
	}

	_, err = client.CreateSandbox(ctx, req)
	return err
}

func sandboxResourceRequest(spec types.SandboxSpec) (int64, int64, error) {
	var cpuMilli int64
	var memBytes int64
	for _, c := range spec.Containers {
		cpu, err := parseCPUMilli(c.Resource.CPU)
		if err != nil {
			return 0, 0, err
		}

		mem, err := parseMemoryBytes(c.Resource.Memory)
		if err != nil {
			return 0, 0, err
		}

		cpuMilli += cpu
		memBytes += mem
	}

	return cpuMilli, memBytes, nil
}

func (s *Service) adjustNodeUsageForSandbox(ctx context.Context, nodeName string, spec types.SandboxSpec, direction int64) error {
	if strings.TrimSpace(nodeName) == "" || direction == 0 {
		return nil
	}

	cpu, mem, err := sandboxResourceRequest(spec)
	if err != nil {
		return err
	}

	return s.repo.AdjustNodeResourceUsage(ctx, nodeName, direction*cpu, direction*mem)
}

func parseCPUMilli(raw string) (int64, error) {
	q, err := k8sresource.ParseQuantity(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}

	return q.MilliValue(), nil
}

func parseMemoryBytes(raw string) (int64, error) {
	q, err := k8sresource.ParseQuantity(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}

	return q.Value(), nil
}

func allocateHostPorts(spec []types.SandboxPortSpec, used map[int]struct{}, minPort, maxPort int) ([]types.SandboxPortAssign, bool) {
	if len(spec) == 0 {
		return nil, true
	}

	if maxPort < minPort {
		return nil, false
	}

	assigned := make([]types.SandboxPortAssign, 0, len(spec))
	localUsed := make(map[int]struct{}, len(used)+len(spec))
	for p := range used {
		localUsed[p] = struct{}{}
	}

	available := make([]int, 0, maxPort-minPort+1)
	for hp := minPort; hp <= maxPort; hp++ {
		if _, exists := localUsed[hp]; !exists {
			available = append(available, hp)
		}
	}

	if len(available) < len(spec) {
		return nil, false
	}

	rand.Shuffle(len(available), func(i, j int) {
		available[i], available[j] = available[j], available[i]
	})

	nextDynamic := 0
	for _, p := range spec {
		hp := available[nextDynamic]
		nextDynamic++

		localUsed[hp] = struct{}{}
		proto := strings.ToLower(strings.TrimSpace(p.Protocol))
		if proto == "" {
			proto = "tcp"
		}

		assigned = append(assigned, types.SandboxPortAssign{HostPort: hp, ContainerPort: p.ContainerPort, Protocol: proto})
	}

	return assigned, true
}

func (s *Service) runSandboxReconcileOnce(ctx context.Context) {
	sandboxes, err := s.sbxRepo.ListSandboxes(ctx)
	if err != nil {
		slog.Warn("reconcile list sandboxes failed", slog.Any("error", err))
		return
	}

	now := time.Now().UTC()
	deleting := 0
	expired := 0
	total := len(sandboxes)
	for _, sbx := range sandboxes {
		if sbx.Status.Phase == types.SandboxPhaseDeleting {
			deleting++
			if err := s.finalizeSandboxDelete(ctx, sbx); err != nil {
				slog.Warn("reconcile finalize delete failed", slog.String("sandbox", sbx.ID), slog.Any("error", err))
			}
			continue
		}

		if sbx.Status.ExpireAt != nil && now.After(*sbx.Status.ExpireAt) {
			expired++
			sbx.Status.Phase = types.SandboxPhaseDeleting
			sbx.Status.LastError = "ttl expired"

			_ = s.sbxRepo.UpdateSandboxStatus(ctx, sbx.ID, sbx.Status)
			slog.Info("reconcile mark deleting by ttl", slog.String("sandbox", sbx.ID), slog.Time("expire_at", *sbx.Status.ExpireAt))
			if err := s.finalizeSandboxDelete(ctx, sbx); err != nil {
				slog.Warn("reconcile ttl delete failed", slog.String("sandbox", sbx.ID), slog.Any("error", err))
			}
		}
	}
	slog.Info("reconcile tick completed", slog.Int("sandbox_total", total), slog.Int("deleting_count", deleting), slog.Int("expired_count", expired))
}

func (s *Service) StartSchedulerLoop(ctx context.Context) {
	go func() {
		t := time.NewTicker(s.cfg.SchedulerInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.runSchedulerOnce(ctx)
			}
		}
	}()
}

func (s *Service) StartSandboxReconcileLoop(ctx context.Context) {
	go func() {
		t := time.NewTicker(s.cfg.ReconcileInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.runSandboxReconcileOnce(ctx)
			}
		}
	}()
}

func (s *Service) TriggerSandboxReconcile(ctx context.Context, id string) error {
	sbx, err := s.sbxRepo.GetSandbox(ctx, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return err
		}

		return err
	}

	if sbx.Status.Phase != types.SandboxPhaseDeleting {
		sbx.Status.Phase = types.SandboxPhaseDeleting
		_ = s.sbxRepo.UpdateSandboxStatus(ctx, sbx.ID, sbx.Status)
	}

	return s.finalizeSandboxDelete(ctx, *sbx)
}

func (s *Service) StartSandboxStatusSyncLoop(ctx context.Context) {
	go func() {
		t := time.NewTicker(s.cfg.StatusSyncInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.runSandboxStatusSyncOnce(ctx)
			}
		}
	}()
}

func (s *Service) runSandboxStatusSyncOnce(ctx context.Context) {
	items, err := s.sbxRepo.ListSandboxes(ctx)
	if err != nil {
		slog.Warn("sandbox status sync list failed", slog.Any("error", err))
		return
	}

	grouped := map[string][]types.Sandbox{}
	for _, sbx := range items {
		if strings.TrimSpace(sbx.Status.NodeName) == "" {
			continue
		}

		if sbx.Status.Phase != types.SandboxPhaseScheduled &&
			sbx.Status.Phase != types.SandboxPhaseRunning &&
			sbx.Status.Phase != types.SandboxPhaseFailed {
			continue
		}

		grouped[sbx.Status.NodeName] = append(grouped[sbx.Status.NodeName], sbx)
	}

	maxParallel := s.cfg.StatusSyncMaxParallel
	if maxParallel < 1 {
		maxParallel = 4
	}

	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup

	for nodeName, sandboxes := range grouped {
		sem <- struct{}{}
		nodeName, sandboxes := nodeName, sandboxes
		wg.Go(func() {
			defer func() { <-sem }()

			c, _, err := s.SandboxOpClientForNode(ctx, nodeName)
			if err != nil {
				slog.Warn("sandbox status sync skip node client", slog.String("node", nodeName), slog.Any("error", err))
				return
			}

			batchSize := s.cfg.StatusSyncBatchSize
			if batchSize < 1 {
				batchSize = 50
			}

			for i := 0; i < len(sandboxes); i += batchSize {
				end := min(i+batchSize, len(sandboxes))
				syncTimeout := s.cfg.StatusSyncTimeout
				if syncTimeout <= 0 {
					syncTimeout = 5 * time.Second
				}

				syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
				s.syncNodeSandboxBatch(syncCtx, c, nodeName, sandboxes[i:end])
				cancel()
			}
		})
	}

	wg.Wait()
}

func (s *Service) syncNodeSandboxBatch(ctx context.Context, c *client.Client, nodeName string, sandboxes []types.Sandbox) {
	nodeExternal := ""
	if n, err := s.repo.GetNode(ctx, nodeName); err == nil {
		if ext := strings.TrimSpace(n.Resources.External); ext != "" {
			nodeExternal = ext
		}
	}

	ids := make([]string, 0, len(sandboxes))
	for _, sbx := range sandboxes {
		ids = append(ids, sbx.ID)
	}

	resp, err := c.SandboxStatuses(ctx, ids)
	if err != nil {
		slog.Warn("sandbox status sync request failed", slog.String("node", nodeName), slog.Any("error", err))
		return
	}

	byID := make(map[string]client.SandboxSyncStatus, len(resp.Items))
	for _, it := range resp.Items {
		byID[it.ID] = it
	}

	for _, sbx := range sandboxes {
		if findMissing(resp.Missing, sbx.ID) {
			latest, err := s.sbxRepo.GetSandbox(ctx, sbx.ID)
			if err != nil {
				continue
			}

			if latest.Status.Phase == types.SandboxPhaseDeleting {
				continue
			}

			// Scheduled sandboxes may not exist on sbxlet yet during create window.
			if latest.Status.Phase == types.SandboxPhaseScheduled {
				continue
			}

			st := latest.Status
			st.Phase = types.SandboxPhaseFailed
			st.LastError = "deleted on sbxlet node"
			st.External = inferSandboxExternal(nodeExternal, latest.Status.External)
			ok, err := s.sbxRepo.UpdateSandboxStatusIfUnchanged(ctx, sbx.ID, st, latest.UpdatedAt)
			if err != nil {
				slog.Warn("sandbox status sync persist failed", slog.String("sandbox", sbx.ID), slog.Any("error", err))
			} else if !ok {
				slog.Debug("sandbox status sync skipped stale write", slog.String("sandbox", sbx.ID))
			}
			continue
		}

		nodeSt, ok := byID[sbx.ID]
		if !ok {
			continue
		}

		latest, err := s.sbxRepo.GetSandbox(ctx, sbx.ID)
		if err != nil {
			continue
		}

		// Do not override destructive transitions from other workflows.
		if latest.Status.Phase == types.SandboxPhaseDeleting {
			continue
		}

		next, changed := mergeSandboxPhaseWithNodeState(latest.Status, nodeSt)
		if ip := strings.TrimSpace(nodeSt.IP); ip != "" && next.IP != ip {
			next.IP = ip
			changed = true
		}

		if ext := inferSandboxExternal(nodeExternal, latest.Status.External); next.External != ext {
			next.External = ext
			changed = true
		}

		if changed {
			ok, err := s.sbxRepo.UpdateSandboxStatusIfUnchanged(ctx, sbx.ID, next, latest.UpdatedAt)
			if err != nil {
				slog.Warn("sandbox status sync persist failed", slog.String("sandbox", sbx.ID), slog.Any("error", err))
			} else if !ok {
				slog.Debug("sandbox status sync skipped stale write", slog.String("sandbox", sbx.ID))
			}
		}
	}
}

func findMissing(items []string, id string) bool {
	return slices.Contains(items, id)
}

func mergeSandboxPhaseWithNodeState(cur types.SandboxStatus, st client.SandboxSyncStatus) (types.SandboxStatus, bool) {
	next := cur
	changed := false

	phase := strings.TrimSpace(st.Phase)

	if strings.EqualFold(phase, "error") {
		msg := strings.TrimSpace(st.Error)
		for _, c := range st.UnhealthyContainers {
			if strings.TrimSpace(c.Error) != "" {
				if msg == "" {
					msg = "container " + c.Name + ": " + strings.TrimSpace(c.Error)
				} else {
					msg += "; container " + c.Name + ": " + strings.TrimSpace(c.Error)
				}
			}
		}

		if msg == "" {
			msg = "sandbox error on sbxlet node"
		}

		if next.Phase != types.SandboxPhaseFailed {
			next.Phase = types.SandboxPhaseFailed
			changed = true
		}

		if next.LastError != msg {
			next.LastError = msg
			changed = true
		}

		return next, changed
	}

	for _, c := range st.UnhealthyContainers {
		msg := strings.TrimSpace(c.Error)
		if msg == "" {
			msg = "container " + c.Name + " unhealthy (" + strings.TrimSpace(c.Phase) + ")"
		} else {
			msg = "container " + c.Name + ": " + msg
		}

		if next.Phase != types.SandboxPhaseFailed {
			next.Phase = types.SandboxPhaseFailed
			changed = true
		}

		if next.LastError != msg {
			next.LastError = msg
			changed = true
		}

		return next, changed
	}

	if strings.EqualFold(phase, "running") {
		if next.Phase != types.SandboxPhaseRunning {
			next.Phase = types.SandboxPhaseRunning
			changed = true
		}

		if next.LastError != "" {
			next.LastError = ""
			changed = true
		}

		return next, changed
	}

	if strings.EqualFold(phase, "creating") {
		if next.Phase == types.SandboxPhaseRunning {
			next.Phase = types.SandboxPhaseScheduled
			changed = true
		}

		return next, changed
	}

	return next, changed
}

func inferSandboxExternal(fromNodeResp, fallback string) string {
	ip := strings.TrimSpace(fromNodeResp)
	if ip != "" {
		return ip
	}

	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return "(none)"
	}

	return fallback
}
