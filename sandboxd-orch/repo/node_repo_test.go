package repo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"sandboxd-o/sandboxd-orch/types"
)

func TestSQLiteNodeRepo_CRUDAndUpdates(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "orch.db")
	r, err := NewSQLite(dbPath)
	if err != nil {
		t.Fatalf("NewSQLite err=%v", err)
	}
	defer r.Close()

	ctx := context.Background()
	if err := r.UpsertNode(ctx, "n1", "127.0.0.1", 8081, "api"); err != nil {
		t.Fatalf("UpsertNode err=%v", err)
	}

	n, err := r.GetNode(ctx, "n1")
	if err != nil {
		t.Fatalf("GetNode err=%v", err)
	}

	if n.Name != "n1" || n.State != types.NodeStateUnknown {
		t.Fatalf("unexpected node: %+v", n)
	}

	now := time.Now().UTC()
	if err := r.UpdateHeartbeat(ctx, "n1", types.NodeStateReady, 2, 0, "", &now); err != nil {
		t.Fatalf("UpdateHeartbeat err=%v", err)
	}

	res := types.NodeResources{CapacityCPUMilli: 1000, AllocatableCPUMilli: 900, ExternalIP: "203.0.113.10"}
	if err := r.UpdateNodeResources(ctx, "n1", res); err != nil {
		t.Fatalf("UpdateNodeResources err=%v", err)
	}

	list, err := r.ListNodes(ctx)
	if err != nil {
		t.Fatalf("ListNodes err=%v", err)
	}

	if len(list) != 1 || list[0].Resources.CapacityCPUMilli != 1000 {
		t.Fatalf("unexpected list: %+v", list)
	}

	if list[0].Resources.ExternalIP != "203.0.113.10" {
		t.Fatalf("external ip=%q", list[0].Resources.ExternalIP)
	}

	if err := r.DeleteNode(ctx, "n1"); err != nil {
		t.Fatalf("DeleteNode err=%v", err)
	}

	list, err = r.ListNodes(ctx)
	if err != nil {
		t.Fatalf("ListNodes after delete err=%v", err)
	}

	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}
}

func TestSQLiteNodeRepo_DeleteNode_ClearsReservedPorts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "orch.db")
	r, err := NewSQLite(dbPath)
	if err != nil {
		t.Fatalf("NewSQLite err=%v", err)
	}
	defer r.Close()

	ctx := context.Background()
	if err := r.UpsertNode(ctx, "n1", "127.0.0.1", 8081, "api"); err != nil {
		t.Fatalf("UpsertNode err=%v", err)
	}

	if err := r.CreateSandbox(ctx, types.Sandbox{
		ID: "sbx-1",
		Spec: types.SandboxSpec{
			Containers: []types.SandboxContainerSpec{{
				Name:     "web",
				Image:    "nginx",
				Resource: types.SandboxResource{CPU: "100m", Memory: "64Mi"},
			}},
		},
		Status: types.SandboxStatus{Phase: types.SandboxPhasePending},
	}); err != nil {
		t.Fatalf("CreateSandbox err=%v", err)
	}

	if err := r.ReserveSandboxPortsAndSchedule(ctx, "sbx-1", "n1", []types.SandboxPortAssign{
		{HostPort: 12080, ContainerPort: 80, Protocol: "tcp"},
	}); err != nil {
		t.Fatalf("ReserveSandboxPortsAndSchedule err=%v", err)
	}

	used, err := r.NodeUsedHostPorts(ctx, "n1")
	if err != nil {
		t.Fatalf("NodeUsedHostPorts before delete err=%v", err)
	}

	if _, ok := used[12080]; !ok {
		t.Fatalf("expected reserved port 12080 before delete, got=%v", used)
	}

	if err := r.DeleteNode(ctx, "n1"); err != nil {
		t.Fatalf("DeleteNode err=%v", err)
	}

	used, err = r.NodeUsedHostPorts(ctx, "n1")
	if err != nil {
		t.Fatalf("NodeUsedHostPorts after delete err=%v", err)
	}

	if len(used) != 0 {
		t.Fatalf("expected no reserved ports after node delete, got=%v", used)
	}

	// Sandbox row remains for control-plane reconciliation semantics.
	if _, err := r.GetSandbox(ctx, "sbx-1"); err != nil {
		t.Fatalf("sandbox row should remain after node delete: %v", err)
	}
}

func TestSQLiteNodeRepo_AdjustNodeResourceUsage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "orch.db")
	r, err := NewSQLite(dbPath)
	if err != nil {
		t.Fatalf("NewSQLite err=%v", err)
	}
	defer r.Close()

	ctx := context.Background()
	if err := r.UpsertNode(ctx, "n1", "127.0.0.1", 8081, "api"); err != nil {
		t.Fatalf("UpsertNode err=%v", err)
	}

	base := types.NodeResources{
		CapacityCPUMilli:    4000,
		CapacityMemoryBytes: 8 * 1024 * 1024 * 1024,
		AllocatableCPUMilli: 3600,
		AllocatableMemory:   6 * 1024 * 1024 * 1024,
		UsedCPUMilli:        200,
		UsedMemoryBytes:     512 * 1024 * 1024,
		AvailableCPUMilli:   3400,
		AvailableMemory:     5632 * 1024 * 1024,
		MaxAllocPercent:     90,
	}
	if err := r.UpdateNodeResources(ctx, "n1", base); err != nil {
		t.Fatalf("UpdateNodeResources err=%v", err)
	}

	// logical increment after scheduling success
	if err := r.AdjustNodeResourceUsage(ctx, "n1", 300, 256*1024*1024); err != nil {
		t.Fatalf("AdjustNodeResourceUsage + err=%v", err)
	}

	n, err := r.GetNode(ctx, "n1")
	if err != nil {
		t.Fatalf("GetNode err=%v", err)
	}

	if n.Resources.UsedCPUMilli != 500 {
		t.Fatalf("used cpu want=500 got=%d", n.Resources.UsedCPUMilli)
	}

	if n.Resources.AvailableCPUMilli != 3100 {
		t.Fatalf("available cpu want=3100 got=%d", n.Resources.AvailableCPUMilli)
	}

	// logical decrement on delete should floor at zero
	if err := r.AdjustNodeResourceUsage(ctx, "n1", -5000, -20*1024*1024*1024); err != nil {
		t.Fatalf("AdjustNodeResourceUsage - err=%v", err)
	}

	n, err = r.GetNode(ctx, "n1")
	if err != nil {
		t.Fatalf("GetNode2 err=%v", err)
	}

	if n.Resources.UsedCPUMilli != 0 || n.Resources.UsedMemoryBytes != 0 {
		t.Fatalf("used resources should floor at zero: %+v", n.Resources)
	}

	if n.Resources.AvailableCPUMilli != base.AllocatableCPUMilli || n.Resources.AvailableMemory != base.AllocatableMemory {
		t.Fatalf("available should return to allocatable: %+v", n.Resources)
	}
}
