package repo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"sandboxd-o/sandboxd-orch/types"
)

func TestSandboxRepo_CRUDAndPortReservation(t *testing.T) {
	r, err := NewSQLite(filepath.Join(t.TempDir(), "repo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	ctx := context.Background()
	sbx := types.Sandbox{
		ID: "sbx-1",
		Spec: types.SandboxSpec{
			Egress: true,
			Containers: []types.SandboxContainerSpec{{
				Name:     "web",
				Image:    "nginx",
				Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"},
			}},
		},
		Status: types.SandboxStatus{Phase: types.SandboxPhasePending},
	}
	if err := r.CreateSandbox(ctx, sbx); err != nil {
		t.Fatal(err)
	}

	got, err := r.GetSandbox(ctx, "sbx-1")
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != "sbx-1" || got.Status.Phase != types.SandboxPhasePending {
		t.Fatalf("unexpected sandbox: %+v", got)
	}

	ports := []types.SandboxPortAssign{{HostPort: 10001, ContainerPort: 80, Protocol: "tcp"}}
	if err := r.ReserveSandboxPortsAndSchedule(ctx, "sbx-1", "n1", ports); err != nil {
		t.Fatal(err)
	}

	got, err = r.GetSandbox(ctx, "sbx-1")
	if err != nil {
		t.Fatal(err)
	}

	if got.Status.Phase != types.SandboxPhaseScheduled || got.Status.NodeName != "n1" {
		t.Fatalf("unexpected status after reserve: %+v", got.Status)
	}

	used, err := r.NodeUsedHostPorts(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := used[10001]; !ok {
		t.Fatalf("expected reserved hostPort 10001, got=%v", used)
	}

	got.Status.Phase = types.SandboxPhaseRunning
	now := time.Now().UTC()
	got.Status.ExpireAt = &now
	if err := r.UpdateSandboxStatus(ctx, got.ID, got.Status); err != nil {
		t.Fatal(err)
	}

	list, err := r.ListSandboxes(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(list) != 1 {
		t.Fatalf("list len=%d", len(list))
	}

	if err := r.ReleaseSandboxPorts(ctx, "sbx-1"); err != nil {
		t.Fatal(err)
	}

	used, err = r.NodeUsedHostPorts(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}

	if len(used) != 0 {
		t.Fatalf("expected no used ports, got=%v", used)
	}

	if err := r.DeleteSandbox(ctx, "sbx-1"); err != nil {
		t.Fatal(err)
	}

	if _, err := r.GetSandbox(ctx, "sbx-1"); err == nil {
		t.Fatal("expected not found after delete")
	}
}

func TestSandboxRepo_ReserveConflict(t *testing.T) {
	r, err := NewSQLite(filepath.Join(t.TempDir(), "repo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx := context.Background()

	for _, id := range []string{"sbx-a", "sbx-b"} {
		if err := r.CreateSandbox(ctx, types.Sandbox{
			ID:     id,
			Spec:   types.SandboxSpec{Containers: []types.SandboxContainerSpec{{Name: "c", Image: "nginx", Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"}}}},
			Status: types.SandboxStatus{Phase: types.SandboxPhasePending},
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := r.ReserveSandboxPortsAndSchedule(ctx, "sbx-a", "n1", []types.SandboxPortAssign{{HostPort: 10002, ContainerPort: 80, Protocol: "tcp"}}); err != nil {
		t.Fatal(err)
	}

	if err := r.ReserveSandboxPortsAndSchedule(ctx, "sbx-b", "n1", []types.SandboxPortAssign{{HostPort: 10002, ContainerPort: 8080, Protocol: "tcp"}}); err == nil {
		t.Fatal("expected conflict error")
	}
}
