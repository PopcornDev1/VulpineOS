# Conversation Yank: Ctrl+Y Copies Latest Agent Response

## Problem

When selecting text in the conversation panel with the mouse, terminal
selection is inherently rectangular — it unavoidably bleeds into the
agent, system, and context panels on the same rows. There is no way to
constrain terminal cell selection to a sub-region of the grid without
layout changes or rendering tricks.

## Goal

Allow the user to copy the latest agent response to the clipboard
without mouse selection, preserving the existing multi-column layout
with zero visual changes.

## Solution

A **Ctrl+Y** yank shortcut that copies the most recent assistant entry
from the conversation model to the system clipboard.

## Design

### Conversation model (`internal/tui/conversation/model.go`)

New method:

```go
// LatestAssistantContent returns the Content of the most recent
// entry with Role == "assistant", or "" if none exists.
func (m Model) LatestAssistantContent() string
```

It walks `m.entries` backward and returns the first entry whose `Role`
field equals `"assistant"`. Returns `""` if no assistant entry exists
(conversation is empty or only contains user messages).

### App keybinding (`internal/tui/app.go`)

1. **`allowFocusedChatShortcut`** — add `"ctrl+y"` as a separate case
   that returns `true` unconditionally (Ctrl+Y never types a literal
   character, so it should always break through to the normal keybind
   handler).

2. **Normal keybinds switch** — add:

   ```go
   case "ctrl+y":
       cmds = append(cmds, a.handleYankResponse())
   ```

3. **`updateChatInput`** (both locked and unlocked paths) — add:

   ```go
   case "ctrl+y":
       cmds = append(cmds, a.handleYankResponse())
       return a, nil
   ```

4. **`handleYankResponse`** — new method:

   ```go
   func (a *App) handleYankResponse() tea.Cmd {
       return func() tea.Msg {
           content := a.conversation.LatestAssistantContent()
           if content == "" {
               return statusNotice{text: "No agent response to copy"}
           }
           if err := clipboard.WriteAll(content); err != nil {
               return statusNotice{text: "Copy failed: " + err.Error()}
           }
           return statusNotice{text: "Copied latest agent response"}
       }
   }
   ```

### Clipboard dependency

`github.com/atotto/clipboard` already exists in `go.mod` as an indirect
dependency. It must be promoted to a direct import in `app.go`.

## Keybinding table

| Key    | Context        | Action                        |
|--------|----------------|-------------------------------|
| Ctrl+Y | Any            | Copy latest assistant content |
| y      | (unchanged)    | N/A (let through to chat)     |

## Out of scope

- No visual layout changes
- No mouse selection changes
- No copying full conversation history
- No copying user messages
- No copying specific entries by index

## Files changed

- `internal/tui/conversation/model.go` — one new method
- `internal/tui/app.go` — keybinding, dispatch, handler, import

## Verification

1. Open TUI with an agent conversation that has at least one assistant
   response.
2. Press Ctrl+Y.
3. Confirm status bar shows "Copied latest agent response".
4. Paste elsewhere — confirm the full assistant response text appears.
5. Test with empty conversation — confirm notice says "No agent
   response to copy".
6. Test with only user messages — same notice.
7. Test with chat input focused and non-empty — Ctrl+Y still fires,
   does not insert characters.
