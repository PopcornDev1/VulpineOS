package settings

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/config"
	"vulpineos/internal/tui/shared"
)

// Tab identifies which settings tab is active.
type Tab int

const (
	TabGeneral Tab = 0
	TabProxies Tab = 1
	TabSkills  Tab = 2
)

// ProxyItem describes a proxy entry in the settings list.
type ProxyItem struct {
	ID      string
	Label   string
	Type    string
	Host    string
	Port    int
	Country string
	Latency string // "45ms" or "untested"
}

// SkillItem describes a skill entry in the settings list.
type SkillItem struct {
	Name    string
	Enabled bool
}

// Model is the Bubbletea model for the settings panel.
type Model struct {
	active bool
	tab    Tab
	width  int
	height int

	// General tab
	provider  string
	model     string
	apiKeySet bool // don't show the actual key, just whether it's set

	// Proxies tab
	proxies     []ProxyItem
	proxyIdx    int
	importing   bool // true when paste-input mode active
	importInput textinput.Model

	// Skills tab
	skills   []SkillItem
	skillIdx int
}

// New creates a new settings panel model.
func New() Model {
	ti := textinput.New()
	ti.Placeholder = "Paste proxy URL (e.g. socks5://user:pass@host:port)..."
	ti.CharLimit = 256
	ti.Width = 60

	return Model{
		tab:         TabGeneral,
		importInput: ti,
	}
}

// SetActive sets whether the settings panel is open.
func (m *Model) SetActive(active bool) {
	m.active = active
	if !active {
		m.importing = false
		m.importInput.Blur()
	}
}

// IsActive returns whether the settings panel is open.
func (m *Model) IsActive() bool {
	return m.active
}

// SetConfig loads current config values into the settings panel.
func (m *Model) SetConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	m.provider = cfg.Provider
	m.model = cfg.Model
	m.apiKeySet = cfg.APIKey != ""

	// Load skills from config
	m.skills = nil
	for _, s := range cfg.GlobalSkills {
		m.skills = append(m.skills, SkillItem{
			Name:    s.Name,
			Enabled: s.Enabled,
		})
	}
	if m.skillIdx >= len(m.skills) {
		m.skillIdx = 0
	}
}

// SetProxies loads the proxy list into the settings panel.
func (m *Model) SetProxies(proxies []ProxyItem) {
	m.proxies = proxies
	if m.proxyIdx >= len(m.proxies) {
		m.proxyIdx = 0
	}
}

// SetSkills loads the skill list into the settings panel.
func (m *Model) SetSkills(skills []SkillItem) {
	m.skills = skills
	if m.skillIdx >= len(m.skills) {
		m.skillIdx = 0
	}
}

// SetSize sets the available dimensions for the settings panel.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.importInput.Width = w - 4
	if m.importInput.Width < 20 {
		m.importInput.Width = 20
	}
}

// Update handles keystrokes when the settings panel is active.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.active {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// If importing proxies, route to text input
		if m.importing {
			return m.updateImportInput(msg)
		}

		switch msg.String() {
		case "esc":
			m.active = false
			return m, func() tea.Msg { return shared.SettingsClosedMsg{} }

		case "1":
			m.tab = TabGeneral
		case "2":
			m.tab = TabProxies
		case "3":
			m.tab = TabSkills

		case "j", "down":
			switch m.tab {
			case TabProxies:
				if len(m.proxies) > 0 && m.proxyIdx < len(m.proxies)-1 {
					m.proxyIdx++
				}
			case TabSkills:
				if len(m.skills) > 0 && m.skillIdx < len(m.skills)-1 {
					m.skillIdx++
				}
			}

		case "k", "up":
			switch m.tab {
			case TabProxies:
				if m.proxyIdx > 0 {
					m.proxyIdx--
				}
			case TabSkills:
				if m.skillIdx > 0 {
					m.skillIdx--
				}
			}

		case "i":
			if m.tab == TabProxies {
				m.importing = true
				m.importInput.Focus()
				return m, textinput.Blink
			}

		case "d":
			if m.tab == TabProxies && len(m.proxies) > 0 {
				m.proxies = append(m.proxies[:m.proxyIdx], m.proxies[m.proxyIdx+1:]...)
				if m.proxyIdx >= len(m.proxies) && m.proxyIdx > 0 {
					m.proxyIdx--
				}
			}

		case "t":
			if m.tab == TabProxies && len(m.proxies) > 0 {
				proxyID := m.proxies[m.proxyIdx].ID
				return m, func() tea.Msg {
					// Placeholder: real proxy testing would happen here
					return shared.ProxyTestedMsg{
						ProxyID: proxyID,
						Latency: "untested",
					}
				}
			}

		case " ":
			if m.tab == TabSkills && len(m.skills) > 0 {
				m.skills[m.skillIdx].Enabled = !m.skills[m.skillIdx].Enabled
			}
		}

	case shared.ProxyTestedMsg:
		for i := range m.proxies {
			if m.proxies[i].ID == msg.ProxyID {
				m.proxies[i].Latency = msg.Latency
				break
			}
		}
	}

	return m, nil
}

// updateImportInput handles keystrokes in proxy import mode.
func (m Model) updateImportInput(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		val := strings.TrimSpace(m.importInput.Value())
		if val != "" {
			// Parse and add proxy (simple placeholder)
			m.proxies = append(m.proxies, ProxyItem{
				ID:      fmt.Sprintf("proxy-%d", len(m.proxies)+1),
				Label:   fmt.Sprintf("imported-%d", len(m.proxies)+1),
				Type:    "http",
				Host:    val,
				Latency: "untested",
			})
		}
		m.importing = false
		m.importInput.Blur()
		m.importInput.Reset()
		return m, nil
	case "esc":
		m.importing = false
		m.importInput.Blur()
		m.importInput.Reset()
		return m, nil
	default:
		var cmd tea.Cmd
		m.importInput, cmd = m.importInput.Update(msg)
		return m, cmd
	}
}

// View renders the active tab of the settings panel.
func (m Model) View() string {
	if !m.active {
		return ""
	}

	var b strings.Builder

	// Tab bar
	tabs := []string{"1:General", "2:Proxies", "3:Skills"}
	for i, t := range tabs {
		if Tab(i) == m.tab {
			b.WriteString(shared.TitleStyle.Render("[" + t + "]"))
		} else {
			b.WriteString(shared.MutedStyle.Render(" " + t + " "))
		}
		if i < len(tabs)-1 {
			b.WriteString(shared.MutedStyle.Render("  "))
		}
	}
	b.WriteString("\n\n")

	switch m.tab {
	case TabGeneral:
		b.WriteString(m.viewGeneral())
	case TabProxies:
		b.WriteString(m.viewProxies())
	case TabSkills:
		b.WriteString(m.viewSkills())
	}

	return b.String()
}

// viewGeneral renders the General settings tab.
func (m Model) viewGeneral() string {
	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render("SETTINGS — General"))
	b.WriteString("\n\n")

	providerName := m.provider
	if p := config.GetProvider(m.provider); p != nil {
		providerName = p.Name
	}

	keyStatus := shared.StoppedStyle.Render("(not set)")
	if m.apiKeySet {
		keyStatus = shared.RunningStyle.Render("(set)")
	}

	b.WriteString(shared.HeaderStyle.Render("Provider:  "))
	b.WriteString(providerName)
	b.WriteString("\n")
	b.WriteString(shared.HeaderStyle.Render("Model:     "))
	b.WriteString(m.model)
	b.WriteString("\n")
	b.WriteString(shared.HeaderStyle.Render("API Key:   "))
	b.WriteString(lipgloss.NewStyle().Render(strings.Repeat("•", 13) + " "))
	b.WriteString(keyStatus)
	b.WriteString("\n\n")
	b.WriteString(shared.MutedStyle.Render("Press 'c' to reconfigure provider/model"))
	b.WriteString("\n")
	b.WriteString(shared.MutedStyle.Render("[Esc] close settings"))

	return b.String()
}

// viewProxies renders the Proxies settings tab.
func (m Model) viewProxies() string {
	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render(fmt.Sprintf("SETTINGS — Proxies (%d)", len(m.proxies))))
	b.WriteString("\n\n")

	if len(m.proxies) == 0 {
		b.WriteString(shared.MutedStyle.Render("No proxies configured."))
		b.WriteString("\n")
	} else {
		for i, p := range m.proxies {
			cursor := "  "
			if i == m.proxyIdx {
				cursor = shared.RunningStyle.Render("▸ ")
			}

			label := lipgloss.NewStyle().Width(12).Render(p.Label)
			typ := lipgloss.NewStyle().Width(6).Render(p.Type)
			host := lipgloss.NewStyle().Width(20).Render(fmt.Sprintf("%s:%d", p.Host, p.Port))
			country := lipgloss.NewStyle().Width(4).Render(p.Country)
			latency := p.Latency

			line := fmt.Sprintf("%s%s%s%s%s%s", cursor, label, typ, host, country, latency)
			if i == m.proxyIdx {
				line = shared.SelectedStyle.Render(line)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	if m.importing {
		b.WriteString("\n")
		b.WriteString(shared.WarmingStyle.Render("Import proxy URL:"))
		b.WriteString("\n")
		b.WriteString(m.importInput.View())
		b.WriteString("\n")
		b.WriteString(shared.MutedStyle.Render("[Enter] add  [Esc] cancel"))
	} else {
		b.WriteString("\n")
		b.WriteString(shared.MutedStyle.Render("[i] import  [d] delete  [t] test  [j/k] navigate"))
		b.WriteString("\n")
		b.WriteString(shared.MutedStyle.Render("[Esc] close settings"))
	}

	return b.String()
}

// viewSkills renders the Skills settings tab.
func (m Model) viewSkills() string {
	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render("SETTINGS — Skills"))
	b.WriteString("\n\n")

	if len(m.skills) == 0 {
		b.WriteString(shared.MutedStyle.Render("No skills configured."))
		b.WriteString("\n")
	} else {
		for i, s := range m.skills {
			cursor := "  "
			if i == m.skillIdx {
				cursor = shared.RunningStyle.Render("▸ ")
			}

			check := "[ ]"
			if s.Enabled {
				check = shared.RunningStyle.Render("[✓]")
			}

			line := fmt.Sprintf("%s%s %s", cursor, check, s.Name)
			if i == m.skillIdx {
				line = shared.SelectedStyle.Render(line)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(shared.MutedStyle.Render("[space] toggle  [j/k] navigate"))
	b.WriteString("\n")
	b.WriteString(shared.MutedStyle.Render("[Esc] close settings"))

	return b.String()
}
