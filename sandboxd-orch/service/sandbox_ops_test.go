package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"sandboxd-o/sandboxd-orch/client"
	"sandboxd-o/sandboxd-orch/config"
	"sandboxd-o/sandboxd-orch/types"
)

func newServiceWithNode(t *testing.T, sandboxd *httptest.Server) *Service {
	t.Helper()
	s, err := New(config.Config{
		SQLitePath:               filepath.Join(t.TempDir(), "orch.db"),
		ProbeTimeout:             time.Second,
		HeartbeatInterval:        time.Second,
		ResourceSyncInterval:     time.Second,
		ResourcePersistMinInt:    time.Millisecond,
		ResourcePersistMaxInt:    time.Second,
		ReadySuccessThreshold:    1,
		NotReadyFailureThreshold: 1,
		ShutdownTimeout:          time.Second,
		SchedulerInterval:        100 * time.Millisecond,
		ReconcileInterval:        100 * time.Millisecond,
		HostPortMin:              10000,
		HostPortMax:              10010,
	})
	if err != nil {
		t.Fatal(err)
	}

	u, _ := url.Parse(sandboxd.URL)
	port, _ := strconv.Atoi(u.Port())
	if _, err := s.RegisterNode(context.Background(), types.RegisterNodeRequest{Name: "n1", IP: u.Hostname(), Port: port}, "api"); err != nil {
		t.Fatal(err)
	}

	// Force ready+resource snapshot for deterministic scheduling.
	now := time.Now().UTC()
	_ = s.repo.UpdateHeartbeat(context.Background(), "n1", types.NodeStateReady, 1, 0, "", &now)
	_ = s.repo.UpdateNodeResources(context.Background(), "n1", types.NodeResources{
		CapacityCPUMilli:    4000,
		CapacityMemoryBytes: 8 * 1024 * 1024 * 1024,
		AllocatableCPUMilli: 3500,
		AllocatableMemory:   7 * 1024 * 1024 * 1024,
		AvailableCPUMilli:   3500,
		AvailableMemory:     7 * 1024 * 1024 * 1024,
	})

	return s
}

func TestSandboxCreateAndSchedule_DynamicPort(t *testing.T) {
	var createCalls int
	sbxNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes" {
			createCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "sbx-a"}})
			return
		}

		if r.Method == http.MethodDelete && r.URL.Path == "/v1/sandboxes/sbx-a" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "sbx-a"})
			return
		}

		http.NotFound(w, r)
	}))
	defer sbxNode.Close()

	s := newServiceWithNode(t, sbxNode)
	defer s.Close()

	_, err := s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "sbx-a",
		Spec: types.SandboxSpec{
			Egress: true,
			Ports:  []types.SandboxPortSpec{{ContainerPort: 80, Protocol: "tcp"}},
			Containers: []types.SandboxContainerSpec{{
				Name:     "web",
				Image:    "nginx",
				Resource: types.SandboxResource{CPU: "200m", Memory: "256Mi"},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	s.runSchedulerOnce(context.Background())
	got, err := s.GetSandbox(context.Background(), "sbx-a")
	if err != nil {
		t.Fatal(err)
	}

	if got.Status.Phase != types.SandboxPhaseRunning {
		t.Fatalf("phase=%s", got.Status.Phase)
	}

	if got.Status.NodeName != "n1" {
		t.Fatalf("node=%s", got.Status.NodeName)
	}

	if len(got.Status.AssignedPorts) != 1 || got.Status.AssignedPorts[0].HostPort < 10000 {
		t.Fatalf("assigned ports=%+v", got.Status.AssignedPorts)
	}

	if createCalls == 0 {
		t.Fatal("expected sandboxd create call")
	}
}

func TestScheduler_PortConflictToFailed(t *testing.T) {
	sbxNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes" {
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "ok"}})
			return
		}
		http.NotFound(w, r)
	}))
	defer sbxNode.Close()

	s := newServiceWithNode(t, sbxNode)
	defer s.Close()

	_, _ = s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "sbx-1",
		Spec: types.SandboxSpec{
			Ports:      []types.SandboxPortSpec{{HostPort: 10005, ContainerPort: 80, Protocol: "tcp"}},
			Containers: []types.SandboxContainerSpec{{Name: "c1", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
	})
	s.runSchedulerOnce(context.Background())

	_, _ = s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "sbx-2",
		Spec: types.SandboxSpec{
			Ports:      []types.SandboxPortSpec{{HostPort: 10005, ContainerPort: 8080, Protocol: "tcp"}},
			Containers: []types.SandboxContainerSpec{{Name: "c2", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
	})
	s.runSchedulerOnce(context.Background())

	got, err := s.GetSandbox(context.Background(), "sbx-2")
	if err != nil {
		t.Fatal(err)
	}

	if got.Status.Phase != types.SandboxPhaseFailed {
		t.Fatalf("phase=%s", got.Status.Phase)
	}
}

func TestSandboxReconcile_TTLDelete(t *testing.T) {
	deleteCalls := 0
	sbxNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes" {
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "sbx-ttl"}})
			return
		}

		if r.Method == http.MethodDelete && r.URL.Path == "/v1/sandboxes/sbx-ttl" {
			deleteCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "sbx-ttl"})
			return
		}

		http.NotFound(w, r)
	}))
	defer sbxNode.Close()

	s := newServiceWithNode(t, sbxNode)
	defer s.Close()

	_, err := s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "sbx-ttl",
		Spec: types.SandboxSpec{
			TTLSeconds: 1,
			Ports:      []types.SandboxPortSpec{{ContainerPort: 80}},
			Containers: []types.SandboxContainerSpec{{Name: "c", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.runSchedulerOnce(context.Background())

	sbx, err := s.GetSandbox(context.Background(), "sbx-ttl")
	if err != nil {
		t.Fatal(err)
	}

	past := time.Now().UTC().Add(-time.Second)
	sbx.Status.ExpireAt = &past
	if err := s.sbxRepo.UpdateSandboxStatus(context.Background(), sbx.ID, sbx.Status); err != nil {
		t.Fatal(err)
	}

	s.runSandboxReconcileOnce(context.Background())
	if deleteCalls == 0 {
		t.Fatal("expected delete call")
	}

	if _, err := s.GetSandbox(context.Background(), "sbx-ttl"); err == nil {
		t.Fatal("expected sandbox deleted")
	}
}

func TestScheduler_ScoringChoosesHigherAvailableCPUNode(t *testing.T) {
	var n1Creates, n2Creates int
	n1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes" {
			n1Creates++
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "x"}})
			return
		}

		http.NotFound(w, r)
	}))
	defer n1.Close()

	n2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes" {
			n2Creates++
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "x"}})
			return
		}

		http.NotFound(w, r)
	}))
	defer n2.Close()

	s := newServiceWithNode(t, n1)
	defer s.Close()

	u2, _ := url.Parse(n2.URL)
	p2, _ := strconv.Atoi(u2.Port())
	_, err := s.RegisterNode(context.Background(), types.RegisterNodeRequest{Name: "n2", IP: u2.Hostname(), Port: p2}, "api")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	_ = s.repo.UpdateHeartbeat(context.Background(), "n2", types.NodeStateReady, 1, 0, "", &now)
	_ = s.repo.UpdateNodeResources(context.Background(), "n1", types.NodeResources{AvailableCPUMilli: 1000, AvailableMemory: 2 * 1024 * 1024 * 1024})
	_ = s.repo.UpdateNodeResources(context.Background(), "n2", types.NodeResources{AvailableCPUMilli: 3000, AvailableMemory: 2 * 1024 * 1024 * 1024})

	_, err = s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "sbx-score",
		Spec: types.SandboxSpec{
			Ports:      []types.SandboxPortSpec{{ContainerPort: 80}},
			Containers: []types.SandboxContainerSpec{{Name: "c", Image: "nginx", Resource: types.SandboxResource{CPU: "500m", Memory: "64Mi"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	s.runSchedulerOnce(context.Background())

	got, err := s.GetSandbox(context.Background(), "sbx-score")
	if err != nil {
		t.Fatal(err)
	}

	if got.Status.NodeName != "n2" {
		t.Fatalf("expected n2 chosen, got=%s", got.Status.NodeName)
	}

	if n1Creates != 0 || n2Creates == 0 {
		t.Fatalf("unexpected create counts n1=%d n2=%d", n1Creates, n2Creates)
	}
}

func TestHostPortReleasedAfterDelete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes" {
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "ok"}})
			return
		}

		if r.Method == http.MethodDelete && (r.URL.Path == "/v1/sandboxes/sbx-del-1" || r.URL.Path == "/v1/sandboxes/sbx-del-2") {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "ok"})
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()
	s := newServiceWithNode(t, server)
	defer s.Close()

	_, _ = s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "sbx-del-1",
		Spec: types.SandboxSpec{
			Ports:      []types.SandboxPortSpec{{HostPort: 10006, ContainerPort: 80, Protocol: "tcp"}},
			Containers: []types.SandboxContainerSpec{{Name: "c1", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
	})

	s.runSchedulerOnce(context.Background())
	if err := s.DeleteSandbox(context.Background(), "sbx-del-1"); err != nil {
		t.Fatal(err)
	}

	_, _ = s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "sbx-del-2",
		Spec: types.SandboxSpec{
			Ports:      []types.SandboxPortSpec{{HostPort: 10006, ContainerPort: 8080, Protocol: "tcp"}},
			Containers: []types.SandboxContainerSpec{{Name: "c2", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
	})

	s.runSchedulerOnce(context.Background())
	got, err := s.GetSandbox(context.Background(), "sbx-del-2")
	if err != nil {
		t.Fatal(err)
	}

	if got.Status.Phase != types.SandboxPhaseRunning {
		t.Fatalf("expected running after reuse of released port, got=%s", got.Status.Phase)
	}
}

func TestReconcile_DeletingPhaseFinalized(t *testing.T) {
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes" {
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "x"}})
			return
		}

		if r.Method == http.MethodDelete && r.URL.Path == "/v1/sandboxes/sbx-del-phase" {
			deletes++
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "x"})
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()
	s := newServiceWithNode(t, server)
	defer s.Close()

	_, _ = s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "sbx-del-phase",
		Spec: types.SandboxSpec{
			Ports:      []types.SandboxPortSpec{{ContainerPort: 80}},
			Containers: []types.SandboxContainerSpec{{Name: "c", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
	})
	s.runSchedulerOnce(context.Background())
	sbx, _ := s.GetSandbox(context.Background(), "sbx-del-phase")
	sbx.Status.Phase = types.SandboxPhaseDeleting
	_ = s.sbxRepo.UpdateSandboxStatus(context.Background(), sbx.ID, sbx.Status)

	s.runSandboxReconcileOnce(context.Background())

	if deletes == 0 {
		t.Fatal("expected delete request")
	}

	if _, err := s.GetSandbox(context.Background(), "sbx-del-phase"); err == nil {
		t.Fatal("expected deleted sandbox")
	}
}

func TestCreateSandboxValidationAndHostPortRangeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()
	s := newServiceWithNode(t, server)
	defer s.Close()

	_, err := s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID:   "",
		Spec: types.SandboxSpec{},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input, got=%v", err)
	}

	_, err = s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "bad-cpu",
		Spec: types.SandboxSpec{
			Containers: []types.SandboxContainerSpec{{Name: "c", Image: "nginx", Resource: types.SandboxResource{CPU: "100x", Memory: "64Mi"}}},
		},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid cpu input, got=%v", err)
	}

	_, err = s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "bad-range",
		Spec: types.SandboxSpec{
			Ports:      []types.SandboxPortSpec{{HostPort: 9999, ContainerPort: 80}},
			Containers: []types.SandboxContainerSpec{{Name: "c", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	s.runSchedulerOnce(context.Background())

	got, err := s.GetSandbox(context.Background(), "bad-range")
	if err != nil {
		t.Fatal(err)
	}

	if got.Status.Phase != types.SandboxPhaseFailed {
		t.Fatalf("expected failed for out-of-range hostport, got=%s", got.Status.Phase)
	}

	_, err = s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "bad-ttl",
		Spec: types.SandboxSpec{
			TTLSeconds: -1,
			Containers: []types.SandboxContainerSpec{{Name: "c", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid ttl input, got=%v", err)
	}
}

func TestDeleteNode_MarksScheduledOrRunningSandboxFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes" {
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "ok"}})
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/sandboxes/sbx-node-del" {
			_ = json.NewEncoder(w).Encode(map[string]any{"deleted": "sbx-node-del"})
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/v1/reconcile" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()
	s := newServiceWithNode(t, server)
	defer s.Close()

	_, _ = s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "sbx-node-del",
		Spec: types.SandboxSpec{
			Ports:      []types.SandboxPortSpec{{HostPort: 10007, ContainerPort: 80, Protocol: "tcp"}},
			Containers: []types.SandboxContainerSpec{{Name: "c", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
	})

	s.runSchedulerOnce(context.Background())

	if err := s.DeleteNode(context.Background(), "n1"); err != nil {
		t.Fatalf("delete node err=%v", err)
	}

	got, err := s.GetSandbox(context.Background(), "sbx-node-del")
	if err != nil {
		t.Fatal(err)
	}

	if got.Status.Phase != types.SandboxPhaseFailed {
		t.Fatalf("phase=%s", got.Status.Phase)
	}

	if got.Status.NodeName != "" {
		t.Fatalf("node expected cleared, got=%s", got.Status.NodeName)
	}

	if len(got.Status.AssignedPorts) != 0 {
		t.Fatalf("assigned ports expected released, got=%v", got.Status.AssignedPorts)
	}

	used, err := s.sbxRepo.NodeUsedHostPorts(context.Background(), "n1")
	if err != nil {
		t.Fatalf("NodeUsedHostPorts err=%v", err)
	}

	if len(used) != 0 {
		t.Fatalf("node reserved ports should be released, got=%v", used)
	}
}

func TestDeleteNode_ForceBehaviorOnNodeAPIFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes" {
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "sbx-force"}})
			return
		}

		if r.Method == http.MethodDelete && r.URL.Path == "/v1/sandboxes/sbx-force" {
			http.Error(w, "delete failed", http.StatusInternalServerError)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/v1/reconcile" {
			http.Error(w, "reconcile failed", http.StatusInternalServerError)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	s := newServiceWithNode(t, server)
	defer s.Close()

	_, _ = s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "sbx-force",
		Spec: types.SandboxSpec{
			Ports:      []types.SandboxPortSpec{{HostPort: 10008, ContainerPort: 80, Protocol: "tcp"}},
			Containers: []types.SandboxContainerSpec{{Name: "c", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
	})
	s.runSchedulerOnce(context.Background())

	if err := s.DeleteNode(context.Background(), "n1"); err == nil {
		t.Fatal("expected delete to fail when node API delete fails without force")
	}

	if _, err := s.GetNode(context.Background(), "n1"); err != nil {
		t.Fatalf("node should remain after failed delete: %v", err)
	}

	if err := s.DeleteNodeForce(context.Background(), "n1", true); err != nil {
		t.Fatalf("force delete err=%v", err)
	}

	if _, err := s.GetNode(context.Background(), "n1"); err == nil {
		t.Fatal("expected node removed after force delete")
	}
}

func TestListAndTriggerReconcile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "sbx-trg"}})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/sandboxes/sbx-trg":
			_ = json.NewEncoder(w).Encode(map[string]any{"deleted": "sbx-trg"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}
	}))
	defer server.Close()

	s := newServiceWithNode(t, server)
	defer s.Close()

	_, err := s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "sbx-trg",
		Spec: types.SandboxSpec{
			Containers: []types.SandboxContainerSpec{
				{Name: "c1", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	s.runSchedulerOnce(context.Background())

	items, err := s.ListSandboxes(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(items) == 0 {
		t.Fatal("expected list items")
	}

	if err := s.TriggerSandboxReconcile(context.Background(), "sbx-trg"); err != nil {
		t.Fatal(err)
	}
}

func TestStartLoops(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "resources": map[string]any{"capacity_cpu_milli": 1000}})
	}))
	defer server.Close()

	s := newServiceWithNode(t, server)
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	s.StartSchedulerLoop(ctx)
	s.StartSandboxReconcileLoop(ctx)
	time.Sleep(120 * time.Millisecond)
	cancel()
}

func TestDeleteSandbox_WhenNodeAlreadyRemoved_CleansLocally(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes" {
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "ok"}})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	s := newServiceWithNode(t, server)
	defer s.Close()

	_, _ = s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "sbx-gone-node",
		Spec: types.SandboxSpec{
			Ports:      []types.SandboxPortSpec{{HostPort: 10008, ContainerPort: 80, Protocol: "tcp"}},
			Containers: []types.SandboxContainerSpec{{Name: "c", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
	})
	s.runSchedulerOnce(context.Background())

	if err := s.repo.DeleteNode(context.Background(), "n1"); err != nil {
		t.Fatalf("delete node direct err=%v", err)
	}

	if err := s.DeleteSandbox(context.Background(), "sbx-gone-node"); err != nil {
		t.Fatalf("delete sandbox should succeed even when node gone: %v", err)
	}

	if _, err := s.GetSandbox(context.Background(), "sbx-gone-node"); err == nil {
		t.Fatal("expected deleted sandbox")
	}
}

func TestDeleteSandbox_EmptyID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	s := newServiceWithNode(t, server)
	defer s.Close()

	if err := s.DeleteSandbox(context.Background(), " "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input, got=%v", err)
	}
}

func TestFinalizeSandboxDelete_ReleasePortsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()

	s := newServiceWithNode(t, server)
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.finalizeSandboxDelete(ctx, types.Sandbox{
		ID: "sbx-release-fail",
		Status: types.SandboxStatus{
			Phase: types.SandboxPhaseDeleting,
		},
	})
	if err == nil {
		t.Fatal("expected release ports error on canceled context")
	}
}

func TestMergeSandboxPhaseWithNodeState(t *testing.T) {
	t.Run("running clears error", func(t *testing.T) {
		cur := types.SandboxStatus{Phase: types.SandboxPhaseScheduled, LastError: "old"}
		next, changed := mergeSandboxPhaseWithNodeState(cur, clientStatus("s1", "running", "", nil))
		if !changed || next.Phase != types.SandboxPhaseRunning || next.LastError != "" {
			t.Fatalf("unexpected next=%+v changed=%v", next, changed)
		}
	})

	t.Run("sandbox error to failed", func(t *testing.T) {
		cur := types.SandboxStatus{Phase: types.SandboxPhaseRunning}
		next, changed := mergeSandboxPhaseWithNodeState(cur, clientStatus("s1", "error", "pull failed", nil))
		if !changed || next.Phase != types.SandboxPhaseFailed || next.LastError == "" {
			t.Fatalf("unexpected next=%+v changed=%v", next, changed)
		}
	})

	t.Run("container unhealthy to failed", func(t *testing.T) {
		cur := types.SandboxStatus{Phase: types.SandboxPhaseRunning}
		next, changed := mergeSandboxPhaseWithNodeState(cur, clientStatus("s1", "running", "", []containerStatus{{Name: "c1", Phase: "stopped"}}))
		if !changed || next.Phase != types.SandboxPhaseFailed {
			t.Fatalf("unexpected next=%+v changed=%v", next, changed)
		}
	})

	t.Run("creating from running goes scheduled", func(t *testing.T) {
		cur := types.SandboxStatus{Phase: types.SandboxPhaseRunning}
		next, changed := mergeSandboxPhaseWithNodeState(cur, clientStatus("s1", "creating", "", nil))
		if !changed || next.Phase != types.SandboxPhaseScheduled {
			t.Fatalf("unexpected next=%+v changed=%v", next, changed)
		}
	})

	t.Run("unknown phase no change", func(t *testing.T) {
		cur := types.SandboxStatus{Phase: types.SandboxPhaseScheduled}
		next, changed := mergeSandboxPhaseWithNodeState(cur, clientStatus("s1", "unknown", "", nil))
		if changed || next.Phase != cur.Phase || next.LastError != cur.LastError {
			t.Fatalf("unexpected next=%+v changed=%v", next, changed)
		}
	})
}

func TestRunSandboxStatusSyncOnce_MarksMissingFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case r.URL.Path == "/v1/node/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "resources": map[string]any{"capacity_cpu_milli": 1000}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/statuses":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "missing": []string{"sbx-missing", "sbx-scheduled-missing"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	s := newServiceWithNode(t, server)
	defer s.Close()
	s.cfg.StatusSyncBatchSize = 50
	s.cfg.StatusSyncInterval = time.Second

	now := time.Now().UTC()
	if err := s.sbxRepo.CreateSandbox(context.Background(), types.Sandbox{
		ID: "sbx-missing",
		Spec: types.SandboxSpec{
			Containers: []types.SandboxContainerSpec{{Name: "c1", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
		Status:    types.SandboxStatus{Phase: types.SandboxPhaseRunning, NodeName: "n1"},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	s.runSandboxStatusSyncOnce(context.Background())
	got, err := s.GetSandbox(context.Background(), "sbx-missing")
	if err != nil {
		t.Fatal(err)
	}

	if got.Status.Phase != types.SandboxPhaseFailed {
		t.Fatalf("phase=%s", got.Status.Phase)
	}

	if got.Status.LastError != "deleted on sbxlet node" {
		t.Fatalf("last_error=%q", got.Status.LastError)
	}

	scheduled := types.Sandbox{
		ID: "sbx-scheduled-missing",
		Spec: types.SandboxSpec{
			Containers: []types.SandboxContainerSpec{{Name: "c1", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
		Status:    types.SandboxStatus{Phase: types.SandboxPhaseScheduled, NodeName: "n1"},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.sbxRepo.CreateSandbox(context.Background(), scheduled); err != nil {
		t.Fatal(err)
	}

	s.runSandboxStatusSyncOnce(context.Background())
	keep, err := s.GetSandbox(context.Background(), "sbx-scheduled-missing")
	if err != nil {
		t.Fatal(err)
	}

	if keep.Status.Phase != types.SandboxPhaseScheduled {
		t.Fatalf("scheduled sandbox must not be failed by missing during create window, got=%s", keep.Status.Phase)
	}
}

func TestRunSandboxStatusSyncOnce_BatchAndStatusUpdate(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case r.URL.Path == "/v1/node/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "external_ip": "203.0.113.10", "resources": map[string]any{"capacity_cpu_milli": 1000}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes/statuses":
			calls++
			if calls == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"external_ip": "203.0.113.10",
					"items": []map[string]any{
						{"id": "sbx-run", "phase": "running"},
					},
					"missing": []string{},
				})
				return
			}

			if calls == 2 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"external_ip": "203.0.113.10",
					"items": []map[string]any{
						{
							"id":    "sbx-err",
							"phase": "error",
							"error": "sandbox error",
							"unhealthy_containers": []map[string]any{
								{"name": "app", "phase": "error", "error": "image pull failed"},
							},
						},
					},
					"missing": []string{},
				})

				return
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"external_ip": "203.0.113.10",
				"items": []map[string]any{
					{"id": "sbx-recover", "phase": "running"},
				},
				"missing": []string{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	s := newServiceWithNode(t, server)
	defer s.Close()
	s.cfg.StatusSyncBatchSize = 1

	now := time.Now().UTC()
	makeSandbox := func(id string) types.Sandbox {
		return types.Sandbox{
			ID: id,
			Spec: types.SandboxSpec{
				Containers: []types.SandboxContainerSpec{{Name: "app", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
			},
			Status:    types.SandboxStatus{Phase: types.SandboxPhaseScheduled, NodeName: "n1"},
			CreatedAt: now,
			UpdatedAt: now,
		}
	}

	if err := s.sbxRepo.CreateSandbox(context.Background(), makeSandbox("sbx-run")); err != nil {
		t.Fatal(err)
	}

	if err := s.sbxRepo.CreateSandbox(context.Background(), makeSandbox("sbx-err")); err != nil {
		t.Fatal(err)
	}

	failed := makeSandbox("sbx-recover")
	failed.Status.Phase = types.SandboxPhaseFailed
	failed.Status.LastError = "container app unhealthy (creating)"
	if err := s.sbxRepo.CreateSandbox(context.Background(), failed); err != nil {
		t.Fatal(err)
	}

	deleting := makeSandbox("sbx-del-guard")
	deleting.Status.Phase = types.SandboxPhaseDeleting
	deleting.Status.LastError = "manual delete in progress"
	if err := s.sbxRepo.CreateSandbox(context.Background(), deleting); err != nil {
		t.Fatal(err)
	}

	s.runSandboxStatusSyncOnce(context.Background())

	if calls != 3 {
		t.Fatalf("expected 3 batched calls, got=%d", calls)
	}

	run, _ := s.GetSandbox(context.Background(), "sbx-run")
	if run.Status.Phase != types.SandboxPhaseRunning {
		t.Fatalf("sbx-run phase=%s", run.Status.Phase)
	}
	if run.Status.ExternalIP != "203.0.113.10" {
		t.Fatalf("sbx-run external_ip=%q", run.Status.ExternalIP)
	}

	errSbx, _ := s.GetSandbox(context.Background(), "sbx-err")
	if errSbx.Status.Phase != types.SandboxPhaseFailed {
		t.Fatalf("sbx-err phase=%s", errSbx.Status.Phase)
	}

	if errSbx.Status.LastError == "" {
		t.Fatal("expected failed reason for sbx-err")
	}

	recovered, _ := s.GetSandbox(context.Background(), "sbx-recover")
	if recovered.Status.Phase != types.SandboxPhaseRunning {
		t.Fatalf("sbx-recover phase=%s", recovered.Status.Phase)
	}
	if recovered.Status.LastError != "" {
		t.Fatalf("sbx-recover last_error=%q", recovered.Status.LastError)
	}

	deletingAfter, _ := s.GetSandbox(context.Background(), "sbx-del-guard")
	if deletingAfter.Status.Phase != types.SandboxPhaseDeleting {
		t.Fatalf("sbx-del-guard phase=%s", deletingAfter.Status.Phase)
	}

	if deletingAfter.Status.LastError != "manual delete in progress" {
		t.Fatalf("sbx-del-guard last_error=%q", deletingAfter.Status.LastError)
	}
}

func TestFindMissing(t *testing.T) {
	if ok := findMissing([]string{"a", "b"}, "c"); ok {
		t.Fatal("expected not found")
	}

	if ok := findMissing([]string{"a", "b"}, "b"); !ok {
		t.Fatal("expected found")
	}
}

func TestInferSandboxExternalIP(t *testing.T) {
	if got := inferSandboxExternalIP(" 203.0.113.10 ", "198.51.100.2"); got != "203.0.113.10" {
		t.Fatalf("prefer response ip, got=%q", got)
	}
	if got := inferSandboxExternalIP(" ", " 198.51.100.2 "); got != "198.51.100.2" {
		t.Fatalf("fallback ip, got=%q", got)
	}
}

type containerStatus struct {
	Name  string
	Phase string
	Error string
}

func clientStatus(id, phase, err string, containers []containerStatus) client.SandboxSyncStatus {
	out := client.SandboxSyncStatus{
		ID:    id,
		Phase: phase,
		Error: err,
	}

	for _, c := range containers {
		out.UnhealthyContainers = append(out.UnhealthyContainers, client.ContainerSyncStatus{
			Name:  c.Name,
			Phase: c.Phase,
			Error: c.Error,
		})
	}

	return out
}
