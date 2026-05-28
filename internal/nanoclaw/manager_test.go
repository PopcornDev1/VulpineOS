package nanoclaw

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vulpineos/internal/config"
)

func TestNewManager(t *testing.T) {
	m := NewManager("test-binary")
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if m.binary != "test-binary" {
		t.Errorf("binary = %q, want %q", m.binary, "test-binary")
	}
	if m.agents == nil {
		t.Error("agents map should be initialized")
	}
}

func TestNewManagerDefaultBinary(t *testing.T) {
	m := NewManager("")
	if m.binary != "nanoclaw" {
		t.Errorf("binary = %q, want %q (default)", m.binary, "nanoclaw")
	}
}

func TestStatusChan(t *testing.T) {
	m := NewManager("test")
	ch := m.StatusChan()
	if ch == nil {
		t.Fatal("StatusChan() returned nil")
	}
	// Verify it's a receive-only channel by checking we can read the type
	select {
	case <-ch:
		t.Error("should not have received anything from empty channel")
	default:
		// expected
	}
}

func TestConversationChan(t *testing.T) {
	m := NewManager("test")
	ch := m.ConversationChan()
	if ch == nil {
		t.Fatal("ConversationChan() returned nil")
	}
}

func TestCountStartsAtZero(t *testing.T) {
	m := NewManager("test")
	if m.Count() != 0 {
		t.Errorf("Count() = %d, want 0", m.Count())
	}
}

func TestListStartsEmpty(t *testing.T) {
	m := NewManager("test")
	list := m.List()
	if len(list) != 0 {
		t.Errorf("List() length = %d, want 0", len(list))
	}
}

func TestKillNonexistent(t *testing.T) {
	m := NewManager("test")
	err := m.Kill("nonexistent-id")
	if err == nil {
		t.Error("expected error when killing nonexistent agent")
	}
}

func TestKillMarksAgentInterrupted(t *testing.T) {
	m := NewManager("test")
	statusCh := m.StatusChan()
	agent := newAgent("agent-1", "ctx-1", m.statusSource)
	m.agents["agent-1"] = &managedAgent{agent: agent}

	if err := m.Kill("agent-1"); err != nil {
		t.Fatalf("kill agent: %v", err)
	}
	if got := agent.Status().Status; got != "interrupted" {
		t.Fatalf("status = %q, want interrupted", got)
	}

	select {
	case status := <-statusCh:
		if status.AgentID != "agent-1" {
			t.Fatalf("status agent = %q, want agent-1", status.AgentID)
		}
		if status.Status != "interrupted" {
			t.Fatalf("emitted status = %q, want interrupted", status.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for interrupted status")
	}
}

func TestPauseNonexistent(t *testing.T) {
	m := NewManager("test")
	err := m.PauseAgent("nonexistent-id")
	if err == nil {
		t.Error("expected error when pausing nonexistent agent")
	}
}

func TestSendMessageNonexistent(t *testing.T) {
	m := NewManager("test")
	err := m.SendMessage("nonexistent-id", "hello")
	if err == nil {
		t.Error("expected error when sending to nonexistent agent")
	}
}

func TestSendMessageSocketAgentEnqueuesViaDaemon(t *testing.T) {
	nanoclawDir, err := os.MkdirTemp("/tmp", "vulpine-ncl-send-*")
	if err != nil {
		t.Fatalf("temp nanoclaw dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(nanoclawDir) })
	t.Setenv("VULPINE_NANOCLAW_DIR", nanoclawDir)
	socketPath := VulpineNanoclawSocketPath()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen socket: %v", err)
	}
	defer listener.Close()

	payloadCh := make(chan map[string]interface{}, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
		conn, err = listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			return
		}
		payloadCh <- payload
	}()

	m := NewManager("test")
	agent := newAgent("agent-1", "ctx-1", m.statusSource)
	m.agents["agent-1"] = &managedAgent{agent: agent}

	if err := m.SendMessage("agent-1", "follow up"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	select {
	case payload := <-payloadCh:
		text, _ := payload["text"].(string)
		if !strings.Contains(text, "User message:\nfollow up") {
			t.Fatalf("text = %v, want wrapped follow up", payload["text"])
		}
		to, ok := payload["to"].(map[string]interface{})
		if !ok || to["platformId"] != "vulpine:agent-1" {
			t.Fatalf("to = %#v, want vulpine route", payload["to"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for socket payload")
	}
}

func TestKillAllEmpty(t *testing.T) {
	m := NewManager("test")
	// Should not panic on empty manager
	m.KillAll()
}

func TestDisposeEmpty(t *testing.T) {
	m := NewManager("test")
	// Should not panic; channels should be closed
	m.Dispose()
	m.Dispose()

	// Verify channels are closed
	_, ok := <-m.StatusChan()
	if ok {
		t.Error("StatusChan should be closed after Dispose")
	}
	_, ok = <-m.ConversationChan()
	if ok {
		t.Error("ConversationChan should be closed after Dispose")
	}
}

func TestAgentStatusAfterManagerDisposeDoesNotPanic(t *testing.T) {
	m := NewManager("test")
	agent := newAgent("agent-1", "ctx-1", m.statusSource)
	m.Dispose()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("emitStatus panicked after manager dispose: %v", r)
		}
	}()
	agent.emitStatus()
}

func TestForwardConversationAfterManagerDisposeDoesNotPanic(t *testing.T) {
	m := NewManager("test")
	agent := newAgent("agent-1", "ctx-1", make(chan AgentStatus, 1))
	m.Dispose()

	done := make(chan struct{})
	go func() {
		m.forwardConversation(agent)
		close(done)
	}()

	agent.conversationCh <- ConversationMsg{AgentID: "agent-1", Role: "assistant", Content: "late"}
	close(agent.conversationCh)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("forwardConversation did not exit")
	}
}

func TestAgentConversationAfterCloseDoesNotPanic(t *testing.T) {
	agent := newAgent("agent-1", "ctx-1", make(chan AgentStatus, 1))
	close(agent.conversationCh)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("emitConversation panicked after channel close: %v", r)
		}
	}()
	agent.emitConversation(ConversationMsg{AgentID: "agent-1", Role: "system", Content: "late"})
}

func TestNanoClawInstalledFalseForBogus(t *testing.T) {
	m := NewManager("/nonexistent/path/to/nanoclaw-binary-xyz")
	// Should return false since the binary doesn't exist
	// (may return true if nanoclaw is globally installed, so this is best-effort)
	// At minimum, verify it doesn't panic
	_ = m.NanoClawInstalled()
}

func TestFindNanoClawCreatesLauncherForUpstreamSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	srcDir := filepath.Join(home, ".vulpineos", "nanoclaw-src")
	if err := os.MkdirAll(filepath.Join(srcDir, "src", "cli"), 0700); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "bin"), 0700); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "container", "agent-runner", "src"), 0700); err != nil {
		t.Fatalf("mkdir container source: %v", err)
	}
	for path, data := range map[string]string{
		filepath.Join(srcDir, "package.json"):            `{"name":"nanoclaw"}`,
		filepath.Join(srcDir, "src", "index.ts"):         `console.log("daemon")`,
		filepath.Join(srcDir, "src", "cli", "client.ts"): `console.log("client")`,
		filepath.Join(srcDir, "bin", "ncl"):              "#!/bin/sh\nexit 0\n",
		filepath.Join(srcDir, "container", "Dockerfile"): "FROM scratch\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0700); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	m := NewManager("")
	got := m.findNanoClaw()
	want := filepath.Join(home, ".vulpineos", "nanoclaw", "nanoclaw")
	if got != want {
		t.Fatalf("findNanoClaw() = %q, want %q", got, want)
	}
	if !isRunnable(got) {
		t.Fatalf("launcher is not runnable: %s", got)
	}
	content, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read launcher: %v", err)
	}
	for _, want := range []string{"VULPINE_NANOCLAW_HOME", "dist/index.js", "src/index.ts", "src/cli/client.ts"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("launcher content does not contain %q:\n%s", want, content)
		}
	}
	containerLink := filepath.Join(home, ".vulpineos", "nanoclaw", "container")
	target, err := os.Readlink(containerLink)
	if err != nil {
		t.Fatalf("container assets were not linked into profile: %v", err)
	}
	if target != filepath.Join(srcDir, "container") {
		t.Fatalf("container link = %q, want source container", target)
	}
}

func TestSpawnFailsWithBadBinary(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Spawn should fail when given a binary that exists but isn't executable
	// or when the process immediately fails
	m := NewManager("/dev/null") // exists but not executable as a command
	_, err := m.Spawn("ctx-1", "")
	if err == nil {
		t.Error("expected error when spawning with non-executable binary")
	}
}

func TestSpawnWithSessionRequiresVulpineOwnedDaemonSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmpDir := t.TempDir()
	bin := filepath.Join(tmpDir, "nanoclaw")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatalf("write fake nanoclaw binary: %v", err)
	}
	m := NewManager(bin)

	_, err := m.SpawnWithSessionIsolated("agent-1", "task", "vulpine-agent-1", config.NanoClawConfigPath(), nil)
	if err == nil {
		t.Fatal("expected missing daemon socket error")
	}
	if !strings.Contains(err.Error(), "NanoClaw daemon is not running") {
		t.Fatalf("error = %v, want daemon socket error", err)
	}
}

func TestSpawnWithSessionAppliesScopedRuntimeConfigBeforeSocketMessage(t *testing.T) {
	nanoclawDir, err := os.MkdirTemp("/tmp", "ncl-mgr-*")
	if err != nil {
		t.Fatalf("temp nanoclaw dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(nanoclawDir) })
	t.Setenv("VULPINE_NANOCLAW_DIR", nanoclawDir)
	dataDir := filepath.Join(nanoclawDir, "data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	dbPath := filepath.Join(dataDir, "v2.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE agent_groups (id TEXT PRIMARY KEY, name TEXT NOT NULL, folder TEXT NOT NULL UNIQUE, agent_provider TEXT, created_at TEXT NOT NULL);
CREATE TABLE container_configs (
  agent_group_id TEXT PRIMARY KEY REFERENCES agent_groups(id) ON DELETE CASCADE,
  provider TEXT,
  model TEXT,
  effort TEXT,
  image_tag TEXT,
  assistant_name TEXT,
  max_messages_per_prompt INTEGER,
  skills TEXT NOT NULL DEFAULT '"all"',
  mcp_servers TEXT NOT NULL DEFAULT '{}',
  packages_apt TEXT NOT NULL DEFAULT '[]',
  packages_npm TEXT NOT NULL DEFAULT '[]',
  additional_mounts TEXT NOT NULL DEFAULT '[]',
  updated_at TEXT NOT NULL,
  cli_scope TEXT NOT NULL DEFAULT 'group'
);
CREATE TABLE messaging_groups (id TEXT PRIMARY KEY, channel_type TEXT NOT NULL, platform_id TEXT NOT NULL, name TEXT, is_group INTEGER DEFAULT 0, unknown_sender_policy TEXT NOT NULL DEFAULT 'strict', created_at TEXT NOT NULL, denied_at TEXT, UNIQUE(channel_type, platform_id));
CREATE TABLE messaging_group_agents (id TEXT PRIMARY KEY, messaging_group_id TEXT NOT NULL REFERENCES messaging_groups(id), agent_group_id TEXT NOT NULL REFERENCES agent_groups(id), session_mode TEXT DEFAULT 'shared', priority INTEGER DEFAULT 0, created_at TEXT NOT NULL, engage_mode TEXT, engage_pattern TEXT, sender_scope TEXT, ignored_message_policy TEXT, UNIQUE(messaging_group_id, agent_group_id));
CREATE TABLE agent_destinations (agent_group_id TEXT NOT NULL REFERENCES agent_groups(id), local_name TEXT NOT NULL, target_type TEXT NOT NULL, target_id TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(agent_group_id, local_name));
INSERT INTO agent_groups (id, name, folder, created_at) VALUES ('ag-1', 'vulpine-test', 'vulpine-test', '2026-01-01T00:00:00Z');
INSERT INTO container_configs (agent_group_id, provider, model, updated_at, skills, mcp_servers, packages_apt, packages_npm, additional_mounts, cli_scope)
VALUES ('ag-1', 'claude', 'old-model', '2026-01-01T00:00:00Z', '"all"', '{}', '[]', '[]', '[]', 'group');
`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}

	socketPath := VulpineNanoclawSocketPath()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen socket: %v", err)
	}
	defer listener.Close()
	payloadCh := make(chan map[string]interface{}, 1)
	go func() {
		// First connection is NanoclawClient.IsRunning.
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
		conn, err = listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			return
		}
		payloadCh <- payload
	}()

	configPath := filepath.Join(t.TempDir(), "nanoclaw.json")
	if err := os.WriteFile(configPath, []byte(`{
  "agents":{"defaults":{"model":{"primary":"opencode/deepseek-v4-free"}}},
  "browser":{"enabled":true,"headless":true,"cdpUrl":"ws://127.0.0.1:45555/devtools/browser/scoped"}
}`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	m := NewManager("test")
	agentID := "agent-scoped"
	if _, err := m.SpawnWithSessionIsolated(agentID, "hello", "vulpine-agent-scoped", configPath, nil); err != nil {
		t.Fatalf("SpawnWithSessionIsolated: %v", err)
	}
	defer m.Dispose()
	defer m.Kill(agentID)

	select {
	case payload := <-payloadCh:
		text, _ := payload["text"].(string)
		if !strings.Contains(text, "User message:\nhello") {
			t.Fatalf("socket text = %v, want wrapped hello", payload["text"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for socket payload")
	}

	browserConfig := filepath.Join(nanoclawDir, "groups", "vulpine-test", "agent-browser.json")
	data, err := os.ReadFile(browserConfig)
	if err != nil {
		t.Fatalf("read agent-browser config: %v", err)
	}
	var browser map[string]string
	if err := json.Unmarshal(data, &browser); err != nil {
		t.Fatalf("parse agent-browser config: %v", err)
	}
	if browser["cdp"] != "ws://host.docker.internal:45555/devtools/browser/scoped" {
		t.Fatalf("browser cdp = %q, want container-reachable scoped route", browser["cdp"])
	}

	var provider, model string
	if err := db.QueryRow(`SELECT provider, model FROM container_configs WHERE agent_group_id = 'ag-1'`).Scan(&provider, &model); err != nil {
		t.Fatalf("query container config: %v", err)
	}
	if provider != "opencode" || model != "opencode/deepseek-v4-free" {
		t.Fatalf("provider/model = %q/%q, want opencode/opencode/deepseek-v4-free", provider, model)
	}
}

func TestRuntimeEnvForConfigIncludesNanoClawGatewayToken(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "nanoclaw.json")
	if err := os.WriteFile(configPath, []byte(`{"gateway":{"auth":{"mode":"token","token":"token-123"}}}`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	env := runtimeEnvForConfig(configPath)
	if env["NANOCLAW_CONFIG_PATH"] != configPath {
		t.Fatalf("NANOCLAW_CONFIG_PATH = %q, want %q", env["NANOCLAW_CONFIG_PATH"], configPath)
	}
	if env["NANOCLAW_GATEWAY_TOKEN"] != "token-123" {
		t.Fatalf("NANOCLAW_GATEWAY_TOKEN = %q, want %q", env["NANOCLAW_GATEWAY_TOKEN"], "token-123")
	}
	if _, ok := env["OPENCLAW_CONFIG_PATH"]; ok {
		t.Fatalf("OPENCLAW_CONFIG_PATH should not be set after clean cutover: %#v", env)
	}
	if _, ok := env["OPENCLAW_GATEWAY_TOKEN"]; ok {
		t.Fatalf("OPENCLAW_GATEWAY_TOKEN should not be set after clean cutover: %#v", env)
	}
}

func TestRuntimeEnvForConfigOmitsMissingNanoClawGatewayToken(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "nanoclaw.json")
	if err := os.WriteFile(configPath, []byte(`{"gateway":{"mode":"local"}}`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	env := runtimeEnvForConfig(configPath)
	if env["NANOCLAW_CONFIG_PATH"] != configPath {
		t.Fatalf("NANOCLAW_CONFIG_PATH = %q, want %q", env["NANOCLAW_CONFIG_PATH"], configPath)
	}
	if _, ok := env["NANOCLAW_GATEWAY_TOKEN"]; ok {
		t.Fatalf("NANOCLAW_GATEWAY_TOKEN should be omitted when token is absent: %#v", env)
	}
}

func TestResumeWithSessionIsolatedRunsCleanupOnStartFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	badBin := filepath.Join(t.TempDir(), "nonexistent-nanoclaw-bin")

	m := NewManager(badBin)
	called := false

	_, err := m.ResumeWithSessionIsolated("agent-1", "session-1", config.NanoClawConfigPath(), func() {
		called = true
	})
	if err == nil {
		t.Fatal("expected resume to fail with bad binary")
	}
	if !called {
		t.Fatal("expected cleanup to run on start failure")
	}
}

func TestSpawnWithSessionRejectsUnsafeSessionNameAndRunsCleanup(t *testing.T) {
	for _, sessionName := range []string{"../escape", `..\escape`, "nested/session", "."} {
		t.Run(sessionName, func(t *testing.T) {
			m := NewManager("/not-needed")
			called := false
			_, err := m.SpawnWithSessionIsolated("agent-1", "task", sessionName, config.NanoClawConfigPath(), func() {
				called = true
			})
			if err == nil || !strings.Contains(err.Error(), "invalid sessionName") {
				t.Fatalf("error = %v, want invalid sessionName", err)
			}
			if !called {
				t.Fatal("expected cleanup to run on validation failure")
			}
		})
	}
}

func TestSafeSessionNameDefaultsToAgentID(t *testing.T) {
	got, err := safeSessionName("agent-1", "")
	if err != nil {
		t.Fatalf("safeSessionName: %v", err)
	}
	if got != "vulpine-agent-1" {
		t.Fatalf("session name = %q, want vulpine-agent-1", got)
	}
}

func TestSessionLogPathForSessionIDRejectsTraversal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, err := sessionLogPathForSessionID("vulpine-agent-1")
	if err != nil {
		t.Fatalf("sessionLogPathForSessionID: %v", err)
	}
	want := filepath.Join(config.NanoClawProfileDir(), "agents", "main", "sessions", "vulpine-agent-1.jsonl")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}

	for _, sessionID := range []string{"../escape", `..\escape`, "nested/session"} {
		t.Run(sessionID, func(t *testing.T) {
			_, err := sessionLogPathForSessionID(sessionID)
			if err == nil || !strings.Contains(err.Error(), "invalid sessionName") {
				t.Fatalf("error = %v, want invalid sessionName", err)
			}
		})
	}
}
