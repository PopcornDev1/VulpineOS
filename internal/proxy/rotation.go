package proxy

import (
	"fmt"
	"sync"
	"time"
)

// RotationConfig holds per-agent proxy rotation settings.
type RotationConfig struct {
	Enabled          bool          `json:"enabled"`                    // rotation on/off (default OFF)
	RotateOnRateLimit bool         `json:"rotateOnRateLimit"`          // rotate on 429/rate limit
	RotateOnBlock    bool          `json:"rotateOnBlock"`              // rotate on IP block
	RotateInterval   time.Duration `json:"rotateInterval"`             // rotate after duration (0 = never)
	SyncFingerprint  bool          `json:"syncFingerprint"`            // update fingerprint geo on rotate (default true)
	ProxyPool        []string      `json:"proxyPool"`                  // proxy URLs to rotate through
	CurrentIndex     int           `json:"currentIndex"`               // current proxy in pool
	lastRotation     time.Time     // when last rotation happened
}

// DefaultRotationConfig returns a RotationConfig with sensible defaults (rotation disabled).
func DefaultRotationConfig() *RotationConfig {
	return &RotationConfig{
		Enabled:         false,
		SyncFingerprint: true,
	}
}

// Rotator manages per-agent proxy rotation.
type Rotator struct {
	mu      sync.Mutex
	configs map[string]*RotationConfig
}

// NewRotator creates a new Rotator.
func NewRotator() *Rotator {
	return &Rotator{
		configs: make(map[string]*RotationConfig),
	}
}

// SetConfig sets the rotation config for an agent.
func (r *Rotator) SetConfig(agentID string, config *RotationConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configs[agentID] = config
}

// GetConfig returns the rotation config for an agent, or nil if not set.
func (r *Rotator) GetConfig(agentID string) *RotationConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.configs[agentID]
}

// ShouldRotate checks whether rotation should happen for the given reason.
// Reasons: "rateLimit", "block", "interval".
func (r *Rotator) ShouldRotate(agentID string, reason string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	cfg := r.configs[agentID]
	if cfg == nil || !cfg.Enabled || len(cfg.ProxyPool) < 2 {
		return false
	}

	switch reason {
	case "rateLimit":
		return cfg.RotateOnRateLimit
	case "block":
		return cfg.RotateOnBlock
	case "interval":
		if cfg.RotateInterval <= 0 {
			return false
		}
		return time.Since(cfg.lastRotation) >= cfg.RotateInterval
	default:
		return false
	}
}

// Rotate advances to the next proxy in the pool for the given agent.
// Returns the new proxy URL.
func (r *Rotator) Rotate(agentID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cfg := r.configs[agentID]
	if cfg == nil {
		return "", fmt.Errorf("no rotation config for agent %q", agentID)
	}
	if len(cfg.ProxyPool) == 0 {
		return "", fmt.Errorf("empty proxy pool for agent %q", agentID)
	}

	cfg.CurrentIndex = (cfg.CurrentIndex + 1) % len(cfg.ProxyPool)
	cfg.lastRotation = time.Now()
	return cfg.ProxyPool[cfg.CurrentIndex], nil
}

// CurrentProxy returns the current proxy URL for the given agent.
func (r *Rotator) CurrentProxy(agentID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	cfg := r.configs[agentID]
	if cfg == nil || len(cfg.ProxyPool) == 0 {
		return ""
	}
	return cfg.ProxyPool[cfg.CurrentIndex]
}

// OnRateLimit is called when a rate limit (429) is detected for an agent.
// If the agent is configured to rotate on rate limits, a rotation is triggered.
func (r *Rotator) OnRateLimit(agentID string) (rotated bool, newProxy string, err error) {
	if !r.ShouldRotate(agentID, "rateLimit") {
		return false, "", nil
	}
	proxy, err := r.Rotate(agentID)
	if err != nil {
		return false, "", err
	}
	return true, proxy, nil
}

// OnBlock is called when an IP block is detected for an agent.
// If the agent is configured to rotate on blocks, a rotation is triggered.
func (r *Rotator) OnBlock(agentID string) (rotated bool, newProxy string, err error) {
	if !r.ShouldRotate(agentID, "block") {
		return false, "", nil
	}
	proxy, err := r.Rotate(agentID)
	if err != nil {
		return false, "", err
	}
	return true, proxy, nil
}
