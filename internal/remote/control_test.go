package remote

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vulpineos/internal/config"
	"vulpineos/internal/nanoclaw"
	"vulpineos/internal/vault"
)

func openControlTestVault(t *testing.T) *vault.DB {
	t.Helper()
	db, err := vault.OpenPath(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func callControl[T any](t *testing.T, api *ControlAPI, method string, params any) T {
	t.Helper()
	rawParams, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	raw, err := api.HandleMessage(method, rawParams)
	if err != nil {
		t.Fatalf("HandleMessage(%s): %v", method, err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return out
}

func TestControlAPISettingsGetReturnsConfigAndProxies(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db := openControlTestVault(t)
	pc := `{"type":"http","host":"127.0.0.1","port":8080,"username":"user","password":"pass"}`
	proxyRow, err := db.AddProxy(pc, `{"country":"US"}`, "http://user:pass@127.0.0.1:8080")
	if err != nil {
		t.Fatalf("AddProxy: %v", err)
	}
	cfg := &config.Config{
		Provider:      "anthropic",
		APIKey:        "sk-secret",
		Model:         "anthropic/claude-sonnet-4-6",
		SetupComplete: true,
		GlobalSkills:  []config.SkillEntry{{Name: "vulpine-browser", Enabled: true}},
	}
	api := &ControlAPI{Config: cfg, Vault: db}

	var out struct {
		Config struct {
			Provider      string `json:"provider"`
			Model         string `json:"model"`
			APIKeySet     bool   `json:"apiKeySet"`
			SetupComplete bool   `json:"setupComplete"`
		} `json:"config"`
		Proxies []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Type    string `json:"type"`
			Host    string `json:"host"`
			Port    int    `json:"port"`
			Country string `json:"country"`
		} `json:"proxies"`
		Skills []config.SkillEntry `json:"skills"`
	}
	out = callControl[struct {
		Config struct {
			Provider      string `json:"provider"`
			Model         string `json:"model"`
			APIKeySet     bool   `json:"apiKeySet"`
			SetupComplete bool   `json:"setupComplete"`
		} `json:"config"`
		Proxies []struct {
			ID      string `json:"id"`
			Label   string `json:"label"`
			Type    string `json:"type"`
			Host    string `json:"host"`
			Port    int    `json:"port"`
			Country string `json:"country"`
		} `json:"proxies"`
		Skills []config.SkillEntry `json:"skills"`
	}](t, api, "settings.get", map[string]any{})

	if out.Config.Provider != "anthropic" || out.Config.Model != "anthropic/claude-sonnet-4-6" || !out.Config.APIKeySet || !out.Config.SetupComplete {
		t.Fatalf("config result = %+v", out.Config)
	}
	if len(out.Proxies) != 1 || out.Proxies[0].ID != proxyRow.ID || out.Proxies[0].Type != "http" || out.Proxies[0].Host != "127.0.0.1" || out.Proxies[0].Port != 8080 || out.Proxies[0].Country != "US" {
		t.Fatalf("proxies = %#v", out.Proxies)
	}
	proxyJSON, _ := json.Marshal(out.Proxies)
	if strings.Contains(string(proxyJSON), "user") || strings.Contains(string(proxyJSON), "pass") {
		t.Fatalf("proxy summary leaked credentials: %s", proxyJSON)
	}
	if len(out.Skills) != 1 || out.Skills[0].Name != "vulpine-browser" || !out.Skills[0].Enabled {
		t.Fatalf("skills = %#v", out.Skills)
	}
}

func TestControlAPIConfigSetPreservesBlankAPIKeyAndRegeneratesNanoClawConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := &config.Config{
		Provider:      "anthropic",
		APIKey:        "sk-existing",
		Model:         "anthropic/claude-sonnet-4-6",
		SetupComplete: true,
	}
	api := &ControlAPI{Config: cfg}

	out := callControl[struct {
		Provider      string `json:"provider"`
		Model         string `json:"model"`
		APIKeySet     bool   `json:"apiKeySet"`
		SetupComplete bool   `json:"setupComplete"`
	}](t, api, "config.set", map[string]any{
		"provider": "anthropic",
		"model":    "anthropic/claude-opus-4-6",
		"apiKey":   "",
	})

	if out.Provider != "anthropic" || out.Model != "anthropic/claude-opus-4-6" || !out.APIKeySet || !out.SetupComplete {
		t.Fatalf("config.set result = %+v", out)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.APIKey != "sk-existing" || loaded.Model != "anthropic/claude-opus-4-6" {
		t.Fatalf("saved config = %#v", loaded)
	}
	data, err := os.ReadFile(config.NanoClawConfigPath())
	if err != nil {
		t.Fatalf("read nanoclaw config: %v", err)
	}
	if !strings.Contains(string(data), "sk-existing") || !strings.Contains(string(data), "anthropic/claude-opus-4-6") {
		t.Fatalf("nanoclaw config was not regenerated with preserved key/model: %s", data)
	}
}

func TestControlAPIStatusGetHandlesTypedNilDaemon(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	api := &ControlAPI{Daemon: (*nanoclaw.Daemon)(nil)}

	out := callControl[struct {
		NanoClawDaemonRunning bool   `json:"nanoclaw_daemon_running"`
		BrowserRoute          string `json:"browser_route"`
	}](t, api, "status.get", map[string]any{})

	if out.NanoClawDaemonRunning {
		t.Fatal("nanoclaw daemon should report stopped for typed nil daemon")
	}
	if out.BrowserRoute != "disabled" {
		t.Fatalf("browser route = %q, want disabled", out.BrowserRoute)
	}
}

func TestControlAPIAgentsCreateDoesNotStartFirstTurn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db := openControlTestVault(t)
	api := &ControlAPI{Vault: db}

	out := callControl[struct {
		AgentID string `json:"agentId"`
	}](t, api, "agents.create", map[string]any{
		"name": "Remote Builder",
		"task": "Wait for user message",
	})
	if strings.TrimSpace(out.AgentID) == "" {
		t.Fatal("agents.create returned empty agentId")
	}

	agent, err := db.GetAgent(out.AgentID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	if agent.Status != "ready" {
		t.Fatalf("agent status = %q, want ready", agent.Status)
	}
	msgs, err := db.GetMessages(out.AgentID)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("agent messages = %#v, want none before first user message", msgs)
	}
}

func TestControlAPIAgentSessionLogRedactsSecrets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db := openControlTestVault(t)
	agent, err := db.CreateAgent("agent", "task", "{}")
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	agentID := agent.ID
	logPath := filepath.Join(config.NanoClawProfileDir(), "agents", "main", "sessions", "vulpine-"+agentID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	rawLog := `{"message":"Authorization: Bearer secret-token","url":"https://example.test/?token=query-secret","payload":{"apiKey":"json-secret"}}` + "\n"
	if err := os.WriteFile(logPath, []byte(rawLog), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	api := &ControlAPI{Vault: db}

	out := callControl[struct {
		Content string `json:"content"`
	}](t, api, "agents.getSessionLog", map[string]any{"agentId": agentID})

	for _, leaked := range []string{"secret-token", "query-secret", "json-secret"} {
		if strings.Contains(out.Content, leaked) {
			t.Fatalf("session log leaked %q: %s", leaked, out.Content)
		}
	}
	if !strings.Contains(out.Content, "Bearer [redacted]") || !strings.Contains(out.Content, "token=[redacted]") {
		t.Fatalf("session log was not redacted as expected: %s", out.Content)
	}
}
