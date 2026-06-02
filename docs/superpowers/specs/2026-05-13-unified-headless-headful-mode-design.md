# Unified Headless/Headful Mode Design

**Date:** 2026-05-13

## Goal

Eliminate the separate headless/headful modes. Users launch vulpineos and never see the browser by default. When they select a context and press a key, that context's window appears. Multiple contexts can be visible simultaneously.

## Architecture

### Core Principle

Single Firefox process (not headless), always running. Control visibility per-context window, not per-Firefox instance.

### Components

1. **Kernel always headful** — Remove conditional `--headless` flag. Always start Firefox without `--headless`, always create WindowController.

2. **Hide on startup** — Immediately after Firefox starts, call `WindowController.HideWhenReady()` to hide the main window within 15 seconds of launch.

3. **Per-context window tracking** — Map each context to its Firefox window. When showing a context, either:
   - Create new window via `--new-window <url>`
   - Or reuse existing window for that context

4. **Show context** — User selects agent/context → presses `v` → system finds/creates window for that context → `Show()` brings it front.

5. **Hide context** — Press `v` again → `Hide()` sends that specific window to background.

6. **Hide all** — Press `Shift+v` → hide all visible context windows.

## Implementation Details

### Kernel Changes

In `internal/kernel/process.go`:

- Remove conditional logic around `--headless` flag
- Always add WindowController (remove `if !cfg.Headless` block)
- Call `k.window.HideWhenReady()` after Firefox starts successfully

### WindowController Changes

In `internal/kernel/window.go`:

- Extend to track multiple windows (context ID → window PID mapping)
- Add method: `ShowContext(contextID string)` — show specific context's window
- Add method: `HideContext(contextID string)` — hide specific context's window
- Add method: `HideAll()` — hide all tracked context windows
- Add method: `RefreshContextWindows()` — poll System Events for all Firefox windows

### Context Window Creation

In Juggler protocol (`additions/juggler/protocol/BrowserHandler.js`):

- When creating a context, open it in a new window: `Browser.createContext({ window: "new" })`
- Track the created window ID and associate with context ID
- Store context→window mapping in kernel for later show/hide operations

### TUI Changes

In `internal/tui/app.go`:

- `handleBrowserToggle()` — for selected context, toggle its window visibility
- Add `handleHideAll()` — hide all context windows
- Keyboard: `v` = toggle current context, `Shift+v` = hide all

### Web Panel Changes

In `web/src/pages/AgentDetail.jsx`:

- Add show/hide buttons per context
- Add "Hide all" button
- Same behavior as TUI

## Data Flow

1. **Launch:** Firefox starts (headful) → HideWhenReady() → main window hidden
2. **Create context:** Juggler creates context → opens in new Firefox window → track window PID
3. **Show context:** User presses `v` → find window PID for context → Show() → window frontmost
4. **Hide context:** User presses `v` again → Hide() → window hidden
5. **Hide all:** User presses `Shift+v` → iterate all context windows → Hide()

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `v` | Toggle current context window visibility |
| `Shift+v` | Hide all visible context windows |

## Edge Cases

- **No context selected:** `v` shows notice "Select a context first"
- **Context has no window:** Create new window with `--new-window` before showing
- **Window already visible:** `v` hides it
- **Firefox crashes:** WindowController handles gracefully, rebuild mapping on restart

## Testing

- Unit tests for WindowController context tracking
- Integration test: launch → hide main → create context → show → hide → hide all
- Manual test: multiple contexts visible simultaneously

## Success Criteria

1. Default launch: browser window never appears automatically
2. Select context + press `v`: that context's window appears
3. Press `v` again: window hides
4. Show context A, then context B: both windows visible
5. Press `Shift+v`: all windows hide
6. Works for both TUI and web panel