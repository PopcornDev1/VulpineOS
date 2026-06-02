# Agent Picker: Inline Palette Truncation + `/agents` Modal

## Problem

The command palette currently surfaces every agent as a `select` entry
(see `app.go:syncCommandPaletteAgents`, `commandpalette.go:SetAgents`).
With more than a handful of agents the inline palette becomes
unmanageable — every agent is mixed in with system commands and there is
no way to search/filter them, scroll the list compactly, or rank them
usefully. There is also no `select` shortcut that the user can remember
without scrolling the whole agent list.

The `select` command is the only way to switch agents from the palette
today, and it accepts a free-form name or ID via `rawInput` (see
`app.go:handleAgentSelect`). That mechanism is implicit and offers no
discovery.

## Goal

- Limit the inline command palette to the **3 most recently used**
  agents, ranked by a new `last_selected_at` timestamp on the agent
  row (falling back to `created_at` when never selected).
- Add a new `/agents` slash command that opens a dedicated modal —
  filterable, scrollable, Enter to select, Esc to cancel — modeled on
  the provider/model list in the existing setup wizard
  (`internal/tui/setup/model.go:viewProvider`, `viewModel`).
- Remove the `/select` command and the `select` command dispatch from
  `app.go:dispatchCommand`. The modal is the only way to switch agents
  from the palette going forward.
- Persist `last_selected_at` to the agents table so the ranking
  survives restarts.

## Design

### Vault schema (`internal/vault/vault.go` or wherever the agents
table is migrated)

Add a `last_selected_at TEXT` column to the `agents` table. Migration
runs in the same place as the existing schema migrations — append an
`ALTER TABLE agents ADD COLUMN last_selected_at TEXT` guarded by a
`PRAGMA user_version` check so it only runs once.

New methods on the vault DB:

```go
// MarkAgentSelected records that the user just switched to this agent.
// Pass zero time to clear.
MarkAgentSelected(id string, t time.Time) error
```

Loaded agents carry `LastSelectedAt time.Time` alongside the existing
fields. When converting from `vault.Agent` to
`agentlist.AgentListItem` (see `agentlist/model.go:91, 168`), populate
`CreatedAt` (already done) and the new `LastSelectedAt`.

### Inline palette ranking (`commandpalette.go:SetAgents`)

`SetAgents` accepts a slice of `Agent`. The caller (currently
`app.go:syncCommandPaletteAgents`) is the place to do the sorting +
truncation, not the palette itself — the palette should remain a dumb
filter over whatever it is given.

New helper in `app.go`:

```go
// topRecentAgents returns up to limit agents sorted by
// last_selected_at desc (zero time last), then created_at desc.
func topRecentAgents(items []agentlist.AgentListItem, limit int) []commandpalette.Agent
```

`syncCommandPaletteAgents` calls it before calling `SetAgents`, so the
inline palette only ever sees the top 3 (or fewer if there are fewer
agents).

### `/agents` modal — new package `internal/tui/agentpicker`

Mirror the structure of the setup wizard but single-step. A new
package `internal/tui/agentpicker` with `model.go` and `model_test.go`:

```go
type Model struct {
    agents  []commandpalette.Agent // full list, not truncated
    query   string
    selected int
    width   int
    height  int
}

func New(agents []commandpalette.Agent) *Model
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd)
func (m *Model) View() string
func (m *Model) SelectedAgent() (commandpalette.Agent, bool) // called by app on close
```

The modal renders:

```
┌─ Select an agent ──────────────────────┐
│ Filter: worker_                        │
│                                        │
│ > worker_1    active                   │
│   worker_2    paused                   │
│   worker_3    active                   │
│                                        │
│ [↑/↓] navigate  type to filter  [Enter] select  [Esc] cancel
└────────────────────────────────────────┘
```

Visual style matches the setup wizard's provider/model pickers
(`setup/model.go:493 viewProvider`, `605 viewModel`) for consistency:
same border, same color palette, same scrollbar treatment, same filter
input styling. No new visual language.

Key handling:
- `esc` / `ctrl+c` / `q` — close, no selection, fire a cancel msg.
- `enter` — close, fire an "agent picked" msg with the selected agent.
- `up` / `down` — move selection through the *filtered* list.
- Printable runes — append to the filter query.
- `backspace` — pop last rune.
- `tab` / `shift+tab` — cycle through results when filter is empty
  (matches setup wizard provider step behavior).

A new shared message in `internal/tui/shared/`:

```go
type AgentPickerCancelledMsg struct{}
type AgentPickerPickedMsg struct {
    AgentID   string
    AgentName string
}
```

### App integration (`app.go`)

1. **New App state** — mirror `setupActive` / `setupWizard` /
   `setupReturnFocus`:

   ```go
   agentPicker        *agentpicker.Model
   agentPickerActive  bool
   agentPickerReturn  int // focus to restore on close
   ```

2. **New methods**:

   ```go
   func (a *App) startAgentPicker() tea.Cmd
   func (a *App) cancelAgentPicker()
   func (a *App) completeAgentPicker(agent commandpalette.Agent)
   ```

   `startAgentPicker` snapshots `a.agentPickerReturn = a.focus`, sets
   focus to whatever the picker renders into (does not matter — the
   picker intercepts input via the `a.agentPickerActive` gate, same
   pattern as `a.setupActive` at `app.go:955`).
   `completeAgentPicker` calls `a.vault.MarkAgentSelected(id, time.Now())`,
   then runs the same selection path that `selectAgentListItem` runs
   today (`app.go:3466`), then refreshes the palette via
   `syncCommandPaletteAgents`.

3. **Remove**:
   - The `case "select":` block in `dispatchCommand`
     (`app.go:3991`).
   - The `handleAgentSelect` function (`app.go:3427`) — it is only
     called by the dispatch.
   - The `select` entry in `defaultCommands()`
     (`commandpalette.go:299`).
   - The `select` agent commands in `SetAgents` — replace with
     `Display = Name + " (" + status + ")"` style, just enough for the
     picker to identify the agent. The picker doesn't need a
     `RawInput` like the old `select` did.

4. **Add** the new `agents` entry in `defaultCommands()`:

   ```go
   {Name: "agents", Description: "Browse all agents", Section: "System"},
   ```

   And the dispatch case:

   ```go
   case "agents":
       return a.startAgentPicker()
   ```

5. **Rendering gate** in the main `View()` — add a branch alongside
   the existing `if a.setupActive` (line 2396) that delegates to
   `a.agentPicker.View()`.

6. **Update gate** in `Update` — add a branch alongside the
   `if a.setupActive` block (line 955) that intercepts key/window
   messages and forwards them to `a.agentPicker.Update(msg)`, then
   checks for the pick/cancel messages produced by the picker.

### Tests

- `internal/tui/agentpicker/model_test.go` — filter narrows the list,
  arrow keys move selection through the filtered list, Enter
  dispatches `AgentPickerPickedMsg`, Esc dispatches
  `AgentPickerCancelledMsg`, backspace pops the filter, empty filter
  shows all agents.
- `internal/tui/commandpalette/commandpalette_test.go` — the
  `TestSlashPrefixedSelectInputDispatchesSelectCommand` and
  `TestExactCommandQueryPrefersCommandOverAgentMatches` tests get
  removed (the `select` command is gone). The
  `TestDefaultCommandsDispatchByTypedName` table-driven test
  automatically picks up the new `agents` command.
- `internal/tui/app_test.go` — replace any tests that invoke
  `/select` (search for `"select"`) with tests that invoke
  `/agents` + drive the picker. Add a new test verifying the inline
  palette only contains 3 agents even when 5 are loaded, sorted by
  `last_selected_at` desc.

## Open Questions

None — clarified upfront:
- Picker is single-step with filter (not the full multi-step setup
  wizard scaffold).
- Sort key is `last_selected_at` desc, persisted to disk.
- `/select` is removed; `/agents` is the only path.

## Out of Scope

- Renaming agents from the picker (use existing `/rename`).
- Creating new agents from the picker (use existing `/new`).
- Deleting agents from the picker (use existing `/delete`).
- Multi-select / batch operations on agents.
- Persisting filter query across picker invocations.
- Hotkey for opening the picker (e.g. Ctrl+G). Future enhancement.

## Success Criteria

1. With 5+ agents loaded, the inline `/` palette shows exactly 3,
   ranked by recency, plus all system commands including the new
   `agents` command.
2. Typing `/agents` and Enter opens a modal listing all agents with a
   working filter, arrow-key navigation, Enter to select, Esc to
   cancel.
3. Selecting an agent from the modal switches the conversation to
   that agent, marks `last_selected_at = now`, and on next invocation
   of `/` the inline palette reflects the new top-3 ranking.
4. After restarting the app, the top-3 ranking persists.
5. `/select <name>` no longer dispatches anything (the command is
   gone from the palette and from the dispatch table).
6. `go test -count=1 ./internal/tui/...` and `go vet ./internal/...`
   pass clean.
