package setup

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/config"
)

type step int

const (
	stepProvider step = iota
	stepAPIKey
	stepDone
)

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
	apiKeyInput   textinput.Model
	modelIdx      int
	cfg           *config.Config
	done          bool
}

// New creates a new setup wizard.
func New() Model {
	ti := textinput.New()
	ti.Placeholder = "Paste your API key here..."
	ti.CharLimit = 200
	ti.Width = 50

	return Model{
		step:        stepProvider,
		providers:   config.Providers,
		apiKeyInput: ti,
		cfg:         &config.Config{},
	}
}

// Config returns the completed config.
func (m Model) Config() *config.Config {
	return m.cfg
}

// Done returns true if setup is complete and user pressed Enter on the final screen.
func (m Model) Done() bool {
	return m.done
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch m.step {
		case stepProvider:
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			case "j", "down":
				if m.providerIdx < len(m.providers)-1 {
					m.providerIdx++
				}
			case "k", "up":
				if m.providerIdx > 0 {
					m.providerIdx--
				}
			case "enter":
				p := m.providers[m.providerIdx]
				m.cfg.Provider = p.ID
				m.cfg.Model = p.DefaultModel
				m.modelIdx = 0
				if !p.NeedsKey {
					// Ollama — skip API key step
					m.cfg.SetupComplete = true
					m.step = stepDone
				} else {
					m.apiKeyInput.Placeholder = fmt.Sprintf("Enter your %s...", p.EnvVar)
					m.apiKeyInput.Focus()
					m.step = stepAPIKey
				}
			}

		case stepAPIKey:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.step = stepProvider
				m.apiKeyInput.Blur()
			case "tab":
				// Cycle model
				p := m.providers[m.providerIdx]
				m.modelIdx = (m.modelIdx + 1) % len(p.Models)
				m.cfg.Model = p.Models[m.modelIdx]
			case "enter":
				key := strings.TrimSpace(m.apiKeyInput.Value())
				if key == "" {
					break
				}
				m.cfg.APIKey = key
				m.cfg.SetupComplete = true
				m.step = stepDone
			default:
				var cmd tea.Cmd
				m.apiKeyInput, cmd = m.apiKeyInput.Update(msg)
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

func (m Model) View() string {
	var content string

	switch m.step {
	case stepProvider:
		content = m.viewProvider()
	case stepAPIKey:
		content = m.viewAPIKey()
	case stepDone:
		content = m.viewDone()
	}

	box := boxStyle.Render(content)

	// Center vertically
	pad := (m.height - lipgloss.Height(box)) / 2
	if pad < 0 {
		pad = 0
	}

	return strings.Repeat("\n", pad) + lipgloss.PlaceHorizontal(m.width, lipgloss.Center, box)
}

func (m Model) viewProvider() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("VulpineOS — First Time Setup"))
	b.WriteString("\n\n")
	b.WriteString("Select your AI provider:\n\n")

	// Show a scrollable window of providers (max 12 visible)
	maxVisible := 12
	start := 0
	if m.providerIdx >= maxVisible {
		start = m.providerIdx - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(m.providers) {
		end = len(m.providers)
	}

	for i := start; i < end; i++ {
		p := m.providers[i]
		envHint := mutedStyle.Render(p.EnvVar)
		if !p.NeedsKey {
			envHint = mutedStyle.Render("No key needed")
		}
		name := fmt.Sprintf("%-28s", p.Name)
		if i == m.providerIdx {
			b.WriteString(activeStyle.Render("▸ ") + lipgloss.NewStyle().Bold(true).Render(name) + "  " + envHint + "\n")
		} else {
			b.WriteString("  " + mutedStyle.Render(name) + "  " + envHint + "\n")
		}
	}

	// Scroll indicators
	scrollInfo := ""
	if start > 0 && end < len(m.providers) {
		scrollInfo = fmt.Sprintf("  ↑ %d above · ↓ %d below", start, len(m.providers)-end)
	} else if start > 0 {
		scrollInfo = fmt.Sprintf("  ↑ %d more above", start)
	} else if end < len(m.providers) {
		scrollInfo = fmt.Sprintf("  ↓ %d more below", len(m.providers)-end)
	}
	if scrollInfo != "" {
		b.WriteString(mutedStyle.Render(scrollInfo) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("[j/k] navigate  [Enter] select  [q] quit  (%d providers)", len(m.providers))))
	return b.String()
}

func (m Model) viewAPIKey() string {
	p := m.providers[m.providerIdx]

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("VulpineOS — %s Setup", p.Name)))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("API Key (%s):\n", p.EnvVar))
	b.WriteString(m.apiKeyInput.View())
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Model: %s\n", activeStyle.Render(m.cfg.Model)))
	b.WriteString(mutedStyle.Render("(Press Tab to change model)"))
	b.WriteString("\n\n")
	b.WriteString(mutedStyle.Render("[Enter] save  [Tab] model  [Esc] back"))
	return b.String()
}

func (m Model) viewDone() string {
	p := config.GetProvider(m.cfg.Provider)
	name := m.cfg.Provider
	if p != nil {
		name = p.Name
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
