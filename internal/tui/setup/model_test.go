package setup

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/config"
)

func TestNewWithConfigSeedsExistingValues(t *testing.T) {
	cfg := &config.Config{
		Provider: "anthropic",
		APIKey:   "sk-test",
		Model:    "anthropic/claude-sonnet-4-6",
	}

	m := newWithConfigAndProviders(cfg, setupTestProviders())

	if m.cfg.Provider != cfg.Provider {
		t.Fatalf("provider = %q, want %q", m.cfg.Provider, cfg.Provider)
	}
	if m.cfg.Model != cfg.Model {
		t.Fatalf("model = %q, want %q", m.cfg.Model, cfg.Model)
	}
	if m.cfg.APIKey != cfg.APIKey {
		t.Fatalf("cfg APIKey = %q, want existing key preserved", m.cfg.APIKey)
	}
	if m.apiKeyInput.Value() != "" {
		t.Fatalf("apiKeyInput = %q, want empty", m.apiKeyInput.Value())
	}
	if got := m.viewAPIKey(); strings.Contains(got, cfg.APIKey) {
		t.Fatalf("viewAPIKey leaked stored key: %q", got)
	}
	if m.providerIdx < 0 || m.providerIdx >= len(m.providers) || m.providers[m.providerIdx].ID != cfg.Provider {
		t.Fatalf("providerIdx does not point at %q", cfg.Provider)
	}
}

func TestExistingAPIKeyCanBeKeptBlank(t *testing.T) {
	cfg := &config.Config{
		Provider: "anthropic",
		APIKey:   "sk-test",
		Model:    "anthropic/claude-sonnet-4-6",
	}

	m := newWithConfigAndProviders(cfg, setupTestProviders())
	m.step = stepAPIKey
	m.apiKeyInput.SetValue("")

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := model.(*Model)

	if updated.step != stepDone {
		t.Fatalf("step = %v, want stepDone", updated.step)
	}
	if updated.cfg.APIKey != cfg.APIKey {
		t.Fatalf("cfg APIKey = %q, want existing key preserved", updated.cfg.APIKey)
	}
}

func TestProviderPickerFiltersByTypedSearch(t *testing.T) {
	m := newWithConfigAndProviders(nil, []config.Provider{
		{ID: "anthropic", Name: "Anthropic", EnvVar: "ANTHROPIC_API_KEY", DefaultModel: "anthropic/claude", Models: []string{"anthropic/claude"}, NeedsKey: true},
		{ID: "opencode", Name: "OpenCode Zen", EnvVar: "OPENCODE_API_KEY", DefaultModel: "opencode/deepseek-v4-flash-free", Models: []string{"opencode/deepseek-v4-flash-free"}, NeedsKey: true},
	})
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(*Model)

	for _, r := range "open" {
		model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(*Model)
	}

	view := m.View()
	if !strings.Contains(view, "Filter: open") {
		t.Fatalf("provider view should show typed filter:\n%s", view)
	}
	if !strings.Contains(view, "OpenCode Zen") {
		t.Fatalf("provider filter should keep OpenCode visible:\n%s", view)
	}
	if strings.Contains(view, "Anthropic") {
		t.Fatalf("provider filter should hide non-matches:\n%s", view)
	}
}

func TestModelPickerFiltersByTypedSearchAndSelectsModel(t *testing.T) {
	m := newWithConfigAndProviders(nil, []config.Provider{
		{
			ID:           "opencode",
			Name:         "OpenCode Zen",
			EnvVar:       "OPENCODE_API_KEY",
			DefaultModel: "opencode/deepseek-v4-flash-free",
			Models: []string{
				"opencode/deepseek-v4-flash-free",
				"opencode/minimax-m2.5",
				"opencode/nemotron-3-super-free",
			},
			NeedsKey: true,
		},
	})
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(*Model)

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(*Model)
	if m.step != stepModel {
		t.Fatalf("step = %v, want model picker step", m.step)
	}

	for _, r := range "mini" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(*Model)
	}
	view := m.View()
	if !strings.Contains(view, "Filter: mini") {
		t.Fatalf("model view should show typed filter:\n%s", view)
	}
	if !strings.Contains(view, "opencode/minimax-m2.5") {
		t.Fatalf("model filter should keep MiniMax visible:\n%s", view)
	}
	if strings.Contains(view, "opencode/deepseek-v4-flash-free") {
		t.Fatalf("model filter should hide non-matches:\n%s", view)
	}

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(*Model)
	if m.cfg.Model != "opencode/minimax-m2.5" {
		t.Fatalf("selected model = %q, want opencode/minimax-m2.5", m.cfg.Model)
	}
	if m.step != stepAPIKey {
		t.Fatalf("step = %v, want API key step after model selection", m.step)
	}
}

func TestSetupViewFitsNarrowTerminal(t *testing.T) {
	m := newWithConfigAndProviders(nil, setupTestProviders())
	model, _ := m.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	mm := model.(*Model)

	assertSetupViewFits(t, mm)
}

func TestSetupProviderViewFitsShortTerminal(t *testing.T) {
	m := newWithConfigAndProviders(nil, setupTestProviders())
	model, _ := m.Update(tea.WindowSizeMsg{Width: 50, Height: 10})
	mm := model.(*Model)

	assertSetupViewFits(t, mm)
}

func TestSetupViewFitsUltraTinyTerminal(t *testing.T) {
	m := newWithConfigAndProviders(nil, setupTestProviders())
	model, _ := m.Update(tea.WindowSizeMsg{Width: 4, Height: 4})
	mm := model.(*Model)

	assertSetupViewFits(t, mm)
}

func TestSetupViewFitsVeryNarrowAPIKeyAndDoneSteps(t *testing.T) {
	cfg := &config.Config{
		Provider: "anthropic",
		APIKey:   "sk-test",
		Model:    "anthropic/claude-sonnet-4-6-with-a-long-name",
	}
	m := newWithConfigAndProviders(cfg, setupTestProviders())
	model, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 10})
	mm := model.(*Model)

	mm.step = stepAPIKey
	assertSetupViewFits(t, mm)

	mm.step = stepDone
	assertSetupViewFits(t, mm)
}

func assertSetupViewFits(t *testing.T, m *Model) {
	t.Helper()
	view := m.View()
	lines := strings.Split(view, "\n")
	if m.height > 0 && len(lines) > m.height {
		t.Fatalf("line count = %d, want <= %d:\n%s", len(lines), m.height, view)
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width > m.width {
			t.Fatalf("line %d width = %d, want <= %d:\n%s", i+1, width, m.width, view)
		}
	}
}

func setupTestProviders() []config.Provider {
	return []config.Provider{
		{
			ID:           "anthropic",
			Name:         "Anthropic (Claude)",
			EnvVar:       "ANTHROPIC_API_KEY",
			DefaultModel: "anthropic/claude-sonnet-4-6",
			Models:       []string{"anthropic/claude-sonnet-4-6", "anthropic/claude-opus-4-6"},
			NeedsKey:     true,
		},
		{
			ID:           "opencode",
			Name:         "OpenCode Zen",
			EnvVar:       "OPENCODE_API_KEY",
			DefaultModel: "opencode/deepseek-v4-flash-free",
			Models:       []string{"opencode/deepseek-v4-flash-free", "opencode/minimax-m2.5"},
			NeedsKey:     true,
		},
		{
			ID:           "ollama",
			Name:         "Ollama (Local)",
			DefaultModel: "ollama/llama3.3",
			Models:       []string{"ollama/llama3.3"},
			NeedsKey:     false,
		},
	}
}

func oauthTestProviders() []config.Provider {
	return []config.Provider{
		{ID: "openai", Name: "OpenAI (GPT)", EnvVar: "OPENAI_API_KEY",
			DefaultModel: "openai/gpt-5.4", Models: []string{"openai/gpt-5.4"},
			NeedsKey: true, OAuthProviderID: "openai-oauth"},
		{ID: "openai-oauth", Name: "OpenAI (ChatGPT Sign-in)", EnvVar: "OPENAI_ACCESS_TOKEN",
			DefaultModel: "openai/gpt-5.5", Models: []string{"openai/gpt-5.5"},
			NeedsKey: false, OAuth: true, Hidden: true},
	}
}

func TestSetupHidesOAuthVariantFromPicker(t *testing.T) {
	m := newWithConfigAndProviders(nil, oauthTestProviders())
	for _, p := range m.providers {
		if p.ID == "openai-oauth" {
			t.Fatal("hidden OAuth-variant provider should not appear in the picker")
		}
	}
}

func TestSetupProviderWithOAuthOffersAuthMethod(t *testing.T) {
	m := newWithConfigAndProviders(nil, oauthTestProviders())
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(*Model)
	// Selecting OpenAI (which has an OAuth variant) goes to the auth-method step.
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(*Model)
	if m.step != stepAuthMethod {
		t.Fatalf("step = %v, want stepAuthMethod", m.step)
	}
}

func TestSetupOAuthMethodSelectsHiddenVariantAndStartsLogin(t *testing.T) {
	m := newWithConfigAndProviders(nil, oauthTestProviders())
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(*Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // select OpenAI -> authMethod
	m = model.(*Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // move to OAuth option
	m = model.(*Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // choose OAuth -> model step
	m = model.(*Model)
	if m.step != stepModel {
		t.Fatalf("step = %v, want stepModel", m.step)
	}
	if m.cfg.Provider != "openai-oauth" {
		t.Fatalf("provider = %q, want openai-oauth", m.cfg.Provider)
	}
	// Selecting the model kicks off the OAuth login (returns a begin command).
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(*Model)
	if m.step != stepOAuth {
		t.Fatalf("step = %v, want stepOAuth", m.step)
	}
	if cmd == nil {
		t.Fatal("selecting an OAuth model should start the login flow")
	}
}

func TestSetupAuthMethodAPIKeyChoiceUsesBaseProvider(t *testing.T) {
	m := newWithConfigAndProviders(nil, oauthTestProviders())
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(*Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // OpenAI -> authMethod
	m = model.(*Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // choose API key (idx 0) -> model
	m = model.(*Model)
	if m.cfg.Provider != "openai" {
		t.Fatalf("provider = %q, want openai", m.cfg.Provider)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // select model -> API key step
	m = model.(*Model)
	if m.step != stepAPIKey {
		t.Fatalf("step = %v, want stepAPIKey", m.step)
	}
}
