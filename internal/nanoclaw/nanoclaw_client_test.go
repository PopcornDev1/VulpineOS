package nanoclaw

import (
	"bufio"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestNanoclawClientWaitsForDelayedResponse(t *testing.T) {
	socketPath := filepath.Join("/tmp", "ncl-client-test.sock")
	_ = os.Remove(socketPath)
	t.Cleanup(func() { _ = os.Remove(socketPath) })
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan struct{})
	clientClosed := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return
		}
		var req map[string]string
		if err := json.Unmarshal([]byte(line), &req); err != nil || req["text"] != "hello" {
			return
		}

		time.Sleep(3 * time.Second)
		_, _ = conn.Write([]byte(`{"text":"delayed"}` + "\n"))
		_, _ = bufio.NewReader(conn).ReadString('\n')
		close(clientClosed)
	}()

	client := &NanoclawClient{socketPath: socketPath}
	var chunks []string
	var completed bool
	err = client.SendMessage("hello", func(chunk string, done bool) {
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if done {
			completed = true
		}
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	if len(chunks) != 1 || chunks[0] != "delayed" {
		t.Fatalf("chunks = %#v, want delayed response", chunks)
	}
	if !completed {
		t.Fatal("expected completion callback after response")
	}
	select {
	case <-clientClosed:
	case <-time.After(time.Second):
		t.Fatal("client did not close after first response")
	}
	<-serverDone
}

func TestNanoclawClientRoutesAgentMessages(t *testing.T) {
	tmpDir, err := os.MkdirTemp("/tmp", "ncl-route-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	socketPath := filepath.Join(tmpDir, "cli.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer listener.Close()

	payloadCh := make(chan map[string]interface{}, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return
		}
		var req map[string]interface{}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			return
		}
		payloadCh <- req
		_, _ = conn.Write([]byte(`{"text":"ok"}` + "\n"))
	}()

	client := &NanoclawClient{socketPath: socketPath}
	if err := client.SendAgentMessage("agent-1", "hello", func(string, bool) {}); err != nil {
		t.Fatalf("SendAgentMessage: %v", err)
	}

	var payload map[string]interface{}
	select {
	case payload = <-payloadCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for routed payload")
	}
	text, _ := payload["text"].(string)
	if !strings.Contains(text, "User message:\nhello") {
		t.Fatalf("text = %v, want wrapped hello", payload["text"])
	}
	to, ok := payload["to"].(map[string]interface{})
	if !ok {
		t.Fatalf("to = %#v, want address", payload["to"])
	}
	if to["channelType"] != "cli" || to["platformId"] != "vulpine:agent-1" || to["threadId"] != nil {
		t.Fatalf("to = %#v, want cli/vulpine:agent-1", to)
	}
	replyTo, ok := payload["reply_to"].(map[string]interface{})
	if !ok {
		t.Fatalf("reply_to = %#v, want address", payload["reply_to"])
	}
	if replyTo["channelType"] != "cli" || replyTo["platformId"] != "vulpine:agent-1" || replyTo["threadId"] != nil {
		t.Fatalf("reply_to = %#v, want cli/vulpine:agent-1", replyTo)
	}
}

func TestNanoclawClientEnqueuesRoutedAgentMessageWithoutWaitingForReply(t *testing.T) {
	tmpDir, err := os.MkdirTemp("/tmp", "ncl-enqueue-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	socketPath := filepath.Join(tmpDir, "cli.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer listener.Close()

	payloadCh := make(chan map[string]interface{}, 1)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			return
		}
		var req map[string]interface{}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			return
		}
		payloadCh <- req
		time.Sleep(2 * time.Second)
	}()

	client := &NanoclawClient{socketPath: socketPath}
	started := time.Now()
	if err := client.EnqueueAgentMessage("agent-1", "hello"); err != nil {
		t.Fatalf("EnqueueAgentMessage: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("EnqueueAgentMessage waited %s for a routed socket reply", elapsed)
	}

	select {
	case payload := <-payloadCh:
		text, _ := payload["text"].(string)
		if !strings.Contains(text, "User message:\nhello") {
			t.Fatalf("text = %v, want wrapped user message", payload["text"])
		}
		if !strings.Contains(text, "send that exact reply and stop") {
			t.Fatalf("text = %v, want exact-reply stop guard", payload["text"])
		}
		to, ok := payload["to"].(map[string]interface{})
		if !ok || to["platformId"] != "vulpine:agent-1" {
			t.Fatalf("to = %#v, want vulpine route", payload["to"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for routed payload")
	}
	<-serverDone
}

func TestFindNanoclawSocketUsesVulpineOwnedPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	socketPath := filepath.Join(home, ".vulpineos", "nanoclaw", "data", "cli.sock")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(socketPath, []byte{}, 0600); err != nil {
		t.Fatalf("write socket marker: %v", err)
	}

	got, ok := FindNanoclawSocket()
	if !ok {
		t.Fatal("FindNanoclawSocket() did not find VulpineOS-owned socket")
	}
	if got != socketPath {
		t.Fatalf("socket path = %q, want %q", got, socketPath)
	}
	if got := GetNanoclawDir(); got != filepath.Join(home, ".vulpineos", "nanoclaw") {
		t.Fatalf("GetNanoclawDir() = %q, want VulpineOS-owned NanoClaw dir", got)
	}
}

func TestEnsureVulpineAgentRouteCreatesMessagingGroupAndWiring(t *testing.T) {
	nanoclawDir := t.TempDir()
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
CREATE TABLE messaging_groups (id TEXT PRIMARY KEY, channel_type TEXT NOT NULL, platform_id TEXT NOT NULL, name TEXT, is_group INTEGER DEFAULT 0, unknown_sender_policy TEXT NOT NULL DEFAULT 'strict', created_at TEXT NOT NULL, denied_at TEXT, UNIQUE(channel_type, platform_id));
CREATE TABLE messaging_group_agents (id TEXT PRIMARY KEY, messaging_group_id TEXT NOT NULL REFERENCES messaging_groups(id), agent_group_id TEXT NOT NULL REFERENCES agent_groups(id), session_mode TEXT DEFAULT 'shared', priority INTEGER DEFAULT 0, created_at TEXT NOT NULL, engage_mode TEXT, engage_pattern TEXT, sender_scope TEXT, ignored_message_policy TEXT, UNIQUE(messaging_group_id, agent_group_id));
CREATE TABLE agent_destinations (agent_group_id TEXT NOT NULL REFERENCES agent_groups(id), local_name TEXT NOT NULL, target_type TEXT NOT NULL, target_id TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(agent_group_id, local_name));
INSERT INTO agent_groups (id, name, folder, created_at) VALUES ('ag-1', 'vulpine-test', 'vulpine-test', '2026-01-01T00:00:00Z');
`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}

	if err := ensureVulpineAgentRoute(nanoclawDir, "agent-1"); err != nil {
		t.Fatalf("ensureVulpineAgentRoute: %v", err)
	}

	var mgID, platformID, unknownSenderPolicy string
	if err := db.QueryRow(`SELECT id, platform_id, unknown_sender_policy FROM messaging_groups WHERE channel_type = 'cli'`).Scan(&mgID, &platformID, &unknownSenderPolicy); err != nil {
		t.Fatalf("query messaging group: %v", err)
	}
	if platformID != "vulpine:agent-1" {
		t.Fatalf("platform_id = %q, want vulpine:agent-1", platformID)
	}
	if unknownSenderPolicy != "public" {
		t.Fatalf("unknown_sender_policy = %q, want public", unknownSenderPolicy)
	}

	var agentGroupID, sessionMode, engageMode, engagePattern string
	if err := db.QueryRow(`SELECT agent_group_id, session_mode, engage_mode, engage_pattern FROM messaging_group_agents WHERE messaging_group_id = ?`, mgID).Scan(&agentGroupID, &sessionMode, &engageMode, &engagePattern); err != nil {
		t.Fatalf("query wiring: %v", err)
	}
	if agentGroupID != "ag-1" || sessionMode != "shared" || engageMode != "pattern" || engagePattern != "." {
		t.Fatalf("wiring = %q %q %q %q, want ag-1 shared pattern .", agentGroupID, sessionMode, engageMode, engagePattern)
	}

	var localName, targetType, targetID string
	if err := db.QueryRow(`SELECT local_name, target_type, target_id FROM agent_destinations WHERE agent_group_id = ?`, agentGroupID).Scan(&localName, &targetType, &targetID); err != nil {
		t.Fatalf("query destination: %v", err)
	}
	if localName != vulpineDestinationName("agent-1") || targetType != "channel" || targetID != mgID {
		t.Fatalf("destination = %q %q %q, want %q channel %q", localName, targetType, targetID, vulpineDestinationName("agent-1"), mgID)
	}
}

func TestNanoclawClientIsRunningRejectsStaleSocket(t *testing.T) {
	socketPath := filepath.Join("/tmp", "ncl-stale-"+shortHash(t.Name())+".sock")
	_ = os.Remove(socketPath)
	t.Cleanup(func() { _ = os.Remove(socketPath) })
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close unix socket: %v", err)
	}

	client := &NanoclawClient{socketPath: socketPath}
	if client.IsRunning() {
		t.Fatal("IsRunning() = true for stale socket path, want false")
	}
}

func TestNanoclawClientIsRunningAcceptsListeningSocket(t *testing.T) {
	socketPath := filepath.Join("/tmp", "ncl-live-"+shortHash(t.Name())+".sock")
	_ = os.Remove(socketPath)
	t.Cleanup(func() { _ = os.Remove(socketPath) })
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
		close(done)
	}()

	client := &NanoclawClient{socketPath: socketPath}
	if !client.IsRunning() {
		t.Fatal("IsRunning() = false for listening socket, want true")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener did not receive IsRunning probe")
	}
}

func TestEnsureVulpineAgentRouteSeedsDefaultAgentGroup(t *testing.T) {
	nanoclawDir := t.TempDir()
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
`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}

	if err := ensureVulpineAgentRoute(nanoclawDir, "agent-1"); err != nil {
		t.Fatalf("ensureVulpineAgentRoute: %v", err)
	}

	var groupID, name, folder string
	if err := db.QueryRow(`SELECT id, name, folder FROM agent_groups`).Scan(&groupID, &name, &folder); err != nil {
		t.Fatalf("query seeded group: %v", err)
	}
	if groupID != defaultNanoClawAgentGroupID || name != "VulpineOS" || folder != "vulpineos" {
		t.Fatalf("seeded group = %q %q %q, want %q VulpineOS vulpineos", groupID, name, folder, defaultNanoClawAgentGroupID)
	}

	var configCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM container_configs WHERE agent_group_id = ?`, groupID).Scan(&configCount); err != nil {
		t.Fatalf("query container config: %v", err)
	}
	if configCount != 1 {
		t.Fatalf("container config count = %d, want 1", configCount)
	}
}

func TestRepairVulpineProfileDatabaseUpsertsProviderModelAndBrowserRoute(t *testing.T) {
	nanoclawDir := t.TempDir()
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
INSERT INTO agent_groups (id, name, folder, created_at) VALUES ('ag-1', 'existing', 'existing', '2026-01-01T00:00:00Z');
INSERT INTO container_configs (agent_group_id, provider, model, updated_at, skills, mcp_servers, packages_apt, packages_npm, additional_mounts, cli_scope)
VALUES ('ag-1', 'claude', 'old-model', '2026-01-01T00:00:00Z', '["vulpine-browser"]', '{"browser":{"command":"vulpineos"}}', '["jq"]', '["left-pad"]', '[]', 'global');
`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}

	if err := RepairVulpineProfileDatabase(nanoclawDir, "opencode-go", "opencode-go/deepseek-v4-flash", "ws://127.0.0.1:9222/devtools/browser/foxbridge"); err != nil {
		t.Fatalf("RepairVulpineProfileDatabase: %v", err)
	}

	var provider, model, skills, mcpServers, cliScope string
	if err := db.QueryRow(`SELECT provider, model, skills, mcp_servers, cli_scope FROM container_configs WHERE agent_group_id = 'ag-1'`).Scan(&provider, &model, &skills, &mcpServers, &cliScope); err != nil {
		t.Fatalf("query updated config: %v", err)
	}
	if provider != "opencode" || model != "opencode-go/deepseek-v4-flash" {
		t.Fatalf("provider/model = %q/%q, want opencode/opencode-go/deepseek-v4-flash", provider, model)
	}
	if skills != `["vulpine-browser"]` || mcpServers != `{"browser":{"command":"vulpineos"}}` || cliScope != "global" {
		t.Fatalf("non-provider config changed: skills=%q mcp=%q cli_scope=%q", skills, mcpServers, cliScope)
	}

	browserConfig := filepath.Join(nanoclawDir, "groups", "existing", "agent-browser.json")
	data, err := os.ReadFile(browserConfig)
	if err != nil {
		t.Fatalf("read agent-browser config: %v", err)
	}
	var browser map[string]string
	if err := json.Unmarshal(data, &browser); err != nil {
		t.Fatalf("parse agent-browser config: %v", err)
	}
	if browser["cdp"] != "ws://host.docker.internal:9222/devtools/browser/foxbridge" {
		t.Fatalf("browser cdp = %q, want host.docker.internal route", browser["cdp"])
	}
}

func TestRepairVulpineProfileDatabaseRoutesOpenRouterThroughOpenCodeRunner(t *testing.T) {
	nanoclawDir := t.TempDir()
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
INSERT INTO agent_groups (id, name, folder, created_at) VALUES ('ag-1', 'existing', 'existing', '2026-01-01T00:00:00Z');
`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}

	model := "openrouter/nousresearch/hermes-3-llama-3.1-405b:free"
	if err := RepairVulpineProfileDatabase(nanoclawDir, "openrouter", model, ""); err != nil {
		t.Fatalf("RepairVulpineProfileDatabase: %v", err)
	}

	var provider, gotModel string
	if err := db.QueryRow(`SELECT provider, model FROM container_configs WHERE agent_group_id = 'ag-1'`).Scan(&provider, &gotModel); err != nil {
		t.Fatalf("query updated config: %v", err)
	}
	if provider != "opencode" || gotModel != model {
		t.Fatalf("provider/model = %q/%q, want opencode/%s", provider, gotModel, model)
	}
}

func TestNanoClawContainerProviderRoutesUnsupportedProvidersThroughOpenCode(t *testing.T) {
	for _, provider := range []string{"openai-oauth", "codex", "openrouter", "opencode-go"} {
		if got := nanoClawContainerProvider(provider); got != "opencode" {
			t.Fatalf("nanoClawContainerProvider(%q) = %q, want opencode", provider, got)
		}
	}
}

func TestEnsureNanoClawContainerConfigReportsProviderModelChanges(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE container_configs (agent_group_id TEXT PRIMARY KEY, provider TEXT, model TEXT, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	changed, err := ensureNanoClawContainerConfig(db, "ag-1", "openai-oauth", "openai/gpt-4.1")
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if !changed {
		t.Fatal("first ensure should report config change")
	}
	changed, err = ensureNanoClawContainerConfig(db, "ag-1", "openai-oauth", "openai/gpt-4.1")
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if changed {
		t.Fatal("second ensure with same config should not report change")
	}
	changed, err = ensureNanoClawContainerConfig(db, "ag-1", "deepseek", "deepseek/deepseek-v4-flash")
	if err != nil {
		t.Fatalf("deepseek ensure: %v", err)
	}
	if !changed {
		t.Fatal("provider/model switch should report config change")
	}
}

func TestNanoClawInstallLabelUsesProfileHash(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nanoclaw")
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("abs dir: %v", err)
	}
	sum := sha1.Sum([]byte(abs))
	want := "nanoclaw-install=" + hex.EncodeToString(sum[:])[:8]
	if got := nanoClawInstallLabel(dir); got != want {
		t.Fatalf("nanoClawInstallLabel() = %q, want %q", got, want)
	}
}
