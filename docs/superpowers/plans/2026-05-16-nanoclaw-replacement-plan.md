# NanoClaw Replacement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace all OpenClaw functionality in VulpineOS with NanoClaw, swapping the underlying agent runtime while preserving the existing subprocess-based architecture.

**Architecture:** Rename internal/openclaw to internal/nanoclaw, update binary finder to locate nanoclaw CLI, replace CLI argument building, update orchestrator to use new package.

**Tech Stack:** Go (VulpineOS), NanoClaw (Node/TypeScript CLI), Docker containers

---

## Pre-requisites

1. NanoClaw must be installed on the system
2. Verify NanoClaw CLI is available: `which nanoclaw` or `nanoclaw --help`

---

### Task 1: Rename openclaw package to nanoclaw

**Files:**
- Rename: `internal/openclaw/` → `internal/nanoclaw/`
- Modify: `internal/nanoclaw/manager.go` - update package name
- Modify: `internal/nanoclaw/agent.go` - update package name
- Modify: `internal/nanoclaw/intro.go` - update package name
- Modify: `internal/nanoclaw/runtime_config.go` - update package name

- [ ] **Step 1: Rename directory**

```bash
cd /path/to/VulpineOS
mv internal/openclaw internal/nanoclaw
```

- [ ] **Step 2: Update package name in manager.go**

```bash
sed -i '' 's/package openclaw/package nanoclaw/' internal/nanoclaw/manager.go
```

- [ ] **Step 3: Update package name in other files**

```bash
sed -i '' 's/package openclaw/package nanoclaw/' internal/nanoclaw/agent.go
sed -i '' 's/package openclaw/package nanoclaw/' internal/nanoclaw/intro.go
sed -i '' 's/package openclaw/package nanoclaw/' internal/nanoclaw/runtime_config.go
```

- [ ] **Step 4: Verify build still works**

```bash
go build -o vulpineos ./cmd/vulpineos 2>&1 | head -20
```

- [ ] **Step 5: Commit**

```bash
git add internal/nanoclaw/
git commit -m "refactor: rename openclaw package to nanoclaw"
```

---

### Task 2: Update binary finder to locate nanoclaw

**Files:**
- Modify: `internal/nanoclaw/manager.go:324-385`

- [ ] **Step 1: Find and read the findOpenClaw function**

```bash
grep -n "func.*findOpenClaw" internal/nanoclaw/manager.go
```

- [ ] **Step 2: Rename function to findNanoClaw**

```go
// findNanoClaw looks for the NanoClaw binary in common locations.
func (m *Manager) findNanoClaw() string {
```

- [ ] **Step 3: Update search paths**

Replace:
- `"openclaw"` → `"nanoclaw"`
- `"node_modules/.bin/openclaw"` → `"node_modules/.bin/nanoclaw"`
- `"node_modules/openclaw/openclaw.mjs"` → `"node_modules/nanoclaw/bin/nanoclaw"`
- `"openclaw/start.sh"` → `"nanoclaw/nanoclaw.sh"`

- [ ] **Step 4: Update all callers**

```bash
sed -i '' 's/findOpenClaw()/findNanoClaw()/g' internal/nanoclaw/manager.go
sed -i '' 's/OpenClawInstalled/NanoClawInstalled/g' internal/nanoclaw/manager.go
```

- [ ] **Step 5: Build and verify**

```bash
go build -o vulpineos ./cmd/vulpineos 2>&1
```

- [ ] **Step 6: Commit**

```bash
git add internal/nanoclaw/manager.go
git commit -m "refactor: update binary finder to locate nanoclaw"
```

---

### Task 3: Replace CLI argument building

**Files:**
- Modify: `internal/nanoclaw/manager.go:313-330` (agentTurnArgs function)
- Modify: `internal/nanoclaw/manager.go:233-255` (SpawnOpenClaw function)

- [ ] **Step 1: Find agentTurnArgs function**

```bash
grep -n "func agentTurnArgs" internal/nanoclaw/manager.go
```

- [ ] **Step 2: Read current implementation**

```go
func agentTurnArgs(sessionName, message string) []string {
    args := []string{
        "--profile", "vulpine",
        "agent",
        "--local",
        "--session-id", sessionName,
        "--json",
    }
    if message != "" {
        args = append(args, "-m", message)
    }
    return args
}
```

- [ ] **Step 3: Replace with NanoClaw args**

```go
// nanoclawArgs builds arguments for NanoClaw CLI
func nanoclawArgs(sessionName, message string) []string {
    args := []string{
        "run",
        "--session", sessionName,
    }
    if message != "" {
        args = append(args, message)
    }
    return args
}
```

- [ ] **Step 4: Update SpawnOpenClaw to SpawnNanoClaw**

Rename the function and update internal calls:

```go
// SpawnNanoClaw spawns an agent using NanoClaw
func (m *Manager) SpawnNanoClaw(task string, agentSkills []config.SkillEntry) (string, error) {
    nanoclawBin := m.findNanoClaw()
    if nanoclawBin == "" {
        return "", fmt.Errorf("NanoClaw not found. Install: git clone https://github.com/nanocoai/nanoclaw.git")
    }
    // ... rest of implementation
}
```

- [ ] **Step 5: Update all callers**

```bash
sed -i '' 's/agentTurnArgs/nanoclawArgs/g' internal/nanoclaw/manager.go
sed -i '' 's/SpawnOpenClaw/SpawnNanoClaw/g' internal/nanoclaw/manager.go
sed -i '' 's/openclawBin/nanoclawBin/g' internal/nanoclaw/manager.go
```

- [ ] **Step 6: Build and verify**

```bash
go build -o vulpineos ./cmd/vulpineos 2>&1
```

- [ ] **Step 7: Commit**

```bash
git add internal/nanoclaw/manager.go
git commit -m "refactor: replace OpenClaw CLI args with NanoClaw"
```

---

### Task 4: Update orchestrator imports

**Files:**
- Modify: `internal/orchestrator/orchestrator.go:17`

- [ ] **Step 1: Update import**

```go
import (
    // ... other imports
    "vulpineos/internal/nanoclaw"
)
```

- [ ] **Step 2: Update Manager creation**

```go
Agents: nanoclaw.NewManager(openclawBinary),
```

to:

```go
Agents: nanoclaw.NewManager(nanoclawBinary),
```

- [ ] **Step 3: Find all nanoclaw references in orchestrator**

```bash
grep -n "openclaw" internal/orchestrator/orchestrator.go
```

- [ ] **Step 4: Update all references**

Replace `"openclaw"` with `"nanoclaw"` where appropriate.

- [ ] **Step 5: Build and verify**

```bash
go build -o vulpineos ./cmd/vulpineos 2>&1
```

- [ ] **Step 6: Commit**

```bash
git add internal/orchestrator/orchestrator.go
git commit -m "refactor: update orchestrator to use nanoclaw"
```

---

### Task 5: Update config generation in main.go

**Files:**
- Modify: `cmd/vulpineos/main.go:467-478` (tryGenerateOpenClawConfig)
- Modify: `cmd/vulpineos/internal/config/config.go`

- [ ] **Step 1: Rename tryGenerateOpenClawConfig**

```go
func tryGenerateNanoClawConfig(cfg *config.Config, vulpineosBinary, camoufoxBinary string) (bool, error) {
```

- [ ] **Step 2: Update internal references**

Replace `GenerateOpenClawConfig` → `GenerateNanoClawConfig` or similar.

- [ ] **Step 3: Find all OpenClaw references in main.go**

```bash
grep -n "OpenClaw\|openclaw" cmd/vulpineos/main.go | head -20
```

- [ ] **Step 4: Update references**

Replace relevant references from `openclaw` to `nanoclaw`.

- [ ] **Step 5: Build and verify**

```bash
go build -o vulpineos ./cmd/vulpineos 2>&1
```

- [ ] **Step 6: Commit**

```bash
git add cmd/vulpineos/main.go
git commit -m "refactor: update main.go to use nanoclaw config"
```

---

### Task 6: Test agent lifecycle

**Files:**
- Integration test or manual testing

- [ ] **Step 1: Start vulpineos**

```bash
cd /path/to/VulpineOS
./vulpineos
```

- [ ] **Step 2: Create a new agent**

Press `n` in TUI to create a new agent.

- [ ] **Step 3: Send a message**

Type a message and send.

- [ ] **Step 4: Verify response received**

Check that the agent responds.

- [ ] **Step 5: Test pause/resume**

Press `p` to pause, `r` to resume.

- [ ] **Step 6: Test kill**

Press `X` to kill all agents.

- [ ] **Step 7: Commit test results**

```bash
git commit -m "test: verify agent lifecycle with nanoclaw"
```

---

### Task 7: Remove OpenClaw-specific code

**Files:**
- Review and clean up any remaining OpenClaw references

- [ ] **Step 1: Search for remaining openclaw references**

```bash
grep -rn "openclaw\|OpenClaw" internal/nanoclaw/ internal/orchestrator/ cmd/vulpineos/main.go 2>/dev/null | grep -v ".go:" | head -10
```

- [ ] **Step 2: Clean up any stale references**

- [ ] **Step 3: Build and verify**

```bash
go build -o vulpineos ./cmd/vulpineos 2>&1
```

- [ ] **Step 4: Commit cleanup**

```bash
git add -A
git commit -m "cleanup: remove remaining OpenClaw references"
```

---

## Verification Commands

```bash
# Build
go build -o vulpineos ./cmd/vulpineos

# Run tests
go test -race ./internal/nanoclaw/... ./internal/orchestrator/...

# Start TUI
./vulpineos
```

## Expected Outcome

- NanoClaw binary is found and used for agent spawning
- All agent lifecycle operations work (create, pause, resume, kill)
- No regressions from the OpenClaw removal
- Faster agent startup due to NanoClaw's lighter footprint
