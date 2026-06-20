package agentcore

import (
	"context"
	"path/filepath"
	"strings"
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

func TestDelegateSubAgent(t *testing.T) {
	m := NewManager(nil, Config{})
	defer m.Dispose()

	mission := Mission{
		RoleSeed:    "You are a test specialist.",
		Objective:   "Do something simple",
		Constraints: []string{"Be quick"},
		OutputSpec:  "Return 'done'",
		MaxTurns:    3,
	}

	agentID, err := m.Delegate(mission)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if agentID == "" {
		t.Fatal("expected non-empty agent ID")
	}

	// List should show the sub-agent
	list := m.List()
	found := false
	for _, a := range list {
		if a.AgentID == agentID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("delegated agent %q not found in List()", agentID)
	}
}

func TestDelegateWithParentID(t *testing.T) {
	m := NewManager(nil, Config{})
	defer m.Dispose()

	mission := Mission{
		Objective: "test",
		MaxTurns:  3,
	}

	agentID, err := m.DelegateForParentMission(mission, "parent-agent")
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}

	// Status should include parent id
	m.mu.RLock()
	ag, ok := m.agents[agentID]
	m.mu.RUnlock()
	if !ok {
		t.Fatal("agent not found in map")
	}
	if ag.parentID != "parent-agent" {
		t.Errorf("expected parentID 'parent-agent', got %q", ag.parentID)
	}
}

func TestDelegateDefaultMaxTurnsStored(t *testing.T) {
	m := NewManager(nil, Config{})
	defer m.Dispose()

	agentID, err := m.Delegate(Mission{Objective: "test default max turns"})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}

	m.mu.RLock()
	ag, ok := m.agents[agentID]
	m.mu.RUnlock()
	if !ok {
		t.Fatal("agent not found in map")
	}
	if ag.maxTurns != 60 {
		t.Fatalf("default maxTurns = %d, want 60", ag.maxTurns)
	}
}

func TestMissionMaxTurnsDefaultsToSixty(t *testing.T) {
	if got := missionMaxTurns(Mission{}); got != 60 {
		t.Fatalf("missionMaxTurns default = %d, want 60", got)
	}
	if got := missionMaxTurns(Mission{MaxTurns: 7}); got != 7 {
		t.Fatalf("missionMaxTurns explicit = %d, want 7", got)
	}
}

func TestReleaseAgent(t *testing.T) {
	m := NewManager(nil, Config{})
	defer m.Dispose()

	mission := Mission{
		Objective: "test",
		MaxTurns:  3,
	}

	agentID, err := m.Delegate(mission)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}

	if err := m.ReleaseAgent(agentID); err != nil {
		t.Fatalf("release agent: %v", err)
	}

	// Agent should be removed from list
	list := m.List()
	for _, a := range list {
		if a.AgentID == agentID {
			t.Errorf("agent %q still present after release", agentID)
		}
	}

	// Release again should fail (already released)
	if err := m.ReleaseAgent(agentID); err == nil {
		t.Error("expected error on releasing released agent")
	}
}

func TestSteerAgent(t *testing.T) {
	m := NewManager(nil, Config{})
	defer m.Dispose()

	mission := Mission{
		Objective: "test",
		MaxTurns:  3,
	}

	agentID, err := m.Delegate(mission)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}

	if err := m.SteerAgent(agentID, "focus on speed"); err != nil {
		t.Fatalf("steer agent: %v", err)
	}

	m.mu.RLock()
	ag, ok := m.agents[agentID]
	m.mu.RUnlock()
	if !ok {
		t.Fatal("agent not found")
	}
	if len(ag.inbox) != 1 {
		t.Fatalf("expected 1 inbox message, got %d", len(ag.inbox))
	}
	if ag.inbox[0] != "focus on speed" {
		t.Errorf("expected inbox[0]='focus on speed', got %q", ag.inbox[0])
	}

	// Multiple steer messages accumulate
	_ = m.SteerAgent(agentID, "also check accuracy")
	m.mu.RLock()
	ag2, _ := m.agents[agentID]
	m.mu.RUnlock()
	if len(ag2.inbox) != 2 {
		t.Errorf("expected 2 inbox messages after second steer, got %d", len(ag2.inbox))
	}

	// Steer nonexistent agent
	if err := m.SteerAgent("nonexistent", "hi"); err == nil {
		t.Error("expected error steering nonexistent agent")
	}
}

func TestAgentStatusNotFound(t *testing.T) {
	m := NewManager(nil, Config{})
	defer m.Dispose()

	_, err := m.AgentStatus("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent agent")
	}
}

func TestTruncateString(t *testing.T) {
	if got := truncateString("hello", 10); got != "hello" {
		t.Errorf("truncateString('hello', 10) = %q, want 'hello'", got)
	}
	if got := truncateString("hello world", 5); got != "hello..." {
		t.Errorf("truncateString('hello world', 5) = %q, want 'hello...'", got)
	}
	if got := truncateString("", 5); got != "" {
		t.Errorf("truncateString('', 5) = %q, want ''", got)
	}
	if got := truncateString("こんにちは", 3); got != "こんに..." {
		t.Errorf("truncateString unicode: got %q, want truncated", got)
	}
}

func TestSubAgentResultStorage(t *testing.T) {
	m := NewManager(nil, Config{})
	defer m.Dispose()

	m.mu.Lock()
	m.agents["test-sub"] = &nativeAgent{
		id:     "test-sub",
		cancel: func() {},
		status: "completed",
		result: "analysis complete: found 3 issues",
	}
	m.mu.Unlock()

	result, err := m.AgentResult("test-sub")
	if err != nil {
		t.Fatalf("AgentResult: %v", err)
	}
	if result != "analysis complete: found 3 issues" {
		t.Errorf("result = %q, want 'analysis complete: found 3 issues'", result)
	}

	// Not found
	_, err = m.AgentResult("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent agent")
	}

	// Not completed
	m.mu.Lock()
	m.agents["still-running"] = &nativeAgent{
		id:     "still-running",
		cancel: func() {},
		status: "running",
		result: "",
	}
	m.mu.Unlock()
	_, err = m.AgentResult("still-running")
	if err == nil {
		t.Error("expected error for non-completed agent")
	}
}

func TestSubAgentResultSurvivesFinish(t *testing.T) {
	m := NewManager(nil, Config{})
	defer m.Dispose()

	ag := &nativeAgent{
		id:     "test-sub-finished",
		cancel: func() {},
		status: "completed",
		result: "result that must survive finish",
	}
	m.mu.Lock()
	m.agents["test-sub-finished"] = ag
	m.mu.Unlock()

	m.finish("test-sub-finished", ag)

	m.mu.RLock()
	_, inMap := m.agents["test-sub-finished"]
	m.mu.RUnlock()
	if inMap {
		t.Error("agent should not be in map after finish")
	}

	result, err := m.AgentResult("test-sub-finished")
	if err != nil {
		t.Fatalf("AgentResult after finish: %v", err)
	}
	if result != "result that must survive finish" {
		t.Errorf("result = %q, want 'result that must survive finish'", result)
	}
}

func TestBuildSubAgentContext(t *testing.T) {
	m := NewManager(nil, Config{})
	defer m.Dispose()

	noop := func() {}
	m.mu.Lock()
	m.agents["sub-1"] = &nativeAgent{id: "sub-1", cancel: noop, parentID: "lead-1", status: "running", objective: "write tests"}
	m.agents["sub-2"] = &nativeAgent{id: "sub-2", cancel: noop, parentID: "lead-1", status: "completed", objective: "review PR"}
	m.agents["sub-other"] = &nativeAgent{id: "sub-other", cancel: noop, parentID: "other-lead", status: "running", objective: "other task"}
	m.mu.Unlock()

	ctx := m.buildSubAgentContext("lead-1")
	if ctx == "" {
		t.Fatal("expected non-empty context for lead-1")
	}
	if !strings.Contains(ctx, "sub-1") || !strings.Contains(ctx, "sub-2") {
		t.Errorf("context missing sub-agent IDs: %s", ctx)
	}
	if !strings.Contains(ctx, "write tests") || !strings.Contains(ctx, "review PR") {
		t.Errorf("context missing objectives: %s", ctx)
	}
	if strings.Contains(ctx, "sub-other") {
		t.Errorf("context should not include other lead's sub-agents: %s", ctx)
	}

	emptyCtx := m.buildSubAgentContext("lead-with-none")
	if emptyCtx != "" {
		t.Errorf("expected empty context for lead with no sub-agents, got %q", emptyCtx)
	}
}

func TestDelegateAgentThroughToolset(t *testing.T) {
	m := NewManager(nil, Config{})
	defer m.Dispose()

	// Wire the Manager as the delegation manager on a toolset.
	ts := &BrowserToolset{}
	ts.SetDelegateManager(m)

	// Delegate via tool dispatch.
	result, isErr, err := ts.Dispatch(context.Background(), toolDelegateAgent,
		`{"objective":"write tests","role_seed":"You are a tester.","max_turns":3}`)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}

	// Parse the agent ID from result
	if !strings.Contains(result, "sub-") {
		t.Errorf("result = %q, want sub-<id>", result)
	}
	agentID := strings.TrimPrefix(result, "Delegated to sub-agent ")
	if agentID == result {
		t.Fatalf("unexpected result format: %q", result)
	}

	// Verify agent appears in List() with the right parent.
	list := m.List()
	found := false
	for _, a := range list {
		if a.AgentID == agentID {
			found = true
			if a.ParentID != "" {
				t.Errorf("parent ID = %q, want '' (lead agent)", a.ParentID)
			}
			break
		}
	}
	if !found {
		t.Errorf("agent %q not found in List()", agentID)
	}

	// Steer via tool dispatch.
	result, isErr, err = ts.Dispatch(context.Background(), toolSteerAgent,
		`{"agent_id":"`+agentID+`","message":"focus on edge cases"}`)
	if err != nil || isErr {
		t.Fatalf("steer: result=%q isErr=%v err=%v", result, isErr, err)
	}

	// Check status via tool dispatch.
	result, isErr, err = ts.Dispatch(context.Background(), toolAgentStatus,
		`{"agent_id":"`+agentID+`"}`)
	if err != nil {
		t.Fatalf("status dispatch: %v", err)
	}
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}
	if result == "" {
		t.Error("expected non-empty status")
	}

	// Release via tool dispatch.
	result, isErr, err = ts.Dispatch(context.Background(), toolReleaseAgent,
		`{"agent_id":"`+agentID+`"}`)
	if err != nil || isErr {
		t.Fatalf("release: result=%q isErr=%v err=%v", result, isErr, err)
	}
}

func TestDelegateAgentThroughToolsetUsesParentID(t *testing.T) {
	m := NewManager(nil, Config{})
	defer m.Dispose()

	ts := &BrowserToolset{}
	ts.SetDelegateManagerForParent(m, "lead-agent")

	result, isErr, err := ts.Dispatch(context.Background(), toolDelegateAgent,
		`{"objective":"write tests","role_seed":"You are a tester.","max_turns":3}`)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if isErr {
		t.Fatalf("unexpected error: %s", result)
	}

	agentID := strings.TrimPrefix(result, "Delegated to sub-agent ")
	if agentID == result {
		t.Fatalf("unexpected result format: %q", result)
	}

	list := m.List()
	found := false
	for _, a := range list {
		if a.AgentID == agentID {
			found = true
			if a.ParentID != "lead-agent" {
				t.Errorf("parent ID = %q, want lead-agent", a.ParentID)
			}
			break
		}
	}
	if !found {
		t.Errorf("agent %q not found in List()", agentID)
	}
}
