package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sandboxd-o/sandboxd-orch/cache"
	"sandboxd-o/sandboxd-orch/config"
	"sandboxd-o/sandboxd-orch/types"
)

type fakeRepo struct {
	mu               sync.Mutex
	nodes            map[string]types.Node
	heartbeatUpdates int
	resourceUpdates  int
	listErr          error
}

func newFakeRepo(nodes ...types.Node) *fakeRepo {
	m := make(map[string]types.Node, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n
	}

	return &fakeRepo{nodes: m}
}

func (r *fakeRepo) Close() error { return nil }

func (r *fakeRepo) UpsertNode(ctx context.Context, name, ip string, port int, source string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := r.nodes[name]
	n.ID, n.IP, n.Port, n.Source = name, ip, port, source
	n.SbxletBaseURL = "http://" + ip + ":" + "18080"
	r.nodes[name] = n
	return nil
}

func (r *fakeRepo) DeleteNode(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.nodes, name)
	return nil
}

func (r *fakeRepo) GetNode(ctx context.Context, name string) (*types.Node, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n, ok := r.nodes[name]
	if !ok {
		return nil, sql.ErrNoRows
	}

	return &n, nil
}

func (r *fakeRepo) ListNodes(ctx context.Context) ([]types.Node, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]types.Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, n)
	}

	return out, nil
}

func (r *fakeRepo) UpdateHeartbeat(ctx context.Context, name string, state types.NodeState, successStreak, failureStreak int, lastError string, beatAt *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := r.nodes[name]
	n.State = state
	n.SuccessStreak = successStreak
	n.FailureStreak = failureStreak
	n.LastError = lastError
	n.LastHeartbeat = beatAt
	r.nodes[name] = n
	r.heartbeatUpdates++

	return nil
}

func (r *fakeRepo) UpdateNodeResources(ctx context.Context, name string, res types.NodeResources) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := r.nodes[name]
	n.Resources = res
	r.nodes[name] = n
	r.resourceUpdates++

	return nil
}

func (r *fakeRepo) AdjustNodeResourceUsage(ctx context.Context, name string, cpuMilliDelta, memBytesDelta int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := r.nodes[name]
	n.Resources.UsedCPUMilli += cpuMilliDelta
	if n.Resources.UsedCPUMilli < 0 {
		n.Resources.UsedCPUMilli = 0
	}

	n.Resources.UsedMemoryBytes += memBytesDelta
	if n.Resources.UsedMemoryBytes < 0 {
		n.Resources.UsedMemoryBytes = 0
	}

	n.Resources.AvailableCPUMilli = max(n.Resources.AllocatableCPUMilli-n.Resources.UsedCPUMilli, 0)

	n.Resources.AvailableMemory = max(n.Resources.AllocatableMemory-n.Resources.UsedMemoryBytes, 0)

	r.nodes[name] = n
	return nil
}

func (r *fakeRepo) SetNodeExternal(ctx context.Context, externalID, nodeID, external string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.nodes[nodeID]
	n.Resources.External = external
	r.nodes[nodeID] = n
	return nil
}

func (r *fakeRepo) DeleteNodeExternal(ctx context.Context, nodeID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.nodes[nodeID]
	n.Resources.External = "(none)"
	r.nodes[nodeID] = n
	return nil
}

func (r *fakeRepo) DeleteExternal(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for nodeID, n := range r.nodes {
		if "ext-"+nodeID == id {
			n.Resources.External = "(none)"
			r.nodes[nodeID] = n
			return nil
		}
	}

	return sql.ErrNoRows
}

func (r *fakeRepo) ListExternals(ctx context.Context) ([]types.External, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]types.External, 0, len(r.nodes))
	for id, n := range r.nodes {
		if strings.TrimSpace(n.Resources.External) == "" || n.Resources.External == "(none)" {
			continue
		}
		out = append(out, types.External{ID: "ext-" + id, NodeID: id, External: n.Resources.External})
	}

	return out, nil
}

func (r *fakeRepo) GetExternal(ctx context.Context, id string) (*types.External, error) {
	all, _ := r.ListExternals(ctx)
	for _, ex := range all {
		if ex.ID == id {
			return &ex, nil
		}
	}

	return nil, sql.ErrNoRows
}

func testSvc(repo *fakeRepo, cfg config.Config) *Service {
	return &Service{cfg: cfg, repo: repo, resources: cache.NewResourceCache()}
}

func TestServiceNodeOpsAndGetters(t *testing.T) {
	r := newFakeRepo()
	s := testSvc(r, config.Config{HTTPAddr: ":8082", ShutdownTimeout: 3 * time.Second, ProbeTimeout: 100 * time.Millisecond, ReadySuccessThreshold: 2, NotReadyFailureThreshold: 2})

	if s.HTTPAddr() != ":8082" {
		t.Fatalf("addr=%s", s.HTTPAddr())
	}

	if s.ShutdownTimeout() != 3*time.Second {
		t.Fatalf("shutdown timeout=%s", s.ShutdownTimeout())
	}

	if _, err := s.RegisterNode(context.Background(), types.RegisterNodeRequest{ID: "n1", IP: "127.0.0.1", Port: 18080}, "api"); err != nil {
		t.Fatalf("register err=%v", err)
	}

	if _, err := s.GetNode(context.Background(), "n1"); err != nil {
		t.Fatalf("get err=%v", err)
	}

	if _, _, err := s.SandboxClientForNode(context.Background(), "n1"); err != nil {
		t.Fatalf("client for node err=%v", err)
	}

	if err := s.DeleteNode(context.Background(), "n1"); err != nil {
		t.Fatalf("delete err=%v", err)
	}

	if err := s.DeleteNode(context.Background(), " "); err == nil {
		t.Fatal("expected invalid input")
	}
}

func TestCreateNodeAndExternalObjects(t *testing.T) {
	r := newFakeRepo()
	s := testSvc(r, config.Config{ProbeTimeout: 10 * time.Millisecond, ReadySuccessThreshold: 1, NotReadyFailureThreshold: 1})
	if _, err := s.CreateNodeObject(context.Background(), types.CreateNodeObjectRequest{
		ID: "n-ext",
		Spec: types.NodeObjectSpec{
			IP:   "127.0.0.1",
			Port: 8081,
		},
	}); err != nil {
		t.Fatalf("CreateNodeObject err=%v", err)
	}
	if err := s.UpsertExternalObject(context.Background(), types.CreateExternalObjectRequest{
		ID: "ext-1",
		Spec: types.ExternalObjectSpec{
			NodeID:   "n-ext",
			External: "host1.swua.kr",
		},
	}); err != nil {
		t.Fatalf("UpsertExternalObject err=%v", err)
	}

	n, _ := r.GetNode(context.Background(), "n-ext")
	if n.Resources.External != "host1.swua.kr" {
		t.Fatalf("external=%q", n.Resources.External)
	}

	if err := s.UpsertExternalObject(context.Background(), types.CreateExternalObjectRequest{}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestExternalServiceOps(t *testing.T) {
	r := newFakeRepo()
	s := testSvc(r, config.Config{ProbeTimeout: 10 * time.Millisecond, ReadySuccessThreshold: 1, NotReadyFailureThreshold: 1})
	if _, err := s.CreateNodeObject(context.Background(), types.CreateNodeObjectRequest{
		ID: "n1", Spec: types.NodeObjectSpec{IP: "127.0.0.1", Port: 8081},
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.UpsertExternalObject(context.Background(), types.CreateExternalObjectRequest{
		ID: "ext-n1", Spec: types.ExternalObjectSpec{NodeID: "n1", External: "host1.swua.kr"},
	}); err != nil {
		t.Fatal(err)
	}

	items, err := s.ListExternals(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(items) != 1 || items[0].ID != "ext-n1" {
		t.Fatalf("items=%+v", items)
	}

	got, err := s.GetExternal(context.Background(), "ext-n1")
	if err != nil {
		t.Fatal(err)
	}

	if got.External != "host1.swua.kr" {
		t.Fatalf("external=%q", got.External)
	}

	if err := s.DeleteExternal(context.Background(), "ext-n1"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.GetExternal(context.Background(), "ext-n1"); err == nil {
		t.Fatal("expected not found")
	}

	if _, err := s.GetExternal(context.Background(), " "); err == nil {
		t.Fatal("expected invalid input for empty id")
	}

	if err := s.DeleteExternal(context.Background(), " "); err == nil {
		t.Fatal("expected invalid input for empty id delete")
	}
}

func TestProbeNodeStateTransitionsAndResourceSync(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/v1/node/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "resources": map[string]any{"capacity_cpu_milli": 1000, "allocatable_cpu_milli": 900, "external": "203.0.113.10"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ready.Close()

	n := types.Node{
		ID:            "n1",
		SbxletBaseURL: ready.URL,
		State:         types.NodeStateUnknown,
		Resources:     types.NodeResources{External: "host1.swua.kr"},
	}
	r := newFakeRepo(n)
	s := testSvc(r, config.Config{ProbeTimeout: time.Second, ReadySuccessThreshold: 2, NotReadyFailureThreshold: 2, ResourcePersistMinInt: time.Hour, ResourcePersistMaxInt: 2 * time.Hour})

	s.probeNode(context.Background(), n)
	cur, _ := r.GetNode(context.Background(), "n1")
	s.probeNode(context.Background(), *cur)
	got, _ := r.GetNode(context.Background(), "n1")
	if got.State != types.NodeStateReady {
		t.Fatalf("state=%s", got.State)
	}

	s.syncNodeResources(context.Background(), n)
	s.syncNodeResources(context.Background(), n)
	r.mu.Lock()
	updates := r.resourceUpdates
	r.mu.Unlock()
	if updates != 1 {
		t.Fatalf("resource updates=%d want=1", updates)
	}

	gotAfterSync, _ := r.GetNode(context.Background(), "n1")
	if gotAfterSync.Resources.External != "host1.swua.kr" {
		t.Fatalf("external=%q", gotAfterSync.Resources.External)
	}

	bad := types.Node{ID: "n2", SbxletBaseURL: "http://127.0.0.1:1", State: types.NodeStateUnknown}
	r.nodes[bad.ID] = bad
	s.probeNode(context.Background(), bad)
	curBad, _ := r.GetNode(context.Background(), "n2")
	s.probeNode(context.Background(), *curBad)
	gotBad, _ := r.GetNode(context.Background(), "n2")
	if gotBad.State != types.NodeStateNotReady {
		t.Fatalf("bad state=%s", gotBad.State)
	}
}

func TestLoopsAndForEachNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/v1/node/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "resources": map[string]any{"capacity_cpu_milli": 1000}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	r := newFakeRepo(
		types.Node{ID: "n1", SbxletBaseURL: server.URL},
		types.Node{ID: "n2", SbxletBaseURL: server.URL},
	)
	s := testSvc(r, config.Config{
		ProbeTimeout:             200 * time.Millisecond,
		HeartbeatParallel:        true,
		HeartbeatMaxParallel:     2,
		HeartbeatInterval:        40 * time.Millisecond,
		ResourceSyncInterval:     40 * time.Millisecond,
		ResourcePersistMinInt:    10 * time.Millisecond,
		ResourcePersistMaxInt:    20 * time.Millisecond,
		ReadySuccessThreshold:    1,
		NotReadyFailureThreshold: 1,
	})

	s.runHeartbeatOnce(context.Background())
	s.runResourceSyncOnce(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	s.StartHeartbeatLoop(ctx)
	s.StartResourceSyncLoop(ctx)
	time.Sleep(120 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	r.mu.Lock()
	hb := r.heartbeatUpdates
	rs := r.resourceUpdates
	r.mu.Unlock()
	if hb == 0 || rs == 0 {
		t.Fatalf("expected loop updates hb=%d rs=%d", hb, rs)
	}

	var count atomic.Int32
	s.forEachNode(context.Background(), []types.Node{{ID: "a"}, {ID: "b"}}, func(context.Context, types.Node) { count.Add(1) })
	if count.Load() != 2 {
		t.Fatalf("count=%d", count.Load())
	}

	r.listErr = errors.New("list fail")
	s.runHeartbeatOnce(context.Background())
	s.runResourceSyncOnce(context.Background())
}

func TestDeleteNodeForce_SkipsNodeAPICalls(t *testing.T) {
	var deleteCalls atomic.Int32
	var reconcileCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes" {
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "sbx-force-node-ops"}})
			return
		}

		if r.Method == http.MethodDelete && r.URL.Path == "/v1/sandboxes/sbx-force-node-ops" {
			deleteCalls.Add(1)
			http.Error(w, "forced failure", http.StatusInternalServerError)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/v1/reconcile" {
			reconcileCalls.Add(1)
			http.Error(w, "forced failure", http.StatusInternalServerError)
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	s := newServiceWithNode(t, server)
	defer s.Close()

	_, _ = s.CreateSandbox(context.Background(), types.CreateSandboxObjectRequest{
		ID: "sbx-force-node-ops",
		Spec: types.SandboxSpec{
			Ports:      []types.SandboxPortSpec{{ContainerPort: 80, Protocol: "tcp"}},
			Containers: []types.SandboxContainerSpec{{Name: "c", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}},
		},
	})
	s.runSchedulerOnce(context.Background())

	if err := s.DeleteNode(context.Background(), "n1"); err == nil {
		t.Fatal("expected non-force delete to fail on node API error")
	}

	if deleteCalls.Load() == 0 {
		t.Fatal("expected non-force path to call node delete API")
	}

	deleteCalls.Store(0)
	reconcileCalls.Store(0)

	if err := s.DeleteNodeForce(context.Background(), "n1", true); err != nil {
		t.Fatalf("force delete err=%v", err)
	}

	if deleteCalls.Load() != 0 {
		t.Fatalf("force delete should skip node delete API, got calls=%d", deleteCalls.Load())
	}

	if reconcileCalls.Load() != 0 {
		t.Fatalf("force delete should skip reconcile API, got calls=%d", reconcileCalls.Load())
	}
}
