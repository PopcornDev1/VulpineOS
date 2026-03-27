package tokenopt

import (
	"sync"
	"time"
)

// CacheStats reports aggregate statistics for the result cache.
type CacheStats struct {
	Entries  int   `json:"entries"`
	TotalHit int64 `json:"totalHits"`
	MaxSize  int   `json:"maxSize"`
}

// CacheEntry holds a cached extraction result.
type CacheEntry struct {
	Value     string
	CreatedAt time.Time
	Hits      int
}

// ResultCache caches browser extraction results to avoid redundant LLM processing.
// Keyed by agentID:key (e.g. agentID:selector or agentID:url).
type ResultCache struct {
	mu      sync.RWMutex
	entries map[string]*CacheEntry
	ttl     time.Duration
	maxSize int
}

// NewResultCache creates a cache with the given TTL. Default max size is 1000 entries.
func NewResultCache(ttl time.Duration) *ResultCache {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &ResultCache{
		entries: make(map[string]*CacheEntry),
		ttl:     ttl,
		maxSize: 1000,
	}
}

// SetMaxSize overrides the default max cache size.
func (c *ResultCache) SetMaxSize(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxSize = n
}

func cacheKey(agentID, key string) string {
	return agentID + ":" + key
}

// Get retrieves a cached value. Returns ("", false) on miss or expiry.
func (c *ResultCache) Get(agentID, key string) (string, bool) {
	c.mu.RLock()
	entry, ok := c.entries[cacheKey(agentID, key)]
	c.mu.RUnlock()

	if !ok {
		return "", false
	}

	if time.Since(entry.CreatedAt) > c.ttl {
		// Expired — remove lazily
		c.mu.Lock()
		delete(c.entries, cacheKey(agentID, key))
		c.mu.Unlock()
		return "", false
	}

	c.mu.Lock()
	entry.Hits++
	c.mu.Unlock()

	return entry.Value, true
}

// Set stores a value in the cache. If the cache is full, evicts the oldest entry.
func (c *ResultCache) Set(agentID, key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	k := cacheKey(agentID, key)

	// Evict if at capacity and this is a new key
	if _, exists := c.entries[k]; !exists && len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	c.entries[k] = &CacheEntry{
		Value:     value,
		CreatedAt: time.Now(),
		Hits:      0,
	}
}

// Invalidate removes all cached entries for the given agent.
func (c *ResultCache) Invalidate(agentID string) {
	prefix := agentID + ":"
	c.mu.Lock()
	defer c.mu.Unlock()

	for k := range c.entries {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.entries, k)
		}
	}
}

// Stats returns aggregate cache statistics.
func (c *ResultCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var totalHits int64
	for _, e := range c.entries {
		totalHits += int64(e.Hits)
	}

	return CacheStats{
		Entries:  len(c.entries),
		TotalHit: totalHits,
		MaxSize:  c.maxSize,
	}
}

// evictOldest removes the entry with the oldest CreatedAt. Must be called with mu held.
func (c *ResultCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	first := true

	for k, e := range c.entries {
		if first || e.CreatedAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = e.CreatedAt
			first = false
		}
	}

	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}
