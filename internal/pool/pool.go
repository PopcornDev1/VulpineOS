package pool

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"vulpineos/internal/juggler"
)

// ContextSlot represents a reusable browser context.
type ContextSlot struct {
	ContextID string
	// UserContextID is the numeric Firefox container id (originAttributes
	// userContextId) for this context. It is the per-context join key used by
	// the C++ fingerprint setters' storage and the networklab ctx-N identity
	// map. The default context is 0.
	UserContextID uint32
	SessionID     string
	UseCount      int
	CreatedAt     time.Time
}

// Config holds pool configuration.
type Config struct {
	PreWarm        int // Contexts to create on startup (default 10)
	MaxActive      int // Max concurrent contexts (default 20)
	MaxUsesPerSlot int // Recycle after N uses (default 50)
}

// DefaultConfig returns sensible defaults for a $40/month VPS.
func DefaultConfig() Config {
	cfg := Config{
		PreWarm:        10,
		MaxActive:      20,
		MaxUsesPerSlot: 50,
	}
	// Production deployments running many agents in one process raise these via
	// env (e.g. VULPINE_POOL_MAX_ACTIVE=300). Load-test on a prod-sized box —
	// each active context costs memory/FDs; the default 20 is conservative.
	if v := envPositiveInt("VULPINE_POOL_MAX_ACTIVE"); v > 0 {
		cfg.MaxActive = v
	}
	if v := envPositiveInt("VULPINE_POOL_PREWARM"); v > 0 {
		cfg.PreWarm = v
	}
	if v := envPositiveInt("VULPINE_POOL_MAX_USES"); v > 0 {
		cfg.MaxUsesPerSlot = v
	}
	return cfg
}

func envPositiveInt(name string) int {
	if s := os.Getenv(name); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// Pool manages a pool of reusable browser contexts.
type Pool struct {
	client    *juggler.Client
	config    Config
	available chan *ContextSlot
	mu        sync.Mutex
	active    map[string]*ContextSlot // contextID -> slot
	total     int
	done      chan struct{}
	closeOnce sync.Once
	closed    bool
}

// New creates a context pool. Call Start() to pre-warm.
func New(client *juggler.Client, config Config) *Pool {
	if config.MaxActive <= 0 {
		config.MaxActive = 20
	}
	if config.MaxUsesPerSlot <= 0 {
		config.MaxUsesPerSlot = 50
	}
	return &Pool{
		client:    client,
		config:    config,
		available: make(chan *ContextSlot, config.MaxActive),
		active:    make(map[string]*ContextSlot),
		done:      make(chan struct{}),
	}
}

// Start pre-warms the pool with initial contexts.
func (p *Pool) Start() error {
	count := p.config.PreWarm
	if count > p.config.MaxActive {
		count = p.config.MaxActive
	}

	type result struct {
		slot *ContextSlot
		err  error
	}
	results := make(chan result, count)

	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			slot, err := p.createSlot()
			results <- result{slot: slot, err: err}
		}(i)
	}

	wg.Wait()
	close(results)

	var success int
	for r := range results {
		if r.err != nil {
			log.Printf("pool: failed to pre-warm context: %v", r.err)
			continue
		}
		p.available <- r.slot
		success++
	}

	log.Printf("pool: pre-warmed %d/%d contexts", success, count)
	return nil
}

// Acquire gets a context slot, creating one if needed. Blocks if at max capacity.
func (p *Pool) Acquire() (*ContextSlot, error) {
	select {
	case <-p.done:
		return nil, fmt.Errorf("pool closed")
	default:
	}

	// Try to get an available slot without blocking
	select {
	case slot := <-p.available:
		p.mu.Lock()
		p.active[slot.ContextID] = slot
		p.mu.Unlock()
		return slot, nil
	default:
	}

	// Check if we can create a new one
	p.mu.Lock()
	if p.total < p.config.MaxActive {
		p.total++
		p.mu.Unlock()
		slot, err := p.createSlotNoCount()
		if err != nil {
			p.mu.Lock()
			p.total--
			p.mu.Unlock()
			return nil, err
		}
		p.mu.Lock()
		p.active[slot.ContextID] = slot
		p.mu.Unlock()
		return slot, nil
	}
	p.mu.Unlock()

	// At capacity — wait for one to be released or pool to close
	select {
	case slot := <-p.available:
		p.mu.Lock()
		p.active[slot.ContextID] = slot
		p.mu.Unlock()
		return slot, nil
	case <-p.done:
		return nil, fmt.Errorf("pool closed")
	}
}

// Release returns a context slot to the pool for reuse.
// If the slot has exceeded max uses, it's destroyed and a new one is created.
func (p *Pool) Release(slot *ContextSlot) {
	p.mu.Lock()
	_, wasActive := p.active[slot.ContextID]
	if wasActive {
		delete(p.active, slot.ContextID)
	}
	closed := p.closed
	p.mu.Unlock()

	if closed {
		if wasActive {
			p.destroySlot(slot)
		}
		return
	}

	slot.UseCount++

	if slot.UseCount >= p.config.MaxUsesPerSlot {
		// Recycle: destroy old, create new
		p.destroySlot(slot)
		newSlot, err := p.createSlot()
		if err != nil {
			log.Printf("pool: failed to recycle context: %v", err)
			return
		}
		slot = newSlot
	}

	select {
	case p.available <- slot:
	case <-p.done:
		p.destroySlot(slot)
	}
}

// Discard destroys a checked-out context instead of returning it to the
// reusable pool. Use this for contexts that have held persistent identity state.
func (p *Pool) Discard(slot *ContextSlot) {
	if p == nil || slot == nil {
		return
	}
	p.mu.Lock()
	_, wasActive := p.active[slot.ContextID]
	if wasActive {
		delete(p.active, slot.ContextID)
	}
	p.mu.Unlock()

	if wasActive {
		p.destroySlot(slot)
	}
}

// Stats returns pool statistics.
func (p *Pool) Stats() (available, active, total int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.available), len(p.active), p.total
}

// Close destroys all contexts and shuts down the pool.
func (p *Pool) Close() {
	var activeSlots []*ContextSlot
	p.closeOnce.Do(func() {
		close(p.done)
		p.mu.Lock()
		p.closed = true
		activeSlots = make([]*ContextSlot, 0, len(p.active))
		for _, slot := range p.active {
			activeSlots = append(activeSlots, slot)
		}
		p.active = make(map[string]*ContextSlot)
		p.total = 0
		p.mu.Unlock()
	})

	// Drain available slots
	for {
		select {
		case slot := <-p.available:
			p.destroySlot(slot)
		default:
			for _, slot := range activeSlots {
				p.destroySlot(slot)
			}
			return
		}
	}
}

func (p *Pool) createSlot() (*ContextSlot, error) {
	slot, err := p.createSlotNoCount()
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	p.total++
	p.mu.Unlock()

	return slot, nil
}

// createSlotNoCount creates a context slot without incrementing the total counter.
// The caller is responsible for managing the total count.
func (p *Pool) createSlotNoCount() (*ContextSlot, error) {
	result, err := p.client.Call("", "Browser.createBrowserContext", map[string]interface{}{
		"removeOnDetach": true,
	})
	if err != nil {
		return nil, fmt.Errorf("create browser context: %w", err)
	}

	var ctx struct {
		BrowserContextID string `json:"browserContextId"`
		UserContextID    uint32 `json:"userContextId"`
	}
	if err := json.Unmarshal(result, &ctx); err != nil {
		return nil, fmt.Errorf("parse context result: %w", err)
	}

	return &ContextSlot{
		ContextID:     ctx.BrowserContextID,
		UserContextID: ctx.UserContextID,
		CreatedAt:     time.Now(),
	}, nil
}

func (p *Pool) destroySlot(slot *ContextSlot) {
	p.client.Call("", "Browser.removeBrowserContext", map[string]interface{}{
		"browserContextId": slot.ContextID,
	})

	p.mu.Lock()
	p.total--
	p.mu.Unlock()
}
