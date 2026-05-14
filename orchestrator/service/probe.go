package service

import (
	"context"
	"log/slog"
	"time"

	"sandboxd-o/orchestrator/client"
	"sandboxd-o/orchestrator/types"
)

func (s *Service) probeNode(ctx context.Context, n types.Node) {
	probeCtx, cancel := context.WithTimeout(ctx, s.cfg.ProbeTimeout)
	defer cancel()

	c := client.New(n.SandboxdBaseURL, s.cfg.ProbeTimeout)
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

	if err := s.repo.UpdateHeartbeat(ctx, n.Name, next.State, next.SuccessStreak, next.FailureStreak, next.LastError, next.LastHeartbeat); err != nil {
		slog.Warn("persist heartbeat failed", slog.String("node", n.Name), slog.Any("error", err))
	}
}

func (s *Service) syncNodeResources(ctx context.Context, n types.Node) {
	probeCtx, cancel := context.WithTimeout(ctx, s.cfg.ProbeTimeout)
	defer cancel()

	c := client.New(n.SandboxdBaseURL, s.cfg.ProbeTimeout)
	st, err := c.NodeStatus(probeCtx)
	if err != nil {
		slog.Warn("node resource sync failed", slog.String("node", n.Name), slog.Any("error", err))
		return
	}

	res := st.Resources
	s.resources.PutCurrent(n.Name, res)
	now := time.Now().UTC()
	if !s.resources.ShouldPersist(n.Name, now, s.cfg.ResourcePersistMinInt, s.cfg.ResourcePersistMaxInt) {
		return
	}

	if err := s.repo.UpdateNodeResources(ctx, n.Name, res); err != nil {
		slog.Warn("persist node resources failed", slog.String("node", n.Name), slog.Any("error", err))
		return
	}

	s.resources.MarkPersisted(n.Name, res, now)
}
