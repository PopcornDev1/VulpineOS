package nanoclaw

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func createMirrorTestProfile(t *testing.T) (string, string, string) {
	t.Helper()
	nanoclawDir := t.TempDir()
	dataDir := filepath.Join(nanoclawDir, "data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "v2.db"))
	if err != nil {
		t.Fatalf("open central db: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE agent_groups (id TEXT PRIMARY KEY, name TEXT NOT NULL, folder TEXT NOT NULL UNIQUE, agent_provider TEXT, created_at TEXT NOT NULL);
CREATE TABLE messaging_groups (id TEXT PRIMARY KEY, channel_type TEXT NOT NULL, platform_id TEXT NOT NULL, name TEXT, is_group INTEGER DEFAULT 0, unknown_sender_policy TEXT NOT NULL DEFAULT 'strict', created_at TEXT NOT NULL, denied_at TEXT, UNIQUE(channel_type, platform_id));
CREATE TABLE messaging_group_agents (id TEXT PRIMARY KEY, messaging_group_id TEXT NOT NULL REFERENCES messaging_groups(id), agent_group_id TEXT NOT NULL REFERENCES agent_groups(id), session_mode TEXT DEFAULT 'shared', priority INTEGER DEFAULT 0, created_at TEXT NOT NULL, engage_mode TEXT, engage_pattern TEXT, sender_scope TEXT, ignored_message_policy TEXT, UNIQUE(messaging_group_id, agent_group_id));
CREATE TABLE sessions (id TEXT PRIMARY KEY, agent_group_id TEXT NOT NULL REFERENCES agent_groups(id), messaging_group_id TEXT REFERENCES messaging_groups(id), thread_id TEXT, agent_provider TEXT, status TEXT DEFAULT 'active', container_status TEXT DEFAULT 'stopped', last_active TEXT, created_at TEXT NOT NULL);
INSERT INTO agent_groups (id, name, folder, created_at) VALUES ('ag-1', 'VulpineOS', 'vulpineos', '2026-01-01T00:00:00Z');
INSERT INTO messaging_groups (id, channel_type, platform_id, name, unknown_sender_policy, created_at) VALUES ('mg-1', 'cli', 'vulpine:agent-1', 'Agent 1', 'public', '2026-01-01T00:00:00Z');
INSERT INTO messaging_group_agents (id, messaging_group_id, agent_group_id, created_at, engage_mode, engage_pattern, sender_scope, ignored_message_policy) VALUES ('wire-1', 'mg-1', 'ag-1', '2026-01-01T00:00:00Z', 'pattern', '.', 'all', 'drop');
INSERT INTO sessions (id, agent_group_id, messaging_group_id, thread_id, created_at, status) VALUES ('sess-1', 'ag-1', 'mg-1', NULL, '2026-01-01T00:00:00Z', 'active');
`)
	if err != nil {
		t.Fatalf("create central schema: %v", err)
	}

	sessionDir := filepath.Join(dataDir, "v2-sessions", "ag-1", "sess-1")
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	for name, schema := range map[string]string{
		"inbound.db": `
CREATE TABLE messages_in (id TEXT PRIMARY KEY, seq INTEGER UNIQUE, kind TEXT NOT NULL, timestamp TEXT NOT NULL, status TEXT DEFAULT 'pending', process_after TEXT, recurrence TEXT, series_id TEXT, tries INTEGER DEFAULT 0, trigger INTEGER NOT NULL DEFAULT 1, platform_id TEXT, channel_type TEXT, thread_id TEXT, content TEXT NOT NULL, source_session_id TEXT, on_wake INTEGER NOT NULL DEFAULT 0);
CREATE TABLE delivered (message_out_id TEXT PRIMARY KEY, platform_message_id TEXT, status TEXT NOT NULL DEFAULT 'delivered', delivered_at TEXT NOT NULL);
`,
		"outbound.db": `
CREATE TABLE messages_out (id TEXT PRIMARY KEY, seq INTEGER UNIQUE, in_reply_to TEXT, timestamp TEXT NOT NULL, deliver_after TEXT, recurrence TEXT, kind TEXT NOT NULL, platform_id TEXT, channel_type TEXT, thread_id TEXT, content TEXT NOT NULL);
CREATE TABLE session_state (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL);
`,
	} {
		db, err := sql.Open("sqlite", filepath.Join(sessionDir, name))
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		if _, err := db.Exec(schema); err != nil {
			db.Close()
			t.Fatalf("create %s schema: %v", name, err)
		}
		db.Close()
	}
	return nanoclawDir, sessionDir, filepath.Join(t.TempDir(), "session.jsonl")
}

func TestNanoClawSessionMirrorWritesInboundOutboundAsNormalizedSessionLog(t *testing.T) {
	nanoclawDir, sessionDir, logPath := createMirrorTestProfile(t)
	now := time.Now().UTC()

	inDB, err := sql.Open("sqlite", filepath.Join(sessionDir, "inbound.db"))
	if err != nil {
		t.Fatalf("open inbound: %v", err)
	}
	_, err = inDB.Exec(`INSERT INTO messages_in (id, seq, kind, timestamp, platform_id, channel_type, content)
VALUES ('in-1', 2, 'chat', ?, 'vulpine:agent-1', 'cli', '{"text":"hello","sender":"vulpine"}')`, now.Format(time.RFC3339Nano))
	inDB.Close()
	if err != nil {
		t.Fatalf("insert inbound: %v", err)
	}

	outDB, err := sql.Open("sqlite", filepath.Join(sessionDir, "outbound.db"))
	if err != nil {
		t.Fatalf("open outbound: %v", err)
	}
	_, err = outDB.Exec(`INSERT INTO messages_out (id, seq, kind, timestamp, platform_id, channel_type, content)
VALUES ('out-1', 3, 'chat', ?, 'vulpine:agent-1', 'cli', '{"text":"done"}')`, now.Format(time.RFC3339Nano))
	outDB.Close()
	if err != nil {
		t.Fatalf("insert outbound: %v", err)
	}

	agent := newAgent("agent-1", "ctx", make(chan AgentStatus, 1))
	mirror := NewNanoClawSessionMirror(nanoclawDir, "agent-1", logPath, agent)
	completed, err := mirror.MirrorOnce(now.Add(-time.Second))
	if err != nil {
		t.Fatalf("MirrorOnce: %v", err)
	}
	if !completed {
		t.Fatal("MirrorOnce did not report a new outbound assistant message")
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	for _, want := range []string{`"source":"nanoclaw-db"`, `"nanoclawMessageId":"in:in-1"`, `"nanoclawMessageId":"out:out-1"`, `"role":"assistant"`, `"text":"done"`} {
		if !strings.Contains(log, want) {
			t.Fatalf("mirrored log missing %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "vulpine-socket") {
		t.Fatalf("mirrored log should not contain synthetic socket source:\n%s", log)
	}

	select {
	case msg := <-agent.conversationCh:
		if msg.Role != "assistant" || msg.Content != "done" {
			t.Fatalf("conversation = %#v, want assistant done", msg)
		}
	default:
		t.Fatal("expected assistant conversation from mirrored outbound row")
	}

	sizeBefore := int64(len(data))
	completed, err = mirror.MirrorOnce(now.Add(-time.Second))
	if err != nil {
		t.Fatalf("second MirrorOnce: %v", err)
	}
	if completed {
		t.Fatal("second MirrorOnce should not re-complete from duplicate rows")
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	if info.Size() != sizeBefore {
		t.Fatalf("second MirrorOnce appended duplicates: size %d want %d", info.Size(), sizeBefore)
	}
}

func TestNanoClawSessionMirrorDoesNotEmitStaleOutboundRows(t *testing.T) {
	nanoclawDir, sessionDir, logPath := createMirrorTestProfile(t)
	now := time.Now().UTC()

	outDB, err := sql.Open("sqlite", filepath.Join(sessionDir, "outbound.db"))
	if err != nil {
		t.Fatalf("open outbound: %v", err)
	}
	_, err = outDB.Exec(`INSERT INTO messages_out (id, seq, kind, timestamp, platform_id, channel_type, content)
VALUES ('out-old', 3, 'chat', ?, 'vulpine:agent-1', 'cli', '{"text":"old reply"}')`, now.Add(-time.Minute).Format(time.RFC3339Nano))
	outDB.Close()
	if err != nil {
		t.Fatalf("insert outbound: %v", err)
	}

	agent := newAgent("agent-1", "ctx", make(chan AgentStatus, 1))
	mirror := NewNanoClawSessionMirror(nanoclawDir, "agent-1", logPath, agent)
	completed, err := mirror.MirrorOnce(now)
	if err != nil {
		t.Fatalf("MirrorOnce: %v", err)
	}
	if completed {
		t.Fatal("stale outbound row should not complete current turn")
	}
	select {
	case msg := <-agent.conversationCh:
		t.Fatalf("stale outbound should not emit conversation message, got %#v", msg)
	default:
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), `"nanoclawMessageId":"out:out-old"`) {
		t.Fatalf("stale outbound should still be marked in session log:\n%s", data)
	}
}

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

func TestMirrorOnceDoesNotCompleteFromStreamOnly(t *testing.T) {
	nanoclawDir, sessionDir, logPath := createMirrorTestProfile(t)
	agent := newAgent("agent-1", "ctx", make(chan AgentStatus, 1))
	mirror := NewNanoClawSessionMirror(nanoclawDir, "agent-1", logPath, agent)

	streamPath := filepath.Join(sessionDir, "stream.jsonl")
	if err := os.WriteFile(streamPath, []byte(`{"t":"partial"}`+"\n"), 0600); err != nil {
		t.Fatalf("write stream: %v", err)
	}

	completed, err := mirror.MirrorOnce(time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("MirrorOnce: %v", err)
	}
	if completed {
		t.Fatal("MirrorOnce should not complete a turn from stream activity alone")
	}
	select {
	case msg := <-agent.conversationCh:
		if msg.Role != "stream" || msg.Content != "partial" || !msg.StreamActive {
			t.Fatalf("stream conversation = %#v, want active partial stream", msg)
		}
	default:
		t.Fatal("expected stream message to still be emitted")
	}
}

func TestMirrorOnceEmitsActivityWithoutCompletingTurn(t *testing.T) {
	nanoclawDir, sessionDir, logPath := createMirrorTestProfile(t)
	agent := newAgent("agent-1", "ctx", make(chan AgentStatus, 1))
	mirror := NewNanoClawSessionMirror(nanoclawDir, "agent-1", logPath, agent)

	streamPath := filepath.Join(sessionDir, "stream.jsonl")
	if err := os.WriteFile(streamPath, []byte(`{"activity":{"phase":"tool_start","tool":"web","text":"Opening example.com","status":"running"}}`+"\n"), 0600); err != nil {
		t.Fatalf("write activity stream: %v", err)
	}

	completed, err := mirror.MirrorOnce(time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("MirrorOnce: %v", err)
	}
	if completed {
		t.Fatal("activity should not complete the current turn")
	}
	select {
	case msg := <-agent.conversationCh:
		if msg.Role != "activity" || msg.Content != "Opening example.com" {
			t.Fatalf("activity conversation = %#v, want activity Opening example.com", msg)
		}
	default:
		t.Fatal("expected activity message to be emitted")
	}
}

func TestNanoClawSessionMirrorTranslatesClaudeTranscriptToolEvents(t *testing.T) {
	nanoclawDir, sessionDir, logPath := createMirrorTestProfile(t)
	now := time.Now().UTC()

	outDB, err := sql.Open("sqlite", filepath.Join(sessionDir, "outbound.db"))
	if err != nil {
		t.Fatalf("open outbound: %v", err)
	}
	_, err = outDB.Exec(`INSERT INTO session_state (key, value, updated_at) VALUES ('continuation:claude', 'cont-123', ?)`, now.Format(time.RFC3339Nano))
	outDB.Close()
	if err != nil {
		t.Fatalf("insert continuation: %v", err)
	}

	transcriptDir := filepath.Join(filepath.Dir(sessionDir), ".claude-shared", "projects", "project-1")
	if err := os.MkdirAll(transcriptDir, 0700); err != nil {
		t.Fatalf("mkdir transcript: %v", err)
	}
	transcript := strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Inspecting the page"},{"type":"tool_use","id":"toolu_1","name":"browser","input":{"action":"open","url":"https://example.com"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"{\"status\":\"error\",\"error\":\"gateway token mismatch\"}","is_error":true}]}}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(transcriptDir, "cont-123.jsonl"), []byte(transcript), 0600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	agent := newAgent("agent-1", "ctx", make(chan AgentStatus, 1))
	mirror := NewNanoClawSessionMirror(nanoclawDir, "agent-1", logPath, agent)
	if _, err := mirror.MirrorOnce(now.Add(-time.Second)); err != nil {
		t.Fatalf("MirrorOnce: %v", err)
	}

	got := []string{
		(<-agent.conversationCh).Content,
		(<-agent.conversationCh).Content,
		(<-agent.conversationCh).Content,
	}
	want := []string{
		"Thinking: Inspecting the page",
		"Running browser open https://example.com",
		"Tool failed: browser open https://example.com — gateway token mismatch",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("conversation[%d] = %q, want %q (all %#v)", i, got[i], want[i], got)
		}
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(data)
	for _, want := range []string{`"source":"nanoclaw-transcript"`, `"type":"toolCall"`, `"role":"toolResult"`} {
		if !strings.Contains(log, want) {
			t.Fatalf("transcript mirror missing %q:\n%s", want, log)
		}
	}
}
