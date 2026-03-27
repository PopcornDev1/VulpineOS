package agentbus

import (
	"sync"
	"testing"
	"time"
)

func TestNewBus(t *testing.T) {
	b := New()
	if b == nil {
		t.Fatal("New returned nil")
	}
	if len(b.PendingMessages()) != 0 {
		t.Error("new bus should have no pending messages")
	}
	if len(b.History()) != 0 {
		t.Error("new bus should have no history")
	}
}

func TestSendRequiresApproval(t *testing.T) {
	b := New()

	var pendingReceived Message
	b.SetPendingCallback(func(msg Message) {
		pendingReceived = msg
	})

	id, err := b.Send(Ask, "agent-1", "agent-2", "what is 2+2?", "")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("Send returned empty ID")
	}

	// Should be pending (no policy → no auto-approve)
	pending := b.PendingMessages()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
	if pending[0].Status != "pending" {
		t.Errorf("status = %s, want pending", pending[0].Status)
	}
	if pendingReceived.ID != id {
		t.Error("pending callback not called")
	}
}

func TestAutoApprovePolicy(t *testing.T) {
	b := New()

	var delivered bool
	var mu sync.Mutex
	b.RegisterHandler("agent-2", func(msg Message) {
		mu.Lock()
		delivered = true
		mu.Unlock()
	})

	b.AddPolicy("agent-1", "agent-2", true)

	_, err := b.Send(Delegate, "agent-1", "agent-2", "do something", "")
	if err != nil {
		t.Fatal(err)
	}

	// Should be auto-approved and delivered (handler runs in goroutine)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	d := delivered
	mu.Unlock()
	if !d {
		t.Error("message should have been auto-delivered")
	}

	// No pending messages
	if len(b.PendingMessages()) != 0 {
		t.Error("should have no pending messages after auto-approve")
	}
}

func TestManualApproval(t *testing.T) {
	b := New()

	var delivered bool
	var mu sync.Mutex
	b.RegisterHandler("agent-2", func(msg Message) {
		mu.Lock()
		delivered = true
		mu.Unlock()
	})

	id, _ := b.Send(Ask, "agent-1", "agent-2", "help?", "")

	// Not delivered yet
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if delivered {
		mu.Unlock()
		t.Fatal("should not be delivered before approval")
	}
	mu.Unlock()

	// Approve
	if err := b.Approve(id); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	if !delivered {
		mu.Unlock()
		t.Fatal("should be delivered after approval")
	}
	mu.Unlock()
}

func TestRejectMessage(t *testing.T) {
	b := New()
	id, _ := b.Send(Ask, "agent-1", "agent-2", "help?", "")

	if err := b.Reject(id); err != nil {
		t.Fatal(err)
	}

	history := b.History()
	if len(history) != 1 || history[0].Status != "rejected" {
		t.Error("message should be rejected")
	}
	if len(b.PendingMessages()) != 0 {
		t.Error("should have no pending after reject")
	}
}

func TestWildcardPolicy(t *testing.T) {
	b := New()

	var delivered bool
	var mu sync.Mutex
	b.RegisterHandler("any-agent", func(msg Message) {
		mu.Lock()
		delivered = true
		mu.Unlock()
	})

	b.AddPolicy("*", "*", true) // all agents can talk

	b.Send(Notify, "agent-x", "any-agent", "hello", "")
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if !delivered {
		mu.Unlock()
		t.Fatal("wildcard policy should auto-deliver")
	}
	mu.Unlock()
}

func TestReplyMessage(t *testing.T) {
	b := New()
	b.AddPolicy("*", "*", true)

	id, _ := b.Send(Ask, "a", "b", "question", "")
	b.Send(Reply, "b", "a", "answer", id)

	history := b.History()
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}
	if history[1].ReplyTo != id {
		t.Error("reply should reference original message")
	}
}

func TestRemovePolicy(t *testing.T) {
	b := New()
	b.AddPolicy("a", "b", true)
	if len(b.Policies()) != 1 {
		t.Fatal("expected 1 policy")
	}
	b.RemovePolicy("a", "b")
	if len(b.Policies()) != 0 {
		t.Error("expected 0 policies after remove")
	}
}

func TestUpdatePolicy(t *testing.T) {
	b := New()
	b.AddPolicy("a", "b", true)
	b.AddPolicy("a", "b", false) // update
	policies := b.Policies()
	if len(policies) != 1 {
		t.Fatal("should have 1 policy")
	}
	if policies[0].AutoApprove {
		t.Error("policy should be updated to false")
	}
}

func TestMarshalJSON(t *testing.T) {
	b := New()
	b.AddPolicy("a", "b", true)
	b.Send(Notify, "a", "b", "test", "")

	data, err := b.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 10 {
		t.Error("JSON too small")
	}
}

func TestNoHandler(t *testing.T) {
	b := New()
	b.AddPolicy("a", "b", true)

	// Should not panic even without handler
	_, err := b.Send(Notify, "a", "b", "hello", "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnregisterHandler(t *testing.T) {
	b := New()
	b.RegisterHandler("a", func(msg Message) {})
	b.UnregisterHandler("a")
	// Should not panic
	b.AddPolicy("x", "a", true)
	b.Send(Notify, "x", "a", "test", "")
}
