package agentcore

import "strings"

// providers.go selects the concrete model client for a configured provider and
// normalizes model slugs. VulpineOS exposes ~30 providers (see
// internal/config.Providers). Three need bespoke request/response shapes:
//
//   - "anthropic"     -> Anthropic Messages API   (anthropic.go)
//   - "openai-oauth"  -> Codex Responses API       (codex.go)
//   - everything else -> OpenAI-compatible chat    (model.go)
//
// "Direct" means each provider is called at its own endpoint with its own auth,
// rather than multiplexed through OpenRouter. Providers without a known direct
// endpoint fall back to OpenRouter (see providerBaseURLs).

// providerKind classifies a provider ID into the client family that serves it.
func providerKind(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic":
		return "anthropic"
	case "openai-oauth":
		return "codex"
	default:
		return "openai"
	}
}

// newCompleter builds the model client for cfg's provider. The returned client
// implements Completer, so the loop is provider-agnostic.
func newCompleter(cfg Config) Completer {
	switch providerKind(cfg.Provider) {
	case "anthropic":
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = AnthropicBaseURL
		}
		return NewAnthropicClient(baseURL, cfg.APIKey)
	case "codex":
		return NewCodexClient()
	default:
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = BaseURLForProvider(cfg.Provider)
		}
		return NewModelClient(baseURL, cfg.APIKey)
	}
}

// stripProviderPrefix removes a leading "<provider>/" segment from a model slug
// so the provider's API receives the bare model id it expects. OpenRouter is the
// exception: it keeps the nested vendor path (e.g. "anthropic/claude-...") after
// the "openrouter/" prefix is removed, which this also handles since provider ==
// "openrouter" there.
func stripProviderPrefix(model, provider string) string {
	model = strings.TrimSpace(model)
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "" {
		model = strings.TrimPrefix(model, provider+"/")
	}
	return model
}

// DefaultFallbackModels is the OpenRouter free-model fallback chain. Only
// meaningful when the active provider is OpenRouter; other providers don't share
// these ids. Kept to currently-available free models (OpenRouter removes free
// models often — the loop also skips any that 404, see IsModelUnavailable).
// NOTE: all OpenRouter free models share one per-key daily request cap, so when
// that cap is hit every entry here will 429 too; the real fix is adding credits
// or using a paid/OAuth provider.
func DefaultFallbackModels() []string {
	return []string{
		"openai/gpt-oss-20b:free",
		"meta-llama/llama-3.3-70b-instruct:free",
	}
}
