// Package webhooks fires HTTP webhooks for VulpineOS events.
package webhooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// EventType identifies the kind of webhook event.
type EventType string

const (
	AgentCompleted    EventType = "agent.completed"
	AgentFailed       EventType = "agent.failed"
	AgentPaused       EventType = "agent.paused"
	RateLimitDetected EventType = "rate_limit.detected"
	InjectionDetected EventType = "injection.detected"
	BudgetAlert       EventType = "budget.alert"
	BudgetExceeded    EventType = "budget.exceeded"
)

// Webhook is a registered webhook endpoint.
type Webhook struct {
	ID     string      `json:"id"`
	URL    string      `json:"url"`
	Events []EventType `json:"events"` // empty = all events
	Secret string      `json:"secret,omitempty"`
	Active bool        `json:"active"`
}

// Payload is the body sent to webhook endpoints.
type Payload struct {
	Event     EventType              `json:"event"`
	Timestamp time.Time              `json:"timestamp"`
	Data      map[string]interface{} `json:"data"`
}

// Manager handles webhook registration and delivery.
type Manager struct {
	mu       sync.RWMutex
	webhooks []Webhook
	client   *http.Client
	counter  int
}

// New creates a new webhook manager.
func New() *Manager {
	return &Manager{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Register adds a webhook endpoint.
func (m *Manager) Register(url string, events []EventType, secret string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counter++
	id := fmt.Sprintf("wh-%d", m.counter)
	m.webhooks = append(m.webhooks, Webhook{
		ID:     id,
		URL:    url,
		Events: events,
		Secret: secret,
		Active: true,
	})
	return id
}

// Unregister removes a webhook.
func (m *Manager) Unregister(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, w := range m.webhooks {
		if w.ID == id {
			m.webhooks = append(m.webhooks[:i], m.webhooks[i+1:]...)
			return
		}
	}
}

// List returns all registered webhooks.
func (m *Manager) List() []Webhook {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Webhook, len(m.webhooks))
	copy(result, m.webhooks)
	return result
}

// Fire sends an event to all matching webhooks.
func (m *Manager) Fire(event EventType, data map[string]interface{}) {
	m.mu.RLock()
	var targets []Webhook
	for _, w := range m.webhooks {
		if !w.Active {
			continue
		}
		if len(w.Events) == 0 || containsEvent(w.Events, event) {
			targets = append(targets, w)
		}
	}
	m.mu.RUnlock()

	if len(targets) == 0 {
		return
	}

	payload := Payload{
		Event:     event,
		Timestamp: time.Now(),
		Data:      data,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("webhooks: marshal error: %v", err)
		return
	}

	for _, wh := range targets {
		go m.deliver(wh, body)
	}
}

func (m *Manager) deliver(wh Webhook, body []byte) {
	req, err := http.NewRequest("POST", wh.URL, bytes.NewReader(body))
	if err != nil {
		log.Printf("webhooks: request error for %s: %v", wh.URL, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-VulpineOS-Event", string(AgentCompleted))
	if wh.Secret != "" {
		req.Header.Set("X-VulpineOS-Secret", wh.Secret)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		log.Printf("webhooks: delivery failed to %s: %v", wh.URL, err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Printf("webhooks: %s returned %d", wh.URL, resp.StatusCode)
	}
}

func containsEvent(events []EventType, target EventType) bool {
	for _, e := range events {
		if e == target {
			return true
		}
	}
	return false
}
