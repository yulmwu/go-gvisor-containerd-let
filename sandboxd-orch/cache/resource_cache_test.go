package cache

import (
	"testing"
	"time"

	"sandboxd-o/sandboxd-orch/types"
)

func TestResourceCache_ShouldPersist(t *testing.T) {
	c := NewResourceCache()
	now := time.Now()
	res := types.NodeResources{AllocatableCPUMilli: 1000}

	c.PutCurrent("n1", res)
	if !c.ShouldPersist("n1", now, time.Second, 5*time.Second) {
		t.Fatal("expected true for first persist")
	}

	c.MarkPersisted("n1", res, now)
	if c.ShouldPersist("n1", now.Add(500*time.Millisecond), time.Second, 5*time.Second) {
		t.Fatal("expected false within min interval with no change")
	}

	changed := res
	changed.UsedCPUMilli = 100
	c.PutCurrent("n1", changed)
	if c.ShouldPersist("n1", now.Add(500*time.Millisecond), time.Second, 5*time.Second) {
		t.Fatal("expected false within min interval despite change")
	}

	if !c.ShouldPersist("n1", now.Add(2*time.Second), time.Second, 5*time.Second) {
		t.Fatal("expected true after min interval with change")
	}
}

func TestResourceCache_Delete(t *testing.T) {
	c := NewResourceCache()
	c.PutCurrent("n1", types.NodeResources{})
	c.Delete("n1")
	if !c.ShouldPersist("n1", time.Now(), time.Second, time.Second) {
		t.Fatal("expected true after delete")
	}
}
