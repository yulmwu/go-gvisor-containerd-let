package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sandboxd-o/sandboxd-let/model"
)

func TestClient_HealthAndNodeStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/v1/node/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "resources": map[string]any{"capacity_cpu_milli": 1000}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	c := New(ts.URL, time.Second)
	if err := c.Healthz(context.Background()); err != nil {
		t.Fatalf("Healthz err=%v", err)
	}

	st, err := c.NodeStatus(context.Background())
	if err != nil {
		t.Fatalf("NodeStatus err=%v", err)
	}

	if st.Resources.CapacityCPUMilli != 1000 {
		t.Fatalf("capacity=%d", st.Resources.CapacityCPUMilli)
	}
}

func TestClient_DoErrorBranches(t *testing.T) {
	t.Run("upstream 4xx message", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad request from upstream", http.StatusBadRequest)
		}))
		defer ts.Close()

		c := New(ts.URL, time.Second)
		_, err := c.GetSandbox(context.Background(), "x")
		if err == nil || !strings.Contains(err.Error(), "bad request from upstream") {
			t.Fatalf("expected upstream message error, got %v", err)
		}
	})

	t.Run("upstream 5xx empty body", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer ts.Close()

		c := New(ts.URL, time.Second)
		_, err := c.GetSandbox(context.Background(), "x")
		if err == nil || !strings.Contains(err.Error(), "502 Bad Gateway") {
			t.Fatalf("expected status fallback, got %v", err)
		}
	})

	t.Run("invalid json response", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{not-json"))
		}))
		defer ts.Close()

		c := New(ts.URL, time.Second)
		_, err := c.GetSandbox(context.Background(), "x")
		if err == nil || !strings.Contains(err.Error(), "decode sandboxd response") {
			t.Fatalf("expected decode error, got %v", err)
		}
	})
}

func TestClient_SandboxOps(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": r.URL.Path, "query": r.URL.RawQuery})
	}))
	defer ts.Close()

	c := New(ts.URL, time.Second)
	ctx := context.Background()
	if _, err := c.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := c.ListSandboxes(ctx, "cur", 2); err != nil {
		t.Fatal(err)
	}

	if _, err := c.GetSandbox(ctx, "s1"); err != nil {
		t.Fatal(err)
	}

	if _, err := c.CreateSandbox(ctx, model.CreateSandboxRequest{ID: "s1", Containers: []model.CreateContainerRequest{{Name: "c", Image: "i", Resource: model.ResourceSpec{CPU: "1m", Memory: "1Mi"}}}}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.DeleteSandbox(ctx, "s1"); err != nil {
		t.Fatal(err)
	}

	if _, err := c.GetContainerLogs(ctx, "s1", "c", "10", 100); err != nil {
		t.Fatal(err)
	}
}
