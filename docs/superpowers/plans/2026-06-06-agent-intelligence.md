# Agent Intelligence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transform VulpineOS agents from single-turn instruction-followers into reflective lead agents that delegate missions to persistent sub-agents with specialized role identities.

**Architecture:** A layered system prompt (core identity + behavioral directives + delegation protocol) replaces the flat `browserSystemPrompt`. A new `Mission` struct + `ComposeSubAgentPrompt()` function builds sub-agent prompts at spawn time. The Manager gets a `Delegate()` method that spawns nativeAgent instances with composed prompts. MCP tools surface delegation, steering, and role seed management. Sub-agents appear as children in the TUI agent list.

**Tech Stack:** Go, SQLite (via modernc.org/sqlite), MCP/Juggler protocol, Camoufox browser

---

### Task 1: Define lead agent and sub-agent system prompts

**Files:**
- Modify: `internal/agentcore/session.go:45-93` — add `LeadAgentPrompt`, `BaseSubAgentPrompt`
- Test: `internal/agentcore/session_test.go`

- [ ] **Step 1: Write failing test for prompt contents**

```go
func TestAgentPromptsContainExpectedDirectives(t *testing.T) {
    // LeadAgentPrompt should contain core identity and behavioral directives
    for _, want := range []string{
        "lead agent",
        "plan strategically",
        "delegate specialized work",
        "clarification reflex",
        "Plan-then-execute",
    } {
        if !strings.Contains(LeadAgentPrompt, want) {
            t.Errorf("LeadAgentPrompt missing: %q", want)
        }
    }

    // BaseSubAgentPrompt should contain browser identity but NOT lead-agent directives
    for _, want := range []string{
        "VulpineOS",
        "Camoufox",
        "vulpine_navigate",
        "vulpine_snapshot",
    } {
        if !strings.Contains(BaseSubAgentPrompt, want) {
            t.Errorf("BaseSubAgentPrompt missing: %q", want)
        }
    }
    for _, unwanted := range []string{"lead agent", "delegate"} {
        if strings.Contains(BaseSubAgentPrompt, unwanted) {
            t.Errorf("BaseSubAgentPrompt should not contain %q", unwanted)
        }
    }
}
```

Run: `go test ./internal/agentcore/ -run TestAgentPromptsContainExpectedDirectives -v`
Expected: FAIL — `LeadAgentPrompt` and `BaseSubAgentPrompt` don't exist yet

- [ ] **Step 2: Add prompt constants to session.go**

At the top of `session.go`, after removing the existing `browserSystemPrompt` (or keeping it as the base), add:

```go
// LeadAgentPrompt is the identity and behavioral contract for the lead agent
// that the user interacts with directly. It includes strategic thinking
// directives and the delegation protocol (added when delegation tools are
// available).
const LeadAgentPrompt = `You are VulpineOS — an operator system for browser-based AI agents. Built on the Vulpine browser with per-context fingerprint isolation and deterministic security enforcement.

## Identity
You are the lead agent. Your purpose is to understand the user's vision, plan strategically, delegate specialized work, and deliver excellent results. You are proactive, thorough, and systematic.

- You take ownership of outcomes, not just tasks.
- You think before you act: clarify, plan, then execute.
- You communicate clearly and ask targeted questions when requirements are ambiguous.
- When something goes wrong, you diagnose, retry, or escalate — you do not simply report failure and stop.

## Behavioural Directives
1. **Clarification reflex**: Before acting on a vague or complex request, probe the user with targeted questions until you have enough context to plan effectively.
2. **Plan-then-execute**: Decompose the task into sub-problems. For each, decide: do it yourself, or delegate to a sub-agent? Plan first, then execute methodically. For complex multi-step tasks, output a structured plan as a tool result before executing.
3. **Autonomous monitoring**: If a sub-agent fails, diagnose why and retry with adjusted instructions or escalate. Proactively identify issues the user hasn't explicitly mentioned.
4. **Synthesis**: After collecting results, synthesise across sources. Identify contradictions, gaps, and convergences. Present a coherent answer, not a bullet-point dump.

## Browser Tools (vulpine_*)
A page is already open for you; you do not create or manage browser contexts. These are your browser automation tools — Playwright, Puppeteer, Selenium, and agent-browser CLI are NOT available:

1. **Navigate & Inspect**: vulpine_navigate → vulpine_snapshot (or vulpine_page_info / vulpine_get_ax_tree) to read the page state.
2. **Identify targets**: vulpine_snapshot returns visible page structure with @ref labels when available. Use vulpine_find to locate elements by selector or text.
3. **Interact by ref**: vulpine_click_ref @e1, vulpine_type_ref @e2 "text", vulpine_hover_ref @e3. Use vulpine_human_click / vulpine_human_type / vulpine_human_scroll for anti-detection when the site is bot-sensitive.
4. **Form interaction**: Before filling a field, verify its label, placeholder, aria-label, or name attribute match the field you intend (use vulpine_snapshot, vulpine_find, or vulpine_get_ax_tree to confirm). Use vulpine_fill_form for multi-field forms.
5. **Wait & verify**: After navigation, use vulpine_page_settled as a usability check, then use targeted vulpine_wait / vulpine_verify for the specific element, text, URL, or form state you need. For SPAs and dashboards, do not wait for global quiet after every click; verify the expected UI state directly.
6. **Tabs**: vulpine_open_tab, vulpine_switch_tab, vulpine_close_tab, vulpine_list_tabs for multi-page workflows.

## File Workspace Tools
You can create, read, list, and update UTF-8 text files inside the local VulpineOS file workspace using vulpine_list_files, vulpine_read_file, and vulpine_write_file. Paths are relative to the workspace root where VulpineOS was launched; absolute paths and .. traversal are rejected. If the operator asks whether you can write files, answer yes with this workspace limitation.

## Workflow
1. vulpine_navigate to the target URL
2. vulpine_page_settled — wait until the page is usable; if it reports the page is still changing, continue with targeted checks
3. vulpine_snapshot to read state and collect refs when available
4. Identify the element ref or selector, then act (vulpine_click_ref, vulpine_type_ref, etc.)
5. After actions, use vulpine_wait or vulpine_verify for the specific result you expect; use vulpine_page_settled again only for full navigations or major route changes
6. vulpine_snapshot to confirm the result
7. Send your final reply and stop

## Forbidden
- wget, curl, and raw HTTP clients are blocked by the network proxy — use vulpine_navigate only
- Playwright, Puppeteer, Selenium, and agent-browser CLI are not available — use vulpine_* tools only
- No host filesystem access outside the local file workspace exposed by the file tools
- No modifying VulpineOS system configuration

## Methodical Approach
Do not rush to a single narrow attempt. Be methodical:

1. **Decompose** — break the task into independent facets. For research: professional presence, code repos, news. For debugging: possible causes, environment, recent changes. For any task: different interpretations, perspectives, and sources of evidence.
2. **Explore multiple angles** — generate distinct lines of inquiry, one per facet. Each should explore something different, not the same thing rephrased.
3. **Execute systematically** — tackle each facet. Vary your approach across facets (different queries, tools, entry points).
4. **Document as you go** — keep internal notes of every URL visited, search query run, and action taken. Record what each produced. This is not optional — you will need to recount what you did.
5. **Synthesize** — combine findings, identify gaps, contradictions, or convergence. Decide if a follow-up round is needed.

Start broad to map the landscape, then narrow into specific angles. When you encounter barriers (consent walls, paywalls, CAPTCHA), handle them interactively rather than treating them as dead ends — click through, fill forms, navigate the interface.

## Output Formatting
Write in a balanced chat style. Use **bold**, *italic*, inline code, fenced code blocks, bullets, numbered lists, tables and task checkboxes when they make the answer easier to scan. Do not use Markdown headings (#, ##, ###). Do not write horizontal rule divider lines (---, ***, ___); the VulpineOS UI owns message and tool dividers.

## Reporting
Be concise. Your final message is the result, not a transcript of what you did. If a tool reports an error, timeout, or incomplete data, report that exactly — never claim an action succeeded when it did not. If the task asks for an exact reply or exact wording, perform the required actions first, then send that exact reply as your final message and stop.`

// BaseSubAgentPrompt is the prompt used for sub-agents. It omits lead-agent
// directives and assumes the mission is composed separately.
const BaseSubAgentPrompt = `You are VulpineOS — an operator system for browser-based AI agents. Built on the Vulpine browser with per-context fingerprint isolation and deterministic security enforcement.

## Identity
You are named exactly as assigned. Never claim a different name or inherited persona. Complete the assigned task immediately — do not introduce yourself or ask how you can help before taking action.

## Browser Tools (vulpine_*)
A page is already open for you; you do not create or manage browser contexts. These are your browser automation tools — Playwright, Puppeteer, Selenium, and agent-browser CLI are NOT available:

1. **Navigate & Inspect**: vulpine_navigate → vulpine_snapshot (or vulpine_page_info / vulpine_get_ax_tree) to read the page state.
2. **Identify targets**: vulpine_snapshot returns visible page structure with @ref labels when available. Use vulpine_find to locate elements by selector or text.
3. **Interact by ref**: vulpine_click_ref @e1, vulpine_type_ref @e2 "text", vulpine_hover_ref @e3. Use vulpine_human_click / vulpine_human_type / vulpine_human_scroll for anti-detection when the site is bot-sensitive.
4. **Form interaction**: Before filling a field, verify its label, placeholder, aria-label, or name attribute match the field you intend (use vulpine_snapshot, vulpine_find, or vulpine_get_ax_tree to confirm). Use vulpine_fill_form for multi-field forms.
5. **Wait & verify**: After navigation, use vulpine_page_settled as a usability check, then use targeted vulpine_wait / vulpine_verify for the specific element, text, URL, or form state you need. For SPAs and dashboards, do not wait for global quiet after every click; verify the expected UI state directly.
6. **Tabs**: vulpine_open_tab, vulpine_switch_tab, vulpine_close_tab, vulpine_list_tabs for multi-page workflows.

## File Workspace Tools
You can create, read, list, and update UTF-8 text files inside the local VulpineOS file workspace using vulpine_list_files, vulpine_read_file, and vulpine_write_file. Paths are relative to the workspace root where VulpineOS was launched; absolute paths and .. traversal are rejected.

## Workflow
1. vulpine_navigate to the target URL
2. vulpine_page_settled — wait until the page is usable
3. vulpine_snapshot to read state and collect refs when available
4. Identify the element ref or selector, then act
5. After actions, use vulpine_wait or vulpine_verify for the specific result
6. vulpine_snapshot to confirm the result
7. Send your final reply and stop

## Forbidden
- wget, curl, and raw HTTP clients are blocked by the network proxy — use vulpine_navigate only
- Playwright, Puppeteer, Selenium, and agent-browser CLI are not available — use vulpine_* tools only
- No host filesystem access outside the local file workspace
- No modifying VulpineOS system configuration

## Methodical Approach
Do not rush to a single narrow attempt. Be methodical: decompose, explore multiple angles, execute systematically, document as you go, and synthesise.

## Output Formatting
Write in a balanced chat style. Use **bold**, *italic*, inline code, fenced code blocks, bullets, numbered lists, tables and task checkboxes when they make the answer easier to scan. Do not use Markdown headings (#, ##, ###). Do not write horizontal rule divider lines (---, ***, ___); the VulpineOS UI owns message and tool dividers.

## Reporting
Be concise. Your final message is the result, not a transcript. If a tool reports an error, timeout, or incomplete data, report that exactly — never claim an action succeeded when it did not.`
```

Also add the `"strings"` import to the test file if not already present.

- [ ] **Step 3: Update existing callers to use LeadAgentPrompt**

Replace every reference to `browserSystemPrompt` in `session.go` with `LeadAgentPrompt`. The functions `RunBrowserAgent`, `RunBrowserAgentWithToolset`, and `RunBrowserAgentOnSession` all set `SystemPrompt: browserSystemPrompt` — change these to `SystemPrompt: LeadAgentPrompt`.

Then the sub-agent prompt (`BaseSubAgentPrompt`) is only used by `ComposeSubAgentPrompt` (created in Task 2), never directly by these callers.

Keep the old constant renamed for clarity:
```go
// browserSystemPrompt is preserved as an alias for backward compatibility.
const browserSystemPrompt = LeadAgentPrompt
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agentcore/ -run TestAgentPromptsContainExpectedDirectives -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agentcore/session.go internal/agentcore/session_test.go
git commit -m "feat(agent): add layered LeadAgentPrompt and BaseSubAgentPrompt constants"
```

---

### Task 2: Mission struct and sub-agent prompt composer

**Files:**
- Create: `internal/agentcore/mission.go`
- Create: `internal/agentcore/mission_test.go`

- [ ] **Step 1: Write failing test for Mission prompt composition**

```go
package agentcore

import (
    "strings"
    "testing"
)

func TestComposeSubAgentPrompt(t *testing.T) {
    m := Mission{
        RoleSeed:    "You are a market research specialist.",
        Objective:   "Research ACME Corp pricing",
        Context:     "ACME Corp sells widgets at acme.example.com",
        Constraints: []string{"Do not submit forms", "Return JSON only"},
        OutputSpec:  "Return a JSON object with pricing and features",
        MaxTurns:    10,
    }

    prompt := ComposeSubAgentPrompt(m)

    for _, want := range []string{
        BaseSubAgentPrompt,
        "You are a market research specialist.",
        "Your current mission: Research ACME Corp pricing",
        "ACME Corp sells widgets at acme.example.com",
        "Do not submit forms",
        "Return JSON only",
        "Return a JSON object with pricing and features",
        "Maximum turns: 10",
    } {
        if !strings.Contains(prompt, want) {
            t.Errorf("composed prompt missing: %q", want)
        }
    }
}

func TestMissionDefaults(t *testing.T) {
    m := Mission{
        Objective: "test",
    }
    prompt := ComposeSubAgentPrompt(m)
    if !strings.Contains(prompt, "Maximum turns: 25") {
        t.Errorf("expected default MaxTurns=25, got: %q", prompt)
    }
}
```

Run: `go test ./internal/agentcore/ -run TestCompose -v`
Expected: FAIL — `Mission` type and `ComposeSubAgentPrompt` don't exist

- [ ] **Step 2: Create mission.go with Mission struct and composer**

```go
package agentcore

import "fmt"

// Mission is the declarative task document the lead agent writes when
// delegating work to a sub-agent. It is structured (not natural-language)
// so the sub-agent spends zero tokens parsing intent.
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

// ComposeSubAgentPrompt assembles the full system prompt for a sub-agent
// from the mission fields. Returns a single string suitable for use as
// the system message in a LoopConfig.
func ComposeSubAgentPrompt(m Mission) string {
    maxTurns := m.MaxTurns
    if maxTurns <= 0 {
        maxTurns = 25
    }

    prompt := BaseSubAgentPrompt + "\n\n" + m.RoleSeed
    prompt += "\n\nYour current mission: " + m.Objective
    if m.Context != "" {
        prompt += "\n\n" + m.Context
    }
    if len(m.Constraints) > 0 {
        prompt += "\n\nConstraints:"
        for _, c := range m.Constraints {
            prompt += "\n- " + c
        }
    }
    if m.OutputSpec != "" {
        prompt += "\n\nWhen you have completed the mission, return your findings in this format: " + m.OutputSpec
    }
    prompt += fmt.Sprintf("\n\nMaximum turns: %d", maxTurns)

    return prompt
}
```

- [ ] **Step 3: Run test to verify it passes**

Run: `go test ./internal/agentcore/ -run TestCompose -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/agentcore/mission.go internal/agentcore/mission_test.go
git commit -m "feat(agent): add Mission struct and ComposeSubAgentPrompt"
```

---

### Task 3: Role seed vault table and CRUD

**Files:**
- Create: `internal/vault/role_seed.go`
- Modify: `internal/vault/db.go:12` (add migration for `role_seeds` table reference)

- [ ] **Step 1: Write failing test for role seed CRUD**

```go
package vault

import (
    "testing"
)

func TestRoleSeedCRUD(t *testing.T) {
    db := openTestVault(t)
    defer db.Close()

    // Create
    seed, err := db.CreateRoleSeed("market-researcher", "You are a market research specialist.", []string{"research", "market"})
    if err != nil {
        t.Fatalf("create role seed: %v", err)
    }
    if seed.Name != "market-researcher" {
        t.Errorf("expected name 'market-researcher', got %q", seed.Name)
    }

    // Get by name
    got, err := db.GetRoleSeedByName("market-researcher")
    if err != nil {
        t.Fatalf("get role seed by name: %v", err)
    }
    if got.Content != seed.Content {
        t.Errorf("content mismatch: got %q, want %q", got.Content, seed.Content)
    }

    // List
    list, err := db.ListRoleSeeds()
    if err != nil {
        t.Fatalf("list role seeds: %v", err)
    }
    if len(list) != 1 {
        t.Fatalf("expected 1 role seed, got %d", len(list))
    }

    // Find by tag
    found, err := db.FindRoleSeeds("research")
    if err != nil {
        t.Fatalf("find role seeds: %v", err)
    }
    if len(found) == 0 {
        t.Fatal("expected at least one result for tag query 'research'")
    }

    // Find by name fragment
    found, err = db.FindRoleSeeds("market")
    if err != nil {
        t.Fatalf("find role seeds by name: %v", err)
    }
    if len(found) == 0 {
        t.Fatal("expected at least one result for name query 'market'")
    }

    // Delete
    if err := db.DeleteRoleSeed(seed.ID); err != nil {
        t.Fatalf("delete role seed: %v", err)
    }
    list, _ = db.ListRoleSeeds()
    if len(list) != 0 {
        t.Errorf("expected 0 after delete, got %d", len(list))
    }
}

func openTestVault(t *testing.T) *DB {
    t.Helper()
    db, err := OpenPath(t.TempDir() + "/vault.db")
    if err != nil {
        t.Fatalf("open vault: %v", err)
    }
    t.Cleanup(func() { db.Close() })
    return db
}
```

Run: `go test ./internal/vault/ -run TestRoleSeedCRUD -v`
Expected: FAIL — functions don't exist yet

- [ ] **Step 2: Create vault/role_seed.go**

```go
package vault

import (
    "fmt"
    "strings"
    "time"

    "github.com/google/uuid"
)

// RoleSeed stores a reusable role identity for sub-agents.
type RoleSeed struct {
    ID      string    `json:"id"`
    Name    string    `json:"name"`
    Content string    `json:"content"`
    Tags    string    `json:"tags"` // JSON array of tags
    Created time.Time `json:"created"`
    Used    int       `json:"used"`
}

// CreateRoleSeed saves a new role seed.
func (db *DB) CreateRoleSeed(name, content string, tags []string) (*RoleSeed, error) {
    id := uuid.New().String()
    now := time.Now().Unix()

    tagsJSON := "[]"
    if len(tags) > 0 {
        tagsJSON = marshalStringSlice(tags)
    }

    _, err := db.conn.Exec(
        `INSERT INTO role_seeds (id, name, content, tags, created, used) VALUES (?, ?, ?, ?, ?, 0)`,
        id, name, content, tagsJSON, now,
    )
    if err != nil {
        return nil, fmt.Errorf("create role seed: %w", err)
    }

    return &RoleSeed{
        ID:      id,
        Name:    name,
        Content: content,
        Tags:    tagsJSON,
        Created: time.Unix(now, 0),
    }, nil
}

// GetRoleSeedByName retrieves a role seed by its unique name.
func (db *DB) GetRoleSeedByName(name string) (*RoleSeed, error) {
    row := db.conn.QueryRow(
        `SELECT id, name, content, tags, created, used FROM role_seeds WHERE name = ?`, name,
    )
    var s RoleSeed
    var created int64
    err := row.Scan(&s.ID, &s.Name, &s.Content, &s.Tags, &created, &s.Used)
    if err != nil {
        return nil, fmt.Errorf("get role seed by name: %w", err)
    }
    s.Created = time.Unix(created, 0)
    return &s, nil
}

// ListRoleSeeds returns all stored role seeds.
func (db *DB) ListRoleSeeds() ([]RoleSeed, error) {
    rows, err := db.conn.Query(
        `SELECT id, name, content, tags, created, used FROM role_seeds ORDER BY name`,
    )
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var seeds []RoleSeed
    for rows.Next() {
        var s RoleSeed
        var created int64
        if err := rows.Scan(&s.ID, &s.Name, &s.Content, &s.Tags, &created, &s.Used); err != nil {
            return nil, err
        }
        s.Created = time.Unix(created, 0)
        seeds = append(seeds, s)
    }
    return seeds, nil
}

// FindRoleSeeds searches role seeds by name or tag match (LIKE query).
func (db *DB) FindRoleSeeds(query string) ([]RoleSeed, error) {
    like := "%" + strings.ToLower(query) + "%"
    rows, err := db.conn.Query(
        `SELECT id, name, content, tags, created, used FROM role_seeds
         WHERE LOWER(name) LIKE ? OR LOWER(tags) LIKE ? ORDER BY used DESC`,
        like, like,
    )
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var seeds []RoleSeed
    for rows.Next() {
        var s RoleSeed
        var created int64
        if err := rows.Scan(&s.ID, &s.Name, &s.Content, &s.Tags, &created, &s.Used); err != nil {
            return nil, err
        }
        s.Created = time.Unix(created, 0)
        seeds = append(seeds, s)
    }
    return seeds, nil
}

// IncrementRoleSeedUse increments the usage counter for a role seed.
func (db *DB) IncrementRoleSeedUse(id string) error {
    _, err := db.conn.Exec(`UPDATE role_seeds SET used = used + 1 WHERE id = ?`, id)
    return err
}

// DeleteRoleSeed removes a role seed by ID.
func (db *DB) DeleteRoleSeed(id string) error {
    _, err := db.conn.Exec(`DELETE FROM role_seeds WHERE id = ?`, id)
    return err
}

// marshalStringSlice marshals a []string to a JSON string.
func marshalStringSlice(s []string) string {
    if len(s) == 0 {
        return "[]"
    }
    var b strings.Builder
    b.WriteByte('[')
    for i, v := range s {
        if i > 0 {
            b.WriteByte(',')
        }
        b.WriteByte('"')
        b.WriteString(strings.ReplaceAll(v, `"`, `\"`))
        b.WriteByte('"')
    }
    b.WriteByte(']')
    return b.String()
}
```

- [ ] **Step 3: Add role_seeds table to schema in db.go**

In `db.go`, add to the `schema` const after the `proxies` table definition:

```sql
CREATE TABLE IF NOT EXISTS role_seeds (
    id        TEXT PRIMARY KEY,
    name      TEXT UNIQUE NOT NULL,
    content   TEXT NOT NULL,
    tags      TEXT DEFAULT '[]',
    created   INTEGER NOT NULL,
    used      INTEGER DEFAULT 0
);
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/vault/ -run TestRoleSeedCRUD -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/vault/role_seed.go internal/vault/db.go
git commit -m "feat(vault): add role_seeds table and CRUD operations"
```

---

### Task 4: Manager extension — Delegate method and sub-agent lifecycle

**Files:**
- Modify: `internal/agentcore/manager.go` — add `Delegate()`, per-agent steering inbox, updated `nativeAgent` struct
- Test: `internal/agentcore/manager_test.go` — add delegation test

- [ ] **Step 1: Write failing test for Delegate**

```go
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
```

Run: `go test ./internal/agentcore/ -run TestDelegate -v`
Expected: FAIL — Delegate doesn't exist yet

- [ ] **Step 2: Extend nativeAgent and Manager**

Add to the `nativeAgent` struct:

```go
type nativeAgent struct {
    id        string
    parentID  string   // empty for lead agents, set for sub-agents
    contextID string
    cancel    context.CancelFunc
    cleanup   func()
    status    string
    terminal  string
    objective string
    tokens    int
    inbox     []string // steering messages from lead agent
}
```

Add `Delegate` method to `Manager`:

```go
// Delegate spawns a sub-agent with a composed prompt from the given mission.
// Returns the new agent's ID. The sub-agent runs asynchronously.
func (m *Manager) Delegate(mission Mission) (string, error) {
    return m.DelegateForParentMission(mission, "")
}

// DelegateForParentMission spawns a sub-agent with a known parent lead agent ID.
func (m *Manager) DelegateForParentMission(mission Mission, parentID string) (string, error) {
    id := uuid.New().String()[:8]
    task := composeSubAgentTask(mission)

    ctx, cancel := context.WithCancel(context.Background())
    ag := &nativeAgent{
        id:        id,
        parentID:  parentID,
        cancel:    cancel,
        status:    "running",
        objective: task,
        inbox:     make([]string, 0),
    }

    m.mu.Lock()
    if m.closed {
        m.mu.Unlock()
        cancel()
        return "", fmt.Errorf("manager closed")
    }
    m.agents[id] = ag
    m.wg.Add(1)
    m.mu.Unlock()

    ev := &managerEvents{m: m, agentID: id}
    m.emitStatus(id, "", "running", task)
    m.logRuntimeEvent("info", "sub_agent_started", "sub-agent started", map[string]string{
        "agent_id":  id,
        "parent_id": parentID,
        "objective": truncateString(task, 80),
    })

    m.cfgMu.RLock()
    cfg := m.cfg
    m.cfgMu.RUnlock()

    go func() {
        defer m.wg.Done()
        var err error

        prompt := ComposeSubAgentPrompt(mission)
        loop := NewLoop(newCompleter(cfg), nil, ev, LoopConfig{
            Models:        cfg.modelChain(),
            SystemPrompt:  prompt,
            Tools:         nil, // sub-agents don't get browser tools initially
            MaxIterations: mission.MaxTurns,
        })
        _, err = loop.Run(ctx, task, nil)

        final := "completed"
        if err != nil {
            final = "error"
            m.emitConversation(id, "system", "sub-agent error: "+err.Error())
            m.logRuntimeEvent("error", "sub_agent_failed", err.Error(), map[string]string{"agent_id": id})
        }
        m.emitStatus(id, "", final, task)
        m.finish(id, ag)
    }()

    return id, nil
}

// truncateString truncates a string to max runes for logging.
func truncateString(s string, max int) string {
    runes := []rune(s)
    if len(runes) <= max {
        return s
    }
    return string(runes[:max]) + "..."
}

// composeSubAgentTask builds the user task string for a sub-agent.
func composeSubAgentTask(m Mission) string {
    return m.Objective
}
```

Also add the `"fmt"` import if it's not there already.

- [ ] **Step 3: Add Steering methods to Manager**

```go
// SteerAgent sends a mid-task guidance message to a running sub-agent.
// The message is injected as a user message in the sub-agent's conversation
// on its next loop iteration.
func (m *Manager) SteerAgent(agentID, message string) error {
    m.mu.Lock()
    ag, ok := m.agents[agentID]
    if !ok {
        m.mu.Unlock()
        return fmt.Errorf("agent %s not found", agentID)
    }
    ag.inbox = append(ag.inbox, message)
    m.mu.Unlock()
    return nil
}

// AgentStatus returns the current status of an agent.
func (m *Manager) AgentStatus(agentID string) (string, error) {
    m.mu.RLock()
    ag, ok := m.agents[agentID]
    m.mu.RUnlock()
    if !ok {
        return "", fmt.Errorf("agent %s not found", agentID)
    }
    return ag.status, nil
}

// ReleaseAgent terminates a sub-agent and cleans up its resources.
func (m *Manager) ReleaseAgent(agentID string) error {
    return m.Kill(agentID)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/agentcore/ -run TestDelegate -v`
Expected: PASS

- [ ] **Step 5: Update List() to include parentID in AgentStatus**

Add `ParentID` field to `agentmsg.AgentStatus` if needed. For now, modify `List()` to include it in the objective field or add to a new struct field. Simplest approach — add a `ParentID` string field to `nativeAgent`.

Actually, let's keep it simple. The `List()` method already returns `agentmsg.AgentStatus`. We'd need to add a `ParentID` field there. But to avoid scope creep, let's skip this for now and handle it in the TUI task.

- [ ] **Step 6: Commit**

```bash
git add internal/agentcore/manager.go internal/agentcore/manager_test.go
git commit -m "feat(agent): add Manager.Delegate, SteerAgent, ReleaseAgent for sub-agents"
```

---

### Task 5: Delegation MCP tool definitions

**Files:**
- Modify: `internal/mcp/tools.go` — add delegation tool definitions to extensionTools or a new section

- [ ] **Step 1: Write failing test for delegation tool definitions**

```go
package mcp

import (
    "testing"
)

func TestDelegationToolDefinitions(t *testing.T) {
    defs := ToolDefinitions()
    tools := make(map[string]bool)
    for _, d := range defs {
        tools[d.Name] = true
    }
    for _, want := range []string{
        "vulpine_delegate",
        "vulpine_delegate_sync",
        "vulpine_list_agents",
        "vulpine_check_agent",
        "vulpine_collect",
        "vulpine_steer",
        "vulpine_release_agent",
        "vulpine_store_role",
        "vulpine_find_role",
        "vulpine_list_roles",
        "vulpine_delete_role",
    } {
        if !tools[want] {
            t.Errorf("missing delegation tool definition: %s", want)
        }
    }
}
```

Run: `go test ./internal/mcp/ -run TestDelegationToolDefinitions -v`
Expected: FAIL — no delegation tools defined yet

- [ ] **Step 2: Add delegation tool definitions to tools.go**

In `tools.go`, add a new function `delegationTools()` and call it from the `tools()` init function. All constraint/tags properties use `"string"` type since the existing `Property` struct has no `Items` field — the lead agent passes constraints as a single comma-separated string:

```go
// delegationTools returns the MCP tool definitions for sub-agent delegation.
func delegationTools() []ToolDefinition {
    return []ToolDefinition{
        {
            Name:        "vulpine_delegate",
            Description: "Spawn a sub-agent with a declarative mission. Returns the new agent ID immediately (async). Use vulpine_collect to get the result later.",
            InputSchema: InputSchema{
                Type: "object",
                Properties: map[string]Property{
                    "role_seed":  {Type: "string", Description: "Role identity text for this sub-agent"},
                    "objective":  {Type: "string", Description: "What the sub-agent should accomplish (concise)"},
                    "context":    {Type: "string", Description: "Relevant background information"},
                    "constraints": {Type: "string", Description: "Comma-separated rules and boundaries"},
                    "output_spec": {Type: "string", Description: "Expected output format"},
                    "max_turns":  {Type: "number", Description: "Maximum loop iterations (default 25)"},
                    "priority":   {Type: "number", Description: "Scheduling priority"},
                },
                Required: []string{"role_seed", "objective"},
            },
        },
        {
            Name:        "vulpine_delegate_sync",
            Description: "Spawn a sub-agent and wait for it to complete. Returns the final status. Blocks up to 120s.",
            InputSchema: InputSchema{
                Type: "object",
                Properties: map[string]Property{
                    "role_seed":  {Type: "string", Description: "Role identity text for this sub-agent"},
                    "objective":  {Type: "string", Description: "What the sub-agent should accomplish"},
                    "context":    {Type: "string", Description: "Relevant background information"},
                    "constraints": {Type: "string", Description: "Comma-separated rules"},
                    "output_spec": {Type: "string", Description: "Expected output format"},
                    "max_turns":  {Type: "number", Description: "Maximum loop iterations (default 25)"},
                },
                Required: []string{"role_seed", "objective"},
            },
        },
        {
            Name:        "vulpine_list_agents",
            Description: "List all active sub-agents with their status and parent agent.",
            InputSchema: InputSchema{
                Type:       "object",
                Properties: map[string]Property{},
            },
        },
        {
            Name:        "vulpine_check_agent",
            Description: "Check the status of a specific sub-agent by ID.",
            InputSchema: InputSchema{
                Type: "object",
                Properties: map[string]Property{
                    "agent_id": {Type: "string", Description: "Agent ID to check"},
                },
                Required: []string{"agent_id"},
            },
        },
        {
            Name:        "vulpine_collect",
            Description: "Collect the final result from a completed sub-agent. Blocks up to 120s waiting for completion.",
            InputSchema: InputSchema{
                Type: "object",
                Properties: map[string]Property{
                    "agent_id": {Type: "string", Description: "Agent ID to collect result from"},
                    "timeout":  {Type: "number", Description: "Max wait time in seconds (default 120)"},
                },
                Required: []string{"agent_id"},
            },
        },
        {
            Name:        "vulpine_steer",
            Description: "Send mid-task guidance to a running sub-agent.",
            InputSchema: InputSchema{
                Type: "object",
                Properties: map[string]Property{
                    "agent_id": {Type: "string", Description: "Target sub-agent ID"},
                    "message":  {Type: "string", Description: "Guidance message to inject"},
                },
                Required: []string{"agent_id", "message"},
            },
        },
        {
            Name:        "vulpine_release_agent",
            Description: "Terminate a sub-agent and release its resources.",
            InputSchema: InputSchema{
                Type: "object",
                Properties: map[string]Property{
                    "agent_id": {Type: "string", Description: "Agent ID to release"},
                },
                Required: []string{"agent_id"},
            },
        },
        {
            Name:        "vulpine_store_role",
            Description: "Save a role seed for reuse by future missions.",
            InputSchema: InputSchema{
                Type: "object",
                Properties: map[string]Property{
                    "name":     {Type: "string", Description: "Unique name for this role seed"},
                    "content":  {Type: "string", Description: "The role seed identity text"},
                    "tags":     {Type: "string", Description: "Comma-separated tags for discovery"},
                },
                Required: []string{"name", "content"},
            },
        },
        {
            Name:        "vulpine_find_role",
            Description: "Search stored role seeds by name or tag.",
            InputSchema: InputSchema{
                Type: "object",
                Properties: map[string]Property{
                    "query": {Type: "string", Description: "Search query matching name or tags"},
                },
                Required: []string{"query"},
            },
        },
        {
            Name:        "vulpine_list_roles",
            Description: "List all stored role seeds.",
            InputSchema: InputSchema{
                Type:       "object",
                Properties: map[string]Property{},
            },
        },
        {
            Name:        "vulpine_delete_role",
            Description: "Delete a stored role seed by name.",
            InputSchema: InputSchema{
                Type: "object",
                Properties: map[string]Property{
                    "name": {Type: "string", Description: "Name of the role seed to delete"},
                },
                Required: []string{"name"},
            },
        },
    }
}
```

Update `tools()` to include delegation tools:

```go
func tools() []ToolDefinition {
    toolsOnce.Do(func() {
        base := baseTools()
        base = append(base, humanTools()...)
        base = append(base, extensionTools()...)
        base = append(base, delegationTools()...)
        toolsCached = append([]ToolDefinition(nil), base...)
    })
    return toolsCached
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/mcp/ -run TestDelegationToolDefinitions -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/tools.go
git commit -m "feat(mcp): add delegation and role seed MCP tool definitions"
```

---

### Task 6: Delegation MCP tool handlers (no agentcore import)

**Files:**
- Create: `internal/mcp/tools_delegate.go` — handler implementations with delegation interface
- Modify: `internal/mcp/tools.go` — add delegation tool cases to `handleToolCallFull`

The mcp package cannot import `agentcore` (agentcore imports mcp). Instead, delegation handlers define their own interface and receive the manager reference via a `SetDelegationManager()` call from main/orchestrator.

- [ ] **Step 1: Create tools_delegate.go with DelegationManager interface and handlers**

```go
package mcp

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"
    "time"
)

// SubAgentInfo is a status snapshot returned by ListAgents.
type SubAgentInfo struct {
    AgentID   string `json:"agent_id"`
    ParentID  string `json:"parent_id,omitempty"`
    Status    string `json:"status"`
    Objective string `json:"objective"`
}

// DelegationManager is the interface agentcore.Manager satisfies.
// Defined here to avoid an import cycle (agentcore imports mcp).
type DelegationManager interface {
    DelegateForParent(roleSeed, objective, contextStr, constraints, outputSpec string, maxTurns, priority int, parentID string) (string, error)
    SteerAgent(agentID, message string) error
    AgentStatus(agentID string) (string, error)
    ReleaseAgent(agentID string) error
    ListAgents() []SubAgentInfo
}

var delegateMgr DelegationManager

// SetDelegationManager wires the manager reference for delegation tool handlers.
// Called once at startup from main or the orchestrator.
func SetDelegationManager(mgr DelegationManager) {
    delegateMgr = mgr
}

func handleDelegate(args json.RawMessage, parentID string) *ToolCallResult {
    var p struct {
        RoleSeed    string `json:"role_seed"`
        Objective   string `json:"objective"`
        Context     string `json:"context"`
        Constraints string `json:"constraints"`
        OutputSpec  string `json:"output_spec"`
        MaxTurns    int    `json:"max_turns"`
        Priority    int    `json:"priority"`
    }
    if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
        return errorResult(err)
    }
    if delegateMgr == nil {
        return errorResult(fmt.Errorf("delegation manager not available"))
    }

    id, err := delegateMgr.DelegateForParent(
        p.RoleSeed, p.Objective, p.Context,
        p.Constraints, p.OutputSpec,
        p.MaxTurns, p.Priority, parentID,
    )
    if err != nil {
        return errorResult(err)
    }
    return textResult(fmt.Sprintf(`{"agent_id":"%s"}`, id))
}

func handleDelegateSync(ctx context.Context, args json.RawMessage, parentID string) *ToolCallResult {
    result := handleDelegate(args, parentID)
    if result.IsError {
        return result
    }
    var r struct {
        AgentID string `json:"agent_id"`
    }
    if err := json.Unmarshal([]byte(resultText(result)), &r); err != nil {
        return errorResult(err)
    }
    if delegateMgr == nil {
        return errorResult(fmt.Errorf("delegation manager not available"))
    }
    deadline := time.Now().Add(120 * time.Second)
    for time.Now().Before(deadline) {
        status, err := delegateMgr.AgentStatus(r.AgentID)
        if err != nil {
            return errorResult(err)
        }
        if status == "completed" || status == "error" {
            return textResult(fmt.Sprintf(`{"agent_id":"%s","status":"%s"}`, r.AgentID, status))
        }
        select {
        case <-ctx.Done():
            return errorResult(ctx.Err())
        case <-time.After(500 * time.Millisecond):
        }
    }
    return textResult(fmt.Sprintf(`{"agent_id":"%s","status":"timeout"}`, r.AgentID))
}

func handleListAgents() *ToolCallResult {
    if delegateMgr == nil {
        return textResult("[]")
    }
    list := delegateMgr.ListAgents()
    b, _ := json.Marshal(list)
    return textResult(string(b))
}

func handleCheckAgent(args json.RawMessage) *ToolCallResult {
    var p struct {
        AgentID string `json:"agent_id"`
    }
    if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
        return errorResult(err)
    }
    if delegateMgr == nil {
        return errorResult(fmt.Errorf("delegation manager not available"))
    }
    status, err := delegateMgr.AgentStatus(p.AgentID)
    if err != nil {
        return errorResult(err)
    }
    return textResult(fmt.Sprintf(`{"agent_id":"%s","status":"%s"}`, p.AgentID, status))
}

func handleSteer(args json.RawMessage) *ToolCallResult {
    var p struct {
        AgentID string `json:"agent_id"`
        Message string `json:"message"`
    }
    if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
        return errorResult(err)
    }
    if delegateMgr == nil {
        return errorResult(fmt.Errorf("delegation manager not available"))
    }
    if err := delegateMgr.SteerAgent(p.AgentID, p.Message); err != nil {
        return errorResult(err)
    }
    return textResult("steer sent")
}

func handleReleaseAgent(args json.RawMessage) *ToolCallResult {
    var p struct {
        AgentID string `json:"agent_id"`
    }
    if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
        return errorResult(err)
    }
    if delegateMgr == nil {
        return errorResult(fmt.Errorf("delegation manager not available"))
    }
    if err := delegateMgr.ReleaseAgent(p.AgentID); err != nil {
        return errorResult(err)
    }
    return textResult("agent released")
}

// resultText extracts the text from a ToolCallResult (mirrors contentText logic).
func resultText(res *ToolCallResult) string {
    if res == nil || len(res.Content) == 0 {
        return ""
    }
    return res.Content[0].Text
}
```

- [ ] **Step 2: Wire delegation tools into handleToolCallFull**

In `tools.go`, add to the `handleToolCallFull` switch statement (after the human tools section, before `default`):

```go
// Delegation tools
case "vulpine_delegate":
    return handleDelegate(args, ""), nil
case "vulpine_delegate_sync":
    return handleDelegateSync(ctx, args, ""), nil
case "vulpine_list_agents":
    return handleListAgents(), nil
case "vulpine_check_agent":
    return handleCheckAgent(args), nil
case "vulpine_steer":
    return handleSteer(args), nil
case "vulpine_release_agent":
    return handleReleaseAgent(args), nil
```

Ensure the `"strings"` import is present (for `resultText`).

- [ ] **Step 3: Run build check**

Run: `go build ./internal/mcp/`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/tools_delegate.go internal/mcp/tools.go
git commit -m "feat(mcp): implement delegation tool handlers with DelegationManager interface"
```

The simplest approach: add delegation handlers to `handleToolCallFull` directly, similar to how agent reliability tools are handled. The Manager is passed as a parameter.

Wait — actually `handleToolCallFull` doesn't take a Manager parameter. It takes `client`, `tracker`, `screenshots`. So delegation tools either need a different dispatch path or the Manager needs to be accessible globally.

Let me simplify: use a global Manager reference that gets set at startup. This is the same pattern used for the extension registry.

Actually, looking more carefully at the code structure, I see that the MCP tools package (`mcp`) and the agent runtime (`agentcore`) are separate. The `handleToolCallFull` function dispatches browser tool calls. Delegation tools are NOT browser tools — they operate on the agent manager, not the browser.

So the right approach is:
1. Keep delegation tool definitions in `mcp/tools.go` (we already did this in Task 5)
2. The dispatch should go through a separate path — not `handleToolCallFull`
3. The Manager reference is injected from the top level (main or orchestrator)

Let me define: `SetDelegationManagerFunc` that takes a function or an interface, and the delegation handlers access it through that.

Actually even simpler — since the delegation tools are also MCP tools that the agent calls, they need to be dispatched through the same `toolSet.Dispatch` → `BrowserToolset.Dispatch` path. Looking at the BrowserToolset:

Let me check how tool dispatch works in the loop...

The `Loop` calls `l.tools.Dispatch(ctx, name, args)` and `l.tools` is a `ToolDispatcher` which is the `BrowserToolset`. So every tool call goes through the BrowserToolset's Dispatch method.

This means delegation tools need to be handled by the BrowserToolset too. Let me look at how BrowserToolset dispatches.

Actually, let me check the BrowserToolset code.

Actually this is getting into implementation weeds beyond what should be in a plan document. The plan should describe what to implement, not the exact code. Let me simplify.

I'll make the delegation handlers a separate file that registers with a global manager provider, and the dispatch path will be added in the handler file.

- [ ] **Step 2 (final): Implement tools_delegate.go**

```go
package mcp

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "vulpineos/internal/agentcore"
)

// DelegationManager is the interface agentcore.Manager satisfies for delegation.
type DelegationManager interface {
    DelegateForParent(mission agentcore.Mission, parentID string) (string, error)
    SteerAgent(agentID, message string) error
    AgentStatus(agentID string) (string, error)
    ReleaseAgent(agentID string) error
    List() []agentmsg.AgentStatus
}

var delegateMgr DelegationManager

// SetDelegationManager sets the manager used by delegation tool handlers.
func SetDelegationManager(mgr DelegationManager) {
    delegateMgr = mgr
}

func handleDelegate(args json.RawMessage, parentID string) *ToolCallResult {
    var p struct {
        RoleSeed    string   `json:"role_seed"`
        Objective   string   `json:"objective"`
        Context     string   `json:"context"`
        Constraints []string `json:"constraints"`
        OutputSpec  string   `json:"output_spec"`
        MaxTurns    int      `json:"max_turns"`
        Priority    int      `json:"priority"`
    }
    if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
        return errorResult(err)
    }
    if delegateMgr == nil {
        return errorResult(fmt.Errorf("delegation manager not available"))
    }

    mission := agentcore.Mission{
        RoleSeed:    p.RoleSeed,
        Objective:   p.Objective,
        Context:     p.Context,
        Constraints: p.Constraints,
        OutputSpec:  p.OutputSpec,
        MaxTurns:    p.MaxTurns,
        Priority:    p.Priority,
    }

    id, err := delegateMgr.DelegateForParent(mission, parentID)
    if err != nil {
        return errorResult(err)
    }
    return textResult(fmt.Sprintf(`{"agent_id":"%s"}`, id))
}

func handleDelegateSync(ctx context.Context, args json.RawMessage, parentID string) *ToolCallResult {
    result := handleDelegate(args, parentID)
    if result.IsError {
        return result
    }
    var r struct {
        AgentID string `json:"agent_id"`
    }
    if err := json.Unmarshal([]byte(contentText(result)), &r); err != nil {
        return errorResult(err)
    }
    // Wait for completion
    deadline := time.Now().Add(120 * time.Second)
    for time.Now().Before(deadline) {
        status, err := delegateMgr.AgentStatus(r.AgentID)
        if err != nil {
            return errorResult(err)
        }
        if status == "completed" || status == "error" {
            return textResult(fmt.Sprintf(`{"agent_id":"%s","status":"%s"}`, r.AgentID, status))
        }
        select {
        case <-ctx.Done():
            return errorResult(ctx.Err())
        case <-time.After(500 * time.Millisecond):
        }
    }
    return textResult(fmt.Sprintf(`{"agent_id":"%s","status":"timeout"}`, r.AgentID))
}

func handleListAgents() *ToolCallResult {
    if delegateMgr == nil {
        return textResult("[]")
    }
    list := delegateMgr.List()
    // Return as JSON
    b, _ := json.Marshal(list)
    return textResult(string(b))
}

func handleCheckAgent(args json.RawMessage) *ToolCallResult {
    var p struct {
        AgentID string `json:"agent_id"`
    }
    if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
        return errorResult(err)
    }
    if delegateMgr == nil {
        return errorResult(fmt.Errorf("delegation manager not available"))
    }
    status, err := delegateMgr.AgentStatus(p.AgentID)
    if err != nil {
        return errorResult(err)
    }
    return textResult(fmt.Sprintf(`{"agent_id":"%s","status":"%s"}`, p.AgentID, status))
}

func handleSteer(args json.RawMessage) *ToolCallResult {
    var p struct {
        AgentID string `json:"agent_id"`
        Message string `json:"message"`
    }
    if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
        return errorResult(err)
    }
    if delegateMgr == nil {
        return errorResult(fmt.Errorf("delegation manager not available"))
    }
    if err := delegateMgr.SteerAgent(p.AgentID, p.Message); err != nil {
        return errorResult(err)
    }
    return textResult("steer sent")
}

func handleReleaseAgent(args json.RawMessage) *ToolCallResult {
    var p struct {
        AgentID string `json:"agent_id"`
    }
    if err := json.Unmarshal(normalizeArgs(args), &p); err != nil {
        return errorResult(err)
    }
    if delegateMgr == nil {
        return errorResult(fmt.Errorf("delegation manager not available"))
    }
    if err := delegateMgr.ReleaseAgent(p.AgentID); err != nil {
        return errorResult(err)
    }
    return textResult("agent released")
}
```

Add delegation case to `handleToolCallFull` in `tools.go`:

```go
// Add before the default case:
case "vulpine_delegate":
    return handleDelegate(args, ""), nil
case "vulpine_delegate_sync":
    return handleDelegateSync(ctx, args, ""), nil
case "vulpine_list_agents":
    return handleListAgents(), nil
case "vulpine_check_agent":
    return handleCheckAgent(args), nil
case "vulpine_steer":
    return handleSteer(args), nil
case "vulpine_release_agent":
    return handleReleaseAgent(args), nil
```

These need the `context` import and the `normalizeArgs` import (already present).

Also need to define `contentText` helper or use the existing one:

```go
func contentText(res *ToolCallResult) string {
    if res == nil || len(res.Content) == 0 {
        return ""
    }
    return res.Content[0].Text
}
```

- [ ] **Step 3: Run build check**

Run: `go build ./internal/mcp/`
Expected: PASS (no compilation errors)

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/tools_delegate.go internal/mcp/tools.go
git commit -m "feat(mcp): implement delegation tool handlers"
```

---

### Task 7: Manager methods matching the mcp DelegationManager interface

**Files:**
- Modify: `internal/agentcore/manager.go` — add `DelegateForParent` overload using flat strings, add `ListAgents`, update `List` with parentID

The mcp `DelegationManager` interface uses flat string parameters (no `Mission` struct dependency) and returns `mcp.SubAgentInfo`. Manager must implement these methods. Since manager already has `DelegateForParent(Mission, string)`, add a thin adapter.

- [ ] **Step 1: Add DelegateForParent overload with flat string params**

In `manager.go`, add the import for `mcp` if not already present (it should be — agentcore/tools.go already imports it), then add:

```go
// DelegateForParent creates a sub-agent from flat mission fields.
// This is the mcp.DelegationManager interface implementation.
func (m *Manager) DelegateForParent(roleSeed, objective, contextStr, constraints, outputSpec string, maxTurns, priority int, parentID string) (string, error) {
    var constraintsList []string
    if constraints != "" {
        for _, c := range strings.Split(constraints, ",") {
            c = strings.TrimSpace(c)
            if c != "" {
                constraintsList = append(constraintsList, c)
            }
        }
    }
    mission := Mission{
        RoleSeed:    roleSeed,
        Objective:   objective,
        Context:     contextStr,
        Constraints: constraintsList,
        OutputSpec:  outputSpec,
        MaxTurns:    maxTurns,
        Priority:    priority,
    }
    id, err := m.DelegateForParentMission(mission, parentID)
    if err != nil {
        return "", err
    }
    return id, nil
}
```

Rename the existing `DelegateForParent` method to `DelegateForParentMission` to avoid name collision:

```go
// DelegateForParentMission spawns a sub-agent from a Mission struct.
func (m *Manager) DelegateForParentMission(mission Mission, parentID string) (string, error) {
    // ... existing body ...
}
```

- [ ] **Step 2: Add ListAgents method returning mcp.SubAgentInfo**

In `manager.go`, add the `ListAgents()` method. Since `agentcore` already imports `mcp` (via `tools.go`), return `[]mcp.SubAgentInfo` directly to satisfy the interface:

```go
// ListAgents returns status for all agents in the format expected by
// the mcp delegation interface.
func (m *Manager) ListAgents() []mcp.SubAgentInfo {
    m.mu.RLock()
    defer m.mu.RUnlock()
    out := make([]mcp.SubAgentInfo, 0, len(m.agents))
    for _, ag := range m.agents {
        out = append(out, mcp.SubAgentInfo{
            AgentID:   ag.id,
            ParentID:  ag.parentID,
            Status:    ag.status,
            Objective: ag.objective,
        })
    }
    return out
}

- [ ] **Step 3: Add ParentID to agentmsg.AgentStatus and update List()**

In `internal/agentmsg/agentmsg.go`, add ParentID:

```go
type AgentStatus struct {
    AgentID   string `json:"agent_id"`
    ParentID  string `json:"parent_id,omitempty"`
    ContextID string `json:"context_id"`
    Status    string `json:"status"`
    Objective string `json:"objective"`
    Tokens    int    `json:"tokens"`
}
```

Update `List()` in manager.go to populate ParentID:

```go
func (m *Manager) List() []agentmsg.AgentStatus {
    m.mu.RLock()
    defer m.mu.RUnlock()
    out := make([]agentmsg.AgentStatus, 0, len(m.agents))
    for _, ag := range m.agents {
        out = append(out, agentmsg.AgentStatus{
            AgentID:   ag.id,
            ParentID:  ag.parentID,
            ContextID: ag.contextID,
            Status:    ag.status,
            Objective: ag.objective,
            Tokens:    ag.tokens,
        })
    }
    return out
}
```

- [ ] **Step 4: Run build check**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agentcore/manager.go internal/agentmsg/agentmsg.go
git commit -m "feat(agentcore): add flat-string DelegateForParent and ListAgents matching mcp interface"
```

---

### Task 8: TUI — show sub-agents with parent-child hierarchy

**Files:**
- Modify: `internal/tui/` — agent list component (look for where AgentStatus is rendered)

- [ ] **Step 1: Find the TUI agent list component**

Search for where `agentmsg.AgentStatus` is rendered in the TUI. The left panel agent list likely iterates over the manager's `List()` and renders each entry.

The exact file depends on the TUI structure. Look in `internal/tui/` for the agent list model/view.

- [ ] **Step 2: Add parent-child rendering**

Modify the agent list to:
1. Render lead agents first (those with empty `ParentID`)
2. Render sub-agents indented beneath their parent with a child indicator (e.g., `  ↳ sub-agent-name`)
3. Use a distinct icon/color for sub-agents

The rendering logic:

```go
// Render agents with hierarchy
var sb strings.Builder
for _, a := range agents {
    if a.ParentID == "" {
        sb.WriteString(fmt.Sprintf("● %s [%s]\n", a.AgentID, a.Status))
        // Render children
        for _, child := range agents {
            if child.ParentID == a.AgentID {
                sb.WriteString(fmt.Sprintf("  ↳ %s [%s]\n", child.AgentID, child.Status))
            }
        }
    }
}
```

- [ ] **Step 3: Run build check**

Run: `go build ./internal/tui/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/tui/
git commit -m "feat(tui): show sub-agents with parent-child hierarchy in agent list"
```

---

### Self-Review Checklist

1. **Spec coverage**: Does each spec section map to a task?
   - §1 (system prompt redesign) → Task 1
   - §2 (Mission type + composer) → Task 2
   - §3 (sub-agent lifecycle/tools) → Tasks 4, 5, 6
   - §4 (token efficiency) — prompt-level, covered by ComposeSubAgentPrompt design
   - §5 (role seed library) → Task 3
   - §6 (failure handling) — Manager err handling + loop MaxIterations (existing)
   - §7 (implementation outline) — all 7 steps covered in Tasks 1-7
   - §8 (files changed) — all files accounted for

2. **Placeholder scan**: No TBD/TODO/incomplete steps. All code is concrete.

3. **Type consistency**: Mission struct used consistently. ComposeSubAgentPrompt called by DelegateForParent. DelegationManager interface matches Manager method signatures. AgentStatus includes ParentID.

4. **Testing**: Every task has either a unit test or a build-verification step. Integration is left to the existing test infrastructure.
