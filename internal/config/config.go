package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds VulpineOS user configuration.
type Config struct {
	Provider                string                  `json:"provider"`
	APIKey                  string                  `json:"apiKey"`
	Model                   string                  `json:"model"`
	SetupComplete           bool                    `json:"setupComplete"`
	BinaryPath              string                  `json:"binaryPath,omitempty"`
	DefaultBudgetMaxCostUSD float64                 `json:"defaultBudgetMaxCostUsd,omitempty"`
	DefaultBudgetMaxTokens  int64                   `json:"defaultBudgetMaxTokens,omitempty"`
	ResizePanelsWithArrows  bool                    `json:"resizePanelsWithArrows,omitempty"`
	FoxbridgeCDPURL         string                  `json:"-"`                      // runtime-only: set when foxbridge is running
	GlobalSkills            []SkillEntry            `json:"globalSkills,omitempty"` // skills enabled for all agents
	AgentSkills             map[string][]SkillEntry `json:"agentSkills,omitempty"`  // agentID → skills for that agent only
}

// SkillEntry describes a skill configuration.
type SkillEntry struct {
	Name    string            `json:"name"` // skill name/key
	Enabled bool              `json:"enabled"`
	Env     map[string]string `json:"env,omitempty"` // per-skill env vars (e.g. API keys)
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
	ID           string   // e.g. "anthropic"
	Name         string   // e.g. "Anthropic (Claude)"
	EnvVar       string   // e.g. "ANTHROPIC_API_KEY"
	DefaultModel string   // e.g. "anthropic/claude-sonnet-4-6"
	Models       []string // available models
	NeedsKey     bool     // false for ollama
}

// Providers is the full registry of NanoClaw-supported AI providers.
// Matches https://docs.nanoclaw.ai/concepts/model-providers
var Providers = []Provider{
	// --- Tier 1: Major cloud providers ---
	{ID: "anthropic", Name: "Anthropic (Claude)", EnvVar: "ANTHROPIC_API_KEY",
		DefaultModel: "anthropic/claude-sonnet-4-6",
		Models:       []string{"anthropic/claude-opus-4-6", "anthropic/claude-sonnet-4-6", "anthropic/claude-haiku-4-5"},
		NeedsKey:     true},
	{ID: "openai", Name: "OpenAI (GPT)", EnvVar: "OPENAI_API_KEY",
		DefaultModel: "openai/gpt-5.4",
		Models:       []string{"openai/gpt-5.4", "openai/gpt-4.1", "openai/gpt-4.1-mini", "openai/o3"},
		NeedsKey:     true},
	{ID: "google", Name: "Google (Gemini)", EnvVar: "GEMINI_API_KEY",
		DefaultModel: "google/gemini-2.5-pro",
		Models:       []string{"google/gemini-3.1-pro-preview", "google/gemini-2.5-pro", "google/gemini-2.5-flash"},
		NeedsKey:     true},
	{ID: "xai", Name: "xAI (Grok)", EnvVar: "XAI_API_KEY",
		DefaultModel: "xai/grok-3",
		Models:       []string{"xai/grok-3", "xai/grok-3-mini"},
		NeedsKey:     true},
	{ID: "zai", Name: "Z.AI (GLM)", EnvVar: "ZAI_API_KEY",
		DefaultModel: "zai/glm-5",
		Models:       []string{"zai/glm-5", "zai/glm-4.7", "zai/glm-4.6"},
		NeedsKey:     true},

	// --- Tier 2: Routing/Gateway providers ---
	{ID: "openrouter", Name: "OpenRouter", EnvVar: "OPENROUTER_API_KEY",
		DefaultModel: "openrouter/anthropic/claude-sonnet-4-6",
		Models:       []string{"openrouter/anthropic/claude-sonnet-4-6", "openrouter/openai/gpt-4.1", "openrouter/google/gemini-2.5-pro"},
		NeedsKey:     true},
	{ID: "groq", Name: "Groq (LPU)", EnvVar: "GROQ_API_KEY",
		DefaultModel: "groq/llama-3.3-70b-versatile",
		Models:       []string{"groq/llama-3.3-70b-versatile", "groq/mixtral-8x7b-32768"},
		NeedsKey:     true},
	{ID: "mistral", Name: "Mistral", EnvVar: "MISTRAL_API_KEY",
		DefaultModel: "mistral/mistral-large-latest",
		Models:       []string{"mistral/mistral-large-latest", "mistral/codestral-latest"},
		NeedsKey:     true},
	{ID: "together", Name: "Together AI", EnvVar: "TOGETHER_API_KEY",
		DefaultModel: "together/meta-llama/Llama-3.3-70B-Instruct-Turbo",
		Models:       []string{"together/meta-llama/Llama-3.3-70B-Instruct-Turbo", "together/deepseek-ai/DeepSeek-R1"},
		NeedsKey:     true},
	{ID: "cerebras", Name: "Cerebras", EnvVar: "CEREBRAS_API_KEY",
		DefaultModel: "cerebras/llama-3.3-70b",
		Models:       []string{"cerebras/llama-3.3-70b"},
		NeedsKey:     true},

	// --- Tier 3: Specialized providers ---
	{ID: "moonshot", Name: "Moonshot (Kimi)", EnvVar: "MOONSHOT_API_KEY",
		DefaultModel: "moonshot/kimi-k2.5",
		Models:       []string{"moonshot/kimi-k2.5"},
		NeedsKey:     true},
	{ID: "kimi-coding", Name: "Kimi Coding", EnvVar: "KIMI_API_KEY",
		DefaultModel: "kimi-coding/k2p5",
		Models:       []string{"kimi-coding/k2p5"},
		NeedsKey:     true},
	{ID: "minimax", Name: "MiniMax", EnvVar: "MINIMAX_API_KEY",
		DefaultModel: "minimax/MiniMax-M2.5",
		Models:       []string{"minimax/MiniMax-M2.5"},
		NeedsKey:     true},
	{ID: "venice", Name: "Venice AI", EnvVar: "VENICE_API_KEY",
		DefaultModel: "venice/llama-3.3-70b",
		Models:       []string{"venice/llama-3.3-70b"},
		NeedsKey:     true},
	{ID: "nvidia", Name: "NVIDIA", EnvVar: "NVIDIA_API_KEY",
		DefaultModel: "nvidia/llama-3.1-405b-instruct",
		Models:       []string{"nvidia/llama-3.1-405b-instruct"},
		NeedsKey:     true},
	{ID: "huggingface", Name: "Hugging Face", EnvVar: "HF_TOKEN",
		DefaultModel: "huggingface/deepseek-ai/DeepSeek-R1",
		Models:       []string{"huggingface/deepseek-ai/DeepSeek-R1"},
		NeedsKey:     true},
	{ID: "volcengine", Name: "Volcengine (Doubao)", EnvVar: "VOLCANO_ENGINE_API_KEY",
		DefaultModel: "volcengine/doubao-seed-1-8-251228",
		Models:       []string{"volcengine/doubao-seed-1-8-251228"},
		NeedsKey:     true},
	{ID: "byteplus", Name: "BytePlus", EnvVar: "BYTEPLUS_API_KEY",
		DefaultModel: "byteplus/seed-1-8-251228",
		Models:       []string{"byteplus/seed-1-8-251228"},
		NeedsKey:     true},
	{ID: "xiaomi", Name: "Xiaomi", EnvVar: "XIAOMI_API_KEY",
		DefaultModel: "xiaomi/MiMo-7B-RL",
		Models:       []string{"xiaomi/MiMo-7B-RL"},
		NeedsKey:     true},
	{ID: "qianfan", Name: "Qianfan (Baidu)", EnvVar: "QIANFAN_API_KEY",
		DefaultModel: "qianfan/ernie-4.5-128k",
		Models:       []string{"qianfan/ernie-4.5-128k"},
		NeedsKey:     true},
	{ID: "modelstudio", Name: "Model Studio (Alibaba)", EnvVar: "MODELSTUDIO_API_KEY",
		DefaultModel: "modelstudio/qwen-plus",
		Models:       []string{"modelstudio/qwen-plus"},
		NeedsKey:     true},

	// --- Gateway/proxy providers ---
	{ID: "opencode", Name: "OpenCode (Zen)", EnvVar: "OPENCODE_API_KEY",
		DefaultModel: "opencode/deepseek-v4-flash-free",
		Models:       []string{"opencode/deepseek-v4-flash-free", "opencode/minimax-m2.5", "opencode/claude-opus-4-6", "opencode/claude-sonnet-4-6"},
		NeedsKey:     true},
	{ID: "opencode-go", Name: "OpenCode Go", EnvVar: "OPENCODE_API_KEY",
		DefaultModel: "opencode-go/minimax-m2.5",
		Models:       []string{"opencode-go/minimax-m2.5", "opencode-go/deepseek-v4-flash", "opencode-go/deepseek-v4-pro", "opencode-go/minimax-m2.7"},
		NeedsKey:     true},
	{ID: "kilocode", Name: "Kilo Gateway", EnvVar: "KILOCODE_API_KEY",
		DefaultModel: "kilocode/anthropic/claude-opus-4.6",
		Models:       []string{"kilocode/anthropic/claude-opus-4.6"},
		NeedsKey:     true},
	{ID: "vercel-ai-gateway", Name: "Vercel AI Gateway", EnvVar: "AI_GATEWAY_API_KEY",
		DefaultModel: "vercel-ai-gateway/anthropic/claude-opus-4.6",
		Models:       []string{"vercel-ai-gateway/anthropic/claude-opus-4.6"},
		NeedsKey:     true},
	{ID: "cloudflare-ai-gateway", Name: "Cloudflare AI Gateway", EnvVar: "CLOUDFLARE_AI_GATEWAY_API_KEY",
		DefaultModel: "cloudflare-ai-gateway/openai/gpt-4.1",
		Models:       []string{"cloudflare-ai-gateway/openai/gpt-4.1"},
		NeedsKey:     true},
	{ID: "synthetic", Name: "Synthetic", EnvVar: "SYNTHETIC_API_KEY",
		DefaultModel: "synthetic/hf:MiniMaxAI/MiniMax-M2.5",
		Models:       []string{"synthetic/hf:MiniMaxAI/MiniMax-M2.5"},
		NeedsKey:     true},
	{ID: "github-copilot", Name: "GitHub Copilot", EnvVar: "GITHUB_TOKEN",
		DefaultModel: "github-copilot/claude-sonnet-4-6",
		Models:       []string{"github-copilot/claude-sonnet-4-6", "github-copilot/gpt-4.1"},
		NeedsKey:     true},

	// --- Local providers (no API key) ---
	{ID: "ollama", Name: "Ollama (Local)", EnvVar: "",
		DefaultModel: "ollama/llama3.3",
		Models:       []string{"ollama/llama3.3", "ollama/qwen3", "ollama/deepseek-r1", "ollama/codellama"},
		NeedsKey:     false},
	{ID: "vllm", Name: "vLLM (Local)", EnvVar: "",
		DefaultModel: "vllm/your-model-id",
		Models:       []string{"vllm/your-model-id"},
		NeedsKey:     false},
	{ID: "sglang", Name: "SGLang (Local)", EnvVar: "",
		DefaultModel: "sglang/your-model-id",
		Models:       []string{"sglang/your-model-id"},
		NeedsKey:     false},
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
// This allows users to use ANY NanoClaw-supported provider even if we don't list it.
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

// ReconfigureFlagPath returns the marker path used to request the setup wizard
// on the next launch without mutating the active config first.
func ReconfigureFlagPath() string {
	return filepath.Join(Dir(), "reconfigure")
}

// Load reads the config from disk. Returns a zero Config if file doesn't exist.
func Load() (*Config, error) {
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			cfg := &Config{}
			cfg.HydrateFromNanoClawProfile()
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.HydrateFromNanoClawProfile()
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

	return writePrivateFile(Path(), data)
}

// RefreshSetupComplete recomputes whether the config is complete enough to skip setup.
func (c *Config) RefreshSetupComplete() bool {
	ready, err := c.NanoClawConfigReady()
	if err != nil || !ready {
		c.SetupComplete = false
		return false
	}
	c.SetupComplete = true
	return true
}

// NanoClawConfigReady reports whether the config is complete enough to write a
// usable NanoClaw profile. Missing provider/model/key is treated as "not ready
// yet" rather than an error so first-run startup can stay quiet. Unknown
// providers still return an error because that indicates a stale or invalid
// config value.
func (c *Config) NanoClawConfigReady() (bool, error) {
	providerID := strings.TrimSpace(c.Provider)
	model := strings.TrimSpace(c.Model)
	if providerID == "" || model == "" {
		return false, nil
	}
	provider := GetProvider(providerID)
	if provider == nil {
		return false, fmt.Errorf("unknown provider: %s", c.Provider)
	}
	if provider.NeedsKey && strings.TrimSpace(c.APIKey) == "" {
		return false, nil
	}
	return true, nil
}

// HydrateFromNanoClawProfile backfills missing local config values from the
// generated NanoClaw profile when that profile is already configured.
func (c *Config) HydrateFromNanoClawProfile() bool {
	existing, err := readExistingNanoClawConfig()
	if err != nil {
		return false
	}

	changed := false
	if strings.TrimSpace(c.Model) == "" {
		if model := openClawPrimaryModel(existing); model != "" {
			c.Model = model
			changed = true
		}
	}
	if providerID := providerIDFromModel(c.Model); providerID != "" {
		if strings.TrimSpace(c.Provider) == "" || strings.TrimSpace(c.APIKey) == "" {
			c.Provider = providerID
			changed = true
		}
	}
	if strings.TrimSpace(c.APIKey) == "" {
		if apiKey := openClawAPIKey(existing, c.Provider); apiKey != "" {
			c.APIKey = apiKey
			changed = true
		}
	}
	before := c.SetupComplete
	c.RefreshSetupComplete()
	return changed || c.SetupComplete != before
}

// RequestReconfigure asks the next launch to run the setup wizard while keeping
// the current config intact until the wizard completes successfully.
func RequestReconfigure() error {
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	return os.WriteFile(ReconfigureFlagPath(), []byte("1\n"), 0600)
}

// ReconfigureRequested reports whether the next launch should open the setup wizard.
func ReconfigureRequested() bool {
	_, err := os.Stat(ReconfigureFlagPath())
	return err == nil
}

// ClearReconfigureRequest removes the reconfigure marker if present.
func ClearReconfigureRequest() error {
	if err := os.Remove(ReconfigureFlagPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// GenerateNanoClawConfig writes an nanoclaw.json that uses VulpineOS as the browser.
func (c *Config) GenerateNanoClawConfig(vulpineosBinary, camoufoxBinary string) error {
	providerID := strings.TrimSpace(c.Provider)
	model := strings.TrimSpace(c.Model)
	apiKey := strings.TrimSpace(c.APIKey)
	provider := GetProvider(providerID)
	if provider == nil {
		return fmt.Errorf("unknown provider: %s", providerID)
	}

	existing, _ := readExistingNanoClawConfig()

	// Build env map
	env := map[string]interface{}{}
	if provider.EnvVar != "" && apiKey != "" {
		env[provider.EnvVar] = apiKey
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
				"workspace": NanoClawWorkspaceDir(),
				"model": map[string]interface{}{
					"primary": model,
				},
				"compaction": map[string]interface{}{
					"mode": "safeguard",
				},
			},
		},
		"gateway": preservedGatewayConfig(existing),
		// Enable browser — if foxbridge is running, route through Camoufox via CDP proxy.
		// Otherwise fall back to NanoClaw's built-in Chromium.
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
		"commands": preservedCommandsConfig(existing),
	}
	if meta, ok := existing["meta"].(map[string]interface{}); ok && len(meta) > 0 {
		config["meta"] = meta
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal nanoclaw config: %w", err)
	}

	// Write to the VulpineOS-owned NanoClaw directory. Remote sessions use
	// temporary runtime overlays, so the user's local config stays stable.
	profileDir := NanoClawProfileDir()
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return fmt.Errorf("create nanoclaw profile dir: %w", err)
	}
	if err := ensureNanoClawWorkspace(); err != nil {
		return fmt.Errorf("create nanoclaw workspace: %w", err)
	}
	if err := ensureNanoClawProfileSkills(); err != nil {
		return fmt.Errorf("create nanoclaw profile skills: %w", err)
	}

	path := filepath.Join(profileDir, "nanoclaw.json")
	return writePrivateFile(path, data)
}

// RepairNanoClawProfile restores VulpineOS-required NanoClaw profile fields after
// NanoClaw rewrites nanoclaw.json and strips custom settings.
func RepairNanoClawProfile(cdpURL string) error {
	profileDir := NanoClawProfileDir()
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return fmt.Errorf("create nanoclaw profile dir: %w", err)
	}
	if err := ensureNanoClawWorkspace(); err != nil {
		return fmt.Errorf("create nanoclaw workspace: %w", err)
	}
	if err := ensureNanoClawProfileSkills(); err != nil {
		return fmt.Errorf("create nanoclaw profile skills: %w", err)
	}

	existing, err := readExistingNanoClawConfig()
	if err != nil {
		return fmt.Errorf("read nanoclaw config: %w", err)
	}
	applyNanoClawProfileDefaults(existing, cdpURL)

	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal repaired nanoclaw config: %w", err)
	}

	return writePrivateFile(NanoClawConfigPath(), data)
}

func writePrivateFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

// NanoClawProfileDir returns the NanoClaw profile directory for VulpineOS.
func NanoClawProfileDir() string {
	return filepath.Join(Dir(), "nanoclaw")
}

// NanoClawConfigPath returns the path to the generated nanoclaw.json.
func NanoClawConfigPath() string {
	return filepath.Join(NanoClawProfileDir(), "nanoclaw.json")
}

// NanoClawWorkspaceDir returns the isolated NanoClaw workspace for VulpineOS.
func NanoClawWorkspaceDir() string {
	return filepath.Join(NanoClawProfileDir(), "workspace")
}

// NanoClawProfileBrowserRoute returns the route implied by the generated
// NanoClaw profile: "camoufox", "headless", "direct", or "" when unavailable.
func NanoClawProfileBrowserRoute() string {
	existing, err := readExistingNanoClawConfig()
	if err != nil {
		return ""
	}
	browser, ok := existing["browser"].(map[string]interface{})
	if !ok || browser == nil {
		return ""
	}
	if strings.TrimSpace(stringDetail(browser["cdpUrl"])) != "" {
		return "camoufox"
	}
	if boolDetail(browser["headless"]) {
		return "headless"
	}
	if boolDetail(browser["enabled"]) {
		return "direct"
	}
	return ""
}

func readExistingNanoClawConfig() (map[string]interface{}, error) {
	data, err := os.ReadFile(NanoClawConfigPath())
	if err != nil {
		return nil, err
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func openClawPrimaryModel(existing map[string]interface{}) string {
	agents, ok := existing["agents"].(map[string]interface{})
	if !ok || agents == nil {
		return ""
	}
	defaults, ok := agents["defaults"].(map[string]interface{})
	if !ok || defaults == nil {
		return ""
	}
	model, ok := defaults["model"].(map[string]interface{})
	if !ok || model == nil {
		return ""
	}
	return strings.TrimSpace(stringDetail(model["primary"]))
}

func providerIDFromModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	providerID, _, ok := strings.Cut(model, "/")
	if !ok {
		return ""
	}
	if GetProvider(providerID) == nil {
		return ""
	}
	return providerID
}

func openClawAPIKey(existing map[string]interface{}, providerID string) string {
	provider := GetProvider(strings.TrimSpace(providerID))
	if provider == nil || provider.EnvVar == "" {
		return ""
	}
	env, ok := existing["env"].(map[string]interface{})
	if !ok || env == nil {
		return ""
	}
	return strings.TrimSpace(stringDetail(env[provider.EnvVar]))
}

func stringDetail(v interface{}) string {
	s, _ := v.(string)
	return s
}

func boolDetail(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

func preservedGatewayConfig(existing map[string]interface{}) map[string]interface{} {
	gateway := map[string]interface{}{
		"mode": "local",
	}
	if existingGateway, ok := existing["gateway"].(map[string]interface{}); ok {
		for key, value := range existingGateway {
			gateway[key] = value
		}
		gateway["mode"] = "local"
	}
	return gateway
}

func preservedCommandsConfig(existing map[string]interface{}) map[string]interface{} {
	commands := map[string]interface{}{
		"native":       "auto",
		"nativeSkills": "auto",
		"restart":      true,
		"ownerDisplay": "raw",
	}
	if existingCommands, ok := existing["commands"].(map[string]interface{}); ok {
		for key, value := range existingCommands {
			commands[key] = value
		}
	}
	return commands
}

func applyNanoClawProfileDefaults(cfg map[string]interface{}, cdpURL string) {
	agents, ok := cfg["agents"].(map[string]interface{})
	if !ok || agents == nil {
		agents = make(map[string]interface{})
		cfg["agents"] = agents
	}
	defaults, ok := agents["defaults"].(map[string]interface{})
	if !ok || defaults == nil {
		defaults = make(map[string]interface{})
		agents["defaults"] = defaults
	}
	defaults["workspace"] = NanoClawWorkspaceDir()
	if _, ok := defaults["compaction"].(map[string]interface{}); !ok {
		defaults["compaction"] = map[string]interface{}{"mode": "safeguard"}
	}

	browser, ok := cfg["browser"].(map[string]interface{})
	if !ok || browser == nil {
		browser = make(map[string]interface{})
		cfg["browser"] = browser
	}
	browser["enabled"] = true
	browser["headless"] = true
	if strings.TrimSpace(cdpURL) != "" {
		browser["cdpUrl"] = cdpURL
	} else {
		delete(browser, "cdpUrl")
	}
}

func ensureNanoClawWorkspace() error {
	workspaceDir := NanoClawWorkspaceDir()
	if err := os.MkdirAll(workspaceDir, 0700); err != nil {
		return err
	}
	bootstrap := map[string]string{
		"AGENTS.md": `# VulpineOS Agent Workspace

This workspace is owned by VulpineOS.

- Follow the agent name and task provided by the current user message.
- Do not claim a different persistent personal identity from old workspace files.
- When you take an action, state the exact action you are about to take.
- After each action, report whether it succeeded, failed, or returned incomplete data.
- Never claim an action succeeded when the tool returned an error, timeout, or incomplete result.
- Keep progress reports concrete and short.`,
		"BOOTSTRAP.md": `# VulpineOS Bootstrap

This workspace is managed by VulpineOS for the current session.

- Use the agent name and task assigned by the current VulpineOS request.
- Ignore older persona/bootstrap text from previous sessions.
- Do not ask the user to define your identity unless the current task explicitly requires that.
- Start by working on the assigned task directly and report concrete progress.
`,
		"IDENTITY.md": `# VulpineOS Identity

- Name: Use the name assigned by the current user message.
- Role: Task-focused AI agent for the current session.
- Rule: Do not override the current task with an older persona or identity file.
`,
		"SOUL.md": `# VulpineOS SOUL

Be direct, accurate, and task-focused.
Do not invent a separate persona when the user has assigned you a role for this session.
`,
		"TOOLS.md": `# VulpineOS Tools

Use the configured browser path first. If a tool or browser path fails, say exactly what failed.
- On a fresh browser session, use ` + "`browser open <url>`" + ` for the first page load.
- ` + "`browser navigate`" + ` without a target only works when a page already exists.
- ` + "`browser start`" + ` starts the browser service but does not create a page by itself.
- VulpineOS snapshots default to a compact profile. If a required target is missing from a truncated snapshot, retry once with expanded limits before concluding it is absent.
`,
	}
	for name, content := range bootstrap {
		path := filepath.Join(workspaceDir, name)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			return err
		}
	}
	return nil
}

func ensureNanoClawProfileSkills() error {
	skillDir := filepath.Join(NanoClawProfileDir(), "skills", "vulpine-browser")
	if err := os.MkdirAll(skillDir, 0700); err != nil {
		return err
	}
	skill := `---
name: vulpine-browser
version: 2.0.0
description: Browse the web through VulpineOS using the built-in browser tool
tools:
  - browser
---

# VulpineOS Browser

VulpineOS already routes NanoClaw's built-in ` + "`browser`" + ` tool through foxbridge into Camoufox.

Rules:

1. Use the ` + "`browser`" + ` tool directly. Do not call ` + "`vulpineos-browser`" + ` or any shell helper.
2. Report the exact action you are about to take before using the browser tool.
3. After each browser action, report whether it succeeded, failed, or returned incomplete data.
4. If the browser tool returns an auth or gateway error, report that error exactly and stop guessing.
5. Never reply with a requested success string if the browser action failed, timed out, or returned incomplete data.
6. On a fresh browser session, use ` + "`browser open <url>`" + ` for the first page load.
7. ` + "`browser navigate`" + ` without a target assumes a page already exists.
8. ` + "`browser start`" + ` starts the service but does not create a page.
9. VulpineOS snapshots default to a compact profile. If a required target is missing from a truncated snapshot, retry with expanded limits before concluding it is absent.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skill), 0600); err != nil {
		return err
	}

	// Add Superpowers collection – a minimal placeholder for each official skill.
	superpowers := []string{
		"brainstorming",
		"using-superpowers",
		"systematic-debugging",
		"writing-plans",
		"requesting-code-review",
		"test-driven-development",
		"executing-plans",
		"subagent-driven-development",
		"verification-before-completion",
		"receiving-code-review",
		"writing-skills",
		"dispatching-parallel-agents",
		"using-git-worktrees",
		"finishing-a-development-branch",
	}
	for _, name := range superpowers {
		skDir := filepath.Join(NanoClawProfileDir(), "skills", name)
		if err := os.MkdirAll(skDir, 0700); err != nil {
			return err
		}
		content := fmt.Sprintf(`---
name: %s
version: 1.0.0
description: Placeholder for the "%s" superpowers skill
tools: []
---
`, name, name)
		if err := os.WriteFile(filepath.Join(skDir, "SKILL.md"), []byte(content), 0600); err != nil {
			return err
		}
	}

	return nil
}
