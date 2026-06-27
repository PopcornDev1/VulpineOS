package remote

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"vulpineos/internal/config"
	"vulpineos/internal/juggler"
	"vulpineos/internal/orchestrator"
	"vulpineos/internal/pool"
	"vulpineos/internal/runtimeaudit"
	"vulpineos/internal/testutil"
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

func TestControlAPIConfigSetPreservesBlankAPIKey(t *testing.T) {
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
}

func TestControlAPICaptchaConfigSummaryAndSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := &config.Config{
		Provider:      "anthropic",
		APIKey:        "sk-existing",
		Model:         "anthropic/claude-sonnet-4-6",
		SetupComplete: true,
		Captcha: config.CaptchaConfig{
			Enabled:          true,
			Provider:         "customer-key",
			ConfirmPolicy:    "per_domain",
			AllowedDomains:   []string{"example.com"},
			SendScreenshots:  false,
			TimeoutSeconds:   30,
			MaxSolvesPerHour: 4,
			MaxCostCents:     50,
		},
	}
	api := &ControlAPI{Config: cfg}

	settings := callControl[struct {
		Config struct {
			Captcha config.CaptchaConfig `json:"captcha"`
		} `json:"config"`
	}](t, api, "settings.get", map[string]any{})
	if !settings.Config.Captcha.Enabled || settings.Config.Captcha.Provider != "customer-key" || settings.Config.Captcha.AllowedDomains[0] != "example.com" {
		t.Fatalf("settings captcha summary = %#v", settings.Config.Captcha)
	}
	settingsJSON, _ := json.Marshal(settings)
	for _, leaked := range []string{"solver-secret", "apiKey"} {
		if strings.Contains(string(settingsJSON), leaked) {
			t.Fatalf("settings leaked captcha secret marker %q: %s", leaked, settingsJSON)
		}
	}

	out := callControl[struct {
		Captcha config.CaptchaConfig `json:"captcha"`
	}](t, api, "config.set", map[string]any{
		"provider": "anthropic",
		"model":    "anthropic/claude-sonnet-4-6",
		"apiKey":   "",
		"captcha": map[string]any{
			"enabled":          true,
			"provider":         "managed",
			"confirmPolicy":    "always",
			"allowedDomains":   []string{"trusted.example"},
			"sendScreenshots":  true,
			"timeoutSeconds":   60,
			"maxSolvesPerHour": 2,
			"maxCostCents":     25,
		},
	})
	if out.Captcha.Provider != "managed" || out.Captcha.ConfirmPolicy != "always" || !out.Captcha.SendScreenshots {
		t.Fatalf("config.set captcha result = %#v", out.Captcha)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Captcha.Provider != "managed" || loaded.Captcha.AllowedDomains[0] != "trusted.example" {
		t.Fatalf("saved captcha config = %#v", loaded.Captcha)
	}
	if loaded.APIKey != "sk-existing" {
		t.Fatalf("blank api key should preserve existing key, got %q", loaded.APIKey)
	}
}

func TestControlAPIStatusGetReportsNativeRuntime(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	api := &ControlAPI{}

	out := callControl[struct {
		AgentRuntime string `json:"agent_runtime"`
		BrowserRoute string `json:"browser_route"`
	}](t, api, "status.get", map[string]any{})

	if out.AgentRuntime != "native" {
		t.Fatalf("agent runtime = %q, want native", out.AgentRuntime)
	}
	if out.BrowserRoute != "disabled" {
		t.Fatalf("browser route = %q, want disabled", out.BrowserRoute)
	}
}

func TestControlAPIStatusGetIncludesLatestBrowserObservation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db := openControlTestVault(t)
	audit := runtimeaudit.New(db)
	if _, err := audit.Log("agentcore", "warn", "browser_observation_lost", "browser observation lost", map[string]string{
		"agent_id": "agent-1",
		"url":      "about:blank",
	}); err != nil {
		t.Fatalf("Log: %v", err)
	}
	api := &ControlAPI{Audit: audit}

	out := callControl[struct {
		Observation struct {
			Component string            `json:"component"`
			Event     string            `json:"event"`
			Level     string            `json:"level"`
			Metadata  map[string]string `json:"metadata"`
		} `json:"browser_observation"`
	}](t, api, "status.get", map[string]any{})

	if out.Observation.Event != "browser_observation_lost" || out.Observation.Level != "warn" {
		t.Fatalf("observation = %#v", out.Observation)
	}
	if out.Observation.Component != "agentcore" {
		t.Fatalf("observation component = %q, want agentcore", out.Observation.Component)
	}
	if out.Observation.Metadata["url"] != "about:blank" {
		t.Fatalf("observation metadata = %#v", out.Observation.Metadata)
	}
}

func TestControlAPIStatusGetReportsLatestUnresolvedBrowserObservation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db := openControlTestVault(t)
	audit := runtimeaudit.New(db)
	if _, err := audit.Log("agentcore", "warn", "browser_observation_lost", "agent A lost", map[string]string{
		"agent_id": "agent-a",
		"url":      "about:blank",
	}); err != nil {
		t.Fatalf("Log lost: %v", err)
	}
	if _, err := audit.Log("agentcore", "info", "browser_observation_observed", "agent B observed", map[string]string{
		"agent_id": "agent-b",
		"url":      "https://example.test",
	}); err != nil {
		t.Fatalf("Log observed: %v", err)
	}
	api := &ControlAPI{Audit: audit}

	out := callControl[struct {
		Observation struct {
			Event    string            `json:"event"`
			Metadata map[string]string `json:"metadata"`
		} `json:"browser_observation"`
	}](t, api, "status.get", map[string]any{})

	if out.Observation.Event != "browser_observation_lost" {
		t.Fatalf("observation event = %q, want unresolved lost warning", out.Observation.Event)
	}
	if out.Observation.Metadata["agent_id"] != "agent-a" {
		t.Fatalf("observation metadata = %#v, want agent-a", out.Observation.Metadata)
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

func TestControlAPIAgentRuntimeConfigCreatesScopedAgentContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondJSON("Browser.createBrowserContext", map[string]string{"browserContextId": "ctx-remote-runtime"})
	fake.RespondJSON("Browser.enable", map[string]any{})
	fake.RespondJSON("Browser.setUserAgentOverride", map[string]any{})
	fake.RespondJSON("Browser.setPlatformOverride", map[string]any{})
	fake.RespondJSON("Browser.setDefaultViewport", map[string]any{})
	fake.RespondJSON("Browser.setLocaleOverride", map[string]any{})
	fake.RespondJSON("Browser.setTimezoneOverride", map[string]any{})
	fake.RespondJSON("Browser.setExtraHTTPHeaders", map[string]any{})

	client := juggler.NewClient(fake)
	defer client.Close()

	db := openControlTestVault(t)
	fp, err := vault.GenerateFingerprint("remote-runtime")
	if err != nil {
		t.Fatalf("GenerateFingerprint: %v", err)
	}
	agent, err := db.CreateAgent("remote-runtime", "use browser", fp)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	orch := orchestrator.New(nil, client, db, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 1})
	if err := orch.Pool.Start(); err != nil {
		t.Fatalf("pool start: %v", err)
	}
	defer orch.Close()

	api := &ControlAPI{
		Config:       &config.Config{FoxbridgeCDPURL: "ws://127.0.0.1:9222"},
		Vault:        db,
		Orchestrator: orch,
		FoxbridgeRunning: func() bool {
			return false
		},
	}
	path, cleanup, err := api.agentRuntimeConfig(agent)
	if err != nil {
		t.Fatalf("agentRuntimeConfig: %v", err)
	}
	defer cleanup()

	// The native runtime drives the pooled context directly and needs no config file.
	if path != "" {
		t.Fatalf("native runtime needs no config file, got path %q", path)
	}

	persisted, err := db.GetAgent(agent.ID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	meta, err := vault.ParseAgentMetadata(persisted.Metadata)
	if err != nil {
		t.Fatalf("ParseAgentMetadata: %v", err)
	}
	if meta.ContextID != "ctx-remote-runtime" {
		t.Fatalf("contextID = %q, want ctx-remote-runtime", meta.ContextID)
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
	// The native runtime persists turns to the vault; the session log is rendered
	// from there and redacted before leaving the host.
	raw := `Authorization: Bearer secret-token url=https://example.test/?token=query-secret apiKey=json-secret`
	if err := db.AppendMessage(agentID, "assistant", raw, 0); err != nil {
		t.Fatalf("AppendMessage: %v", err)
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
