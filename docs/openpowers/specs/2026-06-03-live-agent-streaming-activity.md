# Live Agent Streaming Activity

## Goal

Show useful live progress in the VulpineOS TUI while a NanoClaw-backed agent is working, not only after the final outbound message lands.

The UI should stream assistant-visible text as it is generated and show concise activity updates while the agent is thinking, calling tools, or interacting with websites.

## Background

There is an older streaming design under `docs/superpowers/specs/2026-05-31-streaming-agent-output-design.md`. The current code already implements part of that design:

- `internal/nanoclaw/source_runtime.go` generates an `opencode` provider that requests streamed chat completions and writes content deltas to `/workspace/stream.jsonl`.
- `internal/nanoclaw/session_mirror.go` polls `stream.jsonl` and emits `Role: "stream"` conversation updates.
- `internal/tui/app.go` and `internal/tui/conversation/model.go` render stream updates in-place.
- Recent fixes made stream chunks UI-only, prevented stream activity from completing a turn, and prevented stale outbound rows from being emitted into the current chat.

The missing piece is broader live activity: the operator should see progress while the model is not emitting final text, especially during tool calls and browser work.

## Scope

In scope:

- Stream assistant-visible text live into the selected conversation.
- Show concise activity lines for model/tool/browser progress.
- Preserve final outbound DB messages as the authoritative assistant response.
- Keep stream chunks and transient activity out of persisted conversation history unless explicitly converted into final assistant/system messages.
- Keep existing trace mode as the detailed diagnostic view.
- Add tests for transport parsing, mirror behavior, TUI rendering, and vault persistence boundaries.

Out of scope:

- Exposing chain-of-thought or hidden reasoning text.
- Persisting every token/activity update as chat history.
- Replacing NanoClaw outbound DB delivery.
- Reworking provider architecture beyond the generated `opencode` provider path.
- Browser visual review or screenshot review.

## User-Facing Behavior

When an agent is working, the selected conversation should update progressively:

- Assistant text appears as it streams.
- If no text is streaming yet, show an activity status such as `Thinking...`.
- When the agent calls a tool, show a concise status such as `Running bash...`, `Opening website...`, `Reading page...`, or `Waiting for network idle...`.
- When a browser/web request targets a URL, show the hostname or sanitized URL when safe.
- When a tool completes, replace or update the activity line with a short completion status.
- The final outbound assistant message replaces the stream entry and clears transient activity.

Activity should be operator-readable, not raw protocol logs.

## Privacy And Safety

- Do not stream chain-of-thought. "Thinking" means a status label, not model reasoning.
- Redact or avoid secrets in command lines, URLs, headers, environment variables, and tool results.
- Do not display raw hidden prompts or internal system instructions.
- Keep detailed tool output behind existing trace/session log mechanisms.

## Data Model

Use separate transient event semantics rather than overloading final chat history:

- `stream`: assistant-visible accumulated text, UI-only while active.
- `activity`: transient operator status, UI-only by default.
- `assistant`: final authoritative assistant response from outbound DB.
- `system`: persisted or visible non-transient warnings/errors when appropriate.

`stream` and `activity` should not be returned by normal vault history reads used for future prompts.

## Transport Format

Extend the current sidecar JSONL stream file format beyond text chunks:

```jsonl
{"t":"Hello"}
{"activity":{"phase":"thinking","text":"Thinking..."}}
{"activity":{"phase":"tool_start","tool":"web","text":"Opening example.com"}}
{"activity":{"phase":"tool_done","tool":"web","text":"Page loaded"}}
{"done":"Hello world"}
```

Guidelines:

- `t` remains the text delta path.
- `done` remains the authoritative accumulated stream text marker.
- `activity` carries sanitized, concise progress events.
- Unknown event keys are ignored for forward compatibility.
- The file remains bounded and append-only during a turn.

## Rendering Requirements

- Streamed assistant text updates a single in-progress assistant bubble/entry.
- Activity updates should not overwrite assistant text.
- If text is already streaming, activity may appear as a compact status below or near the streaming entry.
- If no text has arrived, activity can occupy the assistant placeholder area.
- Final assistant output removes transient activity indicators.
- Typing into the chat box remains possible while the agent is working; Enter remains locked until the current turn completes unless explicitly changed later.

## Existing-Code Findings

Relevant existing implementation points:

- `internal/nanoclaw/source_runtime.go`: generated TypeScript provider, SSE parsing, tool execution, stream file writes.
- `internal/nanoclaw/session_mirror.go`: stream file polling, outbound DB mirroring, stale-row protection.
- `internal/nanoclaw/agent.go`: `ConversationMsg`, session log parsing, stream conversation emission.
- `internal/tui/shared/messages.go`: TUI message type crossing the app boundary.
- `internal/tui/app.go`: `ConversationEntryMsg` persistence and selected-conversation updates.
- `internal/tui/conversation/model.go`: in-place assistant stream rendering.
- `internal/vault/agent.go`: persisted history reads now hide `stream` rows.
- `internal/nanoclaw/session_mirror_test.go`, `internal/tui/app_test.go`, `internal/vault/agent_test.go`: regression coverage for streaming boundaries.

No new parallel streaming system should be introduced. The implementation should extend the existing stream-file and mirror path.

## Success Criteria

- A long text response visibly appears before the final outbound DB row is delivered.
- During website/tool work, the TUI shows concise progress activity even when no assistant text is currently streaming.
- Activity updates do not pollute persisted prompt history.
- Final assistant messages remain authoritative and replace/finish stream UI cleanly.
- Stale rows from previous turns do not appear in the current chat.
- Tests cover streaming text, activity events, finalization, stale-event filtering, and vault history filtering.

## Open Questions

- Exact visual treatment for activity lines: inline under assistant stream, status pill, or trace-like compact line.
- Whether activity should be visible only for the selected agent or also summarized as unread/progress state in the agent list.
- Whether activity should include sanitized command snippets for `bash`, or only generic labels.
