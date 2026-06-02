# Unified Headless/Headful Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate separate headless/headful modes. Firefox always runs headful (no --headless), main window hidden on startup, contexts shown/hidden individually with per-context window visibility.

**Architecture:** Single Firefox process, always headful. WindowController manages visibility per-context window. Hide main window on startup via HideWhenReady(), show/hide individual context windows on user command.

**Tech Stack:** Go (kernel, TUI), JavaScript (Juggler protocol), osascript (macOS window control)

---

## File Structure

### Files to Modify

1. `internal/kernel/process.go` — Remove conditional headless, always create WindowController, call HideWhenReady
2. `internal/kernel/window.go` — Add context window tracking (contextID → windowPIDs mapping), show/hide per context, hide all
3. `internal/tui/app.go` — Update handleBrowserToggle for per-context, add handleHideAll, keyboard bindings
4. `web/src/pages/AgentDetail.jsx` — Add show/hide buttons per context, hide all button

### Existing Patterns to Follow

- WindowController uses osascript to control visibility via System Events
- Kernel has `Window() *WindowController` and `IsHeadless() bool` methods
- TUI uses keyboard handler with notice system for feedback

---

## Task 1: Kernel Always Headful

**Files:**
- Modify: `internal/kernel/process.go:138-144` (remove conditional headless flag)
- Modify: `internal/kernel/process.go:217-222` (always create WindowController)
- Modify: `internal/kernel/process.go:225` (call HideWhenReady after start)

- [ ] **Step 1: Read the relevant code sections**

Read `internal/kernel/process.go` lines 130-230 to see current conditional logic.

- [ ] **Step 2: Remove conditional headless flag logic**

In `buildArgs()` function around line 139, remove:
```go
if cfg.Headless {
    args = append(args, "--headless")
}
```

Replace with empty (no --headless flag ever).

- [ ] **Step 3: Always create WindowController**

Around line 218, remove the `if !cfg.Headless` conditional and always create WindowController:
```go
k.headless = false // Always false now
k.window = NewWindowController(cmd.Process.Pid)
```

- [ ] **Step 4: Call HideWhenReady after start**

After successful start (around line 223), add:
```go
go func() {
    time.Sleep(2 * time.Second) // Wait for Firefox to fully start
    if k.window != nil {
        k.window.HideWhenReady()
    }
}()
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/kernel/... -race`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add internal/kernel/process.go
git commit -m "feat: kernel always headful, hide main window on startup"
```

---

## Task 2: WindowController Context Tracking

**Files:**
- Modify: `internal/kernel/window.go` (add context tracking methods)

- [ ] **Step 1: Read window.go**

Read `internal/kernel/window.go` to understand current structure.

- [ ] **Step 2: Add context tracking fields**

Add to WindowController struct:
```go
type WindowController struct {
    // ... existing fields ...
    contextWindows map[string][]int  // contextID -> list of window PIDs
    mu             sync.RWMutex
}
```

- [ ] **Step 3: Add method to register context window**

Add method after line 86:
```go
// RegisterContextWindow registers a window PID for a context.
func (w *WindowController) RegisterContextWindow(contextID string, pid int) {
    w.mu.Lock()
    defer w.mu.Unlock()
    if w.contextWindows == nil {
        w.contextWindows = make(map[string][]int)
    }
    // Avoid duplicates
    for _, p := range w.contextWindows[contextID] {
        if p == pid {
            return
        }
    }
    w.contextWindows[contextID] = append(w.contextWindows[contextID], pid)
}
```

- [ ] **Step 4: Add method to show context**

Add method:
```go
// ShowContext shows the window(s) for a specific context.
func (w *WindowController) ShowContext(contextID string) error {
    w.mu.Lock()
    defer w.mu.Unlock()
    pids := w.contextWindows[contextID]
    if len(pids) == 0 {
        return fmt.Errorf("no windows found for context %s", contextID)
    }
    var lastErr error
    for _, pid := range pids {
        wc := &WindowController{pid: pid}
        if err := wc.Show(); err != nil {
            lastErr = err
            continue
        }
        return nil
    }
    return lastErr
}
```

- [ ] **Step 5: Add method to hide context**

Add method:
```go
// HideContext hides the window(s) for a specific context.
func (w *WindowController) HideContext(contextID string) error {
    w.mu.Lock()
    defer w.mu.Unlock()
    pids := w.contextWindows[contextID]
    if len(pids) == 0 {
        return fmt.Errorf("no windows found for context %s", contextID)
    }
    var lastErr error
    for _, pid := range pids {
        wc := &WindowController{pid: pid}
        if err := wc.Hide(); err != nil {
            lastErr = err
            continue
        }
        return nil
    }
    return lastErr
}
```

- [ ] **Step 6: Add method to hide all contexts**

Add method:
```go
// HideAll hides all tracked context windows.
func (w *WindowController) HideAll() error {
    w.mu.Lock()
    defer w.mu.Unlock()
    var lastErr error
    for contextID, pids := range w.contextWindows {
        for _, pid := range pids {
            wc := &WindowController{pid: pid}
            if err := wc.Hide(); err != nil {
                lastErr = err
            }
        }
    }
    return lastErr
}
```

- [ ] **Step 7: Add method to get visible contexts**

Add method:
```go
// VisibleContexts returns list of contextIDs with visible windows.
func (w *WindowController) VisibleContexts() []string {
    w.mu.Lock()
    defer w.mu.Unlock()
    var result []string
    for contextID, pids := range w.contextWindows {
        for _, pid := range pids {
            wc := &WindowController{pid: pid}
            if visible, _ := wc.Status(); visible {
                result = append(result, contextID)
                break
            }
        }
    }
    return result
}
```

- [ ] **Step 8: Write tests**

Add to `internal/kernel/window_test.go`:
```go
func TestWindowControllerContextTracking(t *testing.T) {
    wc := NewWindowController(os.Getpid())

    // Test empty context
    err := wc.ShowContext("test-context")
    if err == nil {
        t.Fatal("ShowContext should fail for unknown context")
    }

    // Test HideAll with no contexts (should not error)
    err = wc.HideAll()
    if err != nil {
        t.Fatalf("HideAll should not error with no contexts: %v", err)
    }
}
```

- [ ] **Step 9: Run tests**

Run: `go test ./internal/kernel/... -race`
Expected: All pass

- [ ] **Step 10: Commit**

```bash
git add internal/kernel/window.go internal/kernel/window_test.go
git commit -m "feat: add per-context window tracking to WindowController"
```

---

## Task 3: TUI Context Show/Hide

**Files:**
- Modify: `internal/tui/app.go` (update handleBrowserToggle, add handleHideAll)
- Create: `internal/tui/app_context_windows.go` (optional - if app.go gets too large)

- [ ] **Step 1: Read current handleBrowserToggle**

Read `internal/tui/app.go` lines 1669-1700 to see current implementation.

- [ ] **Step 2: Determine selected context ID**

Find how to get the currently selected context ID from the TUI state. Look at:
- `a.contextList` usage
- Context selection methods

- [ ] **Step 3: Update handleBrowserToggle for per-context**

Replace the current `handleBrowserToggle` implementation:
```go
func (a *App) handleBrowserToggle() {
    if a.kernel == nil || a.kernel.Window() == nil {
        a.notice = "Browser not available"
        a.noticeTTL = 3
        return
    }

    contextID := a.contextList.SelectedContextID()
    if contextID == "" {
        a.notice = "Select a context first"
        a.noticeTTL = 3
        return
    }

    window := a.kernel.Window()

    // Check if this context is currently visible
    visible := false
    for _, pid := range window.(*WindowController).GetContextPIDs(contextID) {
        wc := &WindowController{pid: pid}
        if v, _ := wc.Status(); v {
            visible = true
            break
        }
    }

    if visible {
        if err := window.HideContext(contextID); err != nil {
            a.notice = "Failed to hide context: " + err.Error()
        } else {
            a.notice = "Context hidden"
        }
    } else {
        if err := window.ShowContext(contextID); err != nil {
            a.notice = "Failed to show context: " + err.Error()
        } else {
            a.notice = "Context shown — press v to hide"
        }
    }
    a.noticeTTL = 3
}
```

**Note:** The above requires adding `GetContextPIDs` to WindowController. Add it in Task 2 if needed.

- [ ] **Step 4: Add handleHideAll method**

Add to app.go:
```go
func (a *App) handleHideAll() {
    if a.kernel == nil || a.kernel.Window() == nil {
        a.notice = "Browser not available"
        a.noticeTTL = 3
        return
    }

    if err := a.kernel.Window().HideAll(); err != nil {
        a.notice = "Failed to hide all: " + err.Error()
    } else {
        a.notice = "All contexts hidden"
    }
    a.noticeTTL = 3
}
```

- [ ] **Step 5: Add keyboard binding for Shift+v**

Find where keyboard handlers are registered and add:
- `Shift+v` → `handleHideAll`

Look for patterns like `case KeyV:` or similar key handling.

- [ ] **Step 6: Test manually**

Build and test:
```bash
go build -o vulpineos ./cmd/vulpineos
./vulpineos --binary ~/Downloads/Camoufox.app/Contents/MacOS/camoufox
```

Expected: Browser window hidden on startup. Press v with context selected shows that context's window.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/app.go
git commit -m "feat: TUI per-context window show/hide with Shift+v hide all"
```

---

## Task 4: Register Context Windows (Integration)

**Files:**
- Modify: `internal/pool/pool.go` (when context is created, register its window)
- Or: `internal/orchestrator/orchestrator.go`

- [ ] **Step 1: Find where context is created and first page opens**

Look for where `Browser.createBrowserContext` is called and where the first page navigates.

- [ ] **Step 2: Add window registration after context creation**

After context is created and a page is opened, get the window PID and register it:
```go
// After page is created in context
if kernel.Window() != nil {
    // Get the Firefox window PID for this page
    // This may require a Juggler protocol call
    kernel.Window().RegisterContextWindow(contextID, windowPID)
}
```

**Note:** This step may need investigation to find the right place to get the window PID. The Juggler protocol may need a new method to get window info.

- [ ] **Step 3: Commit**

```bash
git add internal/pool/pool.go
git commit -m "feat: register context windows with WindowController"
```

---

## Task 5: Web Panel Context Visibility

**Files:**
- Modify: `web/src/pages/AgentDetail.jsx`

- [ ] **Step 1: Read AgentDetail.jsx**

Find current context display and actions.

- [ ] **Step 2: Add show/hide buttons**

Add buttons per context row:
- "Show" - calls API to show context window
- "Hide" - calls API to hide context window
- "Hide All" button at top

- [ ] **Step 3: Add API endpoints**

In `internal/remote/api.go`, add:
- `Browser.showContext(contextID)`
- `Browser.hideContext(contextID)`
- `Browser.hideAllContexts()`

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/AgentDetail.jsx internal/remote/api.go
git commit -m "feat: web panel context show/hide controls"
```

---

## Testing Checklist

- [ ] Default launch: browser never appears automatically
- [ ] Select context + press v: context window appears
- [ ] Press v again: context window hides
- [ ] Show context A, then context B: both visible
- [ ] Press Shift+v: all windows hide
- [ ] Works in both TUI and web panel

## Dependencies Between Tasks

1. Task 1 (kernel headful) — Can be done first, standalone
2. Task 2 (window context tracking) — Depends on Task 1, can be done in parallel
3. Task 3 (TUI) — Depends on Task 2
4. Task 4 (integration) — Depends on Task 2, requires investigation
5. Task 5 (web panel) — Depends on Task 2, can be done in parallel with Task 3

## Notes

- Task 4 may need additional investigation to find the right integration point
- The Juggler protocol may need extensions to get window PIDs for contexts
- Consider adding a method to list all Firefox windows for initial discovery
