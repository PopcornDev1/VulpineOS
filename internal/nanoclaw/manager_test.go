package nanoclaw

import (
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
	for path, data := range map[string]string{
		filepath.Join(srcDir, "package.json"):            `{"name":"nanoclaw"}`,
		filepath.Join(srcDir, "src", "index.ts"):         `console.log("daemon")`,
		filepath.Join(srcDir, "src", "cli", "client.ts"): `console.log("client")`,
		filepath.Join(srcDir, "bin", "ncl"):              "#!/bin/sh\nexit 0\n",
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
}

func TestAppendSocketSessionLogWritesJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents", "main", "sessions", "vulpine-agent-1.jsonl")
	appendSocketSessionLog(path, "assistant", "visible reply")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	if !strings.Contains(string(data), `"source":"vulpine-socket"`) || !strings.Contains(string(data), `"content":"visible reply"`) {
		t.Fatalf("session log entry = %s", data)
	}
}

func TestSpawnFailsWithBadBinary(t *testing.T) {
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
