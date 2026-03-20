package pool

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"vulpineos/internal/juggler"
)

// mockTransport is a mock Juggler transport for pool tests.
type mockTransport struct {
	mu      sync.Mutex
	counter int
	closed  chan struct{}
	once    sync.Once
}

func newMockTransport() *mockTransport {
	return &mockTransport{closed: make(chan struct{})}
}

func (t *mockTransport) Send(msg *juggler.Message) error {
	select {
	case <-t.closed:
		return fmt.Errorf("closed")
	default:
	}
	// Auto-respond by feeding into the client's read loop
	return nil
}

func (t *mockTransport) Receive() (*juggler.Message, error) {
	// Block forever — pool tests don't rely on the read loop delivering responses
	// We'll use a different approach: direct response via channel
	<-t.closed
	return nil, fmt.Errorf("closed")
}

func (t *mockTransport) Close() error {
	t.once.Do(func() { close(t.closed) })
	return nil
}

func (t *mockTransport) nextContextID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counter++
	return fmt.Sprintf("ctx-%d", t.counter)
}

// autoRespondTransport responds to Juggler calls in-band.
type autoRespondTransport struct {
	mu       sync.Mutex
	counter  int
	incoming chan *juggler.Message
	closed   chan struct{}
	once     sync.Once
}

func newAutoRespondTransport() *autoRespondTransport {
	return &autoRespondTransport{
		incoming: make(chan *juggler.Message, 256),
		closed:   make(chan struct{}),
	}
}

func (t *autoRespondTransport) Send(msg *juggler.Message) error {
	select {
	case <-t.closed:
		return fmt.Errorf("closed")
	default:
	}

	// Generate response based on method
	var result json.RawMessage
	switch msg.Method {
	case "Browser.createBrowserContext":
		t.mu.Lock()
		t.counter++
		id := fmt.Sprintf("ctx-%d", t.counter)
		t.mu.Unlock()
		result, _ = json.Marshal(map[string]string{"browserContextId": id})
	case "Browser.removeBrowserContext":
		result, _ = json.Marshal(map[string]string{})
	default:
		result, _ = json.Marshal(map[string]string{})
	}

	resp := &juggler.Message{
		ID:     msg.ID,
		Result: result,
	}
	t.incoming <- resp
	return nil
}

func (t *autoRespondTransport) Receive() (*juggler.Message, error) {
	select {
	case <-t.closed:
		return nil, fmt.Errorf("closed")
	case msg := <-t.incoming:
		return msg, nil
	}
}

func (t *autoRespondTransport) Close() error {
	t.once.Do(func() { close(t.closed) })
	return nil
}

func TestPool_AcquireReleaseCycle(t *testing.T) {
	tr := newAutoRespondTransport()
	client := juggler.NewClient(tr)
	defer client.Close()

	p := New(client, Config{
		PreWarm:        2,
		MaxActive:      5,
		MaxUsesPerSlot: 100,
	})
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	slot, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if slot.ContextID == "" {
		t.Fatal("expected non-empty ContextID")
	}

	p.Release(slot)

	// Should be able to acquire again (reuse)
	slot2, err := p.Acquire()
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if slot2.ContextID == "" {
		t.Fatal("expected non-empty ContextID on reuse")
	}
	p.Release(slot2)
}

func TestPool_CloseUnblocksAcquire(t *testing.T) {
	tr := newAutoRespondTransport()
	client := juggler.NewClient(tr)
	defer client.Close()

	p := New(client, Config{
		PreWarm:        0,
		MaxActive:      1,
		MaxUsesPerSlot: 100,
	})
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Acquire the only slot
	slot, err := p.Acquire()
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	_ = slot

	done := make(chan error, 1)
	go func() {
		// This should block because pool is at capacity
		_, err := p.Acquire()
		done <- err
	}()

	// Give the goroutine a moment to block
	time.Sleep(50 * time.Millisecond)

	p.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from Acquire after Close, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire did not unblock after Close")
	}
}

func TestPool_LimitsMaxActive(t *testing.T) {
	tr := newAutoRespondTransport()
	client := juggler.NewClient(tr)
	defer client.Close()

	maxActive := 3
	p := New(client, Config{
		PreWarm:        0,
		MaxActive:      maxActive,
		MaxUsesPerSlot: 100,
	})
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Close()

	// Acquire all available slots
	slots := make([]*ContextSlot, 0, maxActive)
	for i := 0; i < maxActive; i++ {
		slot, err := p.Acquire()
		if err != nil {
			t.Fatalf("Acquire %d: %v", i, err)
		}
		slots = append(slots, slot)
	}

	// Next acquire should block
	acquired := make(chan struct{}, 1)
	go func() {
		slot, err := p.Acquire()
		if err == nil {
			p.Release(slot)
		}
		acquired <- struct{}{}
	}()

	select {
	case <-acquired:
		t.Fatal("Acquire should have blocked at max capacity")
	case <-time.After(100 * time.Millisecond):
		// Good — it's blocked
	}

	// Release one slot to unblock
	p.Release(slots[0])

	select {
	case <-acquired:
		// Good — unblocked after release
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire did not unblock after Release")
	}
}
