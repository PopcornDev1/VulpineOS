# Streaming Agent Output for TUI

Deliver LLM tokens progressively to the TUI (token-by-token streaming like ChatGPT) instead of waiting for the complete response to arrive as a single blob.

## Motivation

When using the VulpineOS TUI, agent responses appear all at once — the user sees nothing until the entire LLM response has been generated, processed through tool calls, and written back to NanoClaw's outbound database. For long responses this creates a UX dead zone where the TUI shows only a thinking spinner. The user wants to see the response as it's produced, character by character.

## Architecture

### Layer 1: Transport — Sidecar Stream File

The agent-runner's opencode provider (generated TypeScript in `source_runtime.go`) is modified to:

1. Add `stream: true` to the OpenRouter fetch body
2. Parse the SSE response via Web Streams `ReadableStream` + `TextDecoder`
3. For each content delta, append a JSON line to a stream file on the shared workspace volume

**Stream file format** (append-only JSONL at `/workspace/agent/stream_<sessionId>.jsonl`):

```
{"t":"Hello"}
{"t":" world"}
{"t":"!"}
{"tool":"bash"}
{"done":"Hello world!"}
```

| Entry | Meaning |
|-------|---------|
| `{"t":"..."}` | Content delta chunk from the LLM |
| `{"tool":"name"}` | LLM initiated a tool call mid-stream |
| `{"done":"full text"}` | Streaming complete, this is the authoritative full text |

**Flush strategy:** After each write, `fs.fsyncSync(fd)` ensures the host sees the content promptly. No in-memory accumulation on the container side — just append to disk and yield.

**File limits:** 256KB max. Beyond that, the stream file is truncated and the normal outbound DB path is the sole delivery mechanism (no data loss, just no streaming past 256KB).

### Layer 2: Mirror Integration

The existing `NanoClawSessionMirror` poll loop (50ms–2s adaptive) gains a second task alongside `mirrorOutboundDb()`:

1. On each tick, after outbound DB polling, call `pollStreamFile(sessionID)`
2. Opens `stream_<sessionId>.jsonl` from last known byte offset
3. Parses new lines, accumulates: `acc += line.t` for each `{"t":"..."}` entry
4. If accumulated text grew since last poll: call `agent.handleStreamContent(acc, streaming=true)`
5. If `{"done":"fullText"}` encountered: emit final assistant message, delete stream file, record `streamCompleted[sessionID] = true`
6. When the outbound DB later produces the same message for this session, check `streamCompleted` — if true, skip emitting (dedup)

**Throttling:** Max one emit per 50ms to avoid flooding the TUI.

**Self-throttled backoff:** When streaming is active (new content detected every poll), the poll interval stays at 50ms. When streaming stalls (no new content for 3+ ticks), the interval doubles up to 2s as before.

### Layer 3: TUI Rendering

A new `Role: "stream"` message type carries the entire accumulated text on each update:

```go
type ConversationMsg struct {
    AgentID      string
    Role         string   // "stream" for in-progress, "assistant" for final
    Content      string   // full accumulated text so far
    StreamActive bool     // true while streaming, false on final flush
    Tokens       int
}
```

**Conversation model** (`conversation/model.go`):

- New method: `UpdateLastAssistant(content string)` — finds the last "stream" or "assistant" entry, replaces its `Content`, re-renders markdown
- On `Role: "stream"` arrival:
  - No entries or last entry is user message: append new entry with `StreamActive: true`
  - Last entry is "stream": update in-place
  - Last entry is "assistant": append new entry (new stream segment after tool call)
- On `StreamActive: false` or `Role: "assistant"` final message: clear `StreamActive`, show normal styling

**Visual:**
- Streaming: green diamond `◆` prefix (same as assistant), content rendered incrementally, subtle `▌` blink cursor at end
- Complete: same styling, cursor disappears
- Cancelled mid-stream: `StreamActive` set to false, whatever text arrived stays as the message

**Re-render batching:** A 50ms `tea.Tick` during active streaming coalesces rapid token arrivals into a single render per frame.

### Layer 4: Lifecycle & Cleanup

| Trigger | Action |
|---------|--------|
| Stream done | Mirror deletes the stream file immediately |
| Session cancelled mid-stream | Mirror deletes stream file, emits accumulated text as final assistant message |
| Agent killed / error | Same as cancelled — preserve what arrived |
| Daemon restart | `cleanupVulpineTempFiles()` matches `stream_*.jsonl` in workspace dirs |
| Container exit | Agent-runner `process.on('exit')` unlinks the file |

**Resource bounds per active stream:**
- 0 new goroutines (reuses existing poll loop)
- 1 tracked offset (int64) and accumulated text string in the mirror's session map
- ~100KB max memory per session
- 0 leaked files on clean exit

**Cancellation:** If the user cancels mid-stream, the accumulated text is preserved and emitted as a final `Role: "assistant"` message with whatever content arrived. The thinking animation resumes until cancellation is confirmed, then the partial text is displayed.

## Implementation Plan

### Phase 1: Agent-runner streaming (source_runtime.go)

- Enable `stream: true` on the OpenRouter fetch body in the opencode provider template
- Add SSE parsing: ReadableStream → TextDecoder → line parser → content delta extraction
- Write deltas to `process.env.STREAM_PATH || '/workspace/agent/stream.jsonl'`
- Add `process.on('exit')` cleanup for the stream file
- Cap stream file at 256KB

### Phase 2: Mirror stream polling (session_mirror.go, agent.go)

- Add `sessionStreamOffset map[string]int64` and `sessionAccumulated map[string]string` to the mirror
- `pollStreamFile()` reads from offset, accumulates, emits via `handleStreamContent()`
- `handleStreamContent()` in agent.go emits `Role: "stream"` conversation messages
- Dedup: skip final outbound DB emission when streaming completed for that session

### Phase 3: TUI rendering (conversation/model.go, app.go)

- `Entry.StreamActive` field with blink cursor rendering
- `UpdateLastAssistant()` method for in-place content replacement
- `App.Update()` handler for `Role: "stream"` messages
- 50ms re-render batching via `tea.Tick`

### Phase 4: Cleanup & verification

- Add `stream_*.jsonl` to cleanup prefixes in `cleanupVulpineTempFiles()`
- Unit tests for each layer
- Integration test verifying streaming messages arrive before final message

## Open Questions

- Should tool-call markers (`{"tool":"name"}`) show in the stream text or as a separate line in the conversation? Decision: shown inline as e.g. `\n⚡ bash\n` to give context without creating a new entry.
- Should we write the stream file using a buffered writer with periodic flush (every 100ms) instead of fsync-per-token? Decision: fsync-per-token is fine at LLM token rates (~10-30/s), the workspace volume is a local bind mount.
