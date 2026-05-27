package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	providerRegistryModelsDevURL    = "https://models.dev/api.json"
	providerRegistryOpenCodeZenURL  = "https://opencode.ai/zen/v1/models"
	providerRegistryOpenCodeGoURL   = "https://opencode.ai/zen/go/v1/models"
	providerRegistryOpenRouterURL   = "https://openrouter.ai/api/v1/models"
	providerRegistryVercelURL       = "https://ai-gateway.vercel.sh/v1/models"
	providerRegistryLiteLLMURL      = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
	providerRegistryHTTPClient      = &http.Client{Timeout: 4 * time.Second}
	providerRegistryCacheTTL        = 10 * time.Minute
	providerRegistryDiskCacheMaxAge = 24 * time.Hour

	providerRegistryMu        sync.RWMutex
	providerRegistryProviders []Provider
	providerRegistryFetchedAt time.Time
	providerRegistryHasRemote bool
)

type providerRegistryDiskCache struct {
	Providers   []Provider `json:"providers"`
	RefreshedAt time.Time  `json:"refreshedAt"`
}

// MergedProviders returns the provider metadata VulpineOS should present to
// users: built-in auth metadata plus maintained model catalogs where available.
func MergedProviders() []Provider {
	return providerRegistrySnapshot(true)
}

func providerRegistrySnapshot(refresh bool) []Provider {
	providerRegistryMu.RLock()
	if len(providerRegistryProviders) > 0 && (!refresh || providerRegistryHasRemote) && time.Since(providerRegistryFetchedAt) < providerRegistryCacheTTL {
		defer providerRegistryMu.RUnlock()
		return cloneProviders(providerRegistryProviders)
	}
	providerRegistryMu.RUnlock()

	providerRegistryMu.Lock()
	defer providerRegistryMu.Unlock()
	if len(providerRegistryProviders) > 0 && (!refresh || providerRegistryHasRemote) && time.Since(providerRegistryFetchedAt) < providerRegistryCacheTTL {
		return cloneProviders(providerRegistryProviders)
	}

	merged := cloneProviders(Providers)
	hasRemote := false
	if cached, ok := loadCachedProviderRegistry(); ok {
		merged = mergeProviderLists(merged, cached)
		hasRemote = true
	}

	if refresh {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		discovered := fetchProviderCatalogs(ctx)
		cancel()
		if len(discovered) > 0 {
			merged = mergeProviderLists(cloneProviders(Providers), discovered)
			_ = saveCachedProviderRegistry(merged)
			hasRemote = true
		}
	}

	providerRegistryProviders = merged
	providerRegistryFetchedAt = time.Now()
	providerRegistryHasRemote = hasRemote
	return cloneProviders(merged)
}

func resetProviderRegistryCache() {
	providerRegistryMu.Lock()
	defer providerRegistryMu.Unlock()
	providerRegistryProviders = nil
	providerRegistryFetchedAt = time.Time{}
	providerRegistryHasRemote = false
}

func providerRegistryCachePath() string {
	return filepath.Join(Dir(), "provider-registry-cache.json")
}

func loadCachedProviderRegistry() ([]Provider, bool) {
	data, err := os.ReadFile(providerRegistryCachePath())
	if err != nil {
		return nil, false
	}
	var cached providerRegistryDiskCache
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, false
	}
	if len(cached.Providers) == 0 || time.Since(cached.RefreshedAt) > providerRegistryDiskCacheMaxAge {
		return nil, false
	}
	return cloneProviders(cached.Providers), true
}

func saveCachedProviderRegistry(providers []Provider) error {
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(providerRegistryDiskCache{
		Providers:   cloneProviders(providers),
		RefreshedAt: time.Now(),
	}, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(providerRegistryCachePath(), data)
}

func fetchProviderCatalogs(ctx context.Context) []Provider {
	fetchers := []func(context.Context) ([]Provider, error){
		fetchModelsDevProviders,
		func(ctx context.Context) ([]Provider, error) {
			return fetchOpenAIModelsProvider(ctx, "opencode", "OpenCode (Zen)", "OPENCODE_API_KEY", providerRegistryOpenCodeZenURL)
		},
		func(ctx context.Context) ([]Provider, error) {
			return fetchOpenAIModelsProvider(ctx, "opencode-go", "OpenCode Go", "OPENCODE_API_KEY", providerRegistryOpenCodeGoURL)
		},
		func(ctx context.Context) ([]Provider, error) {
			return fetchOpenAIModelsProvider(ctx, "openrouter", "OpenRouter", "OPENROUTER_API_KEY", providerRegistryOpenRouterURL)
		},
		func(ctx context.Context) ([]Provider, error) {
			return fetchOpenAIModelsProvider(ctx, "vercel-ai-gateway", "Vercel AI Gateway", "AI_GATEWAY_API_KEY", providerRegistryVercelURL)
		},
		fetchLiteLLMProviders,
	}

	results := make([][]Provider, len(fetchers))
	var resultsMu sync.Mutex
	var wg sync.WaitGroup
	for i, fetcher := range fetchers {
		wg.Add(1)
		go func(idx int, fetch func(context.Context) ([]Provider, error)) {
			defer wg.Done()
			providers, err := fetch(ctx)
			if err != nil || len(providers) == 0 {
				return
			}
			resultsMu.Lock()
			results[idx] = providers
			resultsMu.Unlock()
		}(i, fetcher)
	}
	wg.Wait()

	merged := []Provider{}
	for _, providers := range results {
		merged = mergeProviderLists(merged, providers)
	}
	return merged
}

type modelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	Env    []string                  `json:"env"`
	Models map[string]modelsDevModel `json:"models"`
}

type modelsDevModel struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func fetchModelsDevProviders(ctx context.Context) ([]Provider, error) {
	var raw map[string]modelsDevProvider
	if err := getJSON(ctx, providerRegistryModelsDevURL, &raw); err != nil {
		return nil, err
	}

	providers := make([]Provider, 0, len(raw))
	for id, item := range raw {
		providerID := firstNonEmpty(item.ID, id)
		if providerID == "" || len(item.Models) == 0 {
			continue
		}
		models := make([]string, 0, len(item.Models))
		for key, model := range item.Models {
			if !includeCatalogModel(model.Status) {
				continue
			}
			modelID := firstNonEmpty(model.ID, key)
			if modelID == "" {
				continue
			}
			models = append(models, qualifyModelID(providerID, modelID))
		}
		if len(models) == 0 {
			continue
		}
		envVar := firstEnv(item.Env)
		providers = append(providers, Provider{
			ID:           providerID,
			Name:         firstNonEmpty(item.Name, providerDisplayName(providerID)),
			EnvVar:       envVar,
			DefaultModel: defaultModelForProvider(providerID, models),
			Models:       orderModels(providerID, models, ""),
			NeedsKey:     envVar != "",
		})
	}
	sortProviders(providers)
	return providers, nil
}

type openAIModelsResponse struct {
	Data []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"data"`
}

func fetchOpenAIModelsProvider(ctx context.Context, providerID, name, envVar, url string) ([]Provider, error) {
	var raw openAIModelsResponse
	if err := getJSON(ctx, url, &raw); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(raw.Data))
	for _, item := range raw.Data {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		models = append(models, qualifyModelID(providerID, item.ID))
	}
	if len(models) == 0 {
		return nil, nil
	}
	return []Provider{{
		ID:           providerID,
		Name:         name,
		EnvVar:       envVar,
		DefaultModel: defaultModelForProvider(providerID, models),
		Models:       orderModels(providerID, models, ""),
		NeedsKey:     envVar != "",
	}}, nil
}

type liteLLMModel struct {
	Provider string `json:"litellm_provider"`
	Mode     string `json:"mode"`
}

func fetchLiteLLMProviders(ctx context.Context) ([]Provider, error) {
	var raw map[string]liteLLMModel
	if err := getJSON(ctx, providerRegistryLiteLLMURL, &raw); err != nil {
		return nil, err
	}
	byProvider := make(map[string][]string)
	for modelID, item := range raw {
		providerID := normalizeProviderID(item.Provider)
		if providerID == "" || !includeLiteLLMMode(item.Mode) {
			continue
		}
		byProvider[providerID] = append(byProvider[providerID], qualifyModelID(providerID, modelID))
	}
	providers := make([]Provider, 0, len(byProvider))
	for providerID, models := range byProvider {
		if len(models) == 0 {
			continue
		}
		envVar := defaultEnvVar(providerID)
		providers = append(providers, Provider{
			ID:           providerID,
			Name:         providerDisplayName(providerID),
			EnvVar:       envVar,
			DefaultModel: defaultModelForProvider(providerID, models),
			Models:       orderModels(providerID, models, ""),
			NeedsKey:     envVar != "",
		})
	}
	sortProviders(providers)
	return providers, nil
}

func getJSON(ctx context.Context, url string, dest interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := providerRegistryHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

func mergeProviderLists(base []Provider, incoming []Provider) []Provider {
	merged := cloneProviders(base)
	byID := make(map[string]int, len(merged))
	for i, provider := range merged {
		byID[provider.ID] = i
	}
	for _, provider := range incoming {
		provider.ID = strings.TrimSpace(provider.ID)
		if provider.ID == "" {
			continue
		}
		provider.Models = orderModels(provider.ID, provider.Models, provider.DefaultModel)
		if idx, ok := byID[provider.ID]; ok {
			merged[idx] = mergeProvider(merged[idx], provider)
			continue
		}
		if provider.Name == "" {
			provider.Name = providerDisplayName(provider.ID)
		}
		if provider.EnvVar == "" && provider.NeedsKey {
			provider.EnvVar = defaultEnvVar(provider.ID)
		}
		if provider.DefaultModel == "" {
			provider.DefaultModel = defaultModelForProvider(provider.ID, provider.Models)
		}
		byID[provider.ID] = len(merged)
		merged = append(merged, provider)
	}
	return merged
}

func mergeProvider(existing, incoming Provider) Provider {
	out := existing
	if strings.TrimSpace(out.Name) == "" {
		out.Name = incoming.Name
	}
	if strings.TrimSpace(out.EnvVar) == "" {
		out.EnvVar = incoming.EnvVar
	}
	if !out.NeedsKey {
		out.NeedsKey = incoming.NeedsKey && out.EnvVar != ""
	}
	models := mergeModelLists(incoming.Models, existing.Models)
	defaultModel := out.DefaultModel
	if defaultModel == "" {
		defaultModel = incoming.DefaultModel
	}
	models = orderModels(out.ID, models, defaultModel)
	if defaultModel == "" || !containsModel(models, defaultModel) {
		defaultModel = defaultModelForProvider(out.ID, models)
	}
	out.DefaultModel = defaultModel
	out.Models = models
	return out
}

func mergeModelLists(groups ...[]string) []string {
	seen := make(map[string]struct{})
	out := []string{}
	for _, group := range groups {
		for _, model := range group {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			if _, ok := seen[model]; ok {
				continue
			}
			seen[model] = struct{}{}
			out = append(out, model)
		}
	}
	return out
}

func orderModels(providerID string, models []string, preferred string) []string {
	models = mergeModelLists(models)
	sort.Strings(models)
	preferred = strings.TrimSpace(preferred)
	if preferred == "" {
		preferred = preferredDefaultModel(providerID)
	}
	if preferred == "" || !containsModel(models, preferred) {
		return models
	}
	out := []string{preferred}
	for _, model := range models {
		if model != preferred {
			out = append(out, model)
		}
	}
	return out
}

func defaultModelForProvider(providerID string, models []string) string {
	if preferred := preferredDefaultModel(providerID); preferred != "" && containsModel(models, preferred) {
		return preferred
	}
	if len(models) == 0 {
		return providerID + "/default"
	}
	return models[0]
}

func preferredDefaultModel(providerID string) string {
	for _, provider := range Providers {
		if provider.ID == providerID {
			return provider.DefaultModel
		}
	}
	return ""
}

func qualifyModelID(providerID, modelID string) string {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(strings.TrimPrefix(modelID, "~"))
	if providerID == "" || modelID == "" {
		return ""
	}
	if strings.HasPrefix(modelID, providerID+"/") {
		return modelID
	}
	return providerID + "/" + modelID
}

func includeCatalogModel(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "deprecated", "removed", "retired", "inactive":
		return false
	default:
		return true
	}
}

func includeLiteLLMMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "chat", "completion", "responses":
		return true
	default:
		return false
	}
}

func normalizeProviderID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	id = strings.ReplaceAll(id, "_", "-")
	return id
}

func firstEnv(values []string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func defaultEnvVar(providerID string) string {
	switch providerID {
	case "opencode", "opencode-go":
		return "OPENCODE_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "vercel-ai-gateway":
		return "AI_GATEWAY_API_KEY"
	case "ollama", "vllm", "sglang":
		return ""
	default:
		replacer := strings.NewReplacer("-", "_", ".", "_", "/", "_")
		return strings.ToUpper(replacer.Replace(providerID)) + "_API_KEY"
	}
}

func containsModel(models []string, want string) bool {
	for _, model := range models {
		if model == want {
			return true
		}
	}
	return false
}

func cloneProviders(providers []Provider) []Provider {
	out := make([]Provider, len(providers))
	for i, provider := range providers {
		out[i] = provider
		out[i].Models = append([]string(nil), provider.Models...)
	}
	return out
}

func sortProviders(providers []Provider) {
	sort.Slice(providers, func(i, j int) bool {
		if providers[i].Name == providers[j].Name {
			return providers[i].ID < providers[j].ID
		}
		return providers[i].Name < providers[j].Name
	})
}
