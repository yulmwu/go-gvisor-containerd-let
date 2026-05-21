package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"sandboxd-o/sandboxd-orch/client"
	"sandboxd-o/sandboxd-orch/types"
)

func (s *Service) RegisterNode(ctx context.Context, req types.RegisterNodeRequest, source string) (*types.Node, error) {
	if err := validateNodeInput(req.ID, req.IP, req.Port); err != nil {
		return nil, err
	}

	if source == "" {
		source = "api"
	}

	if err := s.repo.UpsertNode(ctx, req.ID, req.IP, req.Port, source); err != nil {
		return nil, err
	}

	n, err := s.repo.GetNode(ctx, req.ID)
	if err != nil {
		return nil, err
	}

	s.probeNode(ctx, *n)
	s.syncNodeResources(ctx, *n)
	return s.repo.GetNode(ctx, req.ID)
}

func (s *Service) CreateNodeObject(ctx context.Context, req types.CreateNodeObjectRequest) (*types.Node, error) {
	return s.RegisterNode(ctx, types.RegisterNodeRequest{
		ID:   strings.TrimSpace(req.ID),
		IP:   strings.TrimSpace(req.Spec.IP),
		Port: req.Spec.Port,
	}, "object")
}

func (s *Service) UpsertExternalObject(ctx context.Context, req types.CreateExternalObjectRequest) error {
	id := strings.TrimSpace(req.ID)
	nodeID := strings.TrimSpace(req.Spec.NodeID)
	external := strings.TrimSpace(req.Spec.External)
	if id == "" {
		return fmt.Errorf("%w: external id is required", ErrInvalidInput)
	}

	if nodeID == "" {
		return fmt.Errorf("%w: spec.node_id is required", ErrInvalidInput)
	}

	if external == "" {
		return fmt.Errorf("%w: spec.external is required", ErrInvalidInput)
	}

	if _, err := s.repo.GetNode(ctx, nodeID); err != nil {
		return fmt.Errorf("%w: referenced node not found", ErrInvalidInput)
	}

	return s.repo.SetNodeExternal(ctx, id, nodeID, external)
}

func (s *Service) DeleteNode(ctx context.Context, id string) error {
	return s.DeleteNodeForce(ctx, id, false)
}

func (s *Service) ListExternals(ctx context.Context) ([]types.External, error) {
	return s.repo.ListExternals(ctx)
}

func (s *Service) GetExternal(ctx context.Context, id string) (*types.External, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalidInput)
	}

	ex, err := s.repo.GetExternal(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, err
	}

	return ex, nil
}

func (s *Service) DeleteExternal(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidInput)
	}

	return s.repo.DeleteExternal(ctx, id)
}

func (s *Service) DeleteNodeForce(ctx context.Context, id string, force bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalidInput)
	}

	if err := s.detachNodeSandboxes(ctx, id, force); err != nil {
		return err
	}

	if err := s.repo.DeleteNode(ctx, id); err != nil {
		return err
	}

	s.resources.Delete(id)
	return nil
}

func (s *Service) detachNodeSandboxes(ctx context.Context, nodeName string, force bool) error {
	if s.sbxRepo == nil {
		return nil
	}

	client, _, err := s.SandboxOpClientForNode(ctx, nodeName)
	if err != nil && !force {
		return fmt.Errorf("prepare node client for delete: %w", err)
	}

	if force {
		client = nil
	}

	items, err := s.sbxRepo.ListSandboxes(ctx)
	if err != nil {
		return err
	}

	for _, sbx := range items {
		if sbx.Status.NodeName != nodeName {
			continue
		}

		if err := s.sbxRepo.ReleaseSandboxPorts(ctx, sbx.ID); err != nil {
			slog.Warn("release sandbox ports failed while detaching node sandbox", slog.String("node", nodeName), slog.String("sandbox", sbx.ID), slog.Any("error", err))
		}

		switch sbx.Status.Phase {
		case types.SandboxPhaseDeleting:
			_ = s.sbxRepo.DeleteSandbox(ctx, sbx.ID)
		case types.SandboxPhaseRunning, types.SandboxPhaseScheduled:
			if client != nil {
				if _, err := client.DeleteSandbox(ctx, sbx.ID); err != nil && !force {
					return fmt.Errorf("delete sandbox on node %s for sandbox %s: %w", nodeName, sbx.ID, err)
				}
			}

			st := sbx.Status
			st.Phase = types.SandboxPhaseFailed
			st.LastError = "node removed"
			st.NodeName = ""
			st.External = ""
			st.AssignedPorts = nil
			_ = s.sbxRepo.UpdateSandboxStatus(ctx, sbx.ID, st)
		}
	}

	if client != nil {
		if _, err := client.Reconcile(ctx); err != nil && !force {
			return fmt.Errorf("reconcile node %s during delete: %w", nodeName, err)
		}
	}

	return nil
}

func (s *Service) ListNodes(ctx context.Context) ([]types.Node, error) {
	return s.repo.ListNodes(ctx)
}

func (s *Service) GetNode(ctx context.Context, id string) (*types.Node, error) {
	return s.repo.GetNode(ctx, id)
}

func (s *Service) SandboxClientForNode(ctx context.Context, id string) (*client.Client, *types.Node, error) {
	return s.sandboxClientForNode(ctx, id, s.cfg.ProbeTimeout)
}

func (s *Service) SandboxOpClientForNode(ctx context.Context, id string) (*client.Client, *types.Node, error) {
	return s.sandboxClientForNode(ctx, id, s.cfg.SandboxOpTimeout)
}

func (s *Service) sandboxClientForNode(ctx context.Context, id string, timeout time.Duration) (*client.Client, *types.Node, error) {
	n, err := s.repo.GetNode(ctx, id)
	if err != nil {
		return nil, nil, err
	}

	c := client.New(n.SbxletBaseURL, timeout)
	return c, n, nil
}

func (s *Service) probeNode(ctx context.Context, n types.Node) {
	probeCtx, cancel := context.WithTimeout(ctx, s.cfg.ProbeTimeout)
	defer cancel()

	c := client.New(n.SbxletBaseURL, s.cfg.ProbeTimeout)
	err := c.Healthz(probeCtx)

	now := time.Now().UTC()
	next := n
	next.LastHeartbeat = &now
	if err == nil {
		next.SuccessStreak++
		if next.SuccessStreak > s.cfg.ReadySuccessThreshold {
			next.SuccessStreak = s.cfg.ReadySuccessThreshold
		}

		next.FailureStreak = 0
		next.LastError = ""
		if next.State != types.NodeStateReady && next.SuccessStreak >= s.cfg.ReadySuccessThreshold {
			next.State = types.NodeStateReady
		}
	} else {
		next.FailureStreak++
		next.SuccessStreak = 0
		next.LastError = err.Error()
		if next.FailureStreak >= s.cfg.NotReadyFailureThreshold {
			next.State = types.NodeStateNotReady
		}
	}

	if next.State == "" {
		next.State = types.NodeStateUnknown
	}

	if err := s.repo.UpdateHeartbeat(ctx, n.ID, next.State, next.SuccessStreak, next.FailureStreak, next.LastError, next.LastHeartbeat); err != nil {
		slog.Warn("persist heartbeat failed", slog.String("node", n.ID), slog.Any("error", err))
	}
}

func (s *Service) syncNodeResources(ctx context.Context, n types.Node) {
	probeCtx, cancel := context.WithTimeout(ctx, s.cfg.ProbeTimeout)
	defer cancel()

	c := client.New(n.SbxletBaseURL, s.cfg.ProbeTimeout)
	st, err := c.NodeStatus(probeCtx)
	if err != nil {
		slog.Warn("node resource sync failed", slog.String("node", n.ID), slog.Any("error", err))
		return
	}

	res := st.Resources
	// External is controlled by External object, not sbxlet node status sync.
	res.External = strings.TrimSpace(n.Resources.External)
	if res.External == "" {
		res.External = "(none)"
	}

	s.resources.PutCurrent(n.ID, res)
	now := time.Now().UTC()
	if !s.resources.ShouldPersist(n.ID, now, s.cfg.ResourcePersistMinInt, s.cfg.ResourcePersistMaxInt) {
		return
	}

	if err := s.repo.UpdateNodeResources(ctx, n.ID, res); err != nil {
		slog.Warn("persist node resources failed", slog.String("node", n.ID), slog.Any("error", err))
		return
	}

	s.resources.MarkPersisted(n.ID, res, now)
}

func (s *Service) StartHeartbeatLoop(ctx context.Context) {
	go func() {
		t := time.NewTicker(s.cfg.HeartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.runHeartbeatOnce(ctx)
			}
		}
	}()
}

func (s *Service) StartResourceSyncLoop(ctx context.Context) {
	go func() {
		t := time.NewTicker(s.cfg.ResourceSyncInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.runResourceSyncOnce(ctx)
			}
		}
	}()
}

func (s *Service) runHeartbeatOnce(ctx context.Context) {
	nodes, err := s.repo.ListNodes(ctx)
	if err != nil {
		slog.Error("list nodes failed", slog.Any("error", err))
		return
	}

	s.forEachNode(ctx, nodes, s.probeNode)
}

func (s *Service) runResourceSyncOnce(ctx context.Context) {
	nodes, err := s.repo.ListNodes(ctx)
	if err != nil {
		slog.Error("list nodes failed for resource sync", slog.Any("error", err))
		return
	}

	s.forEachNode(ctx, nodes, s.syncNodeResources)
}

func (s *Service) forEachNode(ctx context.Context, nodes []types.Node, fn func(context.Context, types.Node)) {
	if !s.cfg.HeartbeatParallel || len(nodes) <= 1 {
		for _, n := range nodes {
			fn(ctx, n)
		}
		return
	}

	parallel := min(s.cfg.HeartbeatMaxParallel, len(nodes))

	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		sem <- struct{}{}
		go func(node types.Node) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(ctx, node)
		}(n)
	}

	wg.Wait()
}
