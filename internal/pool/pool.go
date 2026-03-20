package pool

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"vulpineos/internal/juggler"
)

// ContextSlot represents a reusable browser context.
type ContextSlot struct {
	ContextID string
	SessionID string
	UseCount  int
	CreatedAt time.Time
}

// Config holds pool configuration.
type Config struct {
	PreWarm        int // Contexts to create on startup (default 10)
	MaxActive      int // Max concurrent contexts (default 20)
	MaxUsesPerSlot int // Recycle after N uses (default 50)
}

// DefaultConfig returns sensible defaults for a $40/month VPS.
func DefaultConfig() Config {
	return Config{
		PreWarm:        10,
		MaxActive:      20,
		MaxUsesPerSlot: 50,
	}
}

// Pool manages a pool of reusable browser contexts.
type Pool struct {
	client    *juggler.Client
	config    Config
	available chan *ContextSlot
	mu        sync.Mutex
	active    map[string]*ContextSlot // contextID -> slot
	total     int
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
	}
}

// Start pre-warms the pool with initial contexts.
func (p *Pool) Start() error {
	count := p.config.PreWarm
	if count > p.config.MaxActive {
		count = p.config.MaxActive
	}

	for i := 0; i < count; i++ {
		slot, err := p.createSlot()
		if err != nil {
			log.Printf("pool: failed to pre-warm context %d: %v", i, err)
			continue
		}
		p.available <- slot
	}

	log.Printf("pool: pre-warmed %d/%d contexts", len(p.available), count)
	return nil
}

// Acquire gets a context slot, creating one if needed. Blocks if at max capacity.
func (p *Pool) Acquire() (*ContextSlot, error) {
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
		p.mu.Unlock()
		slot, err := p.createSlot()
		if err != nil {
			return nil, err
		}
		p.mu.Lock()
		p.active[slot.ContextID] = slot
		p.mu.Unlock()
		return slot, nil
	}
	p.mu.Unlock()

	// At capacity — wait for one to be released
	slot := <-p.available
	p.mu.Lock()
	p.active[slot.ContextID] = slot
	p.mu.Unlock()
	return slot, nil
}

// Release returns a context slot to the pool for reuse.
// If the slot has exceeded max uses, it's destroyed and a new one is created.
func (p *Pool) Release(slot *ContextSlot) {
	p.mu.Lock()
	delete(p.active, slot.ContextID)
	p.mu.Unlock()

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

	if !p.closed {
		p.available <- slot
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
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()

	// Drain available slots
	for {
		select {
		case slot := <-p.available:
			p.destroySlot(slot)
		default:
			return
		}
	}
}

func (p *Pool) createSlot() (*ContextSlot, error) {
	result, err := p.client.Call("", "Browser.createBrowserContext", map[string]interface{}{
		"removeOnDetach": true,
	})
	if err != nil {
		return nil, fmt.Errorf("create browser context: %w", err)
	}

	var ctx struct {
		BrowserContextID string `json:"browserContextId"`
	}
	if err := json.Unmarshal(result, &ctx); err != nil {
		return nil, fmt.Errorf("parse context result: %w", err)
	}

	p.mu.Lock()
	p.total++
	p.mu.Unlock()

	return &ContextSlot{
		ContextID: ctx.BrowserContextID,
		CreatedAt: time.Now(),
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
