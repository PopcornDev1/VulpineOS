package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds VulpineOS user configuration.
type Config struct {
	Provider        string          `json:"provider"`
	APIKey          string          `json:"apiKey"`
	Model           string          `json:"model"`
	SetupComplete   bool            `json:"setupComplete"`
	BinaryPath      string          `json:"binaryPath,omitempty"`
	FoxbridgeCDPURL string          `json:"-"` // runtime-only: set when foxbridge is running
	GlobalSkills    []SkillEntry    `json:"globalSkills,omitempty"`  // skills enabled for all agents
	AgentSkills     map[string][]SkillEntry `json:"agentSkills,omitempty"` // agentID → skills for that agent only
}

// SkillEntry describes a skill configuration.
type SkillEntry struct {
	Name    string            `json:"name"`              // skill name/key
	Enabled bool              `json:"enabled"`
	Env     map[string]string `json:"env,omitempty"`     // per-skill env vars (e.g. API keys)
}

// SkillDir returns the path to the global skills directory.
func SkillDir() string {
	return filepath.Join(Dir(), "skills")
}

// AgentSkillDir returns the path to a per-agent skills directory.
func AgentSkillDir(agentID string) string {
	return filepath.Join(Dir(), "agent-skills", agentID, "skills")
}

// AddGlobalSkill adds a skill to the global list.
func (c *Config) AddGlobalSkill(name string, env map[string]string) {
	for i, s := range c.GlobalSkills {
		if s.Name == name {
			c.GlobalSkills[i].Enabled = true
			c.GlobalSkills[i].Env = env
			return
		}
	}
	c.GlobalSkills = append(c.GlobalSkills, SkillEntry{Name: name, Enabled: true, Env: env})
}

// RemoveGlobalSkill disables a global skill.
func (c *Config) RemoveGlobalSkill(name string) {
	for i, s := range c.GlobalSkills {
		if s.Name == name {
			c.GlobalSkills[i].Enabled = false
			return
		}
	}
}

// AddAgentSkill adds a skill to a specific agent.
func (c *Config) AddAgentSkill(agentID, name string, env map[string]string) {
	if c.AgentSkills == nil {
		c.AgentSkills = make(map[string][]SkillEntry)
	}
	skills := c.AgentSkills[agentID]
	for i, s := range skills {
		if s.Name == name {
			skills[i].Enabled = true
			skills[i].Env = env
			c.AgentSkills[agentID] = skills
			return
		}
	}
	c.AgentSkills[agentID] = append(skills, SkillEntry{Name: name, Enabled: true, Env: env})
}

// Provider describes a supported AI model provider.
type Provider struct {
	ID          string   // e.g. "anthropic"
	Name        string   // e.g. "Anthropic (Claude)"
	EnvVar      string   // e.g. "ANTHROPIC_API_KEY"
	DefaultModel string  // e.g. "anthropic/claude-sonnet-4-6"
	Models      []string // available models
	NeedsKey    bool     // false for ollama
}

// Providers is the full registry of OpenClaw-supported AI providers.
// Matches https://docs.openclaw.ai/concepts/model-providers
var Providers = []Provider{
	// --- Tier 1: Major cloud providers ---
	{ID: "anthropic", Name: "Anthropic (Claude)", EnvVar: "ANTHROPIC_API_KEY",
		DefaultModel: "anthropic/claude-sonnet-4-6",
		Models: []string{"anthropic/claude-opus-4-6", "anthropic/claude-sonnet-4-6", "anthropic/claude-haiku-4-5"},
		NeedsKey: true},
	{ID: "openai", Name: "OpenAI (GPT)", EnvVar: "OPENAI_API_KEY",
		DefaultModel: "openai/gpt-5.4",
		Models: []string{"openai/gpt-5.4", "openai/gpt-4.1", "openai/gpt-4.1-mini", "openai/o3"},
		NeedsKey: true},
	{ID: "google", Name: "Google (Gemini)", EnvVar: "GEMINI_API_KEY",
		DefaultModel: "google/gemini-2.5-pro",
		Models: []string{"google/gemini-3.1-pro-preview", "google/gemini-2.5-pro", "google/gemini-2.5-flash"},
		NeedsKey: true},
	{ID: "xai", Name: "xAI (Grok)", EnvVar: "XAI_API_KEY",
		DefaultModel: "xai/grok-3",
		Models: []string{"xai/grok-3", "xai/grok-3-mini"},
		NeedsKey: true},
	{ID: "zai", Name: "Z.AI (GLM)", EnvVar: "ZAI_API_KEY",
		DefaultModel: "zai/glm-5",
		Models: []string{"zai/glm-5", "zai/glm-4.7", "zai/glm-4.6"},
		NeedsKey: true},

	// --- Tier 2: Routing/Gateway providers ---
	{ID: "openrouter", Name: "OpenRouter", EnvVar: "OPENROUTER_API_KEY",
		DefaultModel: "openrouter/anthropic/claude-sonnet-4-6",
		Models: []string{"openrouter/anthropic/claude-sonnet-4-6", "openrouter/openai/gpt-4.1", "openrouter/google/gemini-2.5-pro"},
		NeedsKey: true},
	{ID: "groq", Name: "Groq (LPU)", EnvVar: "GROQ_API_KEY",
		DefaultModel: "groq/llama-3.3-70b-versatile",
		Models: []string{"groq/llama-3.3-70b-versatile", "groq/mixtral-8x7b-32768"},
		NeedsKey: true},
	{ID: "mistral", Name: "Mistral", EnvVar: "MISTRAL_API_KEY",
		DefaultModel: "mistral/mistral-large-latest",
		Models: []string{"mistral/mistral-large-latest", "mistral/codestral-latest"},
		NeedsKey: true},
	{ID: "together", Name: "Together AI", EnvVar: "TOGETHER_API_KEY",
		DefaultModel: "together/meta-llama/Llama-3.3-70B-Instruct-Turbo",
		Models: []string{"together/meta-llama/Llama-3.3-70B-Instruct-Turbo", "together/deepseek-ai/DeepSeek-R1"},
		NeedsKey: true},
	{ID: "cerebras", Name: "Cerebras", EnvVar: "CEREBRAS_API_KEY",
		DefaultModel: "cerebras/llama-3.3-70b",
		Models: []string{"cerebras/llama-3.3-70b"},
		NeedsKey: true},

	// --- Tier 3: Specialized providers ---
	{ID: "moonshot", Name: "Moonshot (Kimi)", EnvVar: "MOONSHOT_API_KEY",
		DefaultModel: "moonshot/kimi-k2.5",
		Models: []string{"moonshot/kimi-k2.5"},
		NeedsKey: true},
	{ID: "kimi-coding", Name: "Kimi Coding", EnvVar: "KIMI_API_KEY",
		DefaultModel: "kimi-coding/k2p5",
		Models: []string{"kimi-coding/k2p5"},
		NeedsKey: true},
	{ID: "minimax", Name: "MiniMax", EnvVar: "MINIMAX_API_KEY",
		DefaultModel: "minimax/MiniMax-M2.5",
		Models: []string{"minimax/MiniMax-M2.5"},
		NeedsKey: true},
	{ID: "venice", Name: "Venice AI", EnvVar: "VENICE_API_KEY",
		DefaultModel: "venice/llama-3.3-70b",
		Models: []string{"venice/llama-3.3-70b"},
		NeedsKey: true},
	{ID: "nvidia", Name: "NVIDIA", EnvVar: "NVIDIA_API_KEY",
		DefaultModel: "nvidia/llama-3.1-405b-instruct",
		Models: []string{"nvidia/llama-3.1-405b-instruct"},
		NeedsKey: true},
	{ID: "huggingface", Name: "Hugging Face", EnvVar: "HF_TOKEN",
		DefaultModel: "huggingface/deepseek-ai/DeepSeek-R1",
		Models: []string{"huggingface/deepseek-ai/DeepSeek-R1"},
		NeedsKey: true},
	{ID: "volcengine", Name: "Volcengine (Doubao)", EnvVar: "VOLCANO_ENGINE_API_KEY",
		DefaultModel: "volcengine/doubao-seed-1-8-251228",
		Models: []string{"volcengine/doubao-seed-1-8-251228"},
		NeedsKey: true},
	{ID: "byteplus", Name: "BytePlus", EnvVar: "BYTEPLUS_API_KEY",
		DefaultModel: "byteplus/seed-1-8-251228",
		Models: []string{"byteplus/seed-1-8-251228"},
		NeedsKey: true},
	{ID: "xiaomi", Name: "Xiaomi", EnvVar: "XIAOMI_API_KEY",
		DefaultModel: "xiaomi/MiMo-7B-RL",
		Models: []string{"xiaomi/MiMo-7B-RL"},
		NeedsKey: true},
	{ID: "qianfan", Name: "Qianfan (Baidu)", EnvVar: "QIANFAN_API_KEY",
		DefaultModel: "qianfan/ernie-4.5-128k",
		Models: []string{"qianfan/ernie-4.5-128k"},
		NeedsKey: true},
	{ID: "modelstudio", Name: "Model Studio (Alibaba)", EnvVar: "MODELSTUDIO_API_KEY",
		DefaultModel: "modelstudio/qwen-plus",
		Models: []string{"modelstudio/qwen-plus"},
		NeedsKey: true},

	// --- Gateway/proxy providers ---
	{ID: "opencode", Name: "OpenCode (Zen)", EnvVar: "OPENCODE_API_KEY",
		DefaultModel: "opencode/claude-opus-4-6",
		Models: []string{"opencode/claude-opus-4-6", "opencode/claude-sonnet-4-6"},
		NeedsKey: true},
	{ID: "kilocode", Name: "Kilo Gateway", EnvVar: "KILOCODE_API_KEY",
		DefaultModel: "kilocode/anthropic/claude-opus-4.6",
		Models: []string{"kilocode/anthropic/claude-opus-4.6"},
		NeedsKey: true},
	{ID: "vercel-ai-gateway", Name: "Vercel AI Gateway", EnvVar: "AI_GATEWAY_API_KEY",
		DefaultModel: "vercel-ai-gateway/anthropic/claude-opus-4.6",
		Models: []string{"vercel-ai-gateway/anthropic/claude-opus-4.6"},
		NeedsKey: true},
	{ID: "cloudflare-ai-gateway", Name: "Cloudflare AI Gateway", EnvVar: "CLOUDFLARE_AI_GATEWAY_API_KEY",
		DefaultModel: "cloudflare-ai-gateway/openai/gpt-4.1",
		Models: []string{"cloudflare-ai-gateway/openai/gpt-4.1"},
		NeedsKey: true},
	{ID: "synthetic", Name: "Synthetic", EnvVar: "SYNTHETIC_API_KEY",
		DefaultModel: "synthetic/hf:MiniMaxAI/MiniMax-M2.5",
		Models: []string{"synthetic/hf:MiniMaxAI/MiniMax-M2.5"},
		NeedsKey: true},
	{ID: "github-copilot", Name: "GitHub Copilot", EnvVar: "GITHUB_TOKEN",
		DefaultModel: "github-copilot/claude-sonnet-4-6",
		Models: []string{"github-copilot/claude-sonnet-4-6", "github-copilot/gpt-4.1"},
		NeedsKey: true},

	// --- Local providers (no API key) ---
	{ID: "ollama", Name: "Ollama (Local)", EnvVar: "",
		DefaultModel: "ollama/llama3.3",
		Models: []string{"ollama/llama3.3", "ollama/qwen3", "ollama/deepseek-r1", "ollama/codellama"},
		NeedsKey: false},
	{ID: "vllm", Name: "vLLM (Local)", EnvVar: "",
		DefaultModel: "vllm/your-model-id",
		Models: []string{"vllm/your-model-id"},
		NeedsKey: false},
	{ID: "sglang", Name: "SGLang (Local)", EnvVar: "",
		DefaultModel: "sglang/your-model-id",
		Models: []string{"sglang/your-model-id"},
		NeedsKey: false},
}

// GetProvider returns the provider by ID.
func GetProvider(id string) *Provider {
	for i := range Providers {
		if Providers[i].ID == id {
			return &Providers[i]
		}
	}
	return nil
}

// CustomProvider creates a provider entry for any provider/model string not in the registry.
// This allows users to use ANY OpenClaw-supported provider even if we don't list it.
func CustomProvider(providerID, envVar string) Provider {
	return Provider{
		ID:           providerID,
		Name:         providerID + " (custom)",
		EnvVar:       envVar,
		DefaultModel: providerID + "/default",
		Models:       []string{},
		NeedsKey:     envVar != "",
	}
}

// Dir returns the VulpineOS config directory (~/.vulpineos).
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vulpineos")
}

// Path returns the config file path.
func Path() string {
	return filepath.Join(Dir(), "config.json")
}

// Load reads the config from disk. Returns a zero Config if file doesn't exist.
func Load() (*Config, error) {
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

// NeedsSetup returns true if first-time setup hasn't been completed.
func (c *Config) NeedsSetup() bool {
	return !c.SetupComplete
}

// Save writes the config to disk.
func (c *Config) Save() error {
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	return os.WriteFile(Path(), data, 0600)
}

// GenerateOpenClawConfig writes an openclaw.json that uses VulpineOS as the browser.
func (c *Config) GenerateOpenClawConfig(vulpineosBinary, camoufoxBinary string) error {
	provider := GetProvider(c.Provider)
	if provider == nil {
		return fmt.Errorf("unknown provider: %s", c.Provider)
	}

	// Build env map
	env := map[string]interface{}{}
	if provider.EnvVar != "" && c.APIKey != "" {
		env[provider.EnvVar] = c.APIKey
	}

	// Build skills entries from global skills config
	skillEntries := map[string]interface{}{}
	extraDirs := []string{}
	for _, s := range c.GlobalSkills {
		entry := map[string]interface{}{"enabled": s.Enabled}
		if len(s.Env) > 0 {
			entry["env"] = s.Env
		}
		skillEntries[s.Name] = entry
	}

	// Add the global skills directory if it exists
	globalSkillDir := SkillDir()
	if _, err := os.Stat(globalSkillDir); err == nil {
		extraDirs = append(extraDirs, globalSkillDir)
	}

	config := map[string]interface{}{
		"channels": map[string]interface{}{},
		"env":      env,
		"agents": map[string]interface{}{
			"defaults": map[string]interface{}{
				"model": map[string]interface{}{
					"primary": c.Model,
				},
				"compaction": map[string]interface{}{
					"mode": "safeguard",
				},
			},
		},
		"gateway": map[string]interface{}{
			"mode": "local",
		},
		// Enable browser — if foxbridge is running, route through Camoufox via CDP proxy.
		// Otherwise fall back to OpenClaw's built-in Chromium.
		"browser": func() map[string]interface{} {
			browserCfg := map[string]interface{}{
				"enabled":  true,
				"headless": true,
			}
			if c.FoxbridgeCDPURL != "" {
				// Route through foxbridge → Camoufox instead of Chrome
				browserCfg["cdpUrl"] = c.FoxbridgeCDPURL
			}
			return browserCfg
		}(),
		"skills": map[string]interface{}{
			"entries": skillEntries,
			"load": map[string]interface{}{
				"extraDirs": extraDirs,
			},
		},
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal openclaw config: %w", err)
	}

	// Write to the OpenClaw profile directory (~/.openclaw-vulpine/)
	// This is where `openclaw --profile vulpine` reads config from
	profileDir := OpenClawProfileDir()
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return fmt.Errorf("create openclaw profile dir: %w", err)
	}

	path := filepath.Join(profileDir, "openclaw.json")
	return os.WriteFile(path, data, 0644)
}

// OpenClawProfileDir returns the OpenClaw profile directory for VulpineOS.
func OpenClawProfileDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".openclaw-vulpine")
}

// OpenClawConfigPath returns the path to the generated openclaw.json.
func OpenClawConfigPath() string {
	return filepath.Join(OpenClawProfileDir(), "openclaw.json")
}
