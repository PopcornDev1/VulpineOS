package monitor

import (
	"strings"
	"sync"
	"time"
)

// AlertType classifies the kind of detection alert.
type AlertType string

const (
	AlertRateLimit AlertType = "rate_limit"
	AlertCaptcha   AlertType = "captcha"
	AlertIPBlock   AlertType = "ip_block"
)

// Alert represents a detected anomaly for an agent.
type Alert struct {
	AgentID   string
	Type      AlertType
	Details   string
	Timestamp time.Time
}

// Monitor scans agent messages for rate limiting and blocking patterns.
type Monitor struct {
	alertCh chan Alert

	mu            sync.Mutex
	blockCounts   map[string]int // per-agent ip_block match count
	disposed      bool
}

// New creates a new Monitor with a buffered alert channel.
func New() *Monitor {
	return &Monitor{
		alertCh:     make(chan Alert, 64),
		blockCounts: make(map[string]int),
	}
}

// AlertChan returns a receive-only channel of alerts.
func (m *Monitor) AlertChan() <-chan Alert {
	return m.alertCh
}

// CheckMessage scans the content string for rate limit, captcha, and IP block patterns.
// Alerts are sent on the alert channel when patterns are detected.
func (m *Monitor) CheckMessage(agentID, content string) {
	m.mu.Lock()
	if m.disposed {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	lower := strings.ToLower(content)

	// Rate limit detection
	if strings.Contains(lower, "429") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "rate limit") {
		m.sendAlert(Alert{
			AgentID:   agentID,
			Type:      AlertRateLimit,
			Details:   "Rate limit pattern detected",
			Timestamp: time.Now(),
		})
	}

	// Captcha detection
	if strings.Contains(lower, "captcha") ||
		strings.Contains(lower, "challenge") ||
		strings.Contains(lower, "verify you are human") {
		m.sendAlert(Alert{
			AgentID:   agentID,
			Type:      AlertCaptcha,
			Details:   "Captcha/challenge pattern detected",
			Timestamp: time.Now(),
		})
	}

	// IP block detection — only alert after 2+ matches per agent
	if strings.Contains(lower, "blocked") ||
		strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "access denied") {
		m.mu.Lock()
		m.blockCounts[agentID]++
		count := m.blockCounts[agentID]
		m.mu.Unlock()

		if count >= 2 {
			m.sendAlert(Alert{
				AgentID:   agentID,
				Type:      AlertIPBlock,
				Details:   "Repeated IP block pattern detected",
				Timestamp: time.Now(),
			})
		}
	}
}

// sendAlert sends an alert on the channel without blocking.
func (m *Monitor) sendAlert(a Alert) {
	select {
	case m.alertCh <- a:
	default:
		// Channel full, drop alert to avoid blocking.
	}
}

// Dispose closes the alert channel and marks the monitor as disposed.
func (m *Monitor) Dispose() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.disposed {
		m.disposed = true
		close(m.alertCh)
	}
}
