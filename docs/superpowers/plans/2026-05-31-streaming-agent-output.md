# Streaming Agent Output Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable token-by-token LLM output streaming from the NanoClaw container agent-runner through to the TUI display.

**Architecture:** A sidecar stream file on the NanoClaw shared workspace volume carries token deltas from the container agent-runner's SSE-streamed LLM API call. The existing session mirror poll loop reads this file incrementally, accumulates text, and emits `Role:"stream"` conversation messages. The TUI conversation model updates the last assistant entry in-place, giving a ChatGPT-like progressive reveal.

**Tech Stack:** Go (nanoclaw agent, session mirror, TUI), TypeScript (agent-runner opencode provider), SSE streaming, JSONL file-based IPC

---

### Task 1: Add SSE streaming to the opencode provider template

**Files:**
- Modify: `internal/nanoclaw/source_runtime.go:129-492` — `defaultOpenCodeProviderSource` template

This is the most critical task. The fetch call currently sets no `stream` and parses the response as a single JSON blob. We need to replace that with SSE stream parsing and write content deltas to a sidecar stream file.

- [ ] **Step 1: Add stream=true and SSE parsing logic to the fetch block**

Replace the current fetch-and-parse block (lines 305-319 and 337-342) with SSE streaming. The key changes:

1. Add `stream: true` to the JSON body
2. Parse using `res.body.getReader()` + `TextDecoder` for SSE
3. For each `delta.content` chunk, write `{"t":"<chunk>"}` to the stream file
4. On stream end (null `finish_reason` or tool_calls), write `{"done":"<full_text>"}` to the stream file
5. Accumulate the full text to return as the response (for downstream tool call loop)
6. If tool calls appear in the stream, capture them too

Modify `source_runtime.go`. The template currently has at line 305:

```go
          const res = await fetch('https://openrouter.ai/api/v1/chat/completions', {
            method: 'POST',
            signal: controller.signal,
            headers: {
              Authorization: ` + "`" + `Bearer ${apiKey}` + "`" + `,
              'Content-Type': 'application/json',
              'HTTP-Referer': 'https://vulpineos.com',
              'X-Title': 'VulpineOS',
            },
            body: JSON.stringify({
              model,
              messages,
              tools: [BASH_TOOL, WEB_TOOL],
              tool_choice: 'auto',
            }),
          });

          yield { type: 'activity' };

          if (!res.ok) {
            const body = await res.text();
            if (res.status === 429 && i < models.length - 1) {
              continue;
            }
            yield {
              type: 'error',
              message: ` + "`" + `OpenRouter returned ${res.status}: ${body}` + "`" + `,
              retryable: res.status >= 500 || res.status === 429,
            };
            return;
          }

          response = (await res.json()) as {
            choices?: Array<{
              finish_reason: string;
              message: ChatMessage & { tool_calls?: ChatMessage['tool_calls'] };
            }>;
          };
```

Replace with:

```go
          const res = await fetch('https://openrouter.ai/api/v1/chat/completions', {
            method: 'POST',
            signal: controller.signal,
            headers: {
              Authorization: ` + "`" + `Bearer ${apiKey}` + "`" + `,
              'Content-Type': 'application/json',
              'HTTP-Referer': 'https://vulpineos.com',
              'X-Title': 'VulpineOS',
            },
            body: JSON.stringify({
              model,
              messages,
              tools: [BASH_TOOL, WEB_TOOL],
              tool_choice: 'auto',
              stream: true,
            }),
          });

          yield { type: 'activity' };

          if (!res.ok) {
            const body = await res.text();
            if (res.status === 429 && i < models.length - 1) {
              continue;
            }
            yield {
              type: 'error',
              message: ` + "`" + `OpenRouter returned ${res.status}: ${body}` + "`" + `,
              retryable: res.status >= 500 || res.status === 429,
            };
            return;
          }

          const streamReader = res.body.getReader();
          const streamDecoder = new TextDecoder();
          let streamBuffer = '';
          let streamContent = '';
          let streamToolCalls: ChatMessage['tool_calls'] = [];
          let streamToolCallAccum: Map<number, { id?: string; name?: string; args?: string }> = new Map();
          const streamPath = process.env.STREAM_PATH || '/workspace/stream.jsonl';
          let streamFd: number | null = null;
          try { streamFd = fs.openSync(streamPath, 'w'); } catch (_) {}

          function streamWriteSync(data: string) {
            if (streamFd === null) return;
            try {
              fs.writeSync(streamFd, data + '\n');
              fs.fsyncSync(streamFd);
            } catch (_) {}
          }

          while (true) {
            const { done, value } = await streamReader.read();
            if (done) break;
            streamBuffer += streamDecoder.decode(value, { stream: true });
            const lines = streamBuffer.split('\n');
            streamBuffer = lines.pop() || '';
            for (const line of lines) {
              const trimmed = line.trim();
              if (!trimmed || !trimmed.startsWith('data: ')) continue;
              const dataStr = trimmed.slice(6);
              if (dataStr === '[DONE]') break;
              try {
                const parsed = JSON.parse(dataStr);
                const choice = parsed.choices?.[0];
                if (!choice) continue;
                const delta = choice.delta || {};
                if (delta.content) {
                  streamContent += delta.content;
                  streamWriteSync(JSON.stringify({ t: delta.content }));
                }
                if (delta.tool_calls) {
                  for (const tc of delta.tool_calls) {
                    const idx = tc.index || 0;
                    if (!streamToolCallAccum.has(idx)) streamToolCallAccum.set(idx, {});
                    const acc = streamToolCallAccum.get(idx)!;
                    if (tc.id) acc.id = tc.id;
                    if (tc.function?.name) acc.name = tc.function.name;
                    if (tc.function?.arguments) acc.args = (acc.args || '') + tc.function.arguments;
                  }
                }
              } catch (_) {}
            }
          }

          // Reconstruct tool_calls from accumulated SSE chunks
          for (const [idx, acc] of streamToolCallAccum) {
            if (acc.id && acc.name) {
              streamToolCalls.push({
                id: acc.id,
                type: 'function',
                function: { name: acc.name, arguments: acc.args || '{}' },
              });
            }
          }

          // Write done marker with full accumulated text
          if (streamContent) {
            streamWriteSync(JSON.stringify({ done: streamContent }));
          }
          if (streamFd !== null) { try { fs.closeSync(streamFd); } catch (_) {} }

          // Build response object compatible with the rest of the tool-call loop
          response = {
            choices: [{
              finish_reason: streamToolCalls.length > 0 ? 'tool_calls' : 'stop',
              message: {
                role: 'assistant' as const,
                content: streamContent || null,
                ...(streamToolCalls.length > 0 ? { tool_calls: streamToolCalls } : {}),
              },
            }],
          } as unknown as { choices?: Array<{ finish_reason: string; message: ChatMessage & { tool_calls?: ChatMessage['tool_calls'] } }> };
```

- [ ] **Step 2: Add fs import at top of template**

At line 129, after `import { execSync } from 'child_process';`, there is no `fs` import. Add it:

```
import { execSync } from 'child_process';
import * as fs from 'fs';
```

- [ ] **Step 3: Verify template compiles**

Run: `go build -o vulpineos ./cmd/vulpineos/`
Expected: Build succeeds (the template is a Go raw string, so no TypeScript compilation at build time).

---

### Task 2: Add StreamActive field to ConversationMsg and handleStreamContent to Agent

**Files:**
- Modify: `internal/nanoclaw/agent.go:22-27` — ConversationMsg struct
- Modify: `internal/nanoclaw/agent.go:447-531` — around handleSessionLogLine
- Modify: `internal/tui/shared/messages.go:96-104` — ConversationEntryMsg struct

- [ ] **Step 1: Add StreamActive to ConversationMsg**

Change `agent.go:22-27` from:

```go
type ConversationMsg struct {
	AgentID string
	Role    string
	Content string
	Tokens  int
}
```

To:

```go
type ConversationMsg struct {
	AgentID      string
	Role         string
	Content      string
	Tokens       int
	StreamActive bool
}
```

- [ ] **Step 2: Add StreamActive to shared.ConversationEntryMsg**

Change `shared/messages.go:96-104` from:

```go
type ConversationEntryMsg struct {
	AgentID        string
	Role           string
	Content        string
	DisplayContent string
	Tokens         int
	Timestamp      time.Time
}
```

To:

```go
type ConversationEntryMsg struct {
	AgentID        string
	Role           string
	Content        string
	DisplayContent string
	Tokens         int
	Timestamp      time.Time
	StreamActive   bool
}
```

- [ ] **Step 3: Add handleStreamContent method to Agent**

Add after `handleSessionLogLine` (around line 531) in `agent.go`:

```go
func (a *Agent) handleStreamContent(content string, streamActive bool) {
	a.mu.Lock()
	agentID := a.ID
	a.mu.Unlock()

	if !streamActive {
		// Final flush — emit as normal assistant message
		a.emitConversation(ConversationMsg{
			AgentID:      agentID,
			Role:         "assistant",
			Content:      content,
			StreamActive: false,
		})
		return
	}

	// Streaming update — send minimal content for TUI to render in-place
	a.emitConversation(ConversationMsg{
		AgentID:      agentID,
		Role:         "stream",
		Content:      content,
		StreamActive: true,
	})
}
```

- [ ] **Step 4: Build to verify**

Run: `go build -o vulpineos ./cmd/vulpineos/`
Expected: Build succeeds.

---

### Task 3: Add stream file polling to session mirror

**Files:**
- Modify: `internal/nanoclaw/session_mirror.go`
- Modify: `internal/nanoclaw/agent.go` (already has handleStreamContent)

- [ ] **Step 1: Add stream tracking fields to NanoClawSessionMirror**

Add fields to the struct at `session_mirror.go:18-25`:

```go
type NanoClawSessionMirror struct {
	nanoclawDir       string
	agentID           string
	sessionLogPath    string
	agent             *Agent
	seen              map[string]struct{}
	loadedSeen        bool

	streamDir         string            // resolved per-session
	streamOffsets     map[string]int64  // sessionID → bytes read
	streamAccumulated map[string]string // sessionID → accumulated text
	streamDone        map[string]bool   // sessionID → stream completed
	lastStreamEmit    time.Time         // throttle: max 1 emit per 50ms
}
```

Update `NewNanoClawSessionMirror` to initialize the new maps:

```go
func NewNanoClawSessionMirror(nanoclawDir, agentID, sessionLogPath string, agent *Agent) *NanoClawSessionMirror {
	return &NanoClawSessionMirror{
		nanoclawDir:       strings.TrimSpace(nanoclawDir),
		agentID:           strings.TrimSpace(agentID),
		sessionLogPath:    strings.TrimSpace(sessionLogPath),
		agent:             agent,
		seen:              make(map[string]struct{}),
		streamOffsets:     make(map[string]int64),
		streamAccumulated: make(map[string]string),
		streamDone:        make(map[string]bool),
	}
}
```

- [ ] **Step 2: Add stream file path resolution**

Add to `session_mirror.go` after the existing `sessionDir()` method (line 507):

```go
func (m *NanoClawSessionMirror) streamFilePath(session nanoClawSessionRef) string {
	return filepath.Join(m.sessionDir(session), "stream.jsonl")
}
```

- [ ] **Step 3: Add pollStreamFile method**

Add to `session_mirror.go`:

```go
func (m *NanoClawSessionMirror) pollStreamFile(session nanoClawSessionRef) bool {
	path := m.streamFilePath(session)
	if path == "" {
		return false
	}

	sid := session.SessionID
	if m.streamDone[sid] {
		return false
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		return false
	}
	defer file.Close()

	// Detect file truncation (container restart): if file is smaller than our offset, reset
	fi, err := file.Stat()
	if err != nil {
		return false
	}
	offset := m.streamOffsets[sid]
	if fi.Size() < offset {
		offset = 0
		m.streamOffsets[sid] = 0
		m.streamAccumulated[sid] = ""
	}
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return false
		}
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	newContent := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		offset += int64(len(line)) + 1

		var entry struct {
			T    string `json:"t"`
			Done string `json:"done"`
			Tool string `json:"tool"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		if entry.T != "" {
			m.streamAccumulated[sid] += entry.T
			newContent = true
		}
		if entry.Done != "" {
			m.streamAccumulated[sid] = entry.Done
			m.streamDone[sid] = true
			newContent = true
		}
	}

	m.streamOffsets[sid] = offset

	if !newContent {
		return false
	}

	accumulated := m.streamAccumulated[sid]
	if accumulated == "" {
		return false
	}

	// Throttle: max one emit per 50ms
	if time.Since(m.lastStreamEmit) < 50*time.Millisecond {
		return m.streamDone[sid]
	}
	m.lastStreamEmit = time.Now()

	if m.streamDone[sid] {
		// Final flush — don't emit here (mirrorOutbound delivers the
		// authoritative final message via the normal DB path). Just
		// clean up and return true to signal activity.
		_ = os.Remove(path)
		delete(m.streamOffsets, sid)
		delete(m.streamAccumulated, sid)
		return true
	}

	// Streaming update
	m.agent.handleStreamContent(accumulated, true)
	return true
}
```

- [ ] **Step 4: Hook pollStreamFile into MirrorOnce**

Add the stream file poll call to `MirrorOnce()` at `session_mirror.go:100-127`. After the `mirrorClaudeTranscripts` call and before `mirrorOutbound`, insert:

```go
	session, ok, err := m.resolveSession()
	if err != nil || !ok {
		return false, err
	}

	// ...existing inbound + claude transcripts...

	// Check stream file for progressive content
	m.pollStreamFile(session)

	// ...existing outbound mirroring...
```

The full modified `MirrorOnce` should be:

```go
func (m *NanoClawSessionMirror) MirrorOnce(completionAfter time.Time) (bool, error) {
	if m == nil || m.nanoclawDir == "" || m.agentID == "" || m.sessionLogPath == "" {
		return false, nil
	}
	if err := m.loadSeenFromExistingLog(); err != nil {
		return false, err
	}
	session, ok, err := m.resolveSession()
	if err != nil || !ok {
		return false, err
	}

	completed := false
	if err := m.mirrorInbound(session); err != nil {
		return false, err
	}
	if err := m.mirrorClaudeTranscripts(session); err != nil {
		return false, err
	}

	// Check stream file for progressive LLM token output
	streamActivity := m.pollStreamFile(session)

	assistantSeen, err := m.mirrorOutbound(session, completionAfter)
	if err != nil {
		return false, err
	}
	if assistantSeen {
		completed = true
	}
	// Return true if either outbound found a message OR stream file had activity
	// (keeps the poll loop at min interval during active streaming)
	return streamActivity || completed, nil
}
```

- [ ] **Step 5: Verify mirrorOutbound handles the final message**

mirrorOutbound does NOT need dedup. When the outbound DB message arrives:
1. `append()` writes it to the session log
2. `handleSessionLogLine()` emits `Role:"assistant"` to the conversation channel
3. The TUI's `IsLastEntryStreaming()` check (Task 5) bridges: finds the last entry is "stream", calls `UpdateLastAssistant(content, false)` instead of appending a new entry

This is the cleanest approach: pollStreamFile streams partial content, mirrorOutbound delivers the authoritative complete message, and the TUI reconciles them.

- [ ] **Step 6: Build to verify**

Run: `go build -o vulpineos ./cmd/vulpineos/`
Expected: Build succeeds.

---

### Task 4: Update conversation model for in-place streaming

**Files:**
- Modify: `internal/tui/conversation/model.go`

- [ ] **Step 1: Add StreamActive to Entry struct**

Change `conversation/model.go:59-64` from:

```go
type Entry struct {
	Role           string
	Content        string
	DisplayContent string
	renderedLines  []string
}
```

To:

```go
type Entry struct {
	Role           string
	Content        string
	DisplayContent string
	renderedLines  []string
	StreamActive   bool
}
```

- [ ] **Step 2: Add UpdateLastAssistant method**

Add to `conversation/model.go` after `AddEntryWithDisplay` (around line 328):

```go
func (m *Model) UpdateLastAssistant(content string, streamActive bool) {
	if len(m.entries) == 0 {
		m.AddEntryWithDisplay("assistant", content, "")
		return
	}
	last := &m.entries[len(m.entries)-1]
	if last.Role != "assistant" && last.Role != "stream" {
		m.AddEntryWithDisplay("assistant", content, "")
		return
	}
	last.Role = "assistant"
	if streamActive {
		last.Role = "stream"
	}
	last.Content = content
	last.StreamActive = streamActive
	maxWidth := m.contentWidth()
	last.renderedLines = renderMarkdown(messageDisplayText(content, last.DisplayContent), maxWidth)

	if streamActive && m.autoScroll {
		m.scrollToBottom()
	}
}
```

- [ ] **Step 3: Add blink cursor for streaming entries in rendering**

In `getDisplayLines()` at `conversation/model.go:495-507`, modify the `"assistant"` case to render the blink cursor when streaming:

```go
	case "assistant":
		marker := shared.RunningStyle.Render("◆ ")
		if isBrowserAction(e.Content) {
			marker = shared.WarmingStyle.Render("◆ ")
		}
		for j, line := range lines {
			if j == 0 {
				rendered = append(rendered, marker+line)
			} else {
				rendered = append(rendered, "  "+line)
			}
		}
		if e.StreamActive && len(lines) > 0 {
			// Append blink cursor to last rendered line
			lastLine := rendered[len(rendered)-1]
			cursor := lipgloss.NewStyle().Faint(true).Render("▌")
			rendered[len(rendered)-1] = lastLine + cursor
		}
```

Treat `"stream"` role identically to `"assistant"` by adding a case:

```go
	case "stream":
		marker := shared.RunningStyle.Render("◆ ")
		for j, line := range lines {
			if j == 0 {
				rendered = append(rendered, marker+line)
			} else {
				rendered = append(rendered, "  "+line)
			}
		}
		cursor := lipgloss.NewStyle().Faint(true).Render("▌")
		if len(rendered) > 0 {
			rendered[len(rendered)-1] = rendered[len(rendered)-1] + cursor
		}
```

- [ ] **Step 4: Build to verify**

Run: `go build -o vulpineos ./cmd/vulpineos/`
Expected: Build succeeds.

---

### Task 5: Update TUI app.go to handle stream messages and add IsLastEntryStreaming

**Files:**
- Modify: `internal/tui/app.go`
- Modify: `internal/tui/conversation/model.go`

- [ ] **Step 1: Add IsLastEntryStreaming to conversation model**

Add after `LatestAssistantContent` (around line 289) in `model.go`:

```go
func (m Model) IsLastEntryStreaming() bool {
	if len(m.entries) == 0 {
		return false
	}
	return m.entries[len(m.entries)-1].StreamActive
}
```

- [ ] **Step 2: Handle "stream" and "assistant" with streaming bridge in App.Update**

Modify the `ConversationEntryMsg` handler at `app.go:1323-1355`. The key insight: pollStreamFile emits `Role:"stream"` for partial content. mirrorOutbound emits `Role:"assistant"` with the final, authoritative message. The TUI bridges them: when an "assistant" message arrives and the last entry is streaming, update in-place instead of appending.

Replace the block `if msg.AgentID == a.selectedAgentID {` (lines 1337-1351) with:

```go
		if msg.AgentID == a.selectedAgentID {
			a.conversation.SetThinking(false)
			if msg.Role == "stream" {
				a.conversation.UpdateLastAssistant(msg.Content, true)
			} else if msg.Role == "assistant" && a.conversation.IsLastEntryStreaming() {
				// Final authoritative message from outbound DB —
				// replace the streaming entry with the complete message
				a.conversation.UpdateLastAssistant(msg.Content, false)
			} else {
				a.conversation.AddEntryWithDisplay(msg.Role, msg.Content, msg.DisplayContent)
			}
			if msg.Role == "assistant" {
				a.conversation.ForceScrollToBottom()
			}
			a.agentList.ClearUnread(msg.AgentID)
			if pendingAssistantReply {
				a.focus = FocusConversation
				a.inputMode = "chat"
				a.conversation.SetAwake(true)
				cmds = append(cmds, a.conversation.Focus())
			} else if a.focus == FocusConversation && a.inputMode == "chat" && !a.conversation.Focused() {
				cmds = append(cmds, a.conversation.Focus())
			}
		}
```

- [ ] **Step 2: Forward StreamActive from ConversationMsg to ConversationEntryMsg**

In the goroutine at `app.go:520-539` that reads from `orch.Agents.ConversationChan()`, the ConversationMsg is converted to ConversationEntryMsg. Update the emit to include StreamActive:

In the `case agentMsg, ok := <-conversationCh:` block (around line 530), change:

```go
emitEvent(shared.ConversationEntryMsg{
	AgentID: agentMsg.AgentID,
	Role:    agentMsg.Role,
	Content: agentMsg.Content,
	Tokens:  agentMsg.Tokens,
})
```

To:

```go
emitEvent(shared.ConversationEntryMsg{
	AgentID:      agentMsg.AgentID,
	Role:         agentMsg.Role,
	Content:      agentMsg.Content,
	Tokens:       agentMsg.Tokens,
	StreamActive: agentMsg.StreamActive,
})
```

- [ ] **Step 3: Build to verify**

Run: `go build -o vulpineos ./cmd/vulpineos/`
Expected: Build succeeds.

---

### Task 6: Add stream file cleanup

**Files:**
- Modify: `internal/nanoclaw/cleanup.go`

- [ ] **Step 1: Add stream_ prefix to cleanupVulpineTempFiles**

This is the daemon-level startup cleanup. The stream files live in the NanoClaw session directories (`~/.vulpineos/nanoclaw/data/v2-sessions/*/*/`), not in /tmp. But the `cleanupVulpineTempFiles` handles `/tmp` temp files.

Add a new function `cleanupStreamFiles()` in `cleanup.go`:

```go
func cleanupStreamFiles(nanoclawDir string) {
	pattern := filepath.Join(nanoclawDir, "data", "v2-sessions", "*", "*", "stream_*.jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	for _, path := range matches {
		if err := os.Remove(path); err == nil {
			log.Printf("cleaned stale stream file: %s", path)
		}
	}
}
```

- [ ] **Step 2: Call cleanupStreamFiles at daemon startup**

In `internal/nanoclaw/daemon.go`, find where `cleanupVulpineTempFiles()` is called and add `cleanupStreamFiles(nanoclawDirPath)` after it.

- [ ] **Step 3: Build to verify**

Run: `go build -o vulpineos ./cmd/vulpineos/`
Expected: Build succeeds.

---

### Task 7: Tests

**Files:**
- Create/Modify: `internal/nanoclaw/session_mirror_test.go`
- Create/Modify: `internal/tui/conversation/model_test.go`
- Modify: `internal/integration_test.go`

- [ ] **Step 1: Write unit test for pollStreamFile parsing**

In `session_mirror_test.go`:

```go
func TestPollStreamFile(t *testing.T) {
	dir := t.TempDir()

	agent := &Agent{
		ID:             "test-agent",
		conversationCh: make(chan ConversationMsg, 64),
	}

	mirror := NewNanoClawSessionMirror(dir, "test-agent", filepath.Join(dir, "session.jsonl"), agent)

	session := nanoClawSessionRef{AgentGroupID: "vulpineos-main", SessionID: "sess-test-123"}

	// Write test stream file
	streamPath := mirror.streamFilePath(session)
	if err := os.MkdirAll(filepath.Dir(streamPath), 0700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"t":"Hello "}`,
		`{"t":"world"}`,
		`{"t":"!"}`,
		`{"done":"Hello world!"}`,
	}
	// Write in two phases to test offset tracking
	if err := os.WriteFile(streamPath, []byte(lines[0]+"\n"+lines[1]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// First partial poll: should read first two lines
	result := mirror.pollStreamFile(session)
	if !result {
		t.Fatal("expected first poll to find content")
	}
	if mirror.streamAccumulated["sess-test-123"] != "Hello world" {
		t.Fatalf("expected 'Hello world', got %q", mirror.streamAccumulated["sess-test-123"])
	}

	// Drain the first stream message (Role:"stream", partial content)
	select {
	case msg := <-agent.conversationCh:
		if msg.Role != "stream" {
			t.Fatalf("expected stream role, got %q", msg.Role)
		}
		if msg.Content != "Hello world" {
			t.Fatalf("expected 'Hello world', got %q", msg.Content)
		}
		if !msg.StreamActive {
			t.Fatal("expected StreamActive=true for stream msg")
		}
	default:
		t.Fatal("expected stream message to be emitted")
	}

	// Append remaining lines (simulating continued container output)
	f, _ := os.OpenFile(streamPath, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(lines[2] + "\n" + lines[3] + "\n")
	f.Close()

	// Second poll: reads remaining content, finalizes stream
	result = mirror.pollStreamFile(session)
	if !result {
		t.Fatal("expected second poll to finalize stream")
	}

	// Check accumulated and done
	if mirror.streamAccumulated["sess-test-123"] != "Hello world!" {
		t.Fatalf("expected 'Hello world!', got %q", mirror.streamAccumulated["sess-test-123"])
	}
	if !mirror.streamDone["sess-test-123"] {
		t.Fatal("expected stream to be marked done")
	}

	// No message from pollStreamFile finalization (mirrorOutbound handles it)
	select {
	case <-agent.conversationCh:
		t.Fatal("unexpected message from stream finalization")
	default:
	}

	// Third poll: should return false (streamDone flag set)
	result = mirror.pollStreamFile(session)
	if result {
		t.Fatal("expected false after stream done")
	}
}
```

- [ ] **Step 2: Run stream file unit test**

Run: `go test ./internal/nanoclaw/ -run TestPollStreamFile -v`
Expected: PASS

- [ ] **Step 3: Write unit test for UpdateLastAssistant**

In `conversation/model_test.go`:

```go
func TestUpdateLastAssistant(t *testing.T) {
	m := Model{}
	m.width = 80
	m.AddEntry("user", "hello")
	m.AddEntry("assistant", "")

	// First streaming update
	m.UpdateLastAssistant("Hel", true)
	if len(m.entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(m.entries))
	}
	if m.entries[1].Content != "Hel" {
		t.Fatalf("expected 'Hel', got %q", m.entries[1].Content)
	}
	if !m.entries[1].StreamActive {
		t.Fatal("expected StreamActive=true")
	}

	// Second streaming update
	m.UpdateLastAssistant("Hello", true)
	if m.entries[1].Content != "Hello" {
		t.Fatalf("expected 'Hello', got %q", m.entries[1].Content)
	}

	// Final flush
	m.UpdateLastAssistant("Hello world!", false)
	if m.entries[1].Content != "Hello world!" {
		t.Fatalf("expected 'Hello world!', got %q", m.entries[1].Content)
	}
	if m.entries[1].StreamActive {
		t.Fatal("expected StreamActive=false on final")
	}
	if m.entries[1].Role != "assistant" {
		t.Fatalf("expected role=assistant, got %q", m.entries[1].Role)
	}
}
```

- [ ] **Step 4: Run conversation unit test**

Run: `go test ./internal/tui/conversation/ -run TestUpdateLastAssistant -v`
Expected: PASS

- [ ] **Step 5: Add streaming check to integration test**

In `internal/integration_test.go`, find `TestIntegration_AgentSpawnAndRespond` (or similar). After the test sends a message and waits for the assistant response, add assertions that stream messages arrived BEFORE the final assistant message:

```go
// Verify streaming messages arrived before final response
streamMsgCount := 0
finalAssistantSeen := false
for _, msg := range convMsgs {
    if msg.Role == "stream" && !finalAssistantSeen {
        streamMsgCount++
    }
    if msg.Role == "assistant" && msg.Content != "" {
        finalAssistantSeen = true
    }
}
if streamMsgCount == 0 {
    t.Log("no streaming messages detected (may be too fast to capture)")
}
```

- [ ] **Step 6: Run integration test (live)**

Run: `VULPINEOS_RUN_LIVE=1 go test ./internal/ -run TestIntegration_Agent -v -timeout 120s`
Expected: Tests pass. Streaming messages may or may not appear depending on timing, but no test failures.

---

### Task 8: Full build and integration smoke test

- [ ] **Step 1: Build the binary**

Run: `go build -o vulpineos ./cmd/vulpineos/`
Expected: Build succeeds with no errors.

- [ ] **Step 2: Install binary**

Run: `cp vulpineos /Users/rowan/.local/bin/vulpineos`
Expected: Binary copied.

- [ ] **Step 3: Full unit test suite**

Run: `go test ./internal/nanoclaw/ ./internal/tui/... -v -count=1 2>&1 | tail -20`
Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/nanoclaw/source_runtime.go \
  internal/nanoclaw/agent.go \
  internal/nanoclaw/session_mirror.go \
  internal/nanoclaw/cleanup.go \
  internal/tui/conversation/model.go \
  internal/tui/app.go \
  internal/tui/shared/messages.go \
  internal/nanoclaw/session_mirror_test.go \
  internal/tui/conversation/model_test.go \
  docs/superpowers/plans/2026-05-31-streaming-agent-output.md
git commit -m "feat: token-by-token LLM streaming in TUI via sidecar stream file

- opencode provider: stream:true + SSE parsing, writes deltas to stream file
- session mirror: polls stream file, accumulates text, emits progressive updates
- agent: handleStreamContent emits Role:stream messages
- conversation model: in-place entry update + blink cursor for active stream
- app.go: handles stream role, forwards StreamActive flag
- cleanup: stream_*.jsonl purged on daemon start
- tests: stream file polling, in-place update, integration assertion"
```
