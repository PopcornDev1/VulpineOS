# Cross-Package Integration Testing Suite Design

## Purpose

Add Go-only integration tests that verify cross-package flows using fake transports (no real browser, no external processes). These complement the existing `TestIntegration_*` tests in `internal/integration_test.go` which require real Camoufox/browser.

## Scope

### New Integration Tests (Fake/No External)

These tests use `testutil.NewFakeJugglerTransport` and in-memory fakes:

1. **Agent Spawn Flow** - Full lifecycle from template to tracked ownership
2. **Agent State Transitions** - Running → Paused → Completed/Failed → cleanup
3. **Pool Context Lifecycle** - Acquire → use → release/reuse
4. **PanelAPI Query Flow** - API → Vault → JSON response

### Existing Tests (Do NOT Duplicate)

| File | Tests | Why |
|------|-------|-----|
| `internal/integration_test.go` | Real browser/kernel flows | Requires Camoufox |
| `internal/agent_soak_integration_test.go` | Multi-agent soak | Requires real browser |
| `internal/nanoclaw/manager_test.go` | Nanoclaw binary spawn | Requires binary |

These stay as slow/regression tests. Our new tests are fast smoke.

## Architecture

### Test Structure

```
internal/integration/
├── integration_suite.go       # Test helpers, fake factories
├── spawn_flow_test.go       # Agent spawn lifecycle
├── state_transition_test.go  # Agent state machine
├── pool_lifecycle_test.go # Context acquire/release
└── panel_query_test.go    # API → Vault flow
```

### Test Helpers

Each test creates a minimal orchestrator with fake transports:

```go
type testEnv struct {
    fakeJuggler *testutil.FakeJugglerTransport
    client    *juggler.Client
    vault     *vault.DB
    pool      *pool.Pool
    orch      *orchestrator.Orchestrator
}

func newTestEnv(t *testing.T) *testEnv
func (e *testEnv) Close()
```

### Key Design Decisions

- **Same fake transport** - Must reuse `testutil.FakeJugglerTransport` from Task 1
- **In-memory vault** - Use `vault.OpenPath(tempDir)`
- **No real pool** - Use production pool with fake juggler client
- **Isolated tests** - Each test is independent, no shared state

## Flows Tested

### 1. Agent Spawn Flow

```
Test: SpawnCitizen → Vault.GetTemplate → Pool.Acquire → Juggler calls → Nanoclaw.WriteSOP → track ownership → Vault.CreateNomadSession

Verifies:
- Template loads from vault
- Pool acquires context
- Juggler creates browser context
- Juggler sets cookies/locale/timezone/fingerprint
- Nanoclaw writes SOP (mocked)
- Orchestrator tracks context ownership
- Vault records session
```

### 2. Agent State Transition Flow

```
Test: Spawn → Pause → Resume → Complete → KillAgent

Verifies:
- Status changes tracked
- Context retained on pause
- Context reused on resume
- Context released on terminal status
```

### 3. Pool Context Lifecycle

```
Test: Acquire × N → Release × N → (reuse or remove)

Verifies:
- New context created when pool empty
- Context reused within MaxUsesPerSlot
- Context removed when limit hit
- Browser.removeBrowserContext called on remove
```

### 4. PanelAPI Query Flow

```
Test: PanelAPI.HandleMessage(agents.getMessages) → Vault → JSON

Verifies:
- Correct JSON structure returned
- Truncation applied at limit
- Invalid agentId rejected
```

## Error Paths

Each flow includes error-at-boundary tests:

- Vault template not found → appropriate error
- Pool acquire fails → context not created in vault
- Juggler create context fails → pool release attempted
- Juggler error response → error propagates
- Nanoclaw spawn fails → context released

## Execution

### Fast Suite (CI)

```bash
go test ./internal/integration/... -count=1
```

Expected runtime: ~2-5 seconds for all tests.

### Race Detection

```bash
go test -race ./internal/integration/... -count=1
```

Must pass with race detector (no data races).

### Non-Goals

These remain slow/external tests (NOT in this suite):
- Real browser rendering
- Nanoclaw binary execution
- Network requests
- File system beyond temp dir

## Data Contracts

Tests MUST verify production data shapes:

- Juggler calls use `Browser.createBrowserContext`, `Browser.removeBrowserContext`, `Browser.setCookies`, `Browser.setUserAgentOverride`, `Browser.setLocaleOverride`, `Browser.setTimezoneOverride` with production JSON field names
- Vault stores agent status, context ownership using existing `AgentMetadata` and `AgentMessage` types
- PanelAPI returns `{messages: [], limit: int, truncated: bool}` matching web panel expectations

## Success Criteria

Integration tests are successful when:

1. All 4 flows covered (spawn, state, pool, panel)
2. Error paths for each boundary verified
3. Full suite runs in <5s without external deps
4. Race detector passes
5. No duplication with existing tests
