package scripting

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"vulpineos/internal/juggler"
)

// mockTransport records calls and returns canned responses.
type mockTransport struct {
	mu       sync.Mutex
	calls    []mockCall
	nextID   int
	sendCh   chan *juggler.Message
	recvCh   chan *juggler.Message
	closed   bool
	closeCh  chan struct{}
}

type mockCall struct {
	Method string
	Params json.RawMessage
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		sendCh:  make(chan *juggler.Message, 100),
		recvCh:  make(chan *juggler.Message, 100),
		closeCh: make(chan struct{}),
	}
}

func (m *mockTransport) Send(msg *juggler.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("transport closed")
	}
	m.calls = append(m.calls, mockCall{Method: msg.Method, Params: msg.Params})

	// Auto-respond with success.
	resp := &juggler.Message{
		ID:     msg.ID,
		Result: json.RawMessage(`{"result":{"value":"mock"}}`),
	}
	m.recvCh <- resp
	return nil
}

func (m *mockTransport) Receive() (*juggler.Message, error) {
	select {
	case msg := <-m.recvCh:
		return msg, nil
	case <-m.closeCh:
		return nil, fmt.Errorf("transport closed")
	}
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		close(m.closeCh)
	}
	return nil
}

func (m *mockTransport) getCalls() []mockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]mockCall, len(m.calls))
	copy(result, m.calls)
	return result
}

func TestParseScript(t *testing.T) {
	data := []byte(`{"steps":[
		{"action":"navigate","target":"https://example.com"},
		{"action":"extract","target":"h1","store":"heading"},
		{"action":"screenshot","store":"page.png"}
	]}`)

	script, err := ParseScript(data)
	if err != nil {
		t.Fatalf("ParseScript error: %v", err)
	}
	if len(script.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(script.Steps))
	}
	if script.Steps[0].Action != "navigate" {
		t.Errorf("step 0: expected navigate, got %s", script.Steps[0].Action)
	}
	if script.Steps[1].Store != "heading" {
		t.Errorf("step 1: expected store=heading, got %s", script.Steps[1].Store)
	}
	if script.Steps[2].Action != "screenshot" {
		t.Errorf("step 2: expected screenshot, got %s", script.Steps[2].Action)
	}
}

func TestParseScriptInvalidJSON(t *testing.T) {
	_, err := ParseScript([]byte(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestVariableSetGet(t *testing.T) {
	transport := newMockTransport()
	client := juggler.NewClient(transport)
	defer client.Close()

	engine := NewEngine(client)

	engine.SetVar("foo", "bar")
	if v := engine.GetVar("foo"); v != "bar" {
		t.Errorf("expected 'bar', got %q", v)
	}
	if v := engine.GetVar("missing"); v != "" {
		t.Errorf("expected empty string for missing var, got %q", v)
	}
}

func TestSetActionStoresVariable(t *testing.T) {
	transport := newMockTransport()
	client := juggler.NewClient(transport)
	defer client.Close()

	engine := NewEngine(client)

	script := &Script{
		Steps: []Step{
			{Action: "set", Target: "greeting", Value: "hello"},
		},
	}

	if err := engine.Execute(script); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if v := engine.GetVar("greeting"); v != "hello" {
		t.Errorf("expected 'hello', got %q", v)
	}
}

func TestExecuteNavigate(t *testing.T) {
	transport := newMockTransport()
	client := juggler.NewClient(transport)
	defer client.Close()

	engine := NewEngine(client)

	script := &Script{
		Steps: []Step{
			{Action: "navigate", Target: "https://example.com"},
		},
	}

	if err := engine.Execute(script); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	calls := transport.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Method != "Page.navigate" {
		t.Errorf("expected Page.navigate, got %s", calls[0].Method)
	}

	// Verify params contain the URL.
	var params map[string]interface{}
	if err := json.Unmarshal(calls[0].Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params["url"] != "https://example.com" {
		t.Errorf("expected url=https://example.com, got %v", params["url"])
	}
}

func TestExecuteEmptyScript(t *testing.T) {
	transport := newMockTransport()
	client := juggler.NewClient(transport)
	defer client.Close()

	engine := NewEngine(client)

	script := &Script{Steps: []Step{}}
	if err := engine.Execute(script); err != nil {
		t.Fatalf("Execute empty script error: %v", err)
	}

	calls := transport.getCalls()
	if len(calls) != 0 {
		t.Errorf("expected 0 calls for empty script, got %d", len(calls))
	}
}

func TestExecuteMultipleSteps(t *testing.T) {
	transport := newMockTransport()
	client := juggler.NewClient(transport)
	defer client.Close()

	engine := NewEngine(client)

	script := &Script{
		Steps: []Step{
			{Action: "navigate", Target: "https://example.com"},
			{Action: "screenshot", Store: "page.png"},
		},
	}

	if err := engine.Execute(script); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	calls := transport.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Method != "Page.navigate" {
		t.Errorf("call 0: expected Page.navigate, got %s", calls[0].Method)
	}
	if calls[1].Method != "Page.screenshot" {
		t.Errorf("call 1: expected Page.screenshot, got %s", calls[1].Method)
	}

	// Screenshot should store variable.
	if v := engine.GetVar("page.png"); v != "page.png" {
		t.Errorf("expected screenshot store='page.png', got %q", v)
	}
}

func TestExecuteUnknownAction(t *testing.T) {
	transport := newMockTransport()
	client := juggler.NewClient(transport)
	defer client.Close()

	engine := NewEngine(client)

	script := &Script{
		Steps: []Step{
			{Action: "bogus"},
		},
	}

	err := engine.Execute(script)
	if err == nil {
		t.Error("expected error for unknown action")
	}
}

func TestIfCondition(t *testing.T) {
	transport := newMockTransport()
	client := juggler.NewClient(transport)
	defer client.Close()

	engine := NewEngine(client)
	engine.SetVar("status", "ok")

	// Passing condition.
	script := &Script{
		Steps: []Step{
			{Action: "if", Target: "status", Value: "ok"},
		},
	}
	if err := engine.Execute(script); err != nil {
		t.Fatalf("if with matching value should pass: %v", err)
	}

	// Failing condition.
	script = &Script{
		Steps: []Step{
			{Action: "if", Target: "status", Value: "error"},
		},
	}
	if err := engine.Execute(script); err == nil {
		t.Error("if with non-matching value should fail")
	}
}

func TestVariableExpansion(t *testing.T) {
	transport := newMockTransport()
	client := juggler.NewClient(transport)
	defer client.Close()

	engine := NewEngine(client)

	script := &Script{
		Steps: []Step{
			{Action: "set", Target: "host", Value: "https://example.com"},
			{Action: "navigate", Target: "${host}/page"},
		},
	}

	if err := engine.Execute(script); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	calls := transport.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 juggler call, got %d", len(calls))
	}

	var params map[string]interface{}
	if err := json.Unmarshal(calls[0].Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params["url"] != "https://example.com/page" {
		t.Errorf("expected expanded URL, got %v", params["url"])
	}
}
