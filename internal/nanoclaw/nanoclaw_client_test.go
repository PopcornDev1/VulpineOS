package nanoclaw

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
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
	if payload["text"] != "hello" {
		t.Fatalf("text = %v, want hello", payload["text"])
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
}
