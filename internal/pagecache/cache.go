// Package pagecache saves and restores page state across agent restarts.
//
// When an agent pauses or completes, the cache captures:
// - Current URL
// - Page HTML content (document.documentElement.outerHTML)
// - Cookies for the domain
// - Scroll position
// - Form values (input/textarea/select)
//
// On resume, the cache restores the page to the same state.
package pagecache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PageState captures everything needed to restore a page.
type PageState struct {
	AgentID    string            `json:"agentId"`
	URL        string            `json:"url"`
	HTML       string            `json:"html,omitempty"`
	Cookies    json.RawMessage   `json:"cookies,omitempty"`
	ScrollX    float64           `json:"scrollX"`
	ScrollY    float64           `json:"scrollY"`
	FormValues map[string]string `json:"formValues,omitempty"` // selector → value
	Title      string            `json:"title"`
	CapturedAt time.Time         `json:"capturedAt"`
}

// Cache manages page state persistence.
type Cache struct {
	mu      sync.RWMutex
	states  map[string]*PageState // agentID → state
	dir     string                // persistence directory
}

// New creates a page cache. If dir is non-empty, states persist to disk.
func New(dir string) *Cache {
	return &Cache{
		states: make(map[string]*PageState),
		dir:    dir,
	}
}

// Save stores a page state for an agent.
func (c *Cache) Save(state *PageState) error {
	if state == nil || state.AgentID == "" {
		return fmt.Errorf("invalid state: missing agentId")
	}
	state.CapturedAt = time.Now()

	c.mu.Lock()
	c.states[state.AgentID] = state
	c.mu.Unlock()

	// Persist to disk if configured
	if c.dir != "" {
		return c.saveToDisk(state)
	}
	return nil
}

// Load retrieves a cached page state for an agent.
func (c *Cache) Load(agentID string) *PageState {
	c.mu.RLock()
	state := c.states[agentID]
	c.mu.RUnlock()

	if state != nil {
		return state
	}

	// Try loading from disk
	if c.dir != "" {
		if s, err := c.loadFromDisk(agentID); err == nil {
			c.mu.Lock()
			c.states[agentID] = s
			c.mu.Unlock()
			return s
		}
	}
	return nil
}

// Delete removes a cached state.
func (c *Cache) Delete(agentID string) {
	c.mu.Lock()
	delete(c.states, agentID)
	c.mu.Unlock()

	if c.dir != "" {
		os.Remove(c.filePath(agentID))
	}
}

// Has returns true if a cached state exists for the agent.
func (c *Cache) Has(agentID string) bool {
	c.mu.RLock()
	_, ok := c.states[agentID]
	c.mu.RUnlock()
	if ok {
		return true
	}
	if c.dir != "" {
		_, err := os.Stat(c.filePath(agentID))
		return err == nil
	}
	return false
}

// List returns all cached agent IDs.
func (c *Cache) List() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]string, 0, len(c.states))
	for id := range c.states {
		ids = append(ids, id)
	}
	return ids
}

func (c *Cache) filePath(agentID string) string {
	return filepath.Join(c.dir, agentID+".json")
}

func (c *Cache) saveToDisk(state *PageState) error {
	if err := os.MkdirAll(c.dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(c.filePath(state.AgentID), data, 0600)
}

func (c *Cache) loadFromDisk(agentID string) (*PageState, error) {
	data, err := os.ReadFile(c.filePath(agentID))
	if err != nil {
		return nil, err
	}
	var state PageState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}
