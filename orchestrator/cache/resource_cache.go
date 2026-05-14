package cache

import (
	"sync"
	"time"

	"sandboxd-o/orchestrator/types"
)

type ResourceCache struct {
	mu    sync.RWMutex
	items map[string]ResourceCacheEntry
}

type ResourceCacheEntry struct {
	Current         types.NodeResources
	LastPersisted   types.NodeResources
	LastPersistedAt time.Time
}

func NewResourceCache() *ResourceCache {
	return &ResourceCache{items: map[string]ResourceCacheEntry{}}
}

func (c *ResourceCache) PutCurrent(name string, res types.NodeResources) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e := c.items[name]
	e.Current = res
	c.items[name] = e
}

func (c *ResourceCache) MarkPersisted(name string, res types.NodeResources, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e := c.items[name]
	e.Current = res
	e.LastPersisted = res
	e.LastPersistedAt = at
	c.items[name] = e
}

func (c *ResourceCache) Delete(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, name)
}

func (c *ResourceCache) ShouldPersist(name string, now time.Time, minInt, maxInt time.Duration) bool {
	c.mu.RLock()
	e, ok := c.items[name]
	c.mu.RUnlock()
	if !ok || e.LastPersistedAt.IsZero() {
		return true
	}

	changed := !sameResources(e.Current, e.LastPersisted)
	if now.Sub(e.LastPersistedAt) >= maxInt {
		return true
	}

	if !changed {
		return false
	}

	if now.Sub(e.LastPersistedAt) < minInt {
		return false
	}

	return true
}

func sameResources(a, b types.NodeResources) bool {
	return a.CapacityCPUMilli == b.CapacityCPUMilli &&
		a.CapacityMemoryBytes == b.CapacityMemoryBytes &&
		a.AllocatableCPUMilli == b.AllocatableCPUMilli &&
		a.AllocatableMemory == b.AllocatableMemory &&
		a.UsedCPUMilli == b.UsedCPUMilli &&
		a.UsedMemoryBytes == b.UsedMemoryBytes &&
		a.AvailableCPUMilli == b.AvailableCPUMilli &&
		a.AvailableMemory == b.AvailableMemory &&
		a.MaxAllocPercent == b.MaxAllocPercent
}
