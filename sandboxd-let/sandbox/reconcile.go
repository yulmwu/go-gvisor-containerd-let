package sandbox

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"sandboxd-o/sandboxd-let/model"
	"sandboxd-o/sandboxd-let/network"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ReconcileOnce aligns state with runtime/network.
func (s *Service) ReconcileOnce(ctx context.Context) error {
	s.dbg("reconcile start")
	ctx = namespaces.WithNamespace(ctx, s.namespace)
	stateList, err := s.store.List()
	if err != nil {
		return err
	}

	stateMap := map[string]*model.Sandbox{}
	for _, sbx := range stateList {
		stateMap[sbx.ID] = sbx
	}

	for _, sbx := range stateList {
		if s.isSandboxLockHeld(sbx.ID) {
			continue
		}

		if sbx.Phase == SandboxPhaseDeleting {
			continue
		}

		if sbx.Phase == SandboxPhaseCreating && time.Since(sbx.CreatedAt) < 2*time.Minute {
			// During initial provisioning window, avoid treating transient
			// not-yet-created runtime/container state as unhealthy.
			continue
		}

		if sbx.Phase == SandboxPhaseCreating && time.Since(sbx.CreatedAt) >= 2*time.Minute {
			// Prevent indefinite creating state if provisioning goroutine is stuck.
			setSandboxPhase(sbx, SandboxPhaseError, "provisioning timeout")
			_ = s.store.Save(sbx)
			s.dbg("reconcile timeout -> error sandbox=%s", sbx.ID)
			continue
		}

		exists := false
		if sbx.PauseID != "" {
			if _, err := s.cri.podSandboxStatus(ctx, sbx.PauseID); err == nil {
				exists = true
			}
		}

		if !exists {
			if s.shouldFinalizeUnhealthy(sbx.ID) {
				s.dbg("reconcile finalize missing runtime sandbox=%s", sbx.ID)
				_ = s.deleteSandboxFromState(ctx, sbx)
				s.clearUnhealthy(sbx.ID)
			}
			continue
		}

		healthy := true
		for _, c := range sbx.Containers {
			if !s.isContainerRunning(ctx, c.ID) {
				healthy = false
				break
			}
		}

		if !healthy {
			if s.shouldFinalizeUnhealthy(sbx.ID) {
				s.dbg("reconcile finalize unhealthy sandbox=%s", sbx.ID)
				_ = s.deleteSandboxFromState(ctx, sbx)
				s.clearUnhealthy(sbx.ID)
			}
			continue
		}

		s.clearUnhealthy(sbx.ID)
	}

	keep := map[string]struct{}{}
	for _, sbx := range stateList {
		keep[sbx.ID] = struct{}{}
	}

	network.DeleteOrphanHostPortDNAT(s.ipt, keep)
	s.dbg("reconcile done state=%d", len(stateList))

	return nil
}

// StartReconcileLoop runs reconcile asynchronously until ctx is canceled.
func (s *Service) StartReconcileLoop(ctx context.Context) {
	go func() {
		t := time.NewTicker(s.cfg.ReconcileInterval)
		defer t.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = s.ReconcileOnce(ctx)
			}
		}
	}()
}

func (s *Service) ListRuntimeSandboxIDs(ctx context.Context) ([]string, error) {
	ctx = namespaces.WithNamespace(ctx, s.namespace)
	pods, err := s.listCRISandboxIDs(ctx)
	if err != nil {
		return nil, err
	}

	ids := map[string]struct{}{}
	for _, id := range pods {
		if id == "" {
			continue
		}

		st, err := s.cri.podSandboxStatus(ctx, id)
		if err != nil {
			continue
		}

		md := st.GetMetadata()
		if md != nil && (strings.HasPrefix(md.GetUid(), "sbx-") || strings.HasPrefix(md.GetName(), "sbx-")) {
			sbxID := strings.TrimSpace(md.GetUid())
			if sbxID == "" {
				sbxID = strings.TrimSpace(md.GetName())
			}

			if sbxID != "" {
				ids[sbxID] = struct{}{}
			}
		}
	}

	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}

	sort.Strings(out)
	return out, nil
}

func (s *Service) IsSandboxHealthy(ctx context.Context, sandboxID string) (bool, error) {
	ctx = namespaces.WithNamespace(ctx, s.namespace)
	sbx, err := s.store.Load(sandboxID)
	if err != nil {
		return false, err
	}

	for _, st := range sbx.Containers {
		details, err := s.cri.containerStatus(ctx, st.ID)
		if err != nil || details.State != runtimeapi.ContainerState_CONTAINER_RUNNING {
			return false, nil
		}
	}

	return true, nil
}

func (s *Service) CleanupSandboxResources(ctx context.Context, sandboxID string) error {
	ctx = namespaces.WithNamespace(ctx, s.namespace)
	sbx, err := s.store.Load(sandboxID)
	if err == nil {
		return s.deleteSandboxFromState(ctx, sbx)
	}

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return s.cleanupOrphanRuntimeSandbox(ctx, sandboxID)
}

func (s *Service) cleanupOrphanRuntimeSandbox(ctx context.Context, sandboxID string) error {
	tmp := &model.Sandbox{ID: sandboxID, Namespace: s.namespace, IP: s.sandboxIPFromCNICache(sandboxID), BridgeName: s.bridgeIF, Containers: map[string]model.ContainerState{}}
	if podID, ok := s.findManagedPodSandboxID(ctx, sandboxID); ok {
		tmp.PauseID = podID
	}

	if len(tmp.Containers) == 0 && tmp.IP == "" && tmp.PauseID == "" {
		// Even without runtime/cni artifacts, DNAT rules may remain after partial failure.
		s.cleanupHostPortPublish(tmp)
		return nil
	}

	return s.deleteSandboxFromState(ctx, tmp)
}

func (s *Service) sandboxIPFromCNICache(sandboxID string) string {
	ip, err := network.LookupSandboxIPv4FromResultCache(sandboxID)
	if err != nil {
		return ""
	}

	return ip
}

func (s *Service) isContainerRunning(ctx context.Context, id string) bool {
	details, err := s.cri.containerStatus(ctx, id)
	return err == nil && details.State == runtimeapi.ContainerState_CONTAINER_RUNNING
}

func (s *Service) shouldFinalizeUnhealthy(sandboxID string) bool {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()

	now := time.Now()
	if _, ok := s.unhealthySince[sandboxID]; !ok {
		s.unhealthySince[sandboxID] = now
		s.unhealthyHits[sandboxID] = 1
		return false
	}

	s.unhealthyHits[sandboxID]++
	if now.Sub(s.unhealthySince[sandboxID]) < s.cfg.ReconcileGrace {
		return false
	}

	return s.unhealthyHits[sandboxID] >= s.cfg.ReconcileHits
}

func (s *Service) clearUnhealthy(sandboxID string) {
	s.reconcileMu.Lock()
	delete(s.unhealthySince, sandboxID)
	delete(s.unhealthyHits, sandboxID)
	s.reconcileMu.Unlock()
}
