# Agent Token Efficiency Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce tokens sent to OpenRouter by the NanoClaw agent provider by trimming web tool DOM snapshots and compressing old tool results within a turn.

**Architecture:** Two self-contained changes to the embedded JS provider in `internal/nanoclaw/source_runtime.go`. The web tool's snapshot command gets viewport-only mode and compact profile flags. The message loop compresses older tool results after all tool calls execute, keeping only the most recent 2 at full size.

**Tech Stack:** Go (embedded JavaScript string), agent-browser CLI, Juggler `Page.getOptimizedDOM`

---

### Task 1: Add snapshot options to web tool parameters

**Files:**
- Modify: `internal/nanoclaw/source_runtime.go:150-164`

- [ ] **1.1 Extend WEB_TOOL with optional parameters**

Change the tool definition to include `viewportOnly`, `profile`, and `maxNodes`:

```js
const WEB_TOOL = {
  type: 'function' as const,
  function: {
    name: 'web',
    description:
      'Fetch a web page using the Camoufox browser via CDP. Uses agent-browser internally. This is the ONLY way to access the web \u2014 wget and curl are blocked by the network proxy. By default returns only viewport-visible elements (compact profile). Pass viewportOnly:false or profile:"full" for full-page analysis.',
    parameters: {
      type: 'object',
      properties: {
        url: { type: 'string', description: 'The URL to fetch' },
        viewportOnly: { type: 'boolean', description: 'Only return elements visible in the current viewport (default: true). Disable for full-page analysis.' },
        profile: { type: 'string', enum: ['compact', 'expanded', 'full'], description: 'Snapshot detail profile. compact: 180 nodes/90 chars per node (default). expanded: 360/160. full: 800/240.' },
        maxNodes: { type: 'number', description: 'Maximum number of DOM nodes to return (overrides profile default). Lower values save tokens.' },
      },
      required: ['url'],
    },
  },
};
```

- [ ] **1.2 Verify the edit**

Run: `grep -n "viewportOnly" internal/nanoclaw/source_runtime.go`
Expected: Shows the new parameter in the WEB_TOOL definition. If not present, the edit hasn't been applied yet.

---

### Task 2: Build snapshot command with optional flags

**Files:**
- Modify: `internal/nanoclaw/source_runtime.go:396-414`

- [ ] **2.1 Add flag construction and fallback logic**

Replace the hardcoded `snapshot -i` command with a constructed command that includes viewport-only and profile flags. If the command fails (CLI doesn't support flags), fallback to `snapshot -i`:

```js
} else if (tc.function.name === 'web') {
  const url = String(args.url ?? '');
  const viewportOnly = args.viewportOnly !== false; // default true
  const profile = String(args.profile || 'compact');
  try {
    const cdpUrl = process.env.AGENT_BROWSER_CDP || process.env.AGENT_BROWSER_CDP_URL;
    const flags = [];
    if (viewportOnly) flags.push('--viewport-only');
    if (profile) flags.push('--profile ' + profile);
    if (typeof args.maxNodes === 'number' && args.maxNodes > 0) flags.push('--max-nodes ' + args.maxNodes);
    const flagStr = flags.length > 0 ? ' ' + flags.join(' ') : '';
    const cmd = 'agent-browser connect ' + cdpUrl + ' && agent-browser open ' + JSON.stringify(url) + ' && agent-browser wait --load networkidle && agent-browser snapshot -i' + flagStr;
    result = execSync(cmd, {
      cwd: '/workspace/agent',
      timeout: 60000,
      encoding: 'utf-8',
      maxBuffer: 50 * 1024 * 1024,
      signal: controller.signal,
    });
  } catch (_err) {
    // Retry without flags if CLI rejected them
    try {
      const cdpUrl = process.env.AGENT_BROWSER_CDP || process.env.AGENT_BROWSER_CDP_URL;
      const cmd = 'agent-browser connect ' + cdpUrl + ' && agent-browser open ' + JSON.stringify(url) + ' && agent-browser wait --load networkidle && agent-browser snapshot -i';
      result = execSync(cmd, {
        cwd: '/workspace/agent',
        timeout: 60000,
        encoding: 'utf-8',
        maxBuffer: 50 * 1024 * 1024,
        signal: controller.signal,
      });
    } catch (_retryErr) {
      const errMsg = _retryErr instanceof Error ? _retryErr.message : String(_retryErr);
      result = 'agent-browser failed: ' + errMsg;
    }
  }
  if (result.length > 50000) {
    result = result.slice(0, 50000) + '\n... [truncated ' + (result.length - 50000) + ' more bytes]';
  }
```

- [ ] **2.2 Verify the edit**

Run: `grep -n "viewportOnly" internal/nanoclaw/source_runtime.go`
Expected: Two matches — one in WEB_TOOL definition, one in the implementation.

---

### Task 3: Add message window management

**Files:**
- Modify: `internal/nanoclaw/source_runtime.go:422-426`

- [ ] **3.1 Add tool result compression after execution loop**

After the tool result push (line 422) and before the `continue` (line 426), add compression for older tool results. This compresses tool results older than the last 2 to reduce re-sent tokens:

```js
          messages.push({ role: 'tool', tool_call_id: tc.id, content: result });
          yield { type: 'activity' };
        }

        // Compress older tool results — keep last 2 full, truncate older ones to save tokens
        let recentToolResults = 0;
        for (let i = messages.length - 1; i >= 0; i--) {
          if (messages[i].role === 'tool') {
            recentToolResults++;
            if (recentToolResults > 2 && messages[i].content.length > 2000) {
              const lines = messages[i].content.split('\n');
              const preview = lines.slice(0, 4).join('\n');
              messages[i].content = preview + `\n... [${messages[i].content.length - preview.length} more bytes — full content already processed above]`;
            }
          }
        }

        continue;
```

- [ ] **3.2 Verify the edit**

Run: `grep -n "Compress older" internal/nanoclaw/source_runtime.go`
Expected: Shows the compression comment and logic at the expected line.

---

### Task 4: Verify compilation and consistency

- [ ] **4.1 Check Go syntax**

Run: `go build ./internal/nanoclaw/`
Expected: No errors (the JS is embedded as a string constant, Go doesn't parse it).

- [ ] **4.2 Check that string escaping is correct**

Run: `grep -c '` + "`" + `' internal/nanoclaw/source_runtime.go`
Expected: An even number of backtick escapes (they're paired in the template literal). If odd, string boundaries are broken.

- [ ] **4.3 Run relevant tests**

Run: `go test ./internal/nanoclaw/... -v -count=1 -timeout 120s 2>&1 | head -100`
Expected: Tests pass.

- [ ] **4.4 Commit**

Run:
```bash
git add internal/nanoclaw/source_runtime.go docs/superpowers/specs/2026-05-31-agent-token-efficiency-design.md docs/superpowers/plans/2026-05-31-agent-token-efficiency.md
git commit -m "feat: reduce agent provider token usage

- Add viewportOnly, profile, maxNodes params to web tool
- Default snapshot to viewport-only + compact profile
- Compress older tool results within a turn to avoid re-sending full DOM
"
```
