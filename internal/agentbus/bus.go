// Package agentbus provides agent-to-agent communication with user approval.
//
// Design principles:
// 1. User controls which agents can communicate (allowlist)
// 2. Messages are queued, not direct — user can review before delivery
// 3. Agents can request help from other agents via the bus
// 4. All messages are logged in the vault for audit
//
// Flow:
//   Agent A calls "delegate" or "ask" → message queued in bus
//   If auto-approve is on for this pair → delivered immediately
//   Otherwise → held for user approval (TUI shows pending)
//   Agent B receives the message → processes → replies
//   Reply delivered back to Agent A
package agentbus

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// MessageType identifies the kind of inter-agent message.
type MessageType string

const (
	// Ask requests information from another agent.
	Ask MessageType = "ask"
	// Delegate requests another agent to perform a task.
	Delegate MessageType = "delegate"
	// Reply is a response to an ask or delegate.
	Reply MessageType = "reply"
	// Notify sends a one-way notification (no reply expected).
	Notify MessageType = "notify"
)

// Message is a communication between agents.
type Message struct {
	ID        string      `json:"id"`
	Type      MessageType `json:"type"`
	FromAgent string      `json:"fromAgent"`
	ToAgent   string      `json:"toAgent"`
	Content   string      `json:"content"`
	ReplyTo   string      `json:"replyTo,omitempty"` // ID of message being replied to
	Status    string      `json:"status"`            // pending, approved, delivered, rejected
	CreatedAt time.Time   `json:"createdAt"`
}

// Policy controls whether agent pairs can communicate automatically.
type Policy struct {
	FromAgent   string `json:"fromAgent"`   // "*" for any
	ToAgent     string `json:"toAgent"`     // "*" for any
	AutoApprove bool   `json:"autoApprove"` // true = deliver without user approval
}

// Bus manages inter-agent messaging.
type Bus struct {
	mu         sync.RWMutex
	messages   []Message
	policies   []Policy
	pending    chan Message         // messages waiting for delivery
	handlers   map[string]Handler   // agentID → delivery handler
	onPending  func(Message)        // callback when message needs approval
	idCounter  int
}

// Handler receives messages for a specific agent.
type Handler func(msg Message)

// New creates a new agent communication bus.
func New() *Bus {
	return &Bus{
		pending:  make(chan Message, 100),
		handlers: make(map[string]Handler),
	}
}

// SetPendingCallback sets a function called when a message needs user approval.
func (b *Bus) SetPendingCallback(fn func(Message)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onPending = fn
}

// AddPolicy adds a communication policy between agents.
func (b *Bus) AddPolicy(from, to string, autoApprove bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Update existing policy if present
	for i, p := range b.policies {
		if p.FromAgent == from && p.ToAgent == to {
			b.policies[i].AutoApprove = autoApprove
			return
		}
	}
	b.policies = append(b.policies, Policy{
		FromAgent:   from,
		ToAgent:     to,
		AutoApprove: autoApprove,
	})
}

// RemovePolicy removes a communication policy.
func (b *Bus) RemovePolicy(from, to string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, p := range b.policies {
		if p.FromAgent == from && p.ToAgent == to {
			b.policies = append(b.policies[:i], b.policies[i+1:]...)
			return
		}
	}
}

// RegisterHandler sets the message delivery handler for an agent.
func (b *Bus) RegisterHandler(agentID string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[agentID] = h
}

// UnregisterHandler removes the handler for an agent.
func (b *Bus) UnregisterHandler(agentID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.handlers, agentID)
}

// Send queues a message from one agent to another.
// Returns the message ID. The message may require user approval before delivery.
func (b *Bus) Send(msgType MessageType, from, to, content, replyTo string) (string, error) {
	b.mu.Lock()
	b.idCounter++
	id := fmt.Sprintf("msg-%d-%d", time.Now().UnixMilli(), b.idCounter)

	msg := Message{
		ID:        id,
		Type:      msgType,
		FromAgent: from,
		ToAgent:   to,
		Content:   content,
		ReplyTo:   replyTo,
		Status:    "pending",
		CreatedAt: time.Now(),
	}

	// Check if auto-approve applies
	autoApprove := b.isAutoApproved(from, to)
	b.messages = append(b.messages, msg)
	onPending := b.onPending
	b.mu.Unlock()

	if autoApprove {
		b.deliver(id)
	} else {
		log.Printf("agentbus: message %s from %s to %s awaiting approval", id, from, to)
		if onPending != nil {
			onPending(msg)
		}
	}

	return id, nil
}

// Approve approves a pending message for delivery.
func (b *Bus) Approve(msgID string) error {
	b.mu.RLock()
	found := false
	for _, m := range b.messages {
		if m.ID == msgID && m.Status == "pending" {
			found = true
			break
		}
	}
	b.mu.RUnlock()

	if !found {
		return fmt.Errorf("message %s not found or not pending", msgID)
	}
	b.deliver(msgID)
	return nil
}

// Reject rejects a pending message.
func (b *Bus) Reject(msgID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, m := range b.messages {
		if m.ID == msgID && m.Status == "pending" {
			b.messages[i].Status = "rejected"
			return nil
		}
	}
	return fmt.Errorf("message %s not found or not pending", msgID)
}

// PendingMessages returns all messages awaiting approval.
func (b *Bus) PendingMessages() []Message {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var pending []Message
	for _, m := range b.messages {
		if m.Status == "pending" {
			pending = append(pending, m)
		}
	}
	return pending
}

// History returns all messages (for audit).
func (b *Bus) History() []Message {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]Message, len(b.messages))
	copy(result, b.messages)
	return result
}

// Policies returns all configured policies.
func (b *Bus) Policies() []Policy {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]Policy, len(b.policies))
	copy(result, b.policies)
	return result
}

// MarshalJSON returns the bus state as JSON (for persistence).
func (b *Bus) MarshalJSON() ([]byte, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return json.Marshal(struct {
		Messages []Message `json:"messages"`
		Policies []Policy  `json:"policies"`
	}{b.messages, b.policies})
}

// deliver transitions a message to "delivered" and calls the target agent's handler.
func (b *Bus) deliver(msgID string) {
	b.mu.Lock()
	var msg *Message
	for i := range b.messages {
		if b.messages[i].ID == msgID {
			b.messages[i].Status = "delivered"
			msg = &b.messages[i]
			break
		}
	}
	handler := b.handlers[msg.ToAgent]
	b.mu.Unlock()

	if msg == nil {
		return
	}

	log.Printf("agentbus: delivering %s from %s to %s: %s",
		msg.Type, msg.FromAgent, msg.ToAgent, msg.Content[:min(50, len(msg.Content))])

	if handler != nil {
		go handler(*msg)
	}
}

// isAutoApproved checks if a from→to pair has auto-approve enabled.
// Must be called with b.mu held.
func (b *Bus) isAutoApproved(from, to string) bool {
	for _, p := range b.policies {
		if (p.FromAgent == from || p.FromAgent == "*") &&
			(p.ToAgent == to || p.ToAgent == "*") {
			return p.AutoApprove
		}
	}
	return false // default: require approval
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
