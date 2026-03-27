package tokenopt

import (
	"testing"
	"time"
)

func TestCacheSetGet(t *testing.T) {
	c := NewResultCache(60 * time.Second)
	c.Set("agent1", "page:/home", `{"nodes":[]}`)

	val, ok := c.Get("agent1", "page:/home")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if val != `{"nodes":[]}` {
		t.Fatalf("unexpected value: %s", val)
	}
}

func TestCacheMiss(t *testing.T) {
	c := NewResultCache(60 * time.Second)
	_, ok := c.Get("agent1", "nonexistent")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestCacheExpiry(t *testing.T) {
	c := NewResultCache(50 * time.Millisecond)
	c.Set("agent1", "key1", "value1")

	// Should hit immediately
	_, ok := c.Get("agent1", "key1")
	if !ok {
		t.Fatal("expected hit before expiry")
	}

	time.Sleep(60 * time.Millisecond)

	// Should miss after TTL
	_, ok = c.Get("agent1", "key1")
	if ok {
		t.Fatal("expected miss after expiry")
	}
}

func TestCacheInvalidate(t *testing.T) {
	c := NewResultCache(60 * time.Second)
	c.Set("agent1", "key1", "v1")
	c.Set("agent1", "key2", "v2")
	c.Set("agent2", "key1", "v3")

	c.Invalidate("agent1")

	if _, ok := c.Get("agent1", "key1"); ok {
		t.Fatal("agent1:key1 should be invalidated")
	}
	if _, ok := c.Get("agent1", "key2"); ok {
		t.Fatal("agent1:key2 should be invalidated")
	}
	if _, ok := c.Get("agent2", "key1"); !ok {
		t.Fatal("agent2:key1 should still exist")
	}
}

func TestCacheStats(t *testing.T) {
	c := NewResultCache(60 * time.Second)
	c.Set("a", "k1", "v1")
	c.Set("a", "k2", "v2")
	c.Get("a", "k1")
	c.Get("a", "k1")

	stats := c.Stats()
	if stats.Entries != 2 {
		t.Fatalf("expected 2 entries, got %d", stats.Entries)
	}
	if stats.TotalHit != 2 {
		t.Fatalf("expected 2 total hits, got %d", stats.TotalHit)
	}
	if stats.MaxSize != 1000 {
		t.Fatalf("expected maxSize 1000, got %d", stats.MaxSize)
	}
}

func TestCacheEviction(t *testing.T) {
	c := NewResultCache(60 * time.Second)
	c.SetMaxSize(3)

	c.Set("a", "k1", "v1")
	time.Sleep(1 * time.Millisecond)
	c.Set("a", "k2", "v2")
	time.Sleep(1 * time.Millisecond)
	c.Set("a", "k3", "v3")
	time.Sleep(1 * time.Millisecond)

	// This should evict k1 (oldest)
	c.Set("a", "k4", "v4")

	if _, ok := c.Get("a", "k1"); ok {
		t.Fatal("k1 should have been evicted")
	}
	if _, ok := c.Get("a", "k2"); !ok {
		t.Fatal("k2 should still exist")
	}

	stats := c.Stats()
	if stats.Entries != 3 {
		t.Fatalf("expected 3 entries after eviction, got %d", stats.Entries)
	}
}

func TestCacheOverwrite(t *testing.T) {
	c := NewResultCache(60 * time.Second)
	c.SetMaxSize(2)

	c.Set("a", "k1", "v1")
	c.Set("a", "k2", "v2")

	// Overwrite existing — should NOT evict
	c.Set("a", "k1", "v1-updated")

	stats := c.Stats()
	if stats.Entries != 2 {
		t.Fatalf("expected 2 entries, got %d", stats.Entries)
	}

	val, ok := c.Get("a", "k1")
	if !ok || val != "v1-updated" {
		t.Fatalf("expected updated value, got %s", val)
	}
}

func TestCacheDefaultTTL(t *testing.T) {
	c := NewResultCache(0) // should default to 60s
	if c.ttl != 60*time.Second {
		t.Fatalf("expected 60s default TTL, got %v", c.ttl)
	}
}
