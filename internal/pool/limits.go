package pool

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"vulpineos/internal/juggler"
)

// MemoryConfig controls per-context memory monitoring.
type MemoryConfig struct {
	MaxPerContextMB int  // max memory per context in MB (0 = unlimited)
	CheckIntervalS  int  // seconds between checks (default 30)
	KillOnExceed    bool // release context from pool if limit exceeded
}

// DefaultMemoryConfig returns sensible defaults.
func DefaultMemoryConfig() MemoryConfig {
	return MemoryConfig{
		MaxPerContextMB: 0,
		CheckIntervalS:  30,
		KillOnExceed:    false,
	}
}

// MemoryMonitor periodically checks memory usage per browser context
// and optionally releases contexts that exceed their budget.
type MemoryMonitor struct {
	pool      *Pool
	client    *juggler.Client
	config    MemoryConfig
	usage     map[string]int64 // contextID → bytes
	mu        sync.Mutex
	done      chan struct{}
	closeOnce sync.Once
}

// NewMemoryMonitor creates a monitor for the given pool.
func NewMemoryMonitor(pool *Pool, client *juggler.Client, cfg MemoryConfig) *MemoryMonitor {
	if cfg.CheckIntervalS <= 0 {
		cfg.CheckIntervalS = 30
	}
	return &MemoryMonitor{
		pool:   pool,
		client: client,
		config: cfg,
		usage:  make(map[string]int64),
		done:   make(chan struct{}),
	}
}

// Start begins periodic memory checks in a background goroutine.
func (m *MemoryMonitor) Start() {
	go m.monitor()
}

// Stop terminates the monitoring goroutine.
func (m *MemoryMonitor) Stop() {
	m.closeOnce.Do(func() {
		close(m.done)
	})
}

// GetUsage returns a snapshot of memory usage per context (contextID → bytes).
func (m *MemoryMonitor) GetUsage() map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.usage))
	for k, v := range m.usage {
		out[k] = v
	}
	return out
}

// Config returns the current memory config.
func (m *MemoryMonitor) Config() MemoryConfig {
	return m.config
}

func (m *MemoryMonitor) monitor() {
	interval := time.Duration(m.config.CheckIntervalS) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.done:
			return
		case <-ticker.C:
			m.check()
		}
	}
}

func (m *MemoryMonitor) check() {
	// Query telemetry from the browser
	result, err := m.client.Call("", "Browser.getTelemetry", nil)
	if err != nil {
		return
	}

	var telemetry struct {
		MemoryMB float64 `json:"memoryMB"`
		Contexts []struct {
			ContextID string  `json:"contextId"`
			MemoryMB  float64 `json:"memoryMB"`
		} `json:"contexts"`
	}
	if err := json.Unmarshal(result, &telemetry); err != nil {
		return
	}

	limitBytes := int64(m.config.MaxPerContextMB) * 1024 * 1024

	m.mu.Lock()
	// Clear stale entries
	m.usage = make(map[string]int64, len(telemetry.Contexts))
	for _, ctx := range telemetry.Contexts {
		bytes := int64(ctx.MemoryMB * 1024 * 1024)
		m.usage[ctx.ContextID] = bytes

		if m.config.KillOnExceed && limitBytes > 0 && bytes > limitBytes {
			log.Printf("pool/memory: context %s exceeds limit (%dMB > %dMB), releasing",
				ctx.ContextID, bytes/(1024*1024), m.config.MaxPerContextMB)
			// Release from pool in a goroutine to avoid holding the lock
			ctxID := ctx.ContextID
			go m.releaseContext(ctxID)
		}
	}
	m.mu.Unlock()
}

func (m *MemoryMonitor) releaseContext(contextID string) {
	m.pool.mu.Lock()
	slot, ok := m.pool.active[contextID]
	m.pool.mu.Unlock()

	if ok && slot != nil {
		m.pool.Release(slot)
	}
}
