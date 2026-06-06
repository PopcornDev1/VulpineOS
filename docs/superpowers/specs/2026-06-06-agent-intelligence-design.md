# Agent Intelligence Design — Lead Agent, Declarative Delegation, and Composite System Prompts

**Date:** 2026-06-06
**Status:** Draft
**Author:** Design session

---

## Problem

VulpineOS agents follow instructions reliably but lack proactive intelligence, autonomous problem-solving, and the ability to delegate work. The current single-agent loop with a flat system prompt produces competent but uninspired results — there is no "wow factor." The agent does not decompose tasks, explore multiple angles independently, escalate issues, or manage sub-agents.

Token efficiency is also a constraint: a single monolithic agent that tries to do everything pays for irrelevant browsing and tool calls in its context window.

---

## Design Overview

Introduce a **lead agent** model: the user interacts with one primary agent that understands their vision, plans strategically, and delegates work to **persistent sub-agents** with specialized roles. The lead agent uses a **"reflect-then-act"** pattern, composing **declarative missions** for sub-agents rather than sending raw chat messages. Sub-agents persist across tasks via role seeds stored in the vault, with selective context retention to avoid stale-context pollution.

---

## 1. Lead Agent Identity & Behavior (System Prompt Redesign)

### Current state

`browserSystemPrompt` in `internal/agentcore/session.go` is a ~90-line flat list of rules covering identity, tools, workflow, and formatting. It tells the agent *what* to do but not *how* to think.

### Proposed: Layered system prompt

The lead agent's prompt is composed from three layers:

**Layer 1 — Core Identity (fixed)**
Establishes purpose, authority, and behavioral stance:

```
You are the lead agent for VulpineOS. Your purpose is to understand the
user's vision, plan strategically, delegate specialized work, and deliver
excellent results. You are proactive, thorough, and systematic.

- You take ownership of outcomes, not just tasks.
- You think before you act: clarify, plan, then execute.
- You communicate clearly and ask targeted questions when requirements
  are ambiguous.
- When something goes wrong, you diagnose, retry, or escalate — you do
  not simply report failure and stop.
```

**Layer 2 — Behavioural Directives (fixed)**
Structured thinking patterns the agent follows. These are prompt directives only — no code-level "reflection phase" is added to the loop. The agent thinks as part of its natural response generation:

1. **Clarification reflex**: Before acting on a vague or complex request, probe the user with targeted questions until you have enough context to plan effectively.
2. **Plan-then-execute**: Decompose the task into sub-problems. For each, decide: do it yourself, or delegate to a sub-agent? Plan first, then execute methodically. For complex multi-step tasks, output a structured plan as a tool result before executing.
3. **Autonomous monitoring**: If a sub-agent fails, diagnose why and retry with adjusted instructions or escalate. Proactively identify issues the user hasn't explicitly mentioned.
4. **Synthesis**: After collecting results, synthesise across sources. Identify contradictions, gaps, and convergences. Present a coherent answer, not a bullet-point dump.

**Layer 3 — Delegation Protocol (added when delegation tools are available)**
How to compose missions, choose role seeds, and steer sub-agents (see Section 2 & 3).

### Composition

Layers 1+2 are always present. Layer 3 is appended when the agent has access to delegation tools. All three are injected as a single `system` message — no multi-message system prompt.

The existing `browserSystemPrompt` becomes the **base sub-agent prompt** (used when spawning sub-agents), stripped of the lead-agent-specific directives.

---

## 2. Declarative Mission Protocol

### Problem

When one AI communicates a task to another AI via natural language, the result is verbose, unfocused, and expensive. The receiving agent wastes tokens parsing narrative and inferring intent.

### Mission structure

When the lead agent delegates, it writes a structured mission document:

```go
type Mission struct {
    AgentID     string   // target sub-agent ID, or empty for auto-select
    RoleSeed    string   // role identity for this sub-agent
    Objective   string   // what to accomplish (concise)
    Context     string   // relevant background information (compact)
    Constraints []string // rules and boundaries
    OutputSpec  string   // expected output format
    MaxTurns    int      // maximum iterations for this mission
    Priority    int      // scheduling priority
}
```

### Sub-agent prompt composition

A sub-agent's system prompt is composed at spawn time:

```
[Base Browser Identity (current browserSystemPrompt)]
+ [RoleSeed]
+ "Your current mission: [Objective]"
+ [Context]
+ "Constraints: [joined Constraints]"
+ "When you have completed the mission, return your findings in this format: [OutputSpec]"
+ "Maximum turns: [MaxTurns]"
```

The sub-agent **never sees** the lead agent's conversation history — just the mission. This prevents context pollution and keeps the sub-agent focused.

### Role seed design

Role seeds are written by the lead agent at spawn time (not pre-baked). They define the sub-agent's identity broadly enough to span multiple missions:

> *Example — Market research specialist:*
> "You are a market research specialist who investigates companies, products, and industries. You are thorough, analytical, and cite your sources. You explore multiple angles: website content, news coverage, social media presence, and technical documentation. You identify both strengths and weaknesses."

The lead agent can save a role seed after a successful mission for reuse.

---

## 3. Sub-agent System — Tools & Lifecycle

### MCP tools

The lead agent manages sub-agents through dedicated tools:

```
vulpine_delegate       → Async spawn — returns agent_id immediately
vulpine_delegate_sync  → Synchronous variant — blocks until complete, returns result
vulpine_list_agents    → List active sub-agents + status
vulpine_check_agent    → Check status of one sub-agent
vulpine_collect        → Collect final result (blocking: waits up to 120s)
vulpine_steer          → Send mid-task guidance to a running sub-agent
vulpine_release_agent  → Terminate a sub-agent
```

### Spawning lifecycle

1. Lead agent calls `vulpine_delegate` with the mission fields
2. The system composes the sub-agent's system prompt: `[Base] + [RoleSeed] + [Mission]`
3. A new `nativeAgent` is spawned via the existing `Manager` with the custom prompt
4. The sub-agent gets its own browser context (Camoufox page, fingerprint, proxy)
5. The agent ID is returned to the lead agent

### Steering mechanism

The Manager holds a per-agent inbox (`map[AgentID][]string`). When the lead agent calls `vulpine_steer`, the Manager appends the message to the target sub-agent's inbox. Between tool calls, the sub-agent loop checks its inbox — if non-empty, pending messages are injected as user messages in the sub-agent's conversation. No polling, no IPC, no Go channel complexity.

### TUI visibility

Sub-agents appear in the TUI left panel agent list alongside the lead agent, rendered as children of their parent. The lead agent appears as the top-level entry; its spawned sub-agents appear indented beneath it with an icon indicating child status. This gives the user full visibility into concurrent agent activity while making the hierarchy clear.

### Context persistence

Between missions, the sub-agent's conversation history is compressed:
- The role seed is preserved
- The previous mission + result is stored as a structured summary (not full transcript)
- Full history is archived in the vault but not loaded into context
- On reassignment, only the fresh mission + compressed summary is injected

---

## 4. Token Efficiency Strategy

### Sub-agent result compression

Sub-agents compress their own results before returning. The existing `compressOldToolResults` in `internal/agentcore/loop.go` handles tool-level compression; this extends to the final reply:
- The sub-agent's final output is its deliverable
- Intermediate tool calls and failures are not returned to the lead agent
- The lead agent sees only the structured summary via `vulpine_collect`

### Context retention boundaries

| Boundary | Retained in context | Archived but excluded |
|---|---|---|
| Lead agent conversation | Full chat history (existing) | — |
| Sub-agent current mission | Mission spec + in-progress turns | — |
| Sub-agent previous missions | Compressed summary only | Full vault archive |
| Sub-agent tool results | Last N verbatim (existing) | Older compressed (existing) |

### Early stopping

If a sub-agent signals completion before `MaxTurns`, the loop terminates immediately (already partially supported). The lead agent can call `vulpine_collect` at any time for progressive results.

---

## 5. Role Seed Library

### Storage

Role seeds are stored in the vault in a new `role_seeds` SQLite table:

```sql
CREATE TABLE role_seeds (
    id        TEXT PRIMARY KEY,
    name      TEXT UNIQUE NOT NULL,
    content   TEXT NOT NULL,  -- the role seed text
    tags      TEXT,           -- JSON array of tags for discovery
    created   INTEGER NOT NULL,
    used      INTEGER DEFAULT 0  -- usage counter
);
```

### Tools

```
vulpine_store_role(name, role_seed, tags?)  → Save a role seed for reuse
vulpine_find_role(query)                     → Find role seeds by name/tag match
vulpine_list_roles                           → List all stored role seeds
vulpine_delete_role(name)                    → Remove a role seed
```

The library starts empty — the lead agent builds it through experience.

---

## 6. Failure Handling

| Scenario | Behaviour |
|---|---|
| Sub-agent error (tool failure, model error) | Lead agent sees error in `vulpine_collect`, decides to retry, adjust, or escalate |
| Sub-agent exceeds MaxTurns | Loop terminates with timeout message; lead agent sees this on collect |
| Browser crash in sub-agent | Manager marks agent as errored; lead agent sees on `vulpine_check_agent` |
| Lead agent error | Existing behaviour — user re-prompts |
| Lead agent stuck in reflection loop | Existing MaxIterations cap on the lead agent's loop catches this |
| Duplicate agent IDs | Manager already handles duplicate spawn detection (existing) |

---

## 7. Implementation Outline

### Priority order

1. **System prompt redesign** (`internal/agentcore/session.go`, `internal/agentprompt/agentprompt.go`)
   — Replace flat prompt with layered composition, define base sub-agent prompt

2. **Mission type + prompt composer** (new `internal/agentcore/mission.go`, `internal/agentcore/composer.go`)
   — Define `Mission` struct, implement `ComposeSubAgentPrompt()` function

3. **Delegation tools** (`internal/mcp/tools.go`, `internal/mcp/tools_agent.go`)
   — Add `vulpine_delegate`, `vulpine_collect`, `vulpine_check_agent`, etc.

4. **Manager extension** (`internal/agentcore/manager.go`)
   — Add `Delegate()` method for spawning sub-agents with custom composed prompts

5. **Role seed library** (`internal/vault/`)
   — New `role_seeds` table, CRUD methods, MCP surface tools

6. **Sub-agent steering** (`internal/agentcore/loop.go`)
   — Guidance channel for mid-task instructions

7. **Integration wiring** (`internal/tui/`, `internal/remote/control.go`)
   — TUI visibility into sub-agents, remote API exposure

---

## 8. Files Changed

| File | Change |
|---|---|
| `internal/agentcore/session.go` | Replace `browserSystemPrompt`; add `LeadAgentPrompt`, `BaseSubAgentPrompt` |
| `internal/agentcore/loop.go` | Add per-agent steering inbox check between tool calls |
| `internal/agentcore/manager.go` | Add `Delegate()`, sub-agent lifecycle methods |
| `internal/agentcore/mission.go` (new) | `Mission` struct, serialization |
| `internal/agentcore/composer.go` (new) | `ComposeSubAgentPrompt(m Mission) string` |
| `internal/agentprompt/agentprompt.go` | Role seed composition helper |
| `internal/mcp/tools.go` | Add delegation tool definitions |
| `internal/mcp/tools_agent.go` | Add delegation tool handlers |
| `internal/mcp/tools_delegate.go` (new) | Delegation tool handler implementations |
| `internal/vault/role_seed.go` (new) | Role seed table, CRUD |
| `internal/vault/db.go` | Add migrations for `role_seeds` table |

---

## 9. Resolved Questions

These were raised during the design session and have since been resolved:

- **TUI visibility**: Sub-agents ARE visible in the TUI's left panel agent list, shown as children of their parent lead agent. This provides clear UI/UX context for all running agents while making the parent-child relationship explicit.
- **`vulpine_human_transfer` tool**: Removed as scope creep. The lead agent indicates when it cannot complete something, and the user can take over naturally.
- **Role seed library lookup**: The existing schema already includes a `tags` field. Name + tag-based search is used — no need for over-engineered vector search.
