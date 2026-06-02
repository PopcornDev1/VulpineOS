package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func withTempHome(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tmpHome); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})
	return tmpHome
}

func requireFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

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

func TestConfigSaveRepairsExistingPermissions(t *testing.T) {
	withTempHome(t)
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(Path(), []byte(`{"apiKey":"loose"}`), 0644); err != nil {
		t.Fatalf("write loose config: %v", err)
	}

	cfg := &Config{
		Provider:      "anthropic",
		APIKey:        "sk-ant-test-key-12345",
		Model:         "anthropic/claude-sonnet-4-6",
		SetupComplete: true,
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	requireFileMode(t, Path(), 0600)
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

func TestReconfigureRequestLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if ReconfigureRequested() {
		t.Fatal("reconfigure request should start cleared")
	}
	if err := RequestReconfigure(); err != nil {
		t.Fatalf("RequestReconfigure: %v", err)
	}
	if !ReconfigureRequested() {
		t.Fatal("reconfigure request should be visible after RequestReconfigure")
	}
	if err := ClearReconfigureRequest(); err != nil {
		t.Fatalf("ClearReconfigureRequest: %v", err)
	}
	if ReconfigureRequested() {
		t.Fatal("reconfigure request should be cleared")
	}
}
