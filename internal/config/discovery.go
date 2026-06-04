package config

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type DiscoveredModel struct {
	Key           string
	Name          string
	Input         string
	ContextWindow int
	Local         bool
}

type DiscoveredProvider struct {
	ID       string
	Name     string
	Models   []DiscoveredModel
	NeedsKey bool
}

type DiscoveryResult struct {
	Providers    []DiscoveredProvider
	DiscoveredAt time.Time
}

var (
	discoveryCache   *DiscoveryResult
	discoveryCacheMu sync.RWMutex
)

func DiscoverModels() (*DiscoveryResult, error) {
	discoveryCacheMu.RLock()
	if discoveryCache != nil && time.Since(discoveryCache.DiscoveredAt) < 10*time.Minute {
		defer discoveryCacheMu.RUnlock()
		return discoveryCache, nil
	}
	discoveryCacheMu.RUnlock()

	discoveryCacheMu.Lock()
	defer discoveryCacheMu.Unlock()
	if discoveryCache != nil && time.Since(discoveryCache.DiscoveredAt) < 10*time.Minute {
		return discoveryCache, nil
	}

	result, err := discoverModelsImpl()
	if err != nil {
		discoveryCache = nil
		return nil, err
	}
	result.DiscoveredAt = time.Now()
	discoveryCache = result
	return result, nil
}

func discoverModelsImpl() (*DiscoveryResult, error) {
	if result := discoverModelsFromProviderRegistry(); result != nil {
		return result, nil
	}
	return nil, fmt.Errorf("provider registry is empty")
}

func discoverModelsFromProviderRegistry() *DiscoveryResult {
	providers := MergedProviders()
	if len(providers) == 0 {
		return nil
	}
	out := make([]DiscoveredProvider, 0, len(providers))
	for _, provider := range providers {
		if provider.ID == "" {
			continue
		}
		models := make([]DiscoveredModel, 0, len(provider.Models))
		for _, model := range provider.Models {
			if strings.TrimSpace(model) == "" {
				continue
			}
			models = append(models, DiscoveredModel{
				Key:  model,
				Name: model,
			})
		}
		if len(models) == 0 {
			continue
		}
		out = append(out, DiscoveredProvider{
			ID:       provider.ID,
			Name:     provider.Name,
			Models:   models,
			NeedsKey: provider.NeedsKey,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return &DiscoveryResult{Providers: out}
}

func providerDisplayName(id string) string {
	switch id {
	case "opencode":
		return "OpenCode (Zen)"
	case "opencode-go":
		return "OpenCode (Go)"
	case "anthropic":
		return "Anthropic (Claude)"
	case "openai":
		return "OpenAI (GPT)"
	case "google":
		return "Google (Gemini)"
	case "xai":
		return "xAI (Grok)"
	case "zai":
		return "Z.AI (GLM)"
	case "openrouter":
		return "OpenRouter"
	case "groq":
		return "Groq (LPU)"
	case "mistral":
		return "Mistral"
	case "together":
		return "Together AI"
	case "cerebras":
		return "Cerebras"
	case "moonshot":
		return "Moonshot (Kimi)"
	case "kimi-coding":
		return "Kimi Coding"
	case "minimax":
		return "MiniMax"
	case "venice":
		return "Venice AI"
	case "nvidia":
		return "NVIDIA"
	case "huggingface":
		return "Hugging Face"
	case "volcengine":
		return "Volcengine (Doubao)"
	case "byteplus":
		return "BytePlus"
	case "xiaomi":
		return "Xiaomi"
	case "qianfan":
		return "Qianfan (Baidu)"
	case "modelstudio":
		return "Model Studio (Alibaba)"
	case "kilocode":
		return "Kilo Gateway"
	case "vercel-ai-gateway":
		return "Vercel AI Gateway"
	case "cloudflare-ai-gateway":
		return "Cloudflare AI Gateway"
	case "synthetic":
		return "Synthetic"
	case "github-copilot":
		return "GitHub Copilot"
	case "ollama":
		return "Ollama (Local)"
	case "vllm":
		return "vLLM (Local)"
	case "sglang":
		return "SGLang (Local)"
	default:
		return id
	}
}

func DiscoverProviderModels(providerID string) ([]string, error) {
	result, err := DiscoverModels()
	if err != nil {
		return nil, err
	}
	for _, p := range result.Providers {
		if p.ID == providerID {
			models := make([]string, len(p.Models))
			for i, m := range p.Models {
				models[i] = m.Key
			}
			return models, nil
		}
	}
	return nil, fmt.Errorf("provider %q not in discovered models", providerID)
}

func (p DiscoveredProvider) DefaultModel() string {
	if len(p.Models) == 0 {
		return p.ID + "/default"
	}
	return p.Models[0].Key
}
