package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"sandboxd-o/sandboxd-orch/config"
	"sandboxd-o/sandboxd-orch/service"
	"sandboxd-o/sandboxd-orch/types"

	"github.com/gin-gonic/gin"
)

func setupHandler(t *testing.T) (*Handler, *service.Service, *httptest.Server) {
	t.Helper()
	sbx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case r.URL.Path == "/v1/node/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "resources": map[string]any{"capacity_cpu_milli": 1000}})
		case r.URL.Path == "/v1/sandboxes" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "next_cursor": ""})
		case r.URL.Path == "/v1/sandboxes" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "s1"}})
		case r.URL.Path == "/v1/sandboxes/s1" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"sandbox": map[string]any{"id": "s1"}})
		case r.URL.Path == "/v1/sandboxes/s1" && r.Method == http.MethodDelete:
			_ = json.NewEncoder(w).Encode(map[string]any{"deleted": "s1"})
		case r.URL.Path == "/v1/sandboxes/s1/containers/c1/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{"logs": map[string]any{"lines": []string{"x"}}})
		case r.URL.Path == "/v1/reconcile":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))

	u, _ := url.Parse(sbx.URL)
	port, _ := strconv.Atoi(u.Port())

	svc, err := service.New(config.Config{
		SQLitePath:               filepath.Join(t.TempDir(), "orch.db"),
		ProbeTimeout:             time.Second,
		HeartbeatInterval:        time.Second,
		ResourceSyncInterval:     time.Second,
		ResourcePersistMinInt:    time.Millisecond,
		ResourcePersistMaxInt:    time.Second,
		ReadySuccessThreshold:    2,
		NotReadyFailureThreshold: 2,
		ShutdownTimeout:          time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.RegisterNode(context.Background(), types.RegisterNodeRequest{Name: "n1", IP: u.Hostname(), Port: port}, "api"); err != nil {
		t.Fatal(err)
	}

	return New(svc, config.Config{}), svc, sbx
}

func TestHandlers_AllEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, svc, sbx := setupHandler(t)
	defer svc.Close()
	defer sbx.Close()

	r := gin.New()
	r.GET("/healthz", h.Healthz)
	r.GET("/nodes", h.ListNodes)
	r.GET("/nodes/:name", h.GetNode)
	r.POST("/nodes/register", h.RegisterNode)
	r.DELETE("/nodes/:name", h.DeleteNode)
	r.POST("/nodes/:name/heartbeat", h.HeartbeatNode)
	r.GET("/nodes/:name/sandboxes", h.NodeListSandboxes)
	r.GET("/nodes/:name/sandboxes/:id", h.NodeGetSandbox)
	r.POST("/nodes/:name/sandboxes", h.NodeCreateSandbox)
	r.DELETE("/nodes/:name/sandboxes/:id", h.NodeDeleteSandbox)
	r.GET("/nodes/:name/sandboxes/:id/containers/:container/logs", h.NodeContainerLogs)
	r.POST("/nodes/:name/reconcile", h.NodeReconcile)
	r.POST("/sandboxes", h.CreateSandbox)
	r.GET("/sandboxes", h.ListSandboxes)
	r.GET("/sandboxes/:id", h.GetSandbox)
	r.DELETE("/sandboxes/:id", h.DeleteSandbox)

	must := func(method, path string, body []byte, code int) {
		t.Helper()
		var reader *bytes.Reader
		if body == nil {
			reader = bytes.NewReader(nil)
		} else {
			reader = bytes.NewReader(body)
		}

		req := httptest.NewRequest(method, path, reader)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != code {
			t.Fatalf("%s %s code=%d want=%d body=%s", method, path, w.Code, code, w.Body.String())
		}
	}

	must(http.MethodGet, "/healthz", nil, 200)
	must(http.MethodGet, "/nodes", nil, 200)
	must(http.MethodGet, "/nodes/n1", nil, 200)
	must(http.MethodGet, "/nodes/no", nil, 404)
	must(http.MethodPost, "/nodes/register", []byte(`{"name":"n2","ip":"127.0.0.1","port":18080}`), 200)
	must(http.MethodPost, "/nodes/register", []byte(`{"name":"","ip":"127.0.0.1","port":18080}`), 400)
	must(http.MethodDelete, "/nodes/n2", nil, 200)
	must(http.MethodDelete, "/nodes/%20", nil, 400)
	must(http.MethodPost, "/nodes/n1/heartbeat", nil, 200)
	must(http.MethodGet, "/nodes/n1/sandboxes?cursor=a&limit=10", nil, 200)
	must(http.MethodGet, "/nodes/n1/sandboxes/s1", nil, 200)
	must(http.MethodPost, "/nodes/n1/sandboxes", []byte(`{"id":"s1","egress":true,"containers":[{"name":"c1","image":"nginx","resource":{"cpu":"100m","memory":"64Mi"}}],"ports":[]}`), 200)
	must(http.MethodPost, "/nodes/n1/sandboxes", []byte(`{"id":"s1"`), 400)
	must(http.MethodDelete, "/nodes/n1/sandboxes/s1", nil, 200)
	must(http.MethodGet, "/nodes/n1/sandboxes/s1/containers/c1/logs?limit=100", nil, 200)
	must(http.MethodPost, "/nodes/n1/reconcile", nil, 200)

	must(http.MethodPost, "/sandboxes", []byte(`{"id":"obj-1","spec":{"egress":true,"containers":[{"name":"app","image":"nginx","resource":{"cpu":"100m","memory":"64Mi"}}]}}`), 201)
	must(http.MethodPost, "/sandboxes", []byte(`{"id":"","spec":{"containers":[]}}`), 400)
	must(http.MethodGet, "/sandboxes", nil, 200)
	must(http.MethodGet, "/sandboxes/obj-1", nil, 200)
	must(http.MethodDelete, "/sandboxes/obj-1", nil, 200)
}

func TestCreateSandbox_RateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, svc, sbx := setupHandler(t)
	defer svc.Close()
	defer sbx.Close()

	// Override handler with very small limiter for deterministic test.
	h = New(svc, config.Config{CreateRPS: 1, CreateBurst: 1})

	r := gin.New()
	r.POST("/sandboxes", h.CreateSandbox)

	body := []byte(`{"id":"rl-1","spec":{"egress":true,"containers":[{"name":"app","image":"nginx","resource":{"cpu":"100m","memory":"64Mi"}}]}}`)
	req1 := httptest.NewRequest(http.MethodPost, "/sandboxes", bytes.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first create code=%d body=%s", w1.Code, w1.Body.String())
	}

	body2 := []byte(`{"id":"rl-2","spec":{"egress":true,"containers":[{"name":"app","image":"nginx","resource":{"cpu":"100m","memory":"64Mi"}}]}}`)
	req2 := httptest.NewRequest(http.MethodPost, "/sandboxes", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second create code=%d want=%d body=%s", w2.Code, http.StatusTooManyRequests, w2.Body.String())
	}
}
