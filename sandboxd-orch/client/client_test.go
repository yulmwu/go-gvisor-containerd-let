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
		if r.URL.Path == "/v1/sandboxes/statuses" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"id": "s1", "phase": "running"},
				},
				"missing": []string{},
			})
			return
		}
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

	st, err := c.SandboxStatuses(ctx, []string{"s1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(st.Items) != 1 || st.Items[0].ID != "s1" {
		t.Fatalf("unexpected sandbox statuses: %+v", st)
	}
}

func TestClient_DoIntoAndEmptyBody(t *testing.T) {
	t.Run("do returns default ok for empty body", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer ts.Close()

		c := New(ts.URL, time.Second)
		out, err := c.Reconcile(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if v, ok := out["ok"].(bool); !ok || !v {
			t.Fatalf("expected ok=true for empty body, got=%v", out)
		}
	})

	t.Run("doInto decodes typed response", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(SandboxStatusesResponse{
				Items: []SandboxSyncStatus{{ID: "s1", Phase: "running"}},
			})
		}))
		defer ts.Close()

		c := New(ts.URL, time.Second)
		var out SandboxStatusesResponse
		if err := c.doInto(context.Background(), http.MethodPost, "/v1/sandboxes/statuses", map[string]any{"ids": []string{"s1"}}, &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Items) != 1 || out.Items[0].ID != "s1" {
			t.Fatalf("unexpected out=%+v", out)
		}
	})

	t.Run("doInto decode error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{bad-json"))
		}))
		defer ts.Close()

		c := New(ts.URL, time.Second)
		var out SandboxStatusesResponse
		err := c.doInto(context.Background(), http.MethodPost, "/v1/sandboxes/statuses", map[string]any{"ids": []string{"s1"}}, &out)
		if err == nil || !strings.Contains(err.Error(), "decode sandboxd response") {
			t.Fatalf("expected decode error, got=%v", err)
		}
	})
}
