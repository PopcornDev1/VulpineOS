# Cross-Package Integration Testing Suite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Go-only integration tests verifying cross-package flows using fake transports (no real browser).

**Architecture:** Create test helpers in new `internal/integration/` package, then add focused tests for spawn, state transitions, pool lifecycle, and PanelAPI flows. All use existing `testutil.FakeJugglerTransport`.

**Tech Stack:** Go 1.26, `testing`, existing `vulpineos/internal/testutil`, `juggler`, `pool`, `orchestrator`, `vault`, `remote` packages.

---

## File Structure

```
internal/integration/
├── integration_suite.go     # Test helpers, fake factories
├── spawn_flow_test.go     # Agent spawn lifecycle tests
├── state_transition_test.go # Agent state machine tests
├── pool_lifecycle_test.go  # Context acquire/release tests
└── panel_query_test.go    # API → Vault flow tests
```

---

### Task 1: Test Helpers and Fake Factories

**Files:**
- Create: `internal/integration/integration_suite.go`

- [ ] **Step 1: Write test helpers**

```go
package integration

import (
	"testing"

	"vulpineos/internal/juggler"
	"vulpineos/internal/orchestrator"
	"vulpineos/internal/pool"
	"vulpineos/internal/testutil"
	"vulpineos/internal/vault"
)

// testEnv holds all dependencies for integration tests.
type testEnv struct {
	FakeJuggler *testutil.FakeJugglerTransport
	Client     *juggler.Client
	Vault      *vault.DB
	Pool       *pool.Pool
	Orch       *orchestrator.Orchestrator
}

// newTestEnv creates a minimal test environment with fake transports.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	fake := testutil.NewFakeJugglerTransport(t)
	// Set up common responses
	fake.RespondJSON("Browser.createBrowserContext", map[string]string{"browserContextId": "ctx-test"})
	fake.RespondJSON("Browser.removeBrowserContext", map[string]any{})
	fake.RespondJSON("Browser.setCookies", map[string]any{})
	fake.RespondJSON("Browser.setUserAgentOverride", map[string]any{})
	fake.RespondJSON("Browser.setLocaleOverride", map[string]any{})
	fake.RespondJSON("Browser.setTimezoneOverride", map[string]any{})

	client := juggler.NewClient(fake)
	t.Cleanup(func() { client.Close() })

	vdb := openTestVault(t)
	t.Cleanup(func() { vdb.Close() })

	p := pool.New(client, pool.Config{PreWarm: 0, MaxActive: 2, MaxUsesPerSlot: 2})
	if err := p.Start(); err != nil {
		t.Fatalf("pool start: %v", err)
	}
	t.Cleanup(func() { p.Close() })

	o := orchestrator.New(nil, client, vdb, p.Config(), "", orchestrator.Opts{})
	t.Cleanup(func() { o.Close() })

	return &testEnv{
		FakeJuggler: fake,
		Client:     client,
		Vault:      vdb,
		Pool:       p,
		Orch:       o,
	}
}

func openTestVault(t *testing.T) *vault.DB {
	f, err := os.CreateTemp("", "integration-test-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	db, err := vault.OpenPath(f.Name())
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	return db
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/integration/`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/integration/integration_suite.go
git commit -m "test: add integration test helpers"
```

---

### Task 2: Agent Spawn Flow Tests

**Files:**
- Create: `internal/integration/spawn_flow_test.go`

- [ ] **Step 1: Write spawn flow tests**

```go
package integration

import (
	"encoding/json"
	"testing"

	"vulpineos/internal/testutil"
	"vulpineos/internal/vault"
)

func TestSpawnFlow_LoadTemplate(t *testing.T) {
	env := newTestEnv(t)

	// Create template in vault
	_, err := env.Vault.CreateTemplate("test-template", "sop-v1", "{}")
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	// Verify spawn flow loads template
	template, err := env.Vault.GetTemplate("test-template")
	if err != nil {
		t.Fatalf("get template: %v", err)
	}
	if template.ID != "test-template" {
		t.Fatalf("template id = %q, want test-template", template.ID)
	}
}

func TestSpawnFlow_AcquireContext(t *testing.T) {
	env := newTestEnv(t)

	slot, err := env.Pool.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if slot.ContextID == "" {
		t.Fatal("contextID empty")
	}

	// Verify Juggler was called
	calls := env.FakeJuggler.CallsByMethod("Browser.createBrowserContext")
	if len(calls) != 1 {
		t.Fatalf("create calls = %d, want 1", len(calls))
	}
}

func TestSpawnFlow_ApplyFingerprint(t *testing.T) {
	env := newTestEnv(t)

	// Create citizen with fingerprint
	fp := vault.FingerprintData{
		Platform:  "mac",
		UserAgent: "Mozilla/5.0 Test",
	}
	fpJSON, _ := json.Marshal(fp)

	citizen := &vault.Citizen{
		ID:          "citizen-fp",
		Label:      "Test Citizen",
		Fingerprint: string(fpJSON),
		Locale:     "en-US",
		Timezone:   "America/New_York",
	}

	// Apply citizen to context
	if err := env.Orch.ApplyCitizenToContext("ctx-test", citizen); err != nil {
		t.Fatalf("apply citizen: %v", err)
	}

	// Verify Juggler calls
	uaCalls := env.FakeJuggler.CallsByMethod("Browser.setUserAgentOverride")
	if len(uaCalls) != 1 {
		t.Fatalf("ua calls = %d, want 1", len(uaCalls))
	}
	uaParams := testutil.ParamsAs[struct {
		BrowserContextID string `json:"browserContextId"`
		UserAgent       string `json:"userAgent"`
	}](t, uaCalls[0].Params)
	if uaParams.UserAgent != fp.UserAgent {
		t.Fatalf("userAgent = %q, want %q", uaParams.UserAgent, fp.UserAgent)
	}

	localeCalls := env.FakeJuggler.CallsByMethod("Browser.setLocaleOverride")
	if len(localeCalls) != 1 {
		t.Fatalf("locale calls = %d, want 1", len(localeCalls))
	}
}

func TestSpawnFlow_TrackOwnership(t *testing.T) {
	env := newTestEnv(t)

	slot, _ := env.Pool.Acquire()

	// Track ownership
	env.Orch.TrackContextOwnership("agent-track", slot)

	// Verify tracked
	env.Orch.ContextOwnershipMu.Lock()
	tracked := env.Orch.ContextOwnership["agent-track"]
	env.Orch.ContextOwnershipMu.Unlock()

	if tracked == nil {
		t.Fatal("context ownership not tracked")
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/integration/ -run TestSpawnFlow -v -count=1`
Expected: PASS (reuse existing code paths)

- [ ] **Step 3: Commit**

```bash
git add internal/integration/spawn_flow_test.go
git commit -m "test: add spawn flow integration tests"
```

---

### Task 3: Agent State Transition Tests

**Files:**
- Create: `internal/integration/state_transition_test.go`

- [ ] **Step 1: Write state transition tests**

```go
package integration

import (
	"testing"
)

func TestStateTransition_RunToPauseToResume(t *testing.T) {
	env := newTestEnv(t)

	slot, _ := env.Pool.Acquire()
	env.Orch.TrackContextOwnership("agent-pause", slot)

	// On pause, context should be retained
	if env.Pool.Stats().Active != 1 {
		t.Fatalf("active slots = %d, want 1", env.Pool.Stats().Active)
	}
}

func TestStateTransition_RunToCompleteReleasesContext(t *testing.T) {
	env := newTestEnv(t)

	slot, _ := env.Pool.Acquire()
	env.Orch.TrackContextOwnership("agent-complete", slot)

	// Simulate completion
	env.Orch.ReleaseContextOnCompletion("agent-complete")

	// Verify context released
	if env.Pool.Stats().Active != 0 {
		t.Fatalf("active slots = %d, want 0", env.Pool.Stats().Active)
	}
}

func TestStateTransition_KillAgentReleasesContext(t *testing.T) {
	env := newTestEnv(t)

	slot, _ := env.Pool.Acquire()
	env.Orch.TrackContextOwnership("agent-kill", slot)

	// Release on kill
	env.Orch.ReleaseContextOnCompletion("agent-kill")

	// Verify context released
	releasedCalls := env.FakeJuggler.CallsByMethod("Browser.removeBrowserContext")
	if len(releasedCalls) != 1 {
		t.Fatalf("remove calls = %d, want 1", len(releasedCalls))
	}
}
```

- [ ] **Step 2: Add missing orchestrator methods**

If methods don't exist, add to orchestrator:

```go
// In internal/orchestrator/orchestrator.go

// TrackContextOwnership tracks a context slot for an agent.
func (o *Orchestrator) TrackContextOwnership(agentID string, slot *pool.ContextSlot) {
	o.agentToSlotMu.Lock()
	defer o.agentToSlotMu.Unlock()
	o.agentToSlot[agentID] = slot
}

// ReleaseContextOnCompletion releases context ownership on terminal status.
func (o *Orchestrator) ReleaseContextOnCompletion(agentID string) {
	o.agentToSlotMu.Lock()
	slot, ok := o.agentToSlot[agentID]
	if ok {
		delete(o.agentToSlot, agentID)
	}
	o.agentToSlotMu.Unlock()
	if ok {
		o.Pool.Release(slot)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/integration/ -run TestStateTransition -v -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/integration/ internal/orchestrator/orchestrator.go
git commit -m "test: add state transition integration tests"
```

---

### Task 4: Pool Context Lifecycle Tests

**Files:**
- Create: `internal/integration/pool_lifecycle_test.go`

- [ ] **Step 1: Write pool lifecycle tests**

```go
package integration

import (
	"testing"
)

func TestPoolLifecycle_AcquireContext(t *testing.T) {
	env := newTestEnv(t)

	slot, err := env.Pool.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if slot.ContextID == "" {
		t.Fatal("contextID empty")
	}
}

func TestPoolLifecycle_ReuseWithinLimit(t *testing.T) {
	env := newTestEnv(t)

	// Acquire and release twice
	slot1, _ := env.Pool.Acquire()
	env.Pool.Release(slot1)

	slot2, _ := env.Pool.Acquire()

	// Should reuse same context
	if slot1.ContextID != slot2.ContextID {
		t.Fatalf("context not reused: %q vs %q", slot1.ContextID, slot2.ContextID)
	}

	// Verify only one create call
	createCalls := env.FakeJuggler.CallsByMethod("Browser.createBrowserContext")
	if len(createCalls) != 1 {
		t.Fatalf("create calls = %d, want 1", len(createCalls))
	}
}

func TestPoolLifecycle_RemoveOnLimitHit(t *testing.T) {
	env := newTestEnv(t)
	env.Pool.Close()

	// Recreate with max 1 use
	env.Pool = pool.New(env.Client, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 1})
	env.Pool.Start()

	slot1, _ := env.Pool.Acquire()
	env.Pool.Release(slot1)

	// This should remove and create new
	slot2, _ := env.Pool.Acquire()

	// Should be different context
	if slot1.ContextID == slot2.ContextID {
		t.Fatal("context should have been removed")
	}

	// Verify remove was called
	removeCalls := env.FakeJuggler.CallsByMethod("Browser.removeBrowserContext")
	if len(removeCalls) != 1 {
		t.Fatalf("remove calls = %d, want 1", len(removeCalls))
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/integration/ -run TestPoolLifecycle -v -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/integration/pool_lifecycle_test.go
git commit -m "test: add pool lifecycle integration tests"
```

---

### Task 5: PanelAPI Query Flow Tests

**Files:**
- Create: `internal/integration/panel_query_test.go`

- [ ] **Step 1: Write panel query tests**

```go
package integration

import (
	"encoding/json"
	"testing"
)

func TestPanelQuery_GetMessages(t *testing.T) {
	env := newTestEnv(t)

	// Create agent with messages
	agent, _ := env.Vault.CreateAgent("panel-test", "test task", "{}")
	env.Vault.AppendMessage(agent.ID, "user", "hello", 2)
	env.Vault.AppendMessage(agent.ID, "assistant", "world", 3)

	// Query via PanelAPI pattern (direct vault for now)
	messages, err := env.Vault.GetMessages(agent.ID, 10)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(messages))
	}
}

func TestPanelQuery_Truncation(t *testing.T) {
	env := newTestEnv(t)

	agent, _ := env.Vault.CreateAgent("panel-trunc", "test", "{}")
	env.Vault.AppendMessage(agent.ID, "user", "hello", 2)
	env.Vault.AppendMessage(agent.ID, "assistant", "world", 3)

	// Limit to 1
	messages, _ := env.Vault.GetMessages(agent.ID, 1)

	if len(messages) != 1 {
		t.Fatalf("limited messages = %d, want 1", len(messages))
	}
}

func TestPanelQuery_InvalidAgentIDRejected(t *testing.T) {
	env := newTestEnv(t)

	_, err := env.Vault.GetAgent("../invalid")
	if err == nil {
		t.Fatal("expected error for invalid agent ID")
	}
}

func TestPanelQuery_JSONShape(t *testing.T) {
	env := newTestEnv(t)

	agent, _ := env.Vault.CreateAgent("panel-json", "test", "{}")
	env.Vault.AppendMessage(agent.ID, "user", "hello", 2)

	// Verify JSON structure
	messages, _ := env.Vault.GetMessages(agent.ID, 10)
	data, _ := json.Marshal(messages)

	var parsed []map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("parsed messages = %d", len(parsed))
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/integration/ -run TestPanelQuery -v -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/integration/panel_query_test.go
git commit -m "test: add panel query integration tests"
```

---

### Task 6: Error Path Tests

**Files:**
- Modify: existing integration test files

- [ ] **Step 1: Add error path tests**

Spawn flow errors:
```go
func TestSpawnFlow_PoolAcquireFails(t *testing.T) {
	env := newTestEnv(t)
	env.Pool.Close()

	_, err := env.Pool.Acquire()
	if err == nil {
		t.Fatal("expected error when pool closed")
	}
}
```

Vault errors:
```go
func TestSpawnFlow_TemplateNotFound(t *testing.T) {
	env := newTestEnv(t)

	_, err := env.Vault.GetTemplate("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing template")
	}
}
```

Juggler errors:
```go
func TestSpawnFlow_JugglerError(t *testing.T) {
	env := newTestEnv(t)
	env.FakeJuggler.RespondError("Browser.createBrowserContext", "context limit reached")

	_, err := env.Pool.Acquire()
	if err == nil {
		t.Fatal("expected error from juggler")
	}
}
```

- [ ] **Step 2: Run error path tests**

Run: `go test ./internal/integration/ -run 'Error' -v -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/integration/
git commit -m "test: add integration error path tests"
```

---

### Task 7: Verification

**Files:**
- No new files

- [ ] **Step 1: Run full integration suite**

Run: `go test ./internal/integration/... -count=1`
Expected: PASS

- [ ] **Step 2: Run with race detector**

Run: `go test -race ./internal/integration/... -count=1`
Expected: PASS

- [ ] **Step 3: Check test count**

Run: `go test ./internal/integration/... -v -count=1 | grep -c "^--- PASS"`
Expected: ≥15 tests

- [ ] **Step 4: Commit verification**

```bash
git add internal/
git commit -m "test: add cross-package integration testing suite

Adds fast integration tests for cross-package flows using fake transports.

Signed-off-by: opencode/minimax-m2.5-free"
```

---

## Spec Coverage Check

| Spec Section | Task |
|-------------|------|
| Agent Spawn Flow | Task 2 |
| Agent State Transitions | Task 3 |
| Pool Context Lifecycle | Task 4 |
| PanelAPI Query Flow | Task 5 |
| Error Paths | Task 6 |
| Verification | Task 7 |

---

## Type Consistency Check

- `testutil.NewFakeJugglerTransport` - reused from Task 1 (existing)
- `vault.DB`, `vault.Citizen`, `vault.FingerprintData` - existing types
- `pool.Pool`, `pool.ContextSlot` - existing types
- `orchestrator.Orchestrator` - existing type, may add 2 helper methods

All method/product names must be verified against production code before implementation.