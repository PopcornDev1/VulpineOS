# Replace OpenClaw with NanoClaw in VulpineOS

## Overview

Replace all OpenClaw functionality in VulpineOS with NanoClaw, swapping the underlying agent runtime while preserving the existing architecture (subprocess-based agent management).

## Motivation

OpenClaw has ~500k lines of code and extensive dependencies. NanoClaw offers:
- ~5k lines - much smaller, easier to understand
- Container-based isolation for security
- Faster startup and execution
- Support for multiple providers (Codex, MiniMax, OpenRouter, Ollama)
- Skill-based feature model (no bloat)

## Architecture

### Current Flow
```
VulpineOS → openclaw agent --local --session-id X -m "task" → JSON output
```

### New Flow
```
VulpineOS → nanoclaw CLI → Container execution → SQLite session files
```

## Components

### 1. internal/nanoclaw/ (renamed from openclaw)

**Changes:**
- Binary finder: `findOpenClaw()` → `findNanoClaw()`
- CLI args: Build NanoClaw command instead of OpenClaw
- Message parsing: Parse NanoClaw output format
- Remove: Session ID handling, config file generation

**Files:**
- `manager.go` - Agent lifecycle, binary finding, subprocess management
- `agent.go` - Agent struct, output parsing
- `intro.go` - System prompt handling

### 2. internal/orchestrator/orchestrator.go

**Changes:**
- Import `vulpineos/internal/nanoclaw` instead of `openclaw`
- Update `Agents` field type
- Remove OpenClaw-specific config preparation

### 3. cmd/vulpineos/main.go

**Changes:**
- Remove `tryGenerateOpenClawConfig()`
- Update gateway to use NanoClaw
- Remove OpenClaw profile references

### 4. internal/config/config.go

**Changes:**
- Keep provider definitions (they're model-agnostic)
- No changes needed to provider registry

## Implementation Phases

### Phase 1: Core Swap (Priority: High)
1. Rename `internal/openclaw` → `internal/nanoclaw`
2. Update binary finder for `nanoclaw` command
3. Replace CLI argument building
4. Test basic agent spawn

### Phase 2: Integration (Priority: High)
1. Update orchestrator imports and Manager creation
2. Update config generation for NanoClaw
3. Update gateway usage

### Phase 3: Cleanup (Priority: Medium)
1. Remove OpenClaw-specific code paths
2. Test all agent workflows (create, pause, resume, kill)
3. Remove dead code

## Key Differences to Handle

| Aspect | OpenClaw | NanoClaw |
|--------|----------|----------|
| CLI | `openclaw agent --local` | `nanoclaw run` |
| Output | JSON stdout | SQLite DB |
| Session | CLI flags | File paths |
| Runtime | Node.js process | Docker container |
| Provider | Built-in | Skill-based |

## Testing

- Unit tests for binary finding
- Integration tests for agent lifecycle
- End-to-end test: spawn agent, send message, receive response
- Performance benchmark: compare agent startup time

## Risk Mitigation

1. **Fallback**: If NanoClaw unavailable, show clear error
2. **Stubs**: Keep minimal stub for compilation during transition
3. **Rollback**: Keep git branch until fully tested

## Success Criteria

- [ ] NanoClaw binary found and executable
- [ ] Agents spawn successfully with NanoClaw
- [ ] Messages sent and responses received
- [ ] All existing TUI hotkeys work
- [ ] No regressions in agent lifecycle (create, pause, resume, kill)
- [ ] Performance improved (faster startup, lower memory)