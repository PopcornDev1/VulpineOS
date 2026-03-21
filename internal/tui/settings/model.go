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

// Section identifies which settings section has focus for j/k navigation.
type Section int

const (
	SectionProxies Section = 0
	SectionSkills  Section = 1
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
	active  bool
	section Section // which section has focus for j/k
	width   int
	height  int
	scroll  int // scroll offset for the full page

	// General
	provider  string
	model     string
	apiKeySet bool // don't show the actual key, just whether it's set

	// Proxies
	proxies     []ProxyItem
	proxyIdx    int
	importing   bool // true when paste-input mode active
	importInput textinput.Model

	// Skills
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
		section:     SectionProxies,
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

		case "tab":
			// Switch section focus
			if m.section == SectionProxies {
				m.section = SectionSkills
			} else {
				// Close settings and let app handle Tab cycling
				m.active = false
				return m, func() tea.Msg { return shared.SettingsClosedMsg{} }
			}

		case "j", "down":
			switch m.section {
			case SectionProxies:
				if len(m.proxies) > 0 && m.proxyIdx < len(m.proxies)-1 {
					m.proxyIdx++
				}
			case SectionSkills:
				if len(m.skills) > 0 && m.skillIdx < len(m.skills)-1 {
					m.skillIdx++
				}
			}

		case "k", "up":
			switch m.section {
			case SectionProxies:
				if m.proxyIdx > 0 {
					m.proxyIdx--
				}
			case SectionSkills:
				if m.skillIdx > 0 {
					m.skillIdx--
				}
			}

		case "i":
			if m.section == SectionProxies {
				m.importing = true
				m.importInput.Focus()
				return m, textinput.Blink
			}

		case "d":
			if m.section == SectionProxies && len(m.proxies) > 0 {
				proxyID := m.proxies[m.proxyIdx].ID
				return m, func() tea.Msg { return shared.ProxyDeleteMsg{ProxyID: proxyID} }
			}

		case "t":
			if m.section == SectionProxies && len(m.proxies) > 0 {
				p := m.proxies[m.proxyIdx]
				return m, func() tea.Msg {
					return shared.ProxyTestRequestMsg{
						ProxyID: p.ID,
						Config:  fmt.Sprintf(`{"type":"%s","host":"%s","port":%d}`, p.Type, p.Host, p.Port),
					}
				}
			}

		case " ":
			if m.section == SectionSkills && len(m.skills) > 0 {
				m.skills[m.skillIdx].Enabled = !m.skills[m.skillIdx].Enabled
				s := m.skills[m.skillIdx]
				return m, func() tea.Msg {
					return shared.SkillToggleMsg{Name: s.Name, Enabled: s.Enabled}
				}
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
		m.importing = false
		m.importInput.Blur()
		m.importInput.Reset()
		if val != "" {
			return m, func() tea.Msg { return shared.ProxyAddMsg{URL: val} }
		}
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

// View renders all settings sections on a single page.
func (m Model) View() string {
	if !m.active {
		return ""
	}

	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render("SETTINGS"))
	b.WriteString("\n\n")

	// --- General section ---
	b.WriteString(m.viewGeneral())
	b.WriteString("\n")

	// Separator
	sep := shared.MutedStyle.Render(strings.Repeat("─", m.width-2))
	b.WriteString(sep)
	b.WriteString("\n\n")

	// --- Proxies section ---
	b.WriteString(m.viewProxies())
	b.WriteString("\n")

	b.WriteString(sep)
	b.WriteString("\n\n")

	// --- Skills section ---
	b.WriteString(m.viewSkills())

	b.WriteString("\n\n")
	b.WriteString(shared.MutedStyle.Render("[Esc] close settings  [Tab] next section"))

	return b.String()
}

// viewGeneral renders the General settings section.
func (m Model) viewGeneral() string {
	var b strings.Builder

	b.WriteString(shared.HeaderStyle.Render("General"))
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
	b.WriteString(lipgloss.NewStyle().Render(strings.Repeat("*", 13) + " "))
	b.WriteString(keyStatus)
	b.WriteString("\n\n")
	b.WriteString(shared.MutedStyle.Render("Press 'c' to reconfigure provider/model"))

	return b.String()
}

// viewProxies renders the Proxies settings section.
func (m Model) viewProxies() string {
	var b strings.Builder

	sectionTitle := fmt.Sprintf("Proxies (%d)", len(m.proxies))
	if m.section == SectionProxies {
		b.WriteString(shared.TitleStyle.Render(sectionTitle))
	} else {
		b.WriteString(shared.HeaderStyle.Render(sectionTitle))
	}
	b.WriteString("\n\n")

	if len(m.proxies) == 0 {
		b.WriteString(shared.MutedStyle.Render("No proxies configured."))
		b.WriteString("\n")
	} else {
		for i, p := range m.proxies {
			cursor := "  "
			if i == m.proxyIdx && m.section == SectionProxies {
				cursor = shared.RunningStyle.Render("| ")
			}

			label := lipgloss.NewStyle().Width(12).Render(p.Label)
			typ := lipgloss.NewStyle().Width(6).Render(p.Type)
			host := lipgloss.NewStyle().Width(20).Render(fmt.Sprintf("%s:%d", p.Host, p.Port))
			country := lipgloss.NewStyle().Width(4).Render(p.Country)
			latency := p.Latency

			line := fmt.Sprintf("%s%s%s%s%s%s", cursor, label, typ, host, country, latency)
			if i == m.proxyIdx && m.section == SectionProxies {
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
	} else if m.section == SectionProxies {
		b.WriteString(shared.MutedStyle.Render("[i] import  [d] delete  [t] test  [j/k] navigate"))
	}

	return b.String()
}

// viewSkills renders the Skills settings section.
func (m Model) viewSkills() string {
	var b strings.Builder

	sectionTitle := "Skills"
	if m.section == SectionSkills {
		b.WriteString(shared.TitleStyle.Render(sectionTitle))
	} else {
		b.WriteString(shared.HeaderStyle.Render(sectionTitle))
	}
	b.WriteString("\n\n")

	if len(m.skills) == 0 {
		b.WriteString(shared.MutedStyle.Render("No skills configured."))
		b.WriteString("\n")
	} else {
		for i, s := range m.skills {
			cursor := "  "
			if i == m.skillIdx && m.section == SectionSkills {
				cursor = shared.RunningStyle.Render("| ")
			}

			check := "[ ]"
			if s.Enabled {
				check = shared.RunningStyle.Render("[+]")
			}

			line := fmt.Sprintf("%s%s %s", cursor, check, s.Name)
			if i == m.skillIdx && m.section == SectionSkills {
				line = shared.SelectedStyle.Render(line)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	if m.section == SectionSkills {
		b.WriteString(shared.MutedStyle.Render("[space] toggle  [j/k] navigate"))
	}

	return b.String()
}
