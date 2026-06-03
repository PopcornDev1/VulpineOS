package setup

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/auth"
	"vulpineos/internal/config"
)

type step int

const (
	stepProvider step = iota
	stepAuthMethod
	stepModel
	stepAPIKey
	stepOAuth
	stepDone
)

// OAuth flow messages (async, driven by tea.Cmds).
type oauthBeganMsg struct {
	login *auth.OpenAILogin
	err   error
}
type oauthCodeMsg struct {
	code string
	err  error
}
type oauthDoneMsg struct{ err error }

// IsAsyncMsg reports whether msg is one of the wizard's internal async messages
// (e.g. the OAuth sign-in flow steps). When the wizard runs embedded inside the
// main TUI, the host must forward these back to Update so the OAuth state
// machine can advance — otherwise the flow stalls before showing the auth URL.
func IsAsyncMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case oauthBeganMsg, oauthCodeMsg, oauthDoneMsg:
		return true
	default:
		return false
	}
}

func beginOAuthCmd() tea.Cmd {
	return func() tea.Msg {
		login, err := auth.BeginOpenAILogin()
		return oauthBeganMsg{login: login, err: err}
	}
}

func waitOAuthCodeCmd(login *auth.OpenAILogin) tea.Cmd {
	return func() tea.Msg {
		code, err := login.WaitForCode(5 * time.Minute)
		return oauthCodeMsg{code: code, err: err}
	}
}

func completeOAuthCmd(login *auth.OpenAILogin, code string) tea.Cmd {
	return func() tea.Msg {
		return oauthDoneMsg{err: login.Complete(code)}
	}
}

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED"))
	activeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED")).Bold(true)
	mutedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981")).Bold(true)
	boxStyle     = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7C3AED")).
			Padding(1, 2).
			Width(76)
)

// Model is the setup wizard Bubbletea model.
type Model struct {
	step          step
	width, height int
	providers     []config.Provider
	providerIdx   int
	providerQuery string
	apiKeyInput   textinput.Model
	modelIdx      int
	modelQuery    string
	cfg           *config.Config
	done          bool

	// Auth-method selection (for providers offering both API key and OAuth).
	baseProvider  config.Provider // the picked base provider when it has an OAuth variant
	authMethodIdx int             // 0 = API key, 1 = OAuth sign-in

	// OAuth sign-in flow state.
	login       *auth.OpenAILogin
	oauthStatus string
	oauthErr    string
	pasteInput  textinput.Model
}

// New creates a new setup wizard.
func New() *Model {
	return NewWithConfig(nil)
}

// NewWithConfig creates a setup wizard seeded from an existing config.
func NewWithConfig(existing *config.Config) *Model {
	return newWithConfigAndProviders(existing, config.MergedProviders())
}

func newWithConfigAndProviders(existing *config.Config, providers []config.Provider) *Model {
	ti := textinput.New()
	ti.Placeholder = "Paste your API key here..."
	ti.CharLimit = 200
	ti.Width = 50
	paste := textinput.New()
	paste.Placeholder = "...or paste the redirect URL here"
	paste.CharLimit = 2000
	paste.Width = 50
	if len(providers) == 0 {
		providers = config.Providers
	}
	// Hide OAuth-variant providers from the picker; they're reached via the
	// base provider's "Sign in" option.
	visible := make([]config.Provider, 0, len(providers))
	for _, p := range providers {
		if p.Hidden {
			continue
		}
		visible = append(visible, p)
	}

	m := Model{
		step:        stepProvider,
		providers:   visible,
		apiKeyInput: ti,
		pasteInput:  paste,
		cfg:         &config.Config{},
	}
	if existing == nil {
		return &m
	}

	m.cfg.Provider = existing.Provider
	m.cfg.APIKey = existing.APIKey
	m.cfg.Model = existing.Model
	m.cfg.BinaryPath = existing.BinaryPath
	m.cfg.GlobalSkills = append([]config.SkillEntry(nil), existing.GlobalSkills...)
	if len(existing.AgentSkills) > 0 {
		m.cfg.AgentSkills = make(map[string][]config.SkillEntry, len(existing.AgentSkills))
		for agentID, skills := range existing.AgentSkills {
			m.cfg.AgentSkills[agentID] = append([]config.SkillEntry(nil), skills...)
		}
	}
	for i, p := range m.providers {
		if p.ID != existing.Provider {
			continue
		}
		m.providerIdx = i
		for idx, model := range p.Models {
			if model == existing.Model {
				m.modelIdx = idx
				break
			}
		}
		break
	}
	return &m
}

// Config returns the completed config.
func (m *Model) Config() *config.Config {
	return m.cfg
}

// Done returns true if setup is complete and user pressed Enter on the final screen.
func (m *Model) Done() bool {
	return m.done
}

func (m *Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.apiKeyInput.Width = m.inputWidth()
		m.pasteInput.Width = m.inputWidth()

	case oauthBeganMsg:
		if m.step != stepOAuth {
			return m, nil
		}
		if msg.err != nil {
			m.oauthErr = msg.err.Error()
			m.oauthStatus = ""
			return m, nil
		}
		m.login = msg.login
		_ = m.login.OpenBrowser()
		if m.login.CallbackBound {
			m.oauthStatus = "Waiting for you to sign in in the browser..."
		} else {
			m.oauthStatus = "Sign in in the browser, then paste the redirect URL below."
		}
		m.pasteInput.Focus()
		return m, waitOAuthCodeCmd(m.login)

	case oauthCodeMsg:
		if m.step != stepOAuth || m.login == nil {
			return m, nil
		}
		if msg.err != nil {
			// Callback path failed or timed out — fall back to manual paste.
			m.oauthStatus = "Paste the full redirect URL from your browser below, then Enter."
			return m, nil
		}
		m.oauthStatus = "Completing sign-in..."
		return m, completeOAuthCmd(m.login, msg.code)

	case oauthDoneMsg:
		if m.step != stepOAuth {
			return m, nil
		}
		if msg.err != nil {
			m.oauthErr = msg.err.Error()
			m.oauthStatus = ""
			return m, nil
		}
		if m.login != nil {
			m.login.Close()
			m.login = nil
		}
		m.cfg.SetupComplete = true
		m.step = stepDone
		return m, nil

	case tea.KeyMsg:
		switch m.step {
		case stepProvider:
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.providerQuery = ""
				m.providerIdx = 0
			case "j":
				if m.providerQuery == "" {
					m.moveProviderSelection(1)
				} else if m.appendProviderQuery(msg) {
					m.providerIdx = 0
				}
			case "down":
				m.moveProviderSelection(1)
			case "k":
				if m.providerQuery == "" {
					m.moveProviderSelection(-1)
				} else if m.appendProviderQuery(msg) {
					m.providerIdx = 0
				}
			case "up":
				m.moveProviderSelection(-1)
			case "backspace", "ctrl+h":
				m.providerQuery = trimLastRune(m.providerQuery)
				m.providerIdx = 0
			case "ctrl+u":
				m.providerQuery = ""
				m.providerIdx = 0
			case "enter":
				p, ok := m.selectedProvider()
				if !ok {
					break
				}
				// Providers offering both API key and OAuth go through an
				// auth-method choice first (e.g. OpenAI: API key or Sign in).
				if p.OAuthProviderID != "" {
					m.baseProvider = p
					m.authMethodIdx = 0
					m.step = stepAuthMethod
					break
				}
				m.applyProviderSelection(p.ID)
				m.step = stepModel
			default:
				if m.appendProviderQuery(msg) {
					m.providerIdx = 0
				}
			}

		case stepAuthMethod:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.step = stepProvider
			case "j", "down":
				if m.authMethodIdx < 1 {
					m.authMethodIdx++
				}
			case "k", "up":
				if m.authMethodIdx > 0 {
					m.authMethodIdx--
				}
			case "enter":
				chosen := m.baseProvider.ID
				if m.authMethodIdx == 1 {
					chosen = m.baseProvider.OAuthProviderID
				}
				m.applyProviderSelection(chosen)
				m.step = stepModel
			}

		case stepModel:
			p, ok := m.selectedConfigProvider()
			if !ok {
				m.step = stepProvider
				break
			}
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				if m.modelQuery != "" {
					m.modelQuery = ""
					m.modelIdx = m.modelIndexFor(p, m.cfg.Model)
				} else {
					m.step = stepProvider
				}
			case "j":
				if m.modelQuery == "" {
					m.moveModelSelection(p, 1)
				} else if m.appendModelQuery(msg) {
					m.modelIdx = 0
				}
			case "down":
				m.moveModelSelection(p, 1)
			case "k":
				if m.modelQuery == "" {
					m.moveModelSelection(p, -1)
				} else if m.appendModelQuery(msg) {
					m.modelIdx = 0
				}
			case "up":
				m.moveModelSelection(p, -1)
			case "backspace", "ctrl+h":
				m.modelQuery = trimLastRune(m.modelQuery)
				m.modelIdx = 0
			case "ctrl+u":
				m.modelQuery = ""
				m.modelIdx = 0
			case "enter":
				model, ok := m.selectedModel(p)
				if !ok {
					break
				}
				m.cfg.Model = model
				if p.OAuth {
					m.oauthErr = ""
					m.oauthStatus = "Starting sign-in..."
					m.pasteInput.SetValue("")
					m.step = stepOAuth
					return m, beginOAuthCmd()
				}
				if !p.NeedsKey {
					m.cfg.SetupComplete = true
					m.step = stepDone
					break
				}
				if strings.TrimSpace(m.cfg.APIKey) != "" {
					m.apiKeyInput.Placeholder = fmt.Sprintf("%s already stored; leave blank to keep it", p.EnvVar)
				} else {
					m.apiKeyInput.Placeholder = fmt.Sprintf("Enter your %s...", p.EnvVar)
				}
				m.apiKeyInput.SetValue("")
				m.apiKeyInput.Focus()
				m.step = stepAPIKey
			default:
				if m.appendModelQuery(msg) {
					m.modelIdx = 0
				}
			}
			if p, ok := m.selectedConfigProvider(); ok {
				if model, ok := m.selectedModel(p); ok {
					m.cfg.Model = model
				}
			}

		case stepAPIKey:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.step = stepModel
				m.apiKeyInput.Blur()
			case "enter":
				key := strings.TrimSpace(m.apiKeyInput.Value())
				if key == "" {
					if strings.TrimSpace(m.cfg.APIKey) == "" {
						break
					}
				} else {
					m.cfg.APIKey = key
				}
				m.cfg.SetupComplete = true
				m.step = stepDone
			default:
				var cmd tea.Cmd
				m.apiKeyInput, cmd = m.apiKeyInput.Update(msg)
				return m, cmd
			}

		case stepOAuth:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				if m.login != nil {
					m.login.Close()
					m.login = nil
				}
				m.pasteInput.Blur()
				m.step = stepModel
			case "enter":
				raw := strings.TrimSpace(m.pasteInput.Value())
				if raw == "" || m.login == nil {
					break
				}
				code, err := m.login.CodeFromRedirectURL(raw)
				if err != nil {
					m.oauthErr = err.Error()
					break
				}
				m.oauthErr = ""
				m.oauthStatus = "Completing sign-in..."
				return m, completeOAuthCmd(m.login, code)
			default:
				var cmd tea.Cmd
				m.pasteInput, cmd = m.pasteInput.Update(msg)
				return m, cmd
			}

		case stepDone:
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "enter":
				m.done = true
				return m, tea.Quit
			}
		}
	}

	return m, nil
}

// applyProviderSelection sets the chosen provider id and reseeds the model
// selection (clearing a stale API key when the provider changed). It resolves
// the provider via the registry so OAuth-variant ids (hidden from the picker)
// work too.
func (m *Model) applyProviderSelection(providerID string) {
	previousProvider := m.cfg.Provider
	m.cfg.Provider = providerID
	if previousProvider != "" && previousProvider != providerID {
		m.cfg.APIKey = ""
	}
	m.modelQuery = ""
	p, ok := m.selectedConfigProvider()
	if !ok {
		return
	}
	m.modelIdx = m.modelIndexFor(p, m.cfg.Model)
	if model, ok := m.selectedModel(p); ok {
		m.cfg.Model = model
	} else {
		m.cfg.Model = p.DefaultModel
	}
}

func (m *Model) moveProviderSelection(delta int) {
	total := len(m.filteredProviderIndices())
	if total == 0 {
		m.providerIdx = 0
		return
	}
	m.providerIdx += delta
	if m.providerIdx < 0 {
		m.providerIdx = 0
	}
	if m.providerIdx >= total {
		m.providerIdx = total - 1
	}
}

func (m *Model) moveModelSelection(p config.Provider, delta int) {
	total := len(m.filteredModelIndices(p))
	if total == 0 {
		m.modelIdx = 0
		return
	}
	m.modelIdx += delta
	if m.modelIdx < 0 {
		m.modelIdx = 0
	}
	if m.modelIdx >= total {
		m.modelIdx = total - 1
	}
}

func (m *Model) appendProviderQuery(msg tea.KeyMsg) bool {
	if len(msg.Runes) == 0 {
		return false
	}
	m.providerQuery += string(msg.Runes)
	return true
}

func (m *Model) appendModelQuery(msg tea.KeyMsg) bool {
	if len(msg.Runes) == 0 {
		return false
	}
	m.modelQuery += string(msg.Runes)
	return true
}

func trimLastRune(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	return string(runes[:len(runes)-1])
}

func (m *Model) selectedProvider() (config.Provider, bool) {
	indices := m.filteredProviderIndices()
	if len(indices) == 0 {
		return config.Provider{}, false
	}
	if m.providerIdx < 0 {
		m.providerIdx = 0
	}
	if m.providerIdx >= len(indices) {
		m.providerIdx = len(indices) - 1
	}
	return m.providers[indices[m.providerIdx]], true
}

func (m *Model) selectedConfigProvider() (config.Provider, bool) {
	for _, p := range m.providers {
		if p.ID == m.cfg.Provider {
			return p, true
		}
	}
	if p := config.GetProvider(m.cfg.Provider); p != nil {
		return *p, true
	}
	return config.Provider{}, false
}

func (m *Model) selectedModel(p config.Provider) (string, bool) {
	models := m.providerModels(p)
	indices := m.filteredModelIndices(p)
	if len(indices) == 0 {
		return "", false
	}
	if m.modelIdx < 0 {
		m.modelIdx = 0
	}
	if m.modelIdx >= len(indices) {
		m.modelIdx = len(indices) - 1
	}
	return models[indices[m.modelIdx]], true
}

func (m *Model) modelIndexFor(p config.Provider, model string) int {
	models := m.providerModels(p)
	for i, candidate := range models {
		if candidate == model {
			return i
		}
	}
	for i, candidate := range models {
		if candidate == p.DefaultModel {
			return i
		}
	}
	return 0
}

func (m *Model) providerModels(p config.Provider) []string {
	// OAuth variants inherit models from their base provider so the full
	// remote-catalog model list is available (not just the built-in subset).
	if p.OAuth {
		for _, bp := range m.providers {
			if bp.OAuthProviderID == p.ID {
				if len(bp.Models) > 0 {
					return bp.Models
				}
				break
			}
		}
	}
	if len(p.Models) > 0 {
		return p.Models
	}
	if strings.TrimSpace(p.DefaultModel) != "" {
		return []string{p.DefaultModel}
	}
	return []string{p.ID + "/default"}
}

func (m *Model) filteredProviderIndices() []int {
	query := strings.ToLower(strings.TrimSpace(m.providerQuery))
	indices := make([]int, 0, len(m.providers))
	for i, p := range m.providers {
		if query == "" || strings.Contains(strings.ToLower(p.ID+" "+p.Name+" "+p.EnvVar), query) {
			indices = append(indices, i)
		}
	}
	return indices
}

func (m *Model) filteredModelIndices(p config.Provider) []int {
	query := strings.ToLower(strings.TrimSpace(m.modelQuery))
	models := m.providerModels(p)
	indices := make([]int, 0, len(models))
	for i, model := range models {
		if query == "" || strings.Contains(strings.ToLower(model), query) {
			indices = append(indices, i)
		}
	}
	return indices
}

func (m *Model) View() string {
	var content string

	switch m.step {
	case stepProvider:
		content = m.viewProvider()
	case stepAuthMethod:
		content = m.viewAuthMethod()
	case stepModel:
		content = m.viewModel()
	case stepAPIKey:
		content = m.viewAPIKey()
	case stepOAuth:
		content = m.viewOAuth()
	case stepDone:
		content = m.viewDone()
	}

	if m.width > 0 && m.width < 8 {
		return fitSetupBlock(content, m.width, m.height)
	}

	box := boxStyle.Width(m.boxWidth()).Render(content)

	// Center vertically
	pad := (m.height - lipgloss.Height(box)) / 2
	if pad < 0 {
		pad = 0
	}

	return fitSetupBlock(strings.Repeat("\n", pad)+lipgloss.PlaceHorizontal(m.width, lipgloss.Center, box), m.width, m.height)
}

func (m *Model) boxWidth() int {
	if m.width <= 0 {
		return 76
	}
	width := m.width - 2
	if width > 76 {
		width = 76
	}
	if width < 12 {
		width = max(1, m.width-2)
	}
	return width
}

func (m *Model) inputWidth() int {
	width := m.boxWidth() - 8
	if width > 50 {
		width = 50
	}
	if width < 10 {
		width = 10
	}
	return width
}

func (m *Model) contentWidth() int {
	width := m.boxWidth() - 4
	if width < 1 {
		return 1
	}
	return width
}

func (m *Model) viewProvider() string {
	if m.height > 0 && m.height < 14 {
		return m.viewProviderCompact()
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("VulpineOS — First Time Setup"))
	b.WriteString("\n\n")
	b.WriteString("Select your AI provider:\n\n")
	b.WriteString(fmt.Sprintf("Filter: %s\n\n", activeStyle.Render(m.providerQuery)))

	filtered := m.filteredProviderIndices()
	if len(filtered) == 0 {
		b.WriteString(mutedStyle.Render("No providers match."))
		b.WriteString("\n\n")
		b.WriteString(mutedStyle.Render("[Backspace] edit  [Esc] clear  [q] quit"))
		return b.String()
	}

	maxVisible := m.maxVisibleProviders(9)
	start := 0
	if m.providerIdx >= maxVisible {
		start = m.providerIdx - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(filtered) {
		end = len(filtered)
	}

	for i := start; i < end; i++ {
		p := m.providers[filtered[i]]
		envText := p.EnvVar
		if !p.NeedsKey {
			envText = "No key needed"
		}
		envHint := mutedStyle.Render(envText)
		rowWidth := m.contentWidth()
		nameWidth := rowWidth - 4 - lipgloss.Width(envText)
		if nameWidth < 8 {
			nameWidth = 8
		}
		name := padSetupText(fitSetupText(p.Name, nameWidth), nameWidth)
		var line string
		if i == m.providerIdx {
			line = activeStyle.Render("▸ ") + lipgloss.NewStyle().Bold(true).Render(name) + "  " + envHint
		} else {
			line = "  " + mutedStyle.Render(name) + "  " + envHint
		}
		b.WriteString(fitSetupLine(line, rowWidth) + "\n")
	}

	// Scroll indicators
	scrollInfo := ""
	if start > 0 && end < len(filtered) {
		scrollInfo = fmt.Sprintf("  ↑ %d above · ↓ %d below", start, len(filtered)-end)
	} else if start > 0 {
		scrollInfo = fmt.Sprintf("  ↑ %d more above", start)
	} else if end < len(filtered) {
		scrollInfo = fmt.Sprintf("  ↓ %d more below", len(filtered)-end)
	}
	if scrollInfo != "" {
		b.WriteString(mutedStyle.Render(scrollInfo) + "\n")
	}

	b.WriteString("\n")
	count := fmt.Sprintf("%d/%d providers", len(filtered), len(m.providers))
	if strings.TrimSpace(m.providerQuery) == "" {
		count = fmt.Sprintf("%d providers", len(m.providers))
	}
	b.WriteString(fitSetupLine(mutedStyle.Render(fmt.Sprintf("[↑/↓] navigate  type to filter  [Enter] select  [Esc] clear  [q] quit  (%s)", count)), m.contentWidth()))
	return b.String()
}

func (m *Model) viewProviderCompact() string {
	var b strings.Builder
	p, ok := m.selectedProvider()
	b.WriteString(titleStyle.Render("VulpineOS Setup"))
	b.WriteString("\n")
	b.WriteString(fitSetupText("Filter: "+m.providerQuery, m.contentWidth()))
	b.WriteString("\n")
	b.WriteString("Provider:\n")
	if !ok {
		b.WriteString(mutedStyle.Render(fitSetupText("No matches", m.contentWidth())))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(fitSetupText("[Backspace] edit", m.contentWidth())))
		return b.String()
	}
	row := fmt.Sprintf("%d/%d  %s", m.providerIdx+1, len(m.filteredProviderIndices()), p.Name)
	b.WriteString(activeStyle.Render(fitSetupText(row, m.contentWidth())))
	b.WriteString("\n")
	hint := "[↑/↓] choose  [Enter] select"
	b.WriteString(mutedStyle.Render(fitSetupText(hint, m.contentWidth())))
	return b.String()
}

func (m *Model) maxVisibleProviders(fixedContentLines int) int {
	maxVisible := 12
	if m.height > 0 {
		contentBudget := m.height - 4
		if contentBudget < 1 {
			contentBudget = 1
		}
		if byHeight := contentBudget - fixedContentLines; byHeight < maxVisible {
			maxVisible = byHeight
		}
	}
	if maxVisible < 1 {
		return 1
	}
	return maxVisible
}

func (m *Model) viewAuthMethod() string {
	width := m.contentWidth()
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("VulpineOS — %s", m.baseProvider.Name)))
	b.WriteString("\n\n")
	b.WriteString("How would you like to sign in?\n\n")

	options := []struct{ label, hint string }{
		{"API key", "Paste your " + m.baseProvider.EnvVar},
		{"Sign in with ChatGPT (OAuth)", "Browser sign-in, no API key"},
	}
	for i, opt := range options {
		hint := mutedStyle.Render("  " + opt.hint)
		if i == m.authMethodIdx {
			b.WriteString(fitSetupLine(activeStyle.Render("▸ ")+lipgloss.NewStyle().Bold(true).Render(opt.label)+hint, width) + "\n")
		} else {
			b.WriteString(fitSetupLine("  "+mutedStyle.Render(opt.label)+hint, width) + "\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(fitSetupLine(mutedStyle.Render("[↑/↓] choose  [Enter] select  [Esc] back"), width))
	return b.String()
}

func (m *Model) viewOAuth() string {
	width := m.contentWidth()
	var b strings.Builder
	b.WriteString(titleStyle.Render("VulpineOS — Sign in with ChatGPT"))
	b.WriteString("\n\n")
	if m.login != nil && m.login.AuthURL != "" {
		b.WriteString("Opening your browser to sign in. If it didn't open, visit:\n")
		b.WriteString(fitSetupLine(activeStyle.Render(m.login.AuthURL), width))
		b.WriteString("\n\n")
	} else {
		b.WriteString(mutedStyle.Render("Preparing sign-in...\n\n"))
	}
	if m.oauthStatus != "" {
		b.WriteString(mutedStyle.Render(fitSetupText(m.oauthStatus, width)))
		b.WriteString("\n\n")
	}
	if m.oauthErr != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Render(fitSetupText("Error: "+m.oauthErr, width)))
		b.WriteString("\n\n")
	}
	b.WriteString(m.pasteInput.View())
	b.WriteString("\n\n")
	b.WriteString(fitSetupLine(mutedStyle.Render("Sign in in the browser. [Enter] use pasted URL  [Esc] back"), width))
	return b.String()
}

func (m *Model) viewModel() string {
	p, ok := m.selectedConfigProvider()
	if !ok {
		return mutedStyle.Render("No provider selected.")
	}
	if m.height > 0 && m.height < 14 {
		return m.viewModelCompact(p)
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("VulpineOS — %s Models", p.Name)))
	b.WriteString("\n\n")
	b.WriteString("Select a model:\n\n")
	b.WriteString(fmt.Sprintf("Filter: %s\n\n", activeStyle.Render(m.modelQuery)))

	models := m.providerModels(p)
	filtered := m.filteredModelIndices(p)
	if len(filtered) == 0 {
		b.WriteString(mutedStyle.Render("No models match."))
		b.WriteString("\n\n")
		b.WriteString(mutedStyle.Render("[Backspace] edit  [Esc] clear"))
		return b.String()
	}

	maxVisible := m.maxVisibleProviders(9)
	start := 0
	if m.modelIdx >= maxVisible {
		start = m.modelIdx - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(filtered) {
		end = len(filtered)
	}
	rowWidth := m.contentWidth()
	for i := start; i < end; i++ {
		model := models[filtered[i]]
		label := fitSetupText(model, max(1, rowWidth-2))
		if i == m.modelIdx {
			b.WriteString(fitSetupLine(activeStyle.Render("▸ ")+lipgloss.NewStyle().Bold(true).Render(label), rowWidth) + "\n")
		} else {
			b.WriteString(fitSetupLine("  "+mutedStyle.Render(label), rowWidth) + "\n")
		}
	}

	scrollInfo := ""
	if start > 0 && end < len(filtered) {
		scrollInfo = fmt.Sprintf("  ↑ %d above · ↓ %d below", start, len(filtered)-end)
	} else if start > 0 {
		scrollInfo = fmt.Sprintf("  ↑ %d more above", start)
	} else if end < len(filtered) {
		scrollInfo = fmt.Sprintf("  ↓ %d more below", len(filtered)-end)
	}
	if scrollInfo != "" {
		b.WriteString(mutedStyle.Render(scrollInfo) + "\n")
	}

	b.WriteString("\n")
	count := fmt.Sprintf("%d/%d models", len(filtered), len(models))
	if strings.TrimSpace(m.modelQuery) == "" {
		count = fmt.Sprintf("%d models", len(models))
	}
	b.WriteString(fitSetupLine(mutedStyle.Render(fmt.Sprintf("[↑/↓] navigate  type to filter  [Enter] select  [Esc] back  (%s)", count)), rowWidth))
	return b.String()
}

func (m *Model) viewModelCompact(p config.Provider) string {
	var b strings.Builder
	width := m.contentWidth()
	b.WriteString(titleStyle.Render("Model"))
	b.WriteString("\n")
	b.WriteString(fitSetupText("Filter: "+m.modelQuery, width))
	b.WriteString("\n")
	model, ok := m.selectedModel(p)
	if !ok {
		b.WriteString(mutedStyle.Render(fitSetupText("No matches", width)))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(fitSetupText("[Backspace] edit", width)))
		return b.String()
	}
	row := fmt.Sprintf("%d/%d  %s", m.modelIdx+1, len(m.filteredModelIndices(p)), model)
	b.WriteString(activeStyle.Render(fitSetupText(row, width)))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(fitSetupText("[↑/↓] choose  [Enter] select", width)))
	return b.String()
}

func (m *Model) viewAPIKey() string {
	p, ok := m.selectedConfigProvider()
	if !ok {
		return mutedStyle.Render("No provider selected.")
	}
	if m.height > 0 && m.height < 14 {
		return m.viewAPIKeyCompact(p)
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("VulpineOS — %s Setup", p.Name)))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("API Key (%s):\n", p.EnvVar))
	b.WriteString(m.apiKeyInput.View())
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Model: %s\n", activeStyle.Render(m.cfg.Model)))
	b.WriteString(mutedStyle.Render("(Esc to change model)"))
	b.WriteString("\n\n")
	b.WriteString(mutedStyle.Render("[Enter] save  [Esc] back"))
	return b.String()
}

func (m *Model) viewAPIKeyCompact(p config.Provider) string {
	var b strings.Builder
	width := m.contentWidth()
	b.WriteString(titleStyle.Render("API Key"))
	b.WriteString("\n")
	b.WriteString(fitSetupText(p.EnvVar, width))
	b.WriteString("\n")
	b.WriteString(fitSetupLine(m.apiKeyInput.View(), width))
	b.WriteString("\n")
	b.WriteString(activeStyle.Render(fitSetupText(m.cfg.Model, width)))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(fitSetupText("[Enter] save  [Esc] back", width)))
	return b.String()
}

func fitSetupText(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	var b strings.Builder
	for _, r := range text {
		next := b.String() + string(r)
		if lipgloss.Width(next) > width-1 {
			break
		}
		b.WriteRune(r)
	}
	b.WriteString("…")
	return b.String()
}

func padSetupText(text string, width int) string {
	for lipgloss.Width(text) < width {
		text += " "
	}
	return text
}

func fitSetupLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	fitted := lipgloss.NewStyle().MaxWidth(width).Render(line)
	lines := strings.Split(fitted, "\n")
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func fitSetupBlock(block string, width, height int) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		lines[i] = fitSetupLine(line, width)
	}
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func (m *Model) viewDone() string {
	p := config.GetProvider(m.cfg.Provider)
	name := m.cfg.Provider
	if p != nil {
		name = p.Name
	}
	if m.height > 0 && m.height < 14 {
		return m.viewDoneCompact(name)
	}

	var b strings.Builder
	b.WriteString(successStyle.Render("VulpineOS — Setup Complete ✓"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Provider:  %s\n", name))
	b.WriteString(fmt.Sprintf("Model:     %s\n", m.cfg.Model))
	b.WriteString(fmt.Sprintf("Config:    %s\n", config.Path()))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Press Enter to launch dashboard..."))
	return b.String()
}

func (m *Model) viewDoneCompact(providerName string) string {
	width := m.contentWidth()
	var b strings.Builder
	b.WriteString(successStyle.Render("Setup Complete"))
	b.WriteString("\n")
	b.WriteString(fitSetupText(providerName, width))
	b.WriteString("\n")
	b.WriteString(fitSetupText(m.cfg.Model, width))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(fitSetupText("Enter to launch", width)))
	return b.String()
}
