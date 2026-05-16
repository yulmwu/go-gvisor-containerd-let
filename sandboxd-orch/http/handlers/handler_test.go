package handlers_test

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

func setupRouter(t *testing.T, sandboxdURL string) *httptest.Server {
	t.Helper()
	cfg := orcfg.Config{
		HTTPAddr:                 ":0",
		SQLitePath:               filepath.Join(t.TempDir(), "orch.db"),
		ProbeTimeout:             time.Second,
		HeartbeatInterval:        5 * time.Second,
		ResourceSyncInterval:     5 * time.Second,
		ResourcePersistMinInt:    time.Second,
		ResourcePersistMaxInt:    time.Minute,
		ReadySuccessThreshold:    2,
		NotReadyFailureThreshold: 2,
		ShutdownTimeout:          time.Second,
	}
	svc, err := service.New(cfg)
	if err != nil {
		t.Fatalf("service.New err=%v", err)
	}

	ip, port := splitLocalURL(t, sandboxdURL)
	_, _ = svc.RegisterNode(context.Background(), types.RegisterNodeRequest{Name: "node1", IP: ip, Port: port}, "api")

	lg, _ := logging.New(logging.Config{}, logging.Options{Service: "test"})
	r := ohttp.NewRouter(svc, cfg, lg)
	return httptest.NewServer(r)
}

func splitLocalURL(t *testing.T, raw string) (string, int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}

	host := u.Hostname()
	port, err := strconv.Atoi(strings.TrimSpace(u.Port()))
	if err != nil {
		t.Fatalf("parse url port %q: %v", raw, err)
	}

	return host, port
}

func TestHealthz(t *testing.T) {
	sbx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer sbx.Close()
	r := setupRouter(t, sbx.URL)
	defer r.Close()

	resp, err := http.Get(r.URL + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz err=%v code=%d", err, resp.StatusCode)
	}
}

func TestRegisterNode_BadRequest(t *testing.T) {
	sbx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer sbx.Close()
	r := setupRouter(t, sbx.URL)
	defer r.Close()

	resp, err := http.Post(r.URL+"/api/v1/nodes/register", "application/json", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("code=%d", resp.StatusCode)
	}
}
