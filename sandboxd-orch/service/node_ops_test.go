package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
		m[n.Name] = n
	}

	return &fakeRepo{nodes: m}
}

func (r *fakeRepo) Close() error { return nil }
func (r *fakeRepo) UpsertNode(ctx context.Context, name, ip string, port int, source string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := r.nodes[name]
	n.Name, n.IP, n.Port, n.Source = name, ip, port, source
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

	if _, err := s.RegisterNode(context.Background(), types.RegisterNodeRequest{Name: "n1", IP: "127.0.0.1", Port: 18080}, "api"); err != nil {
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

func TestProbeNodeStateTransitionsAndResourceSync(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/v1/node/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "resources": map[string]any{"capacity_cpu_milli": 1000, "allocatable_cpu_milli": 900}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ready.Close()

	n := types.Node{Name: "n1", SbxletBaseURL: ready.URL, State: types.NodeStateUnknown}
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

	bad := types.Node{Name: "n2", SbxletBaseURL: "http://127.0.0.1:1", State: types.NodeStateUnknown}
	r.nodes[bad.Name] = bad
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
		types.Node{Name: "n1", SbxletBaseURL: server.URL},
		types.Node{Name: "n2", SbxletBaseURL: server.URL},
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
	s.forEachNode(context.Background(), []types.Node{{Name: "a"}, {Name: "b"}}, func(context.Context, types.Node) { count.Add(1) })
	if count.Load() != 2 {
		t.Fatalf("count=%d", count.Load())
	}

	r.listErr = errors.New("list fail")
	s.runHeartbeatOnce(context.Background())
	s.runResourceSyncOnce(context.Background())
}
