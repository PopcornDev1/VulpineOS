package agentcore

import "testing"

func TestProviderKind(t *testing.T) {
	cases := map[string]string{
		"anthropic":    "anthropic",
		"Anthropic":    "anthropic",
		"openai-oauth": "codex",
		"openrouter":   "openai",
		"openai":       "openai",
		"google":       "openai",
		"":             "openai",
	}
	for in, want := range cases {
		if got := providerKind(in); got != want {
			t.Errorf("providerKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripProviderPrefix(t *testing.T) {
	cases := []struct{ model, provider, want string }{
		{"openai/gpt-5.4", "openai", "gpt-5.4"},
		{"openrouter/anthropic/claude-sonnet-4-6", "openrouter", "anthropic/claude-sonnet-4-6"},
		{"google/gemini-2.5-pro", "google", "gemini-2.5-pro"},
		{"zai/glm-5", "zai", "glm-5"},
		// OpenRouter fallback slugs keep their nested vendor path.
		{"openai/gpt-oss-20b:free", "openrouter", "openai/gpt-oss-20b:free"},
		// openai-oauth default model is "openai/..."; prefix doesn't match, codex normalizes later.
		{"openai/gpt-4.1", "openai-oauth", "openai/gpt-4.1"},
	}
	for _, c := range cases {
		if got := stripProviderPrefix(c.model, c.provider); got != c.want {
			t.Errorf("stripProviderPrefix(%q,%q) = %q, want %q", c.model, c.provider, got, c.want)
		}
	}
}

func TestModelChainStripsAndDedupes(t *testing.T) {
	cfg := Config{
		Provider:       "openrouter",
		Model:          "openrouter/z-ai/glm-4.5-air:free",
		FallbackModels: []string{"openai/gpt-oss-20b:free", "openrouter/z-ai/glm-4.5-air:free"},
	}
	chain := cfg.modelChain()
	want := []string{"z-ai/glm-4.5-air:free", "openai/gpt-oss-20b:free"}
	if len(chain) != len(want) {
		t.Fatalf("chain = %v, want %v", chain, want)
	}
	for i := range want {
		if chain[i] != want[i] {
			t.Fatalf("chain = %v, want %v", chain, want)
		}
	}
}

func TestBaseURLForProviderDirect(t *testing.T) {
	if got := BaseURLForProvider("groq"); got != "https://api.groq.com/openai/v1" {
		t.Errorf("groq base = %q", got)
	}
	if got := BaseURLForProvider("google"); got != "https://generativelanguage.googleapis.com/v1beta/openai" {
		t.Errorf("google base = %q", got)
	}
	// Unknown provider falls back to OpenRouter.
	if got := BaseURLForProvider("totally-unknown"); got != OpenRouterBaseURL {
		t.Errorf("unknown base = %q, want OpenRouter", got)
	}
}

func TestNewCompleterSelectsClient(t *testing.T) {
	if _, ok := newCompleter(Config{Provider: "anthropic", APIKey: "k"}).(*AnthropicClient); !ok {
		t.Error("anthropic provider did not select AnthropicClient")
	}
	if _, ok := newCompleter(Config{Provider: "openai-oauth"}).(*CodexClient); !ok {
		t.Error("openai-oauth provider did not select CodexClient")
	}
	if _, ok := newCompleter(Config{Provider: "openrouter", APIKey: "k"}).(*ModelClient); !ok {
		t.Error("openrouter provider did not select ModelClient")
	}
}

func TestToAnthropicMessagesCoalescesAndSplitsSystem(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "do it"},
		{Role: "assistant", Content: "ok", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "vulpine_navigate", Arguments: `{"url":"x"}`}}}},
		{Role: "tool", ToolCallID: "c1", Content: "navigated"},
		{Role: "tool", ToolCallID: "c1", Content: "second result"},
	}
	system, out := toAnthropicMessages(msgs)
	if system != "be brief" {
		t.Errorf("system = %q", system)
	}
	// Expect: user(text), assistant(text+tool_use), user(two tool_results coalesced).
	if len(out) != 3 {
		t.Fatalf("messages = %d, want 3 (%+v)", len(out), out)
	}
	if out[0].Role != "user" || out[1].Role != "assistant" || out[2].Role != "user" {
		t.Fatalf("roles = %s/%s/%s", out[0].Role, out[1].Role, out[2].Role)
	}
	if len(out[1].Content) != 2 || out[1].Content[1].Type != "tool_use" {
		t.Errorf("assistant blocks = %+v, want text + tool_use", out[1].Content)
	}
	if len(out[2].Content) != 2 || out[2].Content[0].Type != "tool_result" {
		t.Errorf("tool results not coalesced into one user message: %+v", out[2].Content)
	}
}

func TestToCodexInputBuildsResponsesItems(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "bash", Arguments: `{"command":"ls"}`}}}},
		{Role: "tool", ToolCallID: "c1", Content: "files"},
	}
	instructions, input := toCodexInput(msgs)
	if instructions != "sys" {
		t.Errorf("instructions = %q", instructions)
	}
	if len(input) != 3 {
		t.Fatalf("input items = %d, want 3 (%+v)", len(input), input)
	}
	if input[0].Type != "message" || input[0].Role != "user" {
		t.Errorf("item0 = %+v", input[0])
	}
	if input[1].Type != "function_call" || input[1].CallID != "c1" || input[1].Name != "bash" {
		t.Errorf("item1 = %+v", input[1])
	}
	if input[2].Type != "function_call_output" || input[2].CallID != "c1" || input[2].Output != "files" {
		t.Errorf("item2 = %+v", input[2])
	}
}

func TestNormalizeCodexModel(t *testing.T) {
	cases := map[string]string{
		"openai/gpt-5.5": "gpt-5.5",
		"gpt-5.4":        "gpt-5.4",
		"openai/gpt-4.1": "gpt-5.5", // unknown -> default
		"":               "gpt-5.5",
	}
	for in, want := range cases {
		if got := normalizeCodexModel(in); got != want {
			t.Errorf("normalizeCodexModel(%q) = %q, want %q", in, got, want)
		}
	}
}
