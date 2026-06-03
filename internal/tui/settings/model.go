package settings

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/config"
	"vulpineos/internal/tui/shared"
)

// Section identifies which settings section has focus for j/k navigation.
type Section int

const (
	SectionGeneral Section = 0
	SectionProxies Section = 1
	SectionSkills  Section = 2
)

// ProxyItem describes a proxy entry in the settings list.
type ProxyItem struct {
	ID      string
	Label   string
	Type    string
	Host    string
	Port    int
	Config  string
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
		section:     SectionGeneral,
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

// CapturingText returns true when settings owns raw key input.
func (m *Model) CapturingText() bool {
	return m.active && m.importing
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

		case "down":
			// Switch to the next section.
			if m.section == SectionGeneral {
				m.section = SectionProxies
			} else if m.section == SectionProxies {
				m.section = SectionSkills
			} else {
				m.section = SectionGeneral
			}

		case "up":
			// Switch to the previous section.
			if m.section == SectionSkills {
				m.section = SectionProxies
			} else if m.section == SectionProxies {
				m.section = SectionGeneral
			} else {
				m.section = SectionSkills
			}

		case "j":
			// Navigate down within the focused section's list.
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

		case "k":
			// Navigate up within the focused section's list.
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
				config := p.Config
				if config == "" {
					config = fmt.Sprintf(`{"type":"%s","host":"%s","port":%d}`, p.Type, p.Host, p.Port)
				}
				return m, func() tea.Msg {
					return shared.ProxyTestRequestMsg{
						ProxyID: p.ID,
						Config:  config,
					}
				}
			}

		case "c":
			return m, func() tea.Msg { return shared.ReconfigureRequestedMsg{} }

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
				if msg.Country != "" {
					m.proxies[i].Country = msg.Country
				}
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
	contentWidth := m.contentWidth()

	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render("SETTINGS"))
	controls := "[Esc] close  [↑/↓] section"
	pad := contentWidth - lipgloss.Width("SETTINGS") - lipgloss.Width(controls)
	if pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteString(shared.MutedStyle.Render(controls))
	}
	b.WriteString("\n\n")

	if m.height > 0 && m.height <= 18 {
		return m.viewFocusedSection(contentWidth)
	}

	// --- General section ---
	b.WriteString(m.viewGeneral())
	if m.section == SectionGeneral {
		b.WriteString("\n")
		b.WriteString(shared.MutedStyle.Render(clipText("[c] reconfigure", contentWidth)))
	}
	b.WriteString("\n\n")

	// Separator
	sep := shared.MutedStyle.Render(strings.Repeat("─", max(1, contentWidth-2)))
	b.WriteString(sep)
	b.WriteString("\n\n")

	// --- Proxies section ---
	b.WriteString(m.viewProxies())
	b.WriteString("\n")

	b.WriteString(sep)
	b.WriteString("\n\n")

	// --- Skills section ---
	b.WriteString(m.viewSkills())

	content := b.String()
	if m.height > 0 && m.section != SectionGeneral && len(strings.Split(content, "\n")) > m.height {
		return m.viewFocusedSection(contentWidth)
	}
	return m.fitContent(content)
}

func (m Model) viewFocusedSection(contentWidth int) string {
	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render("SETTINGS"))
	controls := "[Esc] close  [↑/↓] section"
	pad := contentWidth - lipgloss.Width("SETTINGS") - lipgloss.Width(controls)
	if pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteString(shared.MutedStyle.Render(controls))
	}
	b.WriteString("\n\n")
	switch m.section {
	case SectionGeneral:
		b.WriteString(m.viewGeneral())
		b.WriteString("\n")
		b.WriteString(shared.MutedStyle.Render(clipText("[c] reconfigure", contentWidth)))
	case SectionProxies:
		b.WriteString(m.viewProxies())
	case SectionSkills:
		b.WriteString(m.viewSkills())
	}
	return m.fitContent(b.String())
}

// viewGeneral renders the General settings section.
func (m Model) viewGeneral() string {
	var b strings.Builder

	if m.section == SectionGeneral {
		b.WriteString(shared.TitleStyle.Render("General"))
	} else {
		b.WriteString(shared.HeaderStyle.Render("General"))
	}
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
	b.WriteString(clipText(providerName, max(1, m.contentWidth()-11)))
	b.WriteString("\n")
	b.WriteString(shared.HeaderStyle.Render("Model:     "))
	b.WriteString(clipText(m.model, max(1, m.contentWidth()-11)))
	b.WriteString("\n")
	b.WriteString(shared.HeaderStyle.Render("API Key:   "))
	b.WriteString(lipgloss.NewStyle().Render(strings.Repeat("*", 13) + " "))
	b.WriteString(keyStatus)

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
		start, end := m.visibleListRange(len(m.proxies), m.proxyIdx)
		for i := start; i < end; i++ {
			p := m.proxies[i]
			cursor := "  "
			if i == m.proxyIdx && m.section == SectionProxies {
				cursor = shared.RunningStyle.Render("| ")
			}

			line := fmt.Sprintf("%s%s %s %s:%d %s %s", cursor, p.Label, p.Type, p.Host, p.Port, p.Country, p.Latency)
			line = clipText(line, m.contentWidth())
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
		b.WriteString(shared.MutedStyle.Render(clipText("[i] import  [d] delete  [t] test  [j/k] navigate", m.contentWidth())))
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
		start, end := m.visibleListRange(len(m.skills), m.skillIdx)
		for i := start; i < end; i++ {
			s := m.skills[i]
			cursor := "  "
			if i == m.skillIdx && m.section == SectionSkills {
				cursor = shared.RunningStyle.Render("| ")
			}

			check := "[ ]"
			if s.Enabled {
				check = shared.RunningStyle.Render("[+]")
			}

			line := fmt.Sprintf("%s%s %s", cursor, check, s.Name)
			line = clipText(line, m.contentWidth())
			if i == m.skillIdx && m.section == SectionSkills {
				line = shared.SelectedStyle.Render(line)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	if m.section == SectionSkills {
		b.WriteString(shared.MutedStyle.Render(clipText("[space] toggle  [j/k] navigate", m.contentWidth())))
	}

	return b.String()
}

func (m Model) contentWidth() int {
	if m.width < 1 {
		return 1
	}
	return m.width
}

func (m Model) visibleListRange(total, selected int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if m.height <= 0 {
		return 0, total
	}
	maxRows := m.height - 5
	if maxRows < 1 {
		maxRows = 1
	}
	if maxRows >= total {
		return 0, total
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= total {
		selected = total - 1
	}
	start := selected - maxRows/2
	if start < 0 {
		start = 0
	}
	if start+maxRows > total {
		start = total - maxRows
	}
	return start, start + maxRows
}

func (m Model) fitContent(content string) string {
	width := m.contentWidth()
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if lipgloss.Width(line) > width {
			lines[i] = lipgloss.NewStyle().MaxWidth(width).Render(line)
		}
	}
	if m.height > 0 && len(lines) > m.height {
		start := m.scroll
		if start < 0 {
			start = 0
		}
		if start > len(lines)-m.height {
			start = max(0, len(lines)-m.height)
		}
		lines = lines[start : start+m.height]
	}
	return strings.Join(lines, "\n")
}

func clipText(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= maxWidth {
		return text
	}
	if maxWidth == 1 {
		return "…"
	}
	suffix := "…"
	limit := maxWidth - lipgloss.Width(suffix)
	if limit < 1 {
		return suffix
	}
	var b strings.Builder
	visibleWidth := 0
	styleOpen := false
	for i := 0; i < len(text) && visibleWidth < limit; {
		if text[i] == '\x1b' && i+1 < len(text) && text[i+1] == '[' {
			end := i + 2
			for end < len(text) && !isANSITerminator(text[end]) {
				end++
			}
			if end < len(text) {
				end++
				seq := text[i:end]
				b.WriteString(seq)
				if strings.HasSuffix(seq, "m") {
					styleOpen = !isANSIReset(seq)
				}
				i = end
				continue
			}
		}

		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		rWidth := lipgloss.Width(string(r))
		if visibleWidth+rWidth > limit {
			break
		}
		b.WriteRune(r)
		visibleWidth += rWidth
		i += size
	}
	if styleOpen {
		b.WriteString("\x1b[0m")
	}
	return b.String() + suffix
}

func isANSITerminator(b byte) bool {
	return b >= 0x40 && b <= 0x7e
}

func isANSIReset(seq string) bool {
	return seq == "\x1b[0m" || seq == "\x1b[m" || strings.Contains(seq, "[0;")
}
