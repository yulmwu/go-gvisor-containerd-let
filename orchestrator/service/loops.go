package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"sandboxd-o/orchestrator/types"
)

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
