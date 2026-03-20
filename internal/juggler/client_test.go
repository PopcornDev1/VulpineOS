package juggler

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// memTransport is an in-memory Transport for testing.
type memTransport struct {
	incoming chan *Message // messages to be received by the client
	outgoing chan *Message // messages sent by the client
	closed   chan struct{}
	once     sync.Once
}

func newMemTransport() *memTransport {
	return &memTransport{
		incoming: make(chan *Message, 64),
		outgoing: make(chan *Message, 64),
		closed:   make(chan struct{}),
	}
}

func (t *memTransport) Send(msg *Message) error {
	select {
	case <-t.closed:
		return fmt.Errorf("transport closed")
	case t.outgoing <- msg:
		return nil
	}
}

func (t *memTransport) Receive() (*Message, error) {
	select {
	case <-t.closed:
		return nil, fmt.Errorf("transport closed")
	case msg := <-t.incoming:
		return msg, nil
	}
}

func (t *memTransport) Close() error {
	t.once.Do(func() { close(t.closed) })
	return nil
}

// respondToRequests reads outgoing messages and replies with canned responses.
func respondToRequests(mt *memTransport) {
	for {
		select {
		case <-mt.closed:
			return
		case req := <-mt.outgoing:
			result, _ := json.Marshal(map[string]string{"echo": req.Method})
			mt.incoming <- &Message{
				ID:     req.ID,
				Result: result,
			}
		}
	}
}

func TestClient_Call_ReturnsResponse(t *testing.T) {
	mt := newMemTransport()
	go respondToRequests(mt)

	c := NewClient(mt)
	defer c.Close()

	result, err := c.Call("", "Test.method", nil)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	var parsed map[string]string
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if parsed["echo"] != "Test.method" {
		t.Errorf("expected echo=Test.method, got %s", parsed["echo"])
	}
}

func TestClient_Subscribe_ReceivesEvents(t *testing.T) {
	mt := newMemTransport()
	c := NewClient(mt)
	defer c.Close()

	received := make(chan json.RawMessage, 1)
	c.Subscribe("Page.loadFired", func(params json.RawMessage) {
		received <- params
	})

	payload, _ := json.Marshal(map[string]int{"timestamp": 42})
	mt.incoming <- &Message{
		Method: "Page.loadFired",
		Params: payload,
	}

	select {
	case params := <-received:
		var parsed map[string]int
		if err := json.Unmarshal(params, &parsed); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if parsed["timestamp"] != 42 {
			t.Errorf("expected timestamp=42, got %d", parsed["timestamp"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestClient_CallWithContext_TimesOut(t *testing.T) {
	mt := newMemTransport()
	// Don't respond — let it hang
	c := NewClient(mt)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.CallWithContext(ctx, "", "Slow.method", nil)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !contains(err.Error(), "context deadline exceeded") {
		t.Errorf("expected context deadline exceeded, got: %v", err)
	}
}

func TestClient_ConcurrentCalls(t *testing.T) {
	mt := newMemTransport()
	go respondToRequests(mt)

	c := NewClient(mt)
	defer c.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			method := fmt.Sprintf("Test.method%d", n)
			result, err := c.Call("", method, nil)
			if err != nil {
				errs <- fmt.Errorf("call %d failed: %w", n, err)
				return
			}
			var parsed map[string]string
			if err := json.Unmarshal(result, &parsed); err != nil {
				errs <- fmt.Errorf("call %d unmarshal: %w", n, err)
				return
			}
			if parsed["echo"] != method {
				errs <- fmt.Errorf("call %d: expected echo=%s, got %s", n, method, parsed["echo"])
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
