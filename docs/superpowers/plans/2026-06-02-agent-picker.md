# Plan: Agent Picker (inline palette truncation + `/agents` modal)

Spec: `docs/superpowers/specs/2026-06-02-agent-picker-design.md`

## Phases

### Phase 1 — Vault: add `last_selected_at` column

**File:** `internal/vault/db.go`
- In `migrateVault`, add:
  ```go
  if err := ensureColumn(conn, "agents", "last_selected_at", "INTEGER DEFAULT 0"); err != nil {
      return fmt.Errorf("migrate vault agents.last_selected_at: %w", err)
  }
  ```

**File:** `internal/vault/models.go`
- Add `LastSelectedAt time.Time` to `Agent` struct (after `LastActive`, ~line 71). JSON tag `last_selected_at`.

**File:** `internal/vault/agent.go`
- New method:
  ```go
  func (db *DB) MarkAgentSelected(id string, t time.Time) error {
      _, err := db.conn.Exec(
          `UPDATE agents SET last_selected_at = ? WHERE id = ?`,
          t.Unix(), id,
      )
      return err
  }
  ```
- Update all three SELECT queries (`GetAgent` ~L53, `ListAgents` ~L74, `ListAgentsByStatus` ~L102) to include `last_selected_at`, and add a `lastSelectedAt int64` scan variable in each loop, populating `a.LastSelectedAt = time.Unix(lastSelectedAt, 0)` (treat 0 as zero time).
- Update `CreateAgentWithID` to set `last_selected_at = 0` in the INSERT (already implicit via DEFAULT 0, but be explicit) and set `LastSelectedAt: time.Time{}` on the returned struct.

**Test:** `internal/vault/agent_test.go`
- Add `TestMarkAgentSelected` — create two agents, mark one selected, verify `GetAgent` returns the new `LastSelectedAt` and the other remains zero. Verify the value persists across a `Close` + `OpenPath` round trip.

**Verify:** `go test -count=1 ./internal/vault/...`

### Phase 2 — `agentlist.AgentListItem` carries `LastSelectedAt`

**File:** `internal/tui/agentlist/model.go`
- Add `LastSelectedAt time.Time` field to `AgentListItem` (~line 25, after `CreatedAt`).
- Update both conversion sites (line 91, line 168) to copy the field from `vault.Agent`.

**No new test needed** — exercised by Phase 5 tests.

### Phase 3 — New `internal/tui/agentpicker` package

**New file:** `internal/tui/agentpicker/model.go`
- `Model` struct: `agents []commandpalette.Agent`, `query string`, `selected int`, `width, height int`.
- `New(agents []commandpalette.Agent) *Model` constructor.
- `SetSize(width, height int)`.
- `Update(msg tea.Msg) (tea.Model, tea.Cmd)` — handle `tea.WindowSizeMsg`, `tea.KeyMsg` (esc/q/ctrl+c → cancel cmd, enter → pick cmd, up/down → move selection, runes → append to query, backspace → pop query).
- `View() string` — render the picker (see spec for layout). Use a compact render when `height < 14`, full render otherwise. Mirror `setup/model.go:viewProvider` and `viewModel` styling: same `titleStyle`/`activeStyle`/`mutedStyle`/`boxStyle` package-level vars, same scroll math, same "X of Y · ↑/↓ navigate · type to filter · [Enter] select · [Esc] cancel" hint line.
- `Done() bool` — true after the user has selected an agent and the picker has dispatched the picked msg.
- `Cancelled() bool` — true after Esc.

Style: the spec says "same color palette". Don't introduce new color constants. The four shared styles are defined in `setup/model.go:23-32` — they're package-private there. Either:
  (a) duplicate them in `agentpicker/model.go` (cheap, isolated), or
  (b) lift them into `internal/tui/shared/colors.go` so both packages share.

Go with (a) for now — two ~6-line style blocks is fine; extracting can wait until a third package needs them. Note in the PR description.

**New file:** `internal/tui/agentpicker/model_test.go`
- `TestNewEmptyShowsAllAgents` — picker with 0/1/5 agents renders correctly, lists all 5.
- `TestFilterNarrowsResults` — type "work" into the filter, assert only matching agents appear.
- `TestArrowKeysMoveSelectionThroughFilteredList` — set query to "a", press down twice, assert selection advances.
- `TestEnterDispatchesPickedMessage` — fire Enter, assert a `shared.AgentPickerPickedMsg` with the right ID.
- `TestEscDispatchesCancelledMessage` — fire Esc, assert `shared.AgentPickerCancelledMsg`.
- `TestBackspacePopsFilter` — type then backspace, assert query shrinks.
- `TestDoneAndCancelledAreExclusive` — pressing Enter then Esc in the same picker, assert final state is `Cancelled` (not `Done`).

**Verify:** `go test -count=1 ./internal/tui/agentpicker/...`

### Phase 4 — Shared messages

**File:** `internal/tui/shared/messages.go` (or wherever the existing `TickMsg`, `ReconfigureRequestedMsg`, `RuntimeEventMsg` live)
- Add:
  ```go
  type AgentPickerPickedMsg struct {
      AgentID   string
      AgentName string
  }

  type AgentPickerCancelledMsg struct{}
  ```

No tests for these directly — they're tested through the picker and through the app dispatch in Phase 6.

### Phase 5 — App: truncation, top-recent helper, sync change

**File:** `internal/tui/app.go`

New helper near `syncCommandPaletteAgents` (~line 3483):

```go
// topRecentAgents returns up to limit agents, sorted by
// last_selected_at desc (zero values last), then by created_at desc.
func topRecentAgents(items []agentlist.AgentListItem, limit int) []commandpalette.Agent
```

Implementation: copy items, sort by `lastSelectedAtCmp` then `createdAtCmp` (use `slices.SortStableFunc`), slice to `limit`, convert.

In `syncCommandPaletteAgents`:
- Change the call site to:
  ```go
  a.commandPalette.SetAgents(topRecentAgents(items, 3))
  ```
- Drop the `selectedAgentID` exclusion (it biased the previous behavior; the modal is the new path, and excluding the current agent from the inline list of "most recently used" is wrong — the user just used it, of course it's the most recent).

### Phase 6 — App: agent picker integration

**File:** `internal/tui/app.go`

1. **Imports** — add `agentpicker "vulpineos/internal/tui/agentpicker"`.

2. **Struct** — add fields next to the `setupWizard` block (~line 189):
   ```go
   agentPicker       *agentpicker.Model
   agentPickerActive bool
   agentPickerReturn int
   ```

3. **New methods** (mirror `startEmbeddedReconfigure` / `cancelEmbeddedReconfigure` / `completeEmbeddedReconfigure` at ~lines 2754, 2797, 2808):
   ```go
   func (a *App) startAgentPicker() tea.Cmd
   func (a *App) cancelAgentPicker()
   func (a *App) completeAgentPicker(agentID, agentName string)
   ```
   - `startAgentPicker`: snapshot `a.agentPickerReturn = a.focus`, build the picker from `topRecentAgents(a.agentList.Agents(), 1<<30)` (all agents, not truncated), seed `a.agentPicker`, set `a.agentPickerActive = true`, send a `tea.WindowSizeMsg` so the picker gets its initial size.
   - `cancelAgentPicker`: set `active=false`, `picker=nil`, restore focus from `agentPickerReturn`, set `settings.SetActive(focus == FocusSettings)`, set a notice "Picker cancelled" with TTL 2.
   - `completeAgentPicker`: `a.vault.MarkAgentSelected(agentID, time.Now())`, then run the same selection logic as `selectAgentListItem` (refactor: extract a `func (a *App) switchToAgent(item agentlist.AgentListItem)` that both call), then refresh `syncCommandPaletteAgents`, then restore focus like cancel does, then set a notice "Switched to <name>".

4. **Dispatch** — in `dispatchCommand` (~line 3985), add `case "agents":` that returns `a.startAgentPicker()`. Remove `case "select":` and the call to `a.handleAgentSelect(rawInput)`.

5. **Remove `handleAgentSelect`** (~line 3427) entirely.

6. **Render gate** — in `View()` (find the `if a.setupActive` block at ~line 2396), add an `else if a.agentPickerActive` branch right after that calls `a.agentPicker.View()`. The setup wizard takes priority because it shouldn't be interrupted by the picker.

7. **Update gate** — in `Update()` (the `if a.setupActive` block at ~line 955), add an `else if a.agentPickerActive` block that intercepts `tea.KeyMsg` and `tea.WindowSizeMsg`, forwards to `a.agentPicker.Update`, and on the way out checks the new shared messages:
   - `shared.AgentPickerPickedMsg` → `a.completeAgentPicker(msg.AgentID, msg.AgentName)`.
   - `shared.AgentPickerCancelledMsg` → `a.cancelAgentPicker()`.

### Phase 7 — Palette: drop `select`, add `agents`

**File:** `internal/tui/commandpalette/commandpalette.go`
- In `defaultCommands()` (~line 297), remove the `select` entry.
- Add `{Name: "agents", Description: "Browse all agents", Section: "System"}`.
- In `commandAliases` (~line 290), remove the `case "select":` branch if any (there isn't one — the `select` command had no aliases — but double-check).

### Phase 8 — Tests

**File:** `internal/tui/commandpalette/commandpalette_test.go`
- Delete `TestSlashPrefixedSelectInputDispatchesSelectCommand` (it asserts `/select fox` dispatches select).
- `TestDefaultCommandsDispatchByTypedName` automatically picks up the new `agents` command via the table — no edit needed beyond verifying it still passes.
- `TestExactCommandQueryPrefersCommandOverAgentMatches` referenced the old `select`-via-palette behavior — delete it (it no longer represents a feature).
- `TestPartialCloseAliasDoesNotPreferQuit` is unrelated, keep it.

**File:** `internal/tui/app_test.go`
- Search for any test that uses `runSlashCommand(t, app, "select ...")` or `"/select"` and rewrite it to use `/agents` + drive the picker. For each:
  - Fire `/agents` slash command → `app.startAgentPicker()`.
  - Send an `up`/`down` KeyMsg enough times to land on the target agent.
  - Send `enter` KeyMsg.
  - Assert `app.selectedAgentID` and the conversation's agent ID.
- Add a new test `TestInlinePaletteTruncatesToThreeRecentAgents`:
  - Create 5 agents in the vault, set `last_selected_at` to descending times (e.g. agent 1 selected most recently, agent 5 never).
  - Call `app.syncCommandPaletteAgents()`.
  - Assert `len(app.commandPalette.filtered)` contains only 3 agent entries (plus the system commands).
  - Assert the top 3 are the agents with the latest `last_selected_at`.
- Add a new test `TestAgentsSlashCommandOpensPicker`:
  - Set up 3 agents, fire `/agents` via `runSlashCommand`.
  - Assert `app.agentPickerActive && app.agentPicker != nil`.
  - Fire Esc, assert `!app.agentPickerActive && app.agentPicker == nil && app.notice == "Picker cancelled"`.
- Add a new test `TestAgentPickerSelectionPersistsLastSelected`:
  - Set up 3 agents, fire `/agents`, drive picker to select agent[1], fire Enter.
  - Assert `app.vault.GetAgent(agent[1].ID).LastSelectedAt` is recent (within 5s of now).
  - Re-fire `syncCommandPaletteAgents`, assert agent[1] is now the top of the inline palette list.

**Verify:** `go test -count=1 ./internal/tui/...` — full TUI suite green.

### Phase 9 — Cleanup

- `go vet ./internal/...` clean.
- `gofmt -w` on every touched file.
- Re-run the `/model` and `/config` and palette-related tests from earlier sessions to make sure the new picker didn't disturb them.
- Update `docs/openpowers/specs/2026-06-02-agent-picker-design.md`'s Success Criteria with the actual run results.

## Files Touched

| File | Reason |
| --- | --- |
| `internal/vault/db.go` | migration: add `last_selected_at` column |
| `internal/vault/models.go` | add `LastSelectedAt` to `Agent` struct |
| `internal/vault/agent.go` | new `MarkAgentSelected`, update 3 SELECTs + INSERT |
| `internal/vault/agent_test.go` | test `MarkAgentSelected` + persistence |
| `internal/tui/agentlist/model.go` | add `LastSelectedAt` to `AgentListItem`, copy in 2 converters |
| `internal/tui/agentpicker/model.go` (new) | picker Bubbletea model |
| `internal/tui/agentpicker/model_test.go` (new) | picker unit tests |
| `internal/tui/shared/messages.go` | add `AgentPickerPickedMsg`, `AgentPickerCancelledMsg` |
| `internal/tui/commandpalette/commandpalette.go` | drop `select` from `defaultCommands`, add `agents` |
| `internal/tui/commandpalette/commandpalette_test.go` | delete `/select` tests |
| `internal/tui/app.go` | new picker fields, `topRecentAgents` helper, `startAgentPicker` / `cancelAgentPicker` / `completeAgentPicker`, render + update gates, dispatch case, remove `handleAgentSelect` and `case "select":` |
| `internal/tui/app_test.go` | rewrite `/select` tests to use picker, add new picker tests |

## Risks

- **DB migration on existing vaults.** A user's existing `agents` table doesn't have `last_selected_at`. `ensureColumn` handles this idempotently — runs an `ALTER TABLE` only if the column is missing. No backfill needed: zero values (from `DEFAULT 0`) just mean "never selected", which the sort already handles (zero time sorts last).
- **Picker input vs conversation input.** The picker intercepts keys via the `a.agentPickerActive` gate, identical to `a.setupActive`. The conversation's text input is not focused while the picker is open (focus snapshot stores whatever it was, then on close restores it). No risk of double-handling.
- **Picking the same agent you're already on.** `completeAgentPicker` still calls `MarkAgentSelected` (updates the timestamp so it moves to the top), still calls `selectAgentListItem` (which is idempotent — sets the same ID, reloads messages, updates the detail panel). Slight wasted work but no behavior bug. Could short-circuit if `agentID == a.selectedAgentID`; not worth the branch for now.
- **Remote control mode.** The picker doesn't need to be remote-aware — `a.agentList` is local, `MarkAgentSelected` is local. The same modal works in both modes.
- **The wizard for setup is multi-step; the picker is single-step.** Two different packages. The picker's `Done()` returns `true` only after Enter on an agent; the setup wizard's `Done()` returns `true` only on the final step. They don't share infrastructure. No conflict.

## Verification

1. `go test -count=1 ./internal/vault/...` — green
2. `go test -count=1 ./internal/tui/agentpicker/...` — green
3. `go test -count=1 ./internal/tui/...` — green (all subpackages)
4. `go vet ./internal/...` — clean
5. Manual smoke (the user does this; not me): create 5 agents, open `/`, see top 3 in the inline list. Type `/agents`, see the modal with all 5. Filter by name, select, verify it becomes the top inline entry on next `/`.

## Out of Scope (deferred)

- Renaming or deleting agents from the picker.
- New-agent creation from the picker.
- Persisting the filter query across invocations.
- A keyboard shortcut (Ctrl+G or similar) to open the picker without typing `/agents`.
- Bulk operations.
