# Live Agent Streaming Activity Implementation Plan

## Spec

`docs/openpowers/specs/2026-06-03-live-agent-streaming-activity.md`

## Goal

Extend the existing NanoClaw stream-file path so the TUI shows live assistant text and concise activity updates while an agent is thinking, using tools, or interacting with websites.

## Constraints

- Do not expose chain-of-thought or hidden reasoning.
- Do not persist transient `stream` or `activity` events into normal vault history.
- Keep final outbound DB rows authoritative.
- Extend existing stream/mirror/TUI code; do not introduce a parallel streaming transport.
- Preserve the recent stale-row and stream-completion fixes.

## Existing Code To Extend

- `internal/nanoclaw/source_runtime.go`: generated `opencode` TypeScript provider, SSE parsing, tool execution, stream JSONL writes.
- `internal/nanoclaw/session_mirror.go`: stream JSONL polling and outbound DB mirroring.
- `internal/nanoclaw/agent.go`: `ConversationMsg` emission and session-log parsing.
- `internal/tui/shared/messages.go`: cross-app conversation message shape.
- `internal/tui/app.go`: message persistence and selected conversation updates.
- `internal/tui/conversation/model.go`: in-place stream rendering.
- `internal/vault/agent.go`: history filtering for transient roles.

## Plan

- [ ] Task 1: Define activity event types in Go and TypeScript-adjacent comments.
  - Add a minimal activity payload shape: phase, text, tool, target, status.
  - Keep it stringly-compatible with JSONL to avoid a broad generated TypeScript type system change.
  - Decide redaction helpers before any activity text reaches the stream file.

- [ ] Task 2: Extend generated provider stream JSONL writes.
  - In `defaultOpenCodeProviderSource`, add `streamActivity(...)` next to the existing `streamWriteSync(...)` helper.
  - Emit `thinking`/`requesting_model` activity before the provider request starts.
  - Emit `tool_start` before `bash` and `web` tool executions.
  - Emit `tool_done` or `tool_error` after tool execution.
  - For `web`, include sanitized URL hostname/path summary when available.
  - For `bash`, include a sanitized short command summary or a generic `Running command...` if redaction is uncertain.

- [ ] Task 3: Parse activity JSONL in the session mirror.
  - Extend `pollStreamFile` to recognize `{"activity": {...}}` events.
  - Track latest activity per session separately from accumulated text.
  - Emit activity through the agent without marking the turn completed.
  - Keep unknown JSONL keys ignored.
  - Preserve current behavior where only fresh outbound rows complete a turn.

- [ ] Task 4: Add activity support to agent conversation messages.
  - Add `Role: "activity"` support to `ConversationMsg` handling.
  - Keep activity messages UI-only and transient.
  - Avoid emitting activity from detailed transcript/tool result parsing unless it is explicitly part of the new live stream path.

- [ ] Task 5: Render activity in the conversation model.
  - Add a transient activity field to the conversation model rather than appending normal history entries.
  - Display activity near the active assistant stream entry.
  - Clear activity on final assistant/system completion.
  - Keep active draft input visible while activity updates arrive.

- [ ] Task 6: Update TUI app message handling.
  - Do not persist `activity` messages to vault.
  - Do not clear `thinking` on `activity` messages.
  - Route `activity` messages to the selected conversation display if they match the selected agent.
  - For background agents, optionally mark unread/progress without stealing focus only if existing unread behavior supports it cleanly.

- [ ] Task 7: Ensure vault history excludes transient roles.
  - Confirm `GetMessages` and `GetRecentMessages` exclude both `stream` and `activity`.
  - Add or update tests to protect this behavior.

- [ ] Task 8: Live-source regeneration and compatibility.
  - Ensure `patchNanoClawSourceRuntime` overwrites the generated provider cleanly.
  - If needed for local validation, apply the generated provider update to the mounted live source under `~/.vulpineos/nanoclaw/container/agent-runner/src/providers/opencode.ts` after code generation is verified.
  - Typecheck the mounted agent-runner with the NanoClaw image if host `bun` is unavailable.

- [ ] Task 9: Tests.
  - `internal/nanoclaw/source_runtime_test.go`: generated provider includes activity JSONL helpers/events and redaction hooks.
  - `internal/nanoclaw/session_mirror_test.go`: activity events emit `Role: "activity"`, do not complete turns, and do not interfere with text accumulation.
  - `internal/nanoclaw/agent_trace_test.go` or agent tests: activity messages emit safely.
  - `internal/tui/conversation/model_test.go`: activity renders transiently and clears on final assistant message.
  - `internal/tui/app_test.go`: activity does not persist or unlock the turn.
  - `internal/vault/agent_test.go`: `activity` is hidden from history reads.

- [ ] Task 10: Verification.
  - Run `go test -count=1 ./internal/nanoclaw ./internal/tui ./internal/vault`.
  - Run `go test -count=1 ./cmd/vulpineos ./internal/...`.
  - Run `go build -o vulpineos ./cmd/vulpineos`.
  - Run NanoClaw agent-runner TypeScript typecheck via image if provider template or live mounted source changes.
  - Manually verify with a prompt that causes web/tool activity: the TUI shows activity before final response and final output matches the latest prompt.

## Risks

- Activity updates could become noisy if every low-level event is displayed. Keep them high-level and replace/update rather than append endlessly.
- Tool arguments can contain secrets. Redaction must happen before writing activity JSONL.
- Streaming file activity and final outbound rows can race. Preserve final-outbound authority and current stale-row protections.
- TypeScript source lives inside a Go raw string; small escaping mistakes can compile Go but fail at runtime/typecheck.

## Rollback Plan

- Disable activity emission while keeping existing text streaming if activity display regresses.
- Since final outbound DB rows remain authoritative, disabling activity should not affect final response delivery.
