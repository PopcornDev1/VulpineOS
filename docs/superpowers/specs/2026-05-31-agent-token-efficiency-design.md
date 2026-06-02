# Token Efficiency Improvements for NanoClaw Agent Provider

## Problem

The NanoClaw agent provider sends excess tokens to OpenRouter in three areas:

1. **Web tool snapshot dumps full-page DOM** — The `web` tool runs `agent-browser snapshot -i` which returns ALL interactive elements across the entire page, including off-screen ads, navigation, and footer links. For a DuckDuckGo search result page, this is ~100+ nodes when only ~20-30 are visible in the viewport.

2. **Messages array grows unboundedly within a single turn** — After each tool call, the entire messages array (including all prior tool results at full size) is re-sent to OpenRouter on the next iteration. Five web calls returning 30KB each means 150KB re-sent to the LLM on the 5th iteration.

3. **No snapshot profile usage** — The Juggler `Page.getOptimizedDOM` supports compact/expanded/full profiles and viewport-only mode, but the `web` tool never requests them.

## Design

### A. Web Tool Snapshot Trimming

**File:** `internal/nanoclaw/source_runtime.go`

#### Tool Definition Changes
Add three optional parameters to the `web` tool's function definition:
- `viewportOnly` (boolean, default true): Only return elements visible in the current viewport
- `profile` (string enum: compact/expanded/full, default compact): Snapshot detail profile
- `maxNodes` (number, optional): Override max nodes returned

#### Implementation Changes
The snapshot command is constructed with flags:
```
agent-browser snapshot -i [--viewport-only] [--profile compact] [--max-nodes 50]
```

- Default command uses `--viewport-only` and `--profile compact`
- If agent-browser doesn't support a flag, catch exec error and retry without that flag
- The compact profile (180 nodes / 90 chars per name) and viewport-only mode are both already implemented in Juggler's `Page.getOptimizedDOM` (PageAgent.js:903-1237)

#### Tool Description
Updated to tell the agent:
- Default behavior: returns only viewport-visible elements with compact detail
- Overrides available: pass `viewportOnly:false` or `profile:"full"` for full-page analysis
- If a target is missing, scroll and call again, or use override

### B. Message Window Management

**File:** `internal/nanoclaw/source_runtime.go`

After executing all tool calls in a turn (after the tool execution `for` loop), compress old tool result messages:

1. Keep the last 2 tool results at full size (LLM may reference the most recent results)
2. Truncate all older tool result messages to a summary format:
   - Successful web call: `[web] <url> — returned N bytes (compact profile, N nodes)`
   - Successful bash call: `[bash] <command> — returned N bytes`
   - Failed call: keep full error message
3. Preserve the assistant tool_call messages (they're small — just function names and args)

This reduces the re-sent payload on subsequent LLM iterations within the same turn by ~80%.

### Token Impact Estimates

| Scenario | Before | After | Savings |
|----------|--------|-------|---------|
| Single web fetch (DDG search) | ~1500 tokens | ~300 tokens | 80% |
| Turn with 5 web calls, 3 iterations each | ~450K tokens | ~90K tokens | 80% |
| Long turn with mixed bash+web | ~600K tokens | ~120K tokens | 80% |

### Risk Mitigation

- **Viewport-only misses off-screen content:** Agent is told in tool description and identity instructions to scroll + resnapshot for more content. The override `viewportOnly:false` is available for full-page analysis.
- **Compact profile truncates long text:** 90 chars per node name is sufficient for search results, links, and buttons. The agent can request `profile:"expanded"` or `profile:"full"` for verbose pages.
- **Tool result compression loses detail:** The LLM already processed the full result. It only needs to know the tool succeeded and a brief summary to reason about next steps.
