package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"sandboxd-o/pkg/logging"
	orcfg "sandboxd-o/sandboxd-orch/config"
	ohttp "sandboxd-o/sandboxd-orch/http"
	"sandboxd-o/sandboxd-orch/service"
	"sandboxd-o/sandboxd-orch/types"
)

func TestAPI_AllEndpoints(t *testing.T) {
	sbx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case r.URL.Path == "/v1/node/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "resources": map[string]any{"capacity_cpu_milli": 4000, "allocatable_cpu_milli": 3600}})
		case r.URL.Path == "/v1/sandboxes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "next_cursor": ""})
		case r.URL.Path == "/v1/sandboxes" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "sbx1"}})
		case r.URL.Path == "/v1/sandboxes/sbx1" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "sbx1"}})
		case r.URL.Path == "/v1/sandboxes/sbx1" && r.Method == http.MethodDelete:
			_ = json.NewEncoder(w).Encode(map[string]any{"deleted": "sbx1"})
		case r.URL.Path == "/v1/sandboxes/sbx1/containers/c1/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{"logs": map[string]any{"lines": []string{"ok"}}})
		case r.URL.Path == "/v1/reconcile":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer sbx.Close()

	svc := newService(t)
	defer svc.Close()
	ip, port := splitURL(t, sbx.URL)
	if _, err := svc.RegisterNode(context.Background(), types.RegisterNodeRequest{Name: "n1", IP: ip, Port: port}, "api"); err != nil {
		t.Fatalf("register node err=%v", err)
	}

	lg, _ := logging.New(logging.Config{}, logging.Options{Service: "test"})
	orch := httptest.NewServer(ohttp.NewRouter(svc, orcfg.Config{}, lg))
	defer orch.Close()

	mustStatus(t, http.MethodGet, orch.URL+"/healthz", nil, 200)
	mustStatus(t, http.MethodGet, orch.URL+"/api/v1/nodes", nil, 200)
	mustStatus(t, http.MethodGet, orch.URL+"/api/v1/nodes/n1", nil, 200)
	mustStatus(t, http.MethodPost, orch.URL+"/api/v1/nodes/n1/heartbeat", nil, 200)
	mustStatus(t, http.MethodGet, orch.URL+"/api/v1/nodes/n1/sandboxes", nil, 200)
	mustStatus(t, http.MethodGet, orch.URL+"/api/v1/nodes/n1/sandboxes/sbx1", nil, 200)
	mustStatus(t, http.MethodDelete, orch.URL+"/api/v1/nodes/n1/sandboxes/sbx1", nil, 200)
	mustStatus(t, http.MethodGet, orch.URL+"/api/v1/nodes/n1/sandboxes/sbx1/containers/c1/logs", nil, 200)
	mustStatus(t, http.MethodPost, orch.URL+"/api/v1/nodes/n1/reconcile", nil, 200)
	mustStatus(t, http.MethodPost, orch.URL+"/api/v1/nodes/register", []byte(`{"name":"","ip":"127.0.0.1","port":18080}`), 400)

	createPayload := []byte(`{"id":"sbx1","egress":true,"containers":[{"name":"c1","image":"nginx","resource":{"cpu":"100m","memory":"64Mi"}}],"ports":[]}`)
	mustStatus(t, http.MethodPost, orch.URL+"/api/v1/nodes/n1/sandboxes", createPayload, 200)
	mustStatus(t, http.MethodPost, orch.URL+"/api/v1/nodes/n1/sandboxes", []byte(`{"id":"bad"`), 400)
	mustStatus(t, http.MethodDelete, orch.URL+"/api/v1/nodes/n1", nil, 200)

	mustStatus(t, http.MethodPost, orch.URL+"/api/v1/sandboxes", []byte(`{"id":"obj-1","spec":{"egress":true,"containers":[{"name":"app","image":"nginx","resource":{"cpu":"100m","memory":"64Mi"}}]}}`), 201)
	mustStatus(t, http.MethodGet, orch.URL+"/api/v1/sandboxes", nil, 200)
	mustStatus(t, http.MethodGet, orch.URL+"/api/v1/sandboxes/obj-1", nil, 200)
	mustStatus(t, http.MethodDelete, orch.URL+"/api/v1/sandboxes/obj-1", nil, 200)
}

func newService(t *testing.T) *service.Service {
	t.Helper()
	cfg := orcfg.Config{
		HTTPAddr:                 ":0",
		SQLitePath:               filepath.Join(t.TempDir(), "orch.db"),
		ProbeTimeout:             time.Second,
		HeartbeatInterval:        time.Second,
		ResourceSyncInterval:     time.Second,
		ResourcePersistMinInt:    time.Millisecond,
		ResourcePersistMaxInt:    time.Second,
		ReadySuccessThreshold:    2,
		NotReadyFailureThreshold: 2,
		ShutdownTimeout:          time.Second,
	}
	s, err := service.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func splitURL(t *testing.T, raw string) (string, int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("splitURL: %v", err)
	}

	port, err := strconv.Atoi(strings.TrimSpace(u.Port()))
	if err != nil {
		t.Fatalf("splitURL port: %v", err)
	}

	return u.Hostname(), port
}

func mustStatus(t *testing.T, method, u string, payload []byte, code int) {
	t.Helper()
	var body *bytes.Reader
	if payload == nil {
		body = bytes.NewReader(nil)
	} else {
		body = bytes.NewReader(payload)
	}

	req, _ := http.NewRequest(method, u, body)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s %s err=%v", method, u, err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != code {
		t.Fatalf("request %s %s code=%d want=%d", method, u, resp.StatusCode, code)
	}
}
