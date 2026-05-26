package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	nanoclawBinary   string
)

func SetNanoClawBinary(path string) {
	nanoclawBinary = path
}

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
	binary := nanoclawBinary
	if binary == "" {
		binary = findNanoClawBinary()
	}
	if binary == "" {
		return nil, fmt.Errorf("nanoclaw binary not found")
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		ctx, cancel := contextWithTimeout(15 * time.Second)
		cmd := exec.CommandContext(ctx, binary, "models", "list", "--all", "--json")
		out, err := cmd.Output()
		cancel()
		if err == nil {
			var raw struct {
				Models []struct {
					Key           string `json:"key"`
					Name          string `json:"name"`
					Input         string `json:"input"`
					ContextWindow int    `json:"contextWindow"`
					Local         bool   `json:"local"`
				} `json:"models"`
			}
			if err := json.Unmarshal(out, &raw); err == nil {
				byProvider := make(map[string]*DiscoveredProvider)
				for _, m := range raw.Models {
					providerID, _, ok := strings.Cut(m.Key, "/")
					if !ok || providerID == "" {
						continue
					}
					providerID = strings.ToLower(strings.TrimSpace(providerID))
					if _, ok := byProvider[providerID]; !ok {
						byProvider[providerID] = &DiscoveredProvider{
							ID:       providerID,
							Name:     providerDisplayName(providerID),
							NeedsKey: true,
						}
					}
					byProvider[providerID].Models = append(byProvider[providerID].Models, DiscoveredModel{
						Key:           m.Key,
						Name:          m.Name,
						Input:         m.Input,
						ContextWindow: m.ContextWindow,
						Local:         m.Local,
					})
				}

				providers := make([]DiscoveredProvider, 0, len(byProvider))
				for _, p := range byProvider {
					if p.ID == "opencode-go" {
						p.NeedsKey = false
					}
					if p.ID == "ollama" || p.ID == "vllm" || p.ID == "sglang" {
						p.NeedsKey = false
					}
					providers = append(providers, *p)
				}

				return &DiscoveryResult{Providers: providers}, nil
			}
			lastErr = err
		} else {
			lastErr = err
		}
	}
	return nil, fmt.Errorf("nanoclaw models list: %w", lastErr)
}

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func findNanoClawBinary() string {
	if path, err := exec.LookPath("nanoclaw"); err == nil {
		return path
	}
	if path, err := exec.LookPath("ncl"); err == nil {
		return path
	}

	paths := []string{
		"./node_modules/.bin/nanoclaw",
		"./node_modules/.bin/ncl",
		"node_modules/.bin/nanoclaw",
		"node_modules/.bin/ncl",
		filepath.Join(Dir(), "nanoclaw", "nanoclaw"),
		filepath.Join(Dir(), "nanoclaw-src", "bin", "ncl"),
		filepath.Join(Dir(), "nanoclaw", "nanoclaw.sh"),
		"/opt/homebrew/bin/nanoclaw",
		"/opt/homebrew/bin/ncl",
		"/usr/local/bin/nanoclaw",
		"/usr/local/bin/ncl",
		"/usr/bin/nanoclaw",
		"/usr/bin/ncl",
	}
	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		} else {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
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

func MergedProviders() []Provider {
	static := Providers
	discovered, _ := DiscoverModels()

	merged := make([]Provider, 0, len(static))
	seen := make(map[string]bool)

	for _, p := range static {
		merged = append(merged, p)
		seen[p.ID] = true
	}

	if discovered != nil {
		for _, dp := range discovered.Providers {
			if seen[dp.ID] {
				continue
			}
			models := make([]string, len(dp.Models))
			for i, m := range dp.Models {
				models[i] = m.Key
			}
			merged = append(merged, Provider{
				ID:           dp.ID,
				Name:         dp.Name,
				EnvVar:       "",
				DefaultModel: dp.DefaultModel(),
				Models:       models,
				NeedsKey:     dp.NeedsKey,
			})
		}
	}

	return merged
}

func (p DiscoveredProvider) DefaultModel() string {
	if len(p.Models) == 0 {
		return p.ID + "/default"
	}
	return p.Models[0].Key
}
