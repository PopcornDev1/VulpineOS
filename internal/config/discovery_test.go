package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindNanoClawBinary(t *testing.T) {
	path := findNanoClawBinary()
	if path == "" {
		t.Skip("nanoclaw binary not found")
	}
	if !isNanoClawBinaryPath(path) {
		t.Errorf("findNanoClawBinary(): got %q, want nanoclaw or ncl binary", path)
	}
}

func TestFindNanoClawBinaryAcceptsNCL(t *testing.T) {
	tmpDir := t.TempDir()
	bin := tmpDir + "/ncl"
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatalf("write ncl: %v", err)
	}
	t.Setenv("PATH", tmpDir)

	path := findNanoClawBinary()
	if path != bin {
		t.Fatalf("findNanoClawBinary() = %q, want %q", path, bin)
	}
}

func TestDiscoverModels(t *testing.T) {
	path := findNanoClawBinary()
	if path == "" {
		t.Skip("nanoclaw binary not found")
	}

	result, err := DiscoverModels()
	if err != nil {
		t.Fatalf("DiscoverModels(): %v", err)
	}
	if len(result.Providers) == 0 {
		t.Fatal("DiscoverModels(): no providers returned")
	}
	var opencodeProvider *DiscoveredProvider
	for _, p := range result.Providers {
		if p.ID == "opencode" {
			opencodeProvider = &p
			break
		}
	}
	if opencodeProvider == nil {
		t.Fatal("DiscoverModels(): no opencode provider found")
	}
	if len(opencodeProvider.Models) < 3 {
		t.Errorf("opencode provider has only %d models, want at least 3", len(opencodeProvider.Models))
	}

	var opencodeGo *DiscoveredProvider
	for _, p := range result.Providers {
		if p.ID == "opencode-go" {
			opencodeGo = &p
			break
		}
	}
	if opencodeGo == nil {
		t.Error("DiscoverModels(): opencode-go not in runtime-discovered providers")
	}

	hasCredentials := false
	for _, m := range opencodeProvider.Models {
		if strings.Contains(m.Key, ":") && strings.Count(m.Key, "/") != 1 {
			hasCredentials = true
		}
		if m.Name == "" {
			t.Errorf("opencode model %q has empty name", m.Key)
		}
	}
	if hasCredentials {
		t.Error("opencode model keys should not contain embedded credentials")
	}
}

func isNanoClawBinaryPath(path string) bool {
	base := filepath.Base(path)
	return strings.Contains(path, "nanoclaw") || base == "ncl"
}

func TestMergedProviders(t *testing.T) {
	merged := MergedProviders()
	if len(merged) == 0 {
		t.Fatal("MergedProviders(): no providers")
	}

	ids := make(map[string]bool)
	for _, p := range merged {
		if ids[p.ID] {
			t.Errorf("duplicate provider ID: %s", p.ID)
		}
		ids[p.ID] = true
	}

	var opencodeIdx, opencodeGoIdx int = -1, -1
	for i, p := range merged {
		if p.ID == "opencode" {
			opencodeIdx = i
		}
		if p.ID == "opencode-go" {
			opencodeGoIdx = i
		}
	}
	if opencodeIdx == -1 {
		t.Error("opencode not in merged providers")
	}
	if opencodeGoIdx == -1 {
		t.Error("opencode-go not in merged providers")
	}
	if opencodeGoIdx < opencodeIdx {
		t.Error("opencode-go should come after opencode in merged list")
	}
}

func TestOpenCodeProviderIncludesFreeZenModels(t *testing.T) {
	provider := GetProvider("opencode")
	if provider == nil {
		t.Fatal("opencode provider not found")
	}
	for _, want := range []string{"opencode/minimax-m2.5", "opencode/deepseek-v4"} {
		if !containsString(provider.Models, want) {
			t.Fatalf("opencode models = %#v, want %s", provider.Models, want)
		}
	}
	if provider.DefaultModel != "opencode/minimax-m2.5" {
		t.Fatalf("opencode default = %q, want free Zen default", provider.DefaultModel)
	}

	goProvider := GetProvider("opencode-go")
	if goProvider == nil || !containsString(goProvider.Models, "opencode-go/deepseek-v4") {
		t.Fatalf("opencode-go models = %#v, want deepseek v4", goProvider)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestProviderDisplayName(t *testing.T) {
	tests := map[string]string{
		"opencode":         "OpenCode (Zen)",
		"opencode-go":      "OpenCode (Go)",
		"anthropic":        "Anthropic (Claude)",
		"openai":           "OpenAI (GPT)",
		"google":           "Google (Gemini)",
		"ollama":           "Ollama (Local)",
		"vllm":             "vLLM (Local)",
		"unknown-provider": "unknown-provider",
	}
	for id, want := range tests {
		got := providerDisplayName(id)
		if got != want {
			t.Errorf("providerDisplayName(%q): got %q, want %q", id, got, want)
		}
	}
}

func TestDiscoveryCache(t *testing.T) {
	path := findNanoClawBinary()
	if path == "" {
		t.Skip("nanoclaw binary not found")
	}

	first, err := DiscoverModels()
	if err != nil {
		t.Fatalf("DiscoverModels() first: %v", err)
	}
	second, err := DiscoverModels()
	if err != nil {
		t.Fatalf("DiscoverModels() second: %v", err)
	}
	if first.DiscoveredAt.Unix() != second.DiscoveredAt.Unix() {
		t.Error("cached result should return same DiscoveredAt timestamp")
	}
}

func TestDiscoverProviderModels(t *testing.T) {
	path := findNanoClawBinary()
	if path == "" {
		t.Skip("nanoclaw binary not found")
	}

	models, err := DiscoverProviderModels("opencode")
	if err != nil {
		t.Fatalf("DiscoverProviderModels(opencode): %v", err)
	}
	if len(models) < 3 {
		t.Errorf("opencode has only %d models, want at least 3", len(models))
	}

	_, err = DiscoverProviderModels("nonexistent-provider-xyz")
	if err == nil {
		t.Error("DiscoverProviderModels(unknown): expected error")
	}
}

func TestOpenclawBinaryDetection(t *testing.T) {
	paths := []string{
		"./node_modules/.bin/nanoclaw",
		"node_modules/.bin/nanoclaw",
		"nanoclaw",
	}
	for _, p := range paths {
		cmd := exec.Command(p, "version")
		if cmd.Run() != nil {
			continue
		}
		found := findNanoClawBinary()
		if found == "" {
			t.Errorf("findNanoClawBinary() returned empty despite %s being runnable", p)
		}
		return
	}
	t.Skip("no nanoclaw binary available")
}
