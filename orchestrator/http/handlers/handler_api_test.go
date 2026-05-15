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

	"sandboxd-o/orchestrator/config"
	"sandboxd-o/orchestrator/service"
	"sandboxd-o/orchestrator/types"

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
	return New(svc), svc, sbx
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
}
