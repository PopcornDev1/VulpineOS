package config

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoverModels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetProviderRegistryCache()
	t.Cleanup(resetProviderRegistryCache)

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

func TestMergedProvidersUsesCatalogSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	resetProviderRegistryCache()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models-dev.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"opencode": {
					"id": "opencode",
					"name": "OpenCode Zen",
					"env": ["OPENCODE_API_KEY"],
					"models": {
						"deepseek-v4-flash-free": {"id": "deepseek-v4-flash-free", "status": "active"},
						"nemotron-3-super-free": {"id": "nemotron-3-super-free", "status": "active"}
					}
				},
				"new-provider": {
					"id": "new-provider",
					"name": "New Provider",
					"env": ["NEW_PROVIDER_API_KEY"],
					"models": {
						"fresh-model": {"id": "fresh-model", "status": "active"}
					}
				}
			}`))
		case "/zen/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"big-pickle"},{"id":"deepseek-v4-flash-free"}]}`))
		case "/zen/go/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"minimax-m2.7"}]}`))
		case "/openrouter/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen/qwen3.7-max"}]}`))
		case "/vercel/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"anthropic/claude-sonnet-4-6"}]}`))
		case "/litellm.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"gpt-catalog-test": {"litellm_provider": "openai", "mode": "chat"},
				"dall-e-test": {"litellm_provider": "openai", "mode": "image_generation"}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldModelsDevURL := providerRegistryModelsDevURL
	oldOpenCodeZenURL := providerRegistryOpenCodeZenURL
	oldOpenCodeGoURL := providerRegistryOpenCodeGoURL
	oldOpenRouterURL := providerRegistryOpenRouterURL
	oldVercelURL := providerRegistryVercelURL
	oldLiteLLMURL := providerRegistryLiteLLMURL
	defer func() {
		providerRegistryModelsDevURL = oldModelsDevURL
		providerRegistryOpenCodeZenURL = oldOpenCodeZenURL
		providerRegistryOpenCodeGoURL = oldOpenCodeGoURL
		providerRegistryOpenRouterURL = oldOpenRouterURL
		providerRegistryVercelURL = oldVercelURL
		providerRegistryLiteLLMURL = oldLiteLLMURL
		resetProviderRegistryCache()
	}()
	providerRegistryModelsDevURL = server.URL + "/models-dev.json"
	providerRegistryOpenCodeZenURL = server.URL + "/zen/v1/models"
	providerRegistryOpenCodeGoURL = server.URL + "/zen/go/v1/models"
	providerRegistryOpenRouterURL = server.URL + "/openrouter/models"
	providerRegistryVercelURL = server.URL + "/vercel/models"
	providerRegistryLiteLLMURL = server.URL + "/litellm.json"

	merged := MergedProviders()

	opencode := findProviderForTest(merged, "opencode")
	if opencode == nil {
		t.Fatal("opencode not found")
	}
	for _, want := range []string{"opencode/big-pickle", "opencode/nemotron-3-super-free"} {
		if !containsString(opencode.Models, want) {
			t.Fatalf("opencode models = %#v, want %s", opencode.Models, want)
		}
	}
	opencodeGo := findProviderForTest(merged, "opencode-go")
	if opencodeGo == nil || !containsString(opencodeGo.Models, "opencode-go/minimax-m2.7") {
		t.Fatalf("opencode-go models = %#v, want minimax m2.7", opencodeGo)
	}
	newProvider := findProviderForTest(merged, "new-provider")
	if newProvider == nil {
		t.Fatal("new-provider from catalog not found")
	}
	if newProvider.EnvVar != "NEW_PROVIDER_API_KEY" {
		t.Fatalf("new-provider env = %q, want NEW_PROVIDER_API_KEY", newProvider.EnvVar)
	}
	if !containsString(newProvider.Models, "new-provider/fresh-model") {
		t.Fatalf("new-provider models = %#v, want fresh model", newProvider.Models)
	}
	openai := findProviderForTest(merged, "openai")
	if openai == nil {
		t.Fatal("openai not found")
	}
	if !containsString(openai.Models, "openai/gpt-catalog-test") {
		t.Fatalf("openai models = %#v, want LiteLLM chat model", openai.Models)
	}
	if containsString(openai.Models, "openai/dall-e-test") {
		t.Fatalf("openai models should not include image-generation-only LiteLLM model: %#v", openai.Models)
	}
}

func findProviderForTest(providers []Provider, id string) *Provider {
	for i := range providers {
		if providers[i].ID == id {
			return &providers[i]
		}
	}
	return nil
}

func TestOpenCodeProviderIncludesFreeZenModels(t *testing.T) {
	provider := GetProvider("opencode")
	if provider == nil {
		t.Fatal("opencode provider not found")
	}
	for _, want := range []string{"opencode/deepseek-v4-flash-free", "opencode/minimax-m2.5"} {
		if !containsString(provider.Models, want) {
			t.Fatalf("opencode models = %#v, want %s", provider.Models, want)
		}
	}
	if provider.DefaultModel != "opencode/deepseek-v4-flash-free" {
		t.Fatalf("opencode default = %q, want free Zen default", provider.DefaultModel)
	}

	goProvider := GetProvider("opencode-go")
	if goProvider == nil || !containsString(goProvider.Models, "opencode-go/deepseek-v4-flash") {
		t.Fatalf("opencode-go models = %#v, want deepseek v4 flash", goProvider)
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
	t.Setenv("HOME", t.TempDir())
	discoveryCacheMu.Lock()
	discoveryCache = nil
	discoveryCacheMu.Unlock()
	resetProviderRegistryCache()
	t.Cleanup(func() {
		discoveryCacheMu.Lock()
		discoveryCache = nil
		discoveryCacheMu.Unlock()
		resetProviderRegistryCache()
	})

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
	t.Setenv("HOME", t.TempDir())
	resetProviderRegistryCache()
	t.Cleanup(resetProviderRegistryCache)

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
