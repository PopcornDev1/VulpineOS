package webhooks

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRegisterAndList(t *testing.T) {
	m := New()
	id := m.Register("https://example.com/hook", []EventType{AgentCompleted}, "secret")
	if id == "" {
		t.Fatal("Register returned empty ID")
	}
	list := m.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 webhook, got %d", len(list))
	}
	if list[0].URL != "https://example.com/hook" {
		t.Error("wrong URL")
	}
	if list[0].Secret != "secret" {
		t.Error("wrong secret")
	}
}

func TestUnregister(t *testing.T) {
	m := New()
	id := m.Register("https://example.com/hook", nil, "")
	m.Unregister(id)
	if len(m.List()) != 0 {
		t.Error("should have 0 webhooks after unregister")
	}
}

func TestFireDelivers(t *testing.T) {
	var received Payload
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := New()
	m.Register(srv.URL, nil, "mysecret") // nil events = all events

	m.Fire(AgentCompleted, map[string]interface{}{
		"agentId": "agent-1",
		"result":  "success",
	})

	// Wait for async delivery
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if received.Event != AgentCompleted {
		t.Errorf("event = %s, want agent.completed", received.Event)
	}
	if received.Data["agentId"] != "agent-1" {
		t.Error("missing agentId in data")
	}
}

func TestEventFiltering(t *testing.T) {
	var called bool
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		called = true
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := New()
	m.Register(srv.URL, []EventType{RateLimitDetected}, "") // only rate limit events

	// Fire a different event — should NOT be delivered
	m.Fire(AgentCompleted, map[string]interface{}{})
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	if called {
		mu.Unlock()
		t.Fatal("webhook should not fire for non-matching event")
	}
	mu.Unlock()

	// Fire matching event — should be delivered
	m.Fire(RateLimitDetected, map[string]interface{}{"url": "example.com"})
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	if !called {
		mu.Unlock()
		t.Fatal("webhook should fire for matching event")
	}
	mu.Unlock()
}

func TestSecretHeader(t *testing.T) {
	var gotSecret string
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotSecret = r.Header.Get("X-VulpineOS-Secret")
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := New()
	m.Register(srv.URL, nil, "my-secret-key")
	m.Fire(AgentCompleted, nil)

	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	if gotSecret != "my-secret-key" {
		t.Errorf("secret = %q, want my-secret-key", gotSecret)
	}
	mu.Unlock()
}

func TestInactiveWebhook(t *testing.T) {
	m := New()
	id := m.Register("https://example.com/hook", nil, "")

	// Deactivate
	m.mu.Lock()
	for i := range m.webhooks {
		if m.webhooks[i].ID == id {
			m.webhooks[i].Active = false
		}
	}
	m.mu.Unlock()

	// Fire — should not panic or attempt delivery
	m.Fire(AgentCompleted, nil)
}
