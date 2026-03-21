package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigSaveLoad(t *testing.T) {
	// Create temp dir to act as config dir
	tmpDir, err := os.MkdirTemp("", "vulpine-config-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Write config directly to temp path
	configPath := filepath.Join(tmpDir, "config.json")

	cfg := &Config{
		Provider:      "anthropic",
		APIKey:        "sk-ant-test-key-12345",
		Model:         "anthropic/claude-sonnet-4-6",
		SetupComplete: true,
		BinaryPath:    "/usr/local/bin/camoufox",
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Load it back
	loadedData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var loaded Config
	if err := json.Unmarshal(loadedData, &loaded); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	if loaded.Provider != cfg.Provider {
		t.Errorf("provider = %q, want %q", loaded.Provider, cfg.Provider)
	}
	if loaded.APIKey != cfg.APIKey {
		t.Errorf("apiKey = %q, want %q", loaded.APIKey, cfg.APIKey)
	}
	if loaded.Model != cfg.Model {
		t.Errorf("model = %q, want %q", loaded.Model, cfg.Model)
	}
	if loaded.SetupComplete != cfg.SetupComplete {
		t.Errorf("setupComplete = %v, want %v", loaded.SetupComplete, cfg.SetupComplete)
	}
	if loaded.BinaryPath != cfg.BinaryPath {
		t.Errorf("binaryPath = %q, want %q", loaded.BinaryPath, cfg.BinaryPath)
	}
	if loaded.NeedsSetup() {
		t.Error("loaded config should not need setup")
	}
}

func TestConfigSkillManagement(t *testing.T) {
	cfg := &Config{}

	// AddGlobalSkill
	cfg.AddGlobalSkill("web-search", map[string]string{"SERP_API_KEY": "key123"})
	if len(cfg.GlobalSkills) != 1 {
		t.Fatalf("expected 1 global skill, got %d", len(cfg.GlobalSkills))
	}
	if cfg.GlobalSkills[0].Name != "web-search" {
		t.Errorf("skill name = %q, want 'web-search'", cfg.GlobalSkills[0].Name)
	}
	if !cfg.GlobalSkills[0].Enabled {
		t.Error("skill should be enabled")
	}
	if cfg.GlobalSkills[0].Env["SERP_API_KEY"] != "key123" {
		t.Errorf("env var = %q, want 'key123'", cfg.GlobalSkills[0].Env["SERP_API_KEY"])
	}

	// Add another skill
	cfg.AddGlobalSkill("code-runner", nil)
	if len(cfg.GlobalSkills) != 2 {
		t.Fatalf("expected 2 global skills, got %d", len(cfg.GlobalSkills))
	}

	// RemoveGlobalSkill (disables, doesn't delete)
	cfg.RemoveGlobalSkill("web-search")
	if cfg.GlobalSkills[0].Enabled {
		t.Error("web-search should be disabled after RemoveGlobalSkill")
	}
	if !cfg.GlobalSkills[1].Enabled {
		t.Error("code-runner should still be enabled")
	}

	// Re-add same skill should re-enable
	cfg.AddGlobalSkill("web-search", map[string]string{"SERP_API_KEY": "newkey"})
	if !cfg.GlobalSkills[0].Enabled {
		t.Error("web-search should be re-enabled")
	}
	if cfg.GlobalSkills[0].Env["SERP_API_KEY"] != "newkey" {
		t.Errorf("env var should be updated to 'newkey', got %q", cfg.GlobalSkills[0].Env["SERP_API_KEY"])
	}

	// AddAgentSkill
	cfg.AddAgentSkill("agent-001", "browser-use", nil)
	if cfg.AgentSkills == nil {
		t.Fatal("AgentSkills map should be initialized")
	}
	skills := cfg.AgentSkills["agent-001"]
	if len(skills) != 1 {
		t.Fatalf("expected 1 agent skill, got %d", len(skills))
	}
	if skills[0].Name != "browser-use" {
		t.Errorf("agent skill name = %q, want 'browser-use'", skills[0].Name)
	}

	// Add another skill to same agent
	cfg.AddAgentSkill("agent-001", "file-editor", map[string]string{"SANDBOX": "true"})
	skills = cfg.AgentSkills["agent-001"]
	if len(skills) != 2 {
		t.Fatalf("expected 2 agent skills, got %d", len(skills))
	}

	// Add skill to different agent
	cfg.AddAgentSkill("agent-002", "web-search", nil)
	if len(cfg.AgentSkills["agent-002"]) != 1 {
		t.Errorf("expected 1 skill for agent-002, got %d", len(cfg.AgentSkills["agent-002"]))
	}

	// RemoveGlobalSkill on non-existent should be a no-op
	cfg.RemoveGlobalSkill("nonexistent-skill")
	if len(cfg.GlobalSkills) != 2 {
		t.Errorf("skill count should not change for non-existent removal")
	}
}

func TestGenerateOpenClawConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vulpine-openclaw-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a skills directory so it gets included
	skillDir := filepath.Join(tmpDir, "skills")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Provider:      "anthropic",
		APIKey:        "sk-ant-test-key-99999",
		Model:         "anthropic/claude-sonnet-4-6",
		SetupComplete: true,
		GlobalSkills: []SkillEntry{
			{Name: "web-search", Enabled: true, Env: map[string]string{"KEY": "val"}},
			{Name: "disabled-skill", Enabled: false},
		},
	}

	// GenerateOpenClawConfig writes to Dir() which we can't easily override,
	// so we test the output shape by calling it and checking the file.
	err = cfg.GenerateOpenClawConfig("/usr/local/bin/vulpineos", "/usr/local/bin/camoufox")
	if err != nil {
		t.Fatalf("GenerateOpenClawConfig: %v", err)
	}

	// Read the generated file
	ocPath := OpenClawConfigPath()
	data, err := os.ReadFile(ocPath)
	if err != nil {
		t.Fatalf("read openclaw.json: %v", err)
	}
	defer os.Remove(ocPath)

	var oc map[string]interface{}
	if err := json.Unmarshal(data, &oc); err != nil {
		t.Fatalf("openclaw.json not valid JSON: %v", err)
	}

	// Verify browser.enabled is false
	browser, ok := oc["browser"].(map[string]interface{})
	if !ok {
		t.Fatal("missing browser section")
	}
	if browser["enabled"] != false {
		t.Errorf("browser.enabled = %v, want false", browser["enabled"])
	}

	// Verify plugins.mcp.servers.vulpineos exists
	plugins, ok := oc["plugins"].(map[string]interface{})
	if !ok {
		t.Fatal("missing plugins section")
	}
	mcpSection, ok := plugins["mcp"].(map[string]interface{})
	if !ok {
		t.Fatal("missing plugins.mcp section")
	}
	servers, ok := mcpSection["servers"].(map[string]interface{})
	if !ok {
		t.Fatal("missing plugins.mcp.servers section")
	}
	vulpServer, ok := servers["vulpineos"].(map[string]interface{})
	if !ok {
		t.Fatal("missing plugins.mcp.servers.vulpineos")
	}
	if vulpServer["command"] != "/usr/local/bin/vulpineos" {
		t.Errorf("command = %v, want /usr/local/bin/vulpineos", vulpServer["command"])
	}

	// Verify args include --mcp-server and --binary and --headless
	args, ok := vulpServer["args"].([]interface{})
	if !ok {
		t.Fatal("missing args array")
	}
	argStrs := make([]string, len(args))
	for i, a := range args {
		argStrs[i], _ = a.(string)
	}
	hasFlag := func(flag string) bool {
		for _, a := range argStrs {
			if a == flag {
				return true
			}
		}
		return false
	}
	if !hasFlag("--mcp-server") {
		t.Error("args missing --mcp-server")
	}
	if !hasFlag("--headless") {
		t.Error("args missing --headless")
	}
	if !hasFlag("--binary") {
		t.Error("args missing --binary")
	}

	// Verify env contains ANTHROPIC_API_KEY
	env, ok := oc["env"].(map[string]interface{})
	if !ok {
		t.Fatal("missing env section")
	}
	if env["ANTHROPIC_API_KEY"] != "sk-ant-test-key-99999" {
		t.Errorf("env ANTHROPIC_API_KEY = %v, want sk-ant-test-key-99999", env["ANTHROPIC_API_KEY"])
	}

	// Verify skills.entries contains global skills
	skills, ok := oc["skills"].(map[string]interface{})
	if !ok {
		t.Fatal("missing skills section")
	}
	entries, ok := skills["entries"].(map[string]interface{})
	if !ok {
		t.Fatal("missing skills.entries")
	}
	ws, ok := entries["web-search"].(map[string]interface{})
	if !ok {
		t.Fatal("missing skills.entries.web-search")
	}
	if ws["enabled"] != true {
		t.Errorf("web-search enabled = %v, want true", ws["enabled"])
	}
	ds, ok := entries["disabled-skill"].(map[string]interface{})
	if !ok {
		t.Fatal("missing skills.entries.disabled-skill")
	}
	if ds["enabled"] != false {
		t.Errorf("disabled-skill enabled = %v, want false", ds["enabled"])
	}

	// Verify model is set
	agents, ok := oc["agents"].(map[string]interface{})
	if !ok {
		t.Fatal("missing agents section")
	}
	defaults, ok := agents["defaults"].(map[string]interface{})
	if !ok {
		t.Fatal("missing agents.defaults")
	}
	model, ok := defaults["model"].(map[string]interface{})
	if !ok {
		t.Fatal("missing agents.defaults.model")
	}
	if model["primary"] != "anthropic/claude-sonnet-4-6" {
		t.Errorf("model.primary = %v, want anthropic/claude-sonnet-4-6", model["primary"])
	}
}

func TestGenerateOpenClawConfig_UnknownProvider(t *testing.T) {
	cfg := &Config{
		Provider: "nonexistent-provider",
		APIKey:   "key",
		Model:    "model",
	}
	err := cfg.GenerateOpenClawConfig("/bin/vulpineos", "")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestGetProvider(t *testing.T) {
	p := GetProvider("anthropic")
	if p == nil {
		t.Fatal("expected non-nil provider for 'anthropic'")
	}
	if p.Name != "Anthropic (Claude)" {
		t.Errorf("name = %q, want 'Anthropic (Claude)'", p.Name)
	}
	if p.EnvVar != "ANTHROPIC_API_KEY" {
		t.Errorf("envVar = %q, want ANTHROPIC_API_KEY", p.EnvVar)
	}

	p = GetProvider("nonexistent")
	if p != nil {
		t.Error("expected nil for unknown provider")
	}

	// Ollama doesn't need a key
	p = GetProvider("ollama")
	if p == nil {
		t.Fatal("expected non-nil provider for 'ollama'")
	}
	if p.NeedsKey {
		t.Error("ollama should not need a key")
	}
}

func TestCustomProvider(t *testing.T) {
	cp := CustomProvider("my-provider", "MY_KEY")
	if cp.ID != "my-provider" {
		t.Errorf("id = %q, want 'my-provider'", cp.ID)
	}
	if !cp.NeedsKey {
		t.Error("custom provider with envVar should need key")
	}

	cp2 := CustomProvider("local-llm", "")
	if cp2.NeedsKey {
		t.Error("custom provider without envVar should not need key")
	}
}

func TestNeedsSetup(t *testing.T) {
	cfg := &Config{}
	if !cfg.NeedsSetup() {
		t.Error("empty config should need setup")
	}

	cfg.SetupComplete = true
	if cfg.NeedsSetup() {
		t.Error("config with setupComplete should not need setup")
	}
}
