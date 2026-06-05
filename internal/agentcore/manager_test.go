package agentcore

import (
	"path/filepath"
	"testing"
	"time"

	"vulpineos/internal/agentmsg"
	"vulpineos/internal/runtimeaudit"
	"vulpineos/internal/vault"
)

type fakeStore struct {
	agent *vault.Agent
	err   error
}

func (f *fakeStore) GetAgent(id string) (*vault.Agent, error) { return f.agent, f.err }

func TestResolveContextID(t *testing.T) {
	m := NewManager(nil, Config{})
	defer m.Dispose()

	// No store -> empty.
	if got := m.resolveContextID("a1"); got != "" {
		t.Errorf("no store: got %q, want empty", got)
	}

	// Store with a pooled context id in metadata -> resolved.
	m.SetVault(&fakeStore{agent: &vault.Agent{
		ID:       "a1",
		Metadata: vault.MarshalAgentMetadata(vault.AgentMetadata{ContextID: "ctx-123"}),
	}})
	if got := m.resolveContextID("a1"); got != "ctx-123" {
		t.Errorf("resolved context = %q, want ctx-123", got)
	}

	// Empty/blank metadata -> empty.
	m.SetVault(&fakeStore{agent: &vault.Agent{ID: "a1", Metadata: "{}"}})
	if got := m.resolveContextID("a1"); got != "" {
		t.Errorf("blank metadata: got %q, want empty", got)
	}
}

func TestManagerLogsNativeRunLifecycleOnStartupFailure(t *testing.T) {
	db := openManagerTestVault(t)
	m := NewManager(nil, Config{Provider: "openrouter", Model: "test/model"})
	m.SetRuntimeAudit(runtimeaudit.New(db))
	defer m.Dispose()

	statusCh := m.StatusChan()
	if _, err := m.SpawnWithSessionIsolated("agent-1", "do something", "session-1", "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	waitForManagerStatus(t, statusCh, "error")

	events, err := db.ListRuntimeEvents(vault.RuntimeEventFilter{Component: "agentcore", Limit: 10})
	if err != nil {
		t.Fatalf("list runtime events: %v", err)
	}
	if !hasRuntimeEvent(events, "native_agent_started") {
		t.Fatalf("runtime events missing native_agent_started: %#v", events)
	}
	if !hasRuntimeEvent(events, "native_agent_failed") {
		t.Fatalf("runtime events missing native_agent_failed: %#v", events)
	}
}

func TestManagerEventsLogToolActivity(t *testing.T) {
	db := openManagerTestVault(t)
	m := NewManager(nil, Config{})
	m.SetRuntimeAudit(runtimeaudit.New(db))
	defer m.Dispose()

	events := &managerEvents{m: m, agentID: "agent-1"}
	events.OnToolCall("vulpine_navigate", `{"url":"https://example.com"}`)
	events.OnToolResult("vulpine_navigate", "ok", false)
	events.OnToolResult("vulpine_click_ref", "missing ref", true)

	stored, err := db.ListRuntimeEvents(vault.RuntimeEventFilter{Component: "agentcore", Limit: 10})
	if err != nil {
		t.Fatalf("list runtime events: %v", err)
	}
	for _, event := range []string{"native_agent_tool_call", "native_agent_tool_completed", "native_agent_tool_failed"} {
		if !hasRuntimeEvent(stored, event) {
			t.Fatalf("runtime events missing %s: %#v", event, stored)
		}
	}
}

func openManagerTestVault(t *testing.T) *vault.DB {
	t.Helper()
	db, err := vault.OpenPath(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func waitForManagerStatus(t *testing.T, ch <-chan agentmsg.AgentStatus, want string) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case status, ok := <-ch:
			if !ok {
				t.Fatalf("status channel closed before %q", want)
			}
			if status.Status == want {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for status %q", want)
		}
	}
}

func hasRuntimeEvent(events []vault.RuntimeEvent, event string) bool {
	for _, item := range events {
		if item.Event == event {
			return true
		}
	}
	return false
}
