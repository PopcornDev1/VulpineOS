package vault

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	f, err := os.CreateTemp("", "vault-agent-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	db, err := OpenPath(f.Name())
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCreateAndGetAgent(t *testing.T) {
	db := openTestDB(t)

	fp, err := GenerateFingerprint("test-seed")
	if err != nil {
		t.Fatalf("generate fingerprint: %v", err)
	}

	agent, err := db.CreateAgent("ResearchBot", "Search the web", fp)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	if agent.Name != "ResearchBot" {
		t.Errorf("expected name 'ResearchBot', got '%s'", agent.Name)
	}
	if agent.Task != "Search the web" {
		t.Errorf("expected task 'Search the web', got '%s'", agent.Task)
	}
	if agent.Status != "created" {
		t.Errorf("expected status 'created', got '%s'", agent.Status)
	}
	if agent.ID == "" {
		t.Error("expected non-empty ID")
	}

	// Round-trip via GetAgent
	got, err := db.GetAgent(agent.ID)
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if got.Name != agent.Name {
		t.Errorf("name mismatch: got '%s', want '%s'", got.Name, agent.Name)
	}
	if got.Task != agent.Task {
		t.Errorf("task mismatch: got '%s', want '%s'", got.Task, agent.Task)
	}
	if got.Fingerprint != fp {
		t.Errorf("fingerprint mismatch")
	}
	if got.Status != "created" {
		t.Errorf("status mismatch: got '%s', want 'created'", got.Status)
	}
	if got.Metadata != "{}" {
		t.Errorf("metadata mismatch: got '%s', want '{}'", got.Metadata)
	}
}

func TestListAgentsOrder(t *testing.T) {
	db := openTestDB(t)

	a1, err := db.CreateAgent("First", "task1", "{}")
	if err != nil {
		t.Fatalf("create agent 1: %v", err)
	}

	a2, err := db.CreateAgent("Second", "task2", "{}")
	if err != nil {
		t.Fatalf("create agent 2: %v", err)
	}

	// Force a2 to have a later last_active by setting it directly
	_, err = db.conn.Exec(`UPDATE agents SET last_active = ? WHERE id = ?`,
		time.Now().Unix()+100, a2.ID)
	if err != nil {
		t.Fatal(err)
	}

	agents, err := db.ListAgents()
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	// Most recently active first
	if agents[0].ID != a2.ID {
		t.Errorf("expected second agent first, got %s", agents[0].Name)
	}
	if agents[1].ID != a1.ID {
		t.Errorf("expected first agent second, got %s", agents[1].Name)
	}
}

func TestListAgentsByStatus(t *testing.T) {
	db := openTestDB(t)

	_, err := db.CreateAgent("Bot1", "task1", "{}")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := db.CreateAgent("Bot2", "task2", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateAgentStatus(a2.ID, "active"); err != nil {
		t.Fatal(err)
	}

	active, err := db.ListAgentsByStatus("active")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != a2.ID {
		t.Errorf("expected 1 active agent (Bot2), got %d", len(active))
	}

	created, err := db.ListAgentsByStatus("created")
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 {
		t.Errorf("expected 1 created agent, got %d", len(created))
	}
}

func TestAppendAndGetMessages(t *testing.T) {
	db := openTestDB(t)

	agent, err := db.CreateAgent("ChatBot", "chat", "{}")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.AppendMessage(agent.ID, "user", "Hello", 5); err != nil {
		t.Fatalf("append msg 1: %v", err)
	}
	if err := db.AppendMessage(agent.ID, "assistant", "Hi there!", 8); err != nil {
		t.Fatalf("append msg 2: %v", err)
	}
	if err := db.AppendMessage(agent.ID, "user", "How are you?", 6); err != nil {
		t.Fatalf("append msg 3: %v", err)
	}

	msgs, err := db.GetMessages(agent.ID)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "Hello" {
		t.Errorf("msg[0] mismatch: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "Hi there!" {
		t.Errorf("msg[1] mismatch: %+v", msgs[1])
	}
	if msgs[2].Role != "user" || msgs[2].Content != "How are you?" {
		t.Errorf("msg[2] mismatch: %+v", msgs[2])
	}
	if msgs[0].Tokens != 5 {
		t.Errorf("expected 5 tokens, got %d", msgs[0].Tokens)
	}
}

func TestGetRecentMessages(t *testing.T) {
	db := openTestDB(t)

	agent, err := db.CreateAgent("ChatBot", "chat", "{}")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		if err := db.AppendMessage(agent.ID, "user", "msg", 1); err != nil {
			t.Fatal(err)
		}
	}

	recent, err := db.GetRecentMessages(agent.ID, 3)
	if err != nil {
		t.Fatalf("get recent: %v", err)
	}
	if len(recent) != 3 {
		t.Fatalf("expected 3 recent messages, got %d", len(recent))
	}
	// Should be in chronological order (oldest of the 3 first)
	if recent[0].ID > recent[1].ID || recent[1].ID > recent[2].ID {
		t.Errorf("messages not in chronological order: %d, %d, %d",
			recent[0].ID, recent[1].ID, recent[2].ID)
	}
}

func TestDeleteAgentCascadesMessages(t *testing.T) {
	db := openTestDB(t)

	agent, err := db.CreateAgent("EphemeralBot", "task", "{}")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.AppendMessage(agent.ID, "user", "hello", 1); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendMessage(agent.ID, "assistant", "world", 1); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteAgent(agent.ID); err != nil {
		t.Fatalf("delete agent: %v", err)
	}

	// Agent should be gone
	_, err = db.GetAgent(agent.ID)
	if err == nil {
		t.Error("expected error getting deleted agent")
	}

	// Messages should be gone (cascade)
	msgs, err := db.GetMessages(agent.ID)
	if err != nil {
		t.Fatalf("get messages after delete: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after cascade delete, got %d", len(msgs))
	}
}

func TestUpdateAgentStatus(t *testing.T) {
	db := openTestDB(t)

	agent, err := db.CreateAgent("Bot", "task", "{}")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateAgentStatus(agent.ID, "active"); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, err := db.GetAgent(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" {
		t.Errorf("expected status 'active', got '%s'", got.Status)
	}
	if got.LastActive.Before(agent.LastActive) {
		t.Error("expected last_active to be updated")
	}
}

func TestUpdateAgentTokens(t *testing.T) {
	db := openTestDB(t)

	agent, err := db.CreateAgent("Bot", "task", "{}")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateAgentTokens(agent.ID, 42000); err != nil {
		t.Fatalf("update tokens: %v", err)
	}

	got, err := db.GetAgent(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalTokens != 42000 {
		t.Errorf("expected 42000 tokens, got %d", got.TotalTokens)
	}
}

func TestGenerateFingerprintValidJSON(t *testing.T) {
	fp, err := GenerateFingerprint("test-agent-id")
	if err != nil {
		t.Fatalf("generate fingerprint: %v", err)
	}

	// Must be valid JSON
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(fp), &data); err != nil {
		t.Fatalf("fingerprint not valid JSON: %v", err)
	}

	// Must have expected fields (Camoufox config format with dot-separated keys)
	requiredFields := []string{"navigator.userAgent", "navigator.platform", "screen.width", "screen.height"}
	for _, field := range requiredFields {
		if _, ok := data[field]; !ok {
			t.Errorf("missing field: %s", field)
		}
	}

	// OS consistency: platform and UA must agree
	ua, _ := data["navigator.userAgent"].(string)
	platform, _ := data["navigator.platform"].(string)
	if strings.Contains(platform, "Win") && !strings.Contains(ua, "Windows") {
		t.Error("platform says Windows but UA doesn't")
	}
	if strings.Contains(platform, "Mac") && !strings.Contains(ua, "Mac") {
		t.Error("platform says Mac but UA doesn't")
	}
	if strings.Contains(platform, "Linux") && !strings.Contains(ua, "Linux") {
		t.Error("platform says Linux but UA doesn't")
	}

	// Note: determinism only guaranteed for fallback generator.
	// BrowserForge uses its own RNG so results vary.

	// Different seed -> different fingerprint (with very high probability)
	fp3, err := GenerateFingerprint("other-agent-id")
	if err != nil {
		t.Fatal(err)
	}
	if fp == fp3 {
		t.Error("different seeds should produce different fingerprints")
	}
}

func TestFingerprintSummary(t *testing.T) {
	fp, err := GenerateFingerprint("summary-test")
	if err != nil {
		t.Fatal(err)
	}

	summary := FingerprintSummary(fp)
	if summary == "" || summary == "unknown" {
		t.Errorf("expected readable summary, got '%s'", summary)
	}

	// Should contain OS, browser, and resolution
	if !strings.Contains(summary, "/") {
		t.Errorf("summary should contain '/' separators, got '%s'", summary)
	}
	if !strings.Contains(summary, "x") {
		t.Errorf("summary should contain resolution with 'x', got '%s'", summary)
	}

	// Invalid JSON should return fallback
	bad := FingerprintSummary("not json")
	if bad != "unknown" {
		t.Errorf("expected 'unknown' for bad JSON, got '%s'", bad)
	}
}
