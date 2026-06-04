// Package agentpicker implements a single-step modal for selecting an agent.
// It mirrors the visual style of the setup wizard's provider/model pickers
// (same color palette, border, scroll math, hint line) so the two modals
// feel like the same UI element.
package agentpicker

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/tui/commandpalette"
	"vulpineos/internal/tui/shared"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED"))
	activeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED")).Bold(true)
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	boxStyle    = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7C3AED")).
			Padding(1, 2).
			Width(64)
)

// Model is the Bubbletea model for the agent picker modal.
type Model struct {
	agents   []commandpalette.Agent
	query    string
	selected int // index into the filtered list
	width    int
	height   int
}

// New creates a new agent picker over the given agents.
func New(agents []commandpalette.Agent) *Model {
	return &Model{
		agents:   append([]commandpalette.Agent(nil), agents...),
		selected: 0,
	}
}

// SetSize updates the render size.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Update handles key events and window resizes. The picker dispatches
// AgentPickerPickedMsg when the user presses Enter and
// AgentPickerCancelledMsg when they press Esc/ctrl+c.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			return m, func() tea.Msg { return shared.AgentPickerCancelledMsg{} }
		case "up":
			m.move(-1)
			return m, nil
		case "down":
			m.move(1)
			return m, nil
		case "enter":
			agent, ok := m.selectedAgent()
			if !ok {
				return m, nil
			}
			return m, func() tea.Msg {
				return shared.AgentPickerPickedMsg{
					AgentID:   agent.ID,
					AgentName: agent.Name,
				}
			}
		case "backspace", "ctrl+h":
			m.popQuery()
			return m, nil
		case "ctrl+u":
			m.query = ""
			m.selected = 0
			return m, nil
		default:
			if len(msg.Runes) > 0 {
				m.query += string(msg.Runes)
				m.selected = 0
				return m, nil
			}
		}
	}
	return m, nil
}

func (m *Model) move(delta int) {
	filtered := m.filtered()
	if len(filtered) == 0 {
		return
	}
	m.selected += delta
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(filtered) {
		m.selected = len(filtered) - 1
	}
}

func (m *Model) popQuery() {
	runes := []rune(m.query)
	if len(runes) == 0 {
		return
	}
	m.query = string(runes[:len(runes)-1])
	m.selected = 0
}

func (m *Model) filtered() []commandpalette.Agent {
	if m.query == "" {
		return m.agents
	}
	q := strings.ToLower(m.query)
	out := make([]commandpalette.Agent, 0, len(m.agents))
	for _, a := range m.agents {
		if strings.Contains(strings.ToLower(a.Name), q) || strings.Contains(strings.ToLower(a.Status), q) {
			out = append(out, a)
		}
	}
	return out
}

func (m *Model) selectedAgent() (commandpalette.Agent, bool) {
	filtered := m.filtered()
	if m.selected < 0 || m.selected >= len(filtered) {
		return commandpalette.Agent{}, false
	}
	return filtered[m.selected], true
}

// View renders the picker.
func (m *Model) View() string {
	var content string
	if m.height > 0 && m.height < 14 {
		content = m.viewCompact()
	} else {
		content = m.viewFull()
	}
	box := boxStyle.Width(m.boxWidth()).Render(content)
	if m.width <= 0 {
		return box
	}
	pad := (m.height - lipgloss.Height(box)) / 2
	if pad < 0 {
		pad = 0
	}
	return fitBlock(strings.Repeat("\n", pad)+lipgloss.PlaceHorizontal(m.width, lipgloss.Center, box), m.width, m.height)
}

func (m *Model) viewFull() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Select an agent"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Filter: %s\n\n", activeStyle.Render(m.query)))

	filtered := m.filtered()
	if len(filtered) == 0 {
		b.WriteString(mutedStyle.Render("No agents match."))
		b.WriteString("\n\n")
		b.WriteString(mutedStyle.Render("[Backspace] edit  [Esc] cancel"))
		return b.String()
	}

	maxVisible := m.maxVisible(7)
	start := 0
	if m.selected >= maxVisible {
		start = m.selected - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(filtered) {
		end = len(filtered)
	}
	rowWidth := m.contentWidth()
	for i := start; i < end; i++ {
		a := filtered[i]
		nameWidth := rowWidth - 4 - lipgloss.Width(a.Status)
		if nameWidth < 8 {
			nameWidth = 8
		}
		name := fitText(a.Name, nameWidth)
		var line string
		if i == m.selected {
			line = activeStyle.Render("▸ ") + lipgloss.NewStyle().Bold(true).Render(name) + "  " + mutedStyle.Render(a.Status)
		} else {
			line = "  " + mutedStyle.Render(name) + "  " + mutedStyle.Render(a.Status)
		}
		b.WriteString(fitLine(line, rowWidth) + "\n")
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
	count := fmt.Sprintf("%d/%d agents", len(filtered), len(m.agents))
	if strings.TrimSpace(m.query) == "" {
		count = fmt.Sprintf("%d agents", len(m.agents))
	}
	b.WriteString(fitLine(mutedStyle.Render(fmt.Sprintf("[↑/↓] navigate  type to filter  [Enter] select  [Esc] cancel  (%s)", count)), m.contentWidth()))
	return b.String()
}

func (m *Model) viewCompact() string {
	var b strings.Builder
	width := m.contentWidth()
	b.WriteString(titleStyle.Render("Select an agent"))
	b.WriteString("\n")
	b.WriteString(fitText("Filter: "+m.query, width))
	b.WriteString("\n")
	a, ok := m.selectedAgent()
	if !ok {
		b.WriteString(mutedStyle.Render(fitText("No matches", width)))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(fitText("[Backspace] edit", width)))
		return b.String()
	}
	row := fmt.Sprintf("%d/%d  %s", m.selected+1, len(m.filtered()), a.Name)
	b.WriteString(activeStyle.Render(fitText(row, width)))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(fitText("[↑/↓] choose  [Enter] select", width)))
	return b.String()
}

func (m *Model) maxVisible(fixedContentLines int) int {
	maxVisible := 12
	if m.height > 0 {
		budget := m.height - fixedContentLines
		if budget < 1 {
			budget = 1
		}
		if budget < maxVisible {
			maxVisible = budget
		}
	}
	if maxVisible < 1 {
		return 1
	}
	return maxVisible
}

func (m *Model) boxWidth() int {
	if m.width <= 0 {
		return 64
	}
	width := m.width - 2
	if width > 64 {
		width = 64
	}
	if width < 12 {
		width = maxInt(1, m.width-2)
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

func fitText(text string, width int) string {
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

func fitLine(line string, width int) string {
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

func fitBlock(block string, width, height int) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		lines[i] = fitLine(line, width)
	}
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
