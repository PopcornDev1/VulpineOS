package agentlist

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/tui/shared"
	"vulpineos/internal/vault"
)

// AgentListItem represents one agent in the list.
type AgentListItem struct {
	ID     string
	Name   string
	Status string
	Tokens int
}

// Model holds the selectable agent list state.
type Model struct {
	agents   []AgentListItem
	selected int
	width    int
	height   int
}

// New creates a new agent list panel.
func New() Model {
	return Model{
		width: 20,
	}
}

// SetWidth sets the render width.
func (m *Model) SetWidth(w int) {
	m.width = w
}

// SetHeight sets the render height.
func (m *Model) SetHeight(h int) {
	m.height = h
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case shared.AgentStatusMsg:
		m.UpdateStatus(msg.AgentID, msg.Status)
		// Update tokens if provided.
		for i := range m.agents {
			if m.agents[i].ID == msg.AgentID {
				m.agents[i].Tokens = msg.Tokens
			}
		}
	case shared.AgentCreatedMsg:
		m.AddAgent(msg.Agent)
	}
	return m, nil
}

// SetAgents bulk-loads agents from the vault.
func (m *Model) SetAgents(agents []vault.Agent) {
	m.agents = make([]AgentListItem, len(agents))
	for i, a := range agents {
		m.agents[i] = AgentListItem{
			ID:     a.ID,
			Name:   a.Name,
			Status: a.Status,
			Tokens: a.TotalTokens,
		}
	}
	if m.selected >= len(m.agents) {
		m.selected = max(0, len(m.agents)-1)
	}
}

// MoveUp moves selection up.
func (m *Model) MoveUp() {
	if m.selected > 0 {
		m.selected--
	}
}

// MoveDown moves selection down.
func (m *Model) MoveDown() {
	if m.selected < len(m.agents)-1 {
		m.selected++
	}
}

// SelectedAgentID returns the ID of the currently selected agent.
func (m Model) SelectedAgentID() string {
	if len(m.agents) == 0 || m.selected >= len(m.agents) {
		return ""
	}
	return m.agents[m.selected].ID
}

// AddAgent adds a new agent to the list.
func (m *Model) AddAgent(a vault.Agent) {
	m.agents = append(m.agents, AgentListItem{
		ID:     a.ID,
		Name:   a.Name,
		Status: a.Status,
		Tokens: a.TotalTokens,
	})
}

// RemoveAgent removes an agent by ID.
func (m *Model) RemoveAgent(id string) {
	for i, a := range m.agents {
		if a.ID == id {
			m.agents = append(m.agents[:i], m.agents[i+1:]...)
			if m.selected >= len(m.agents) && m.selected > 0 {
				m.selected--
			}
			return
		}
	}
}

// UpdateStatus updates an agent's status by ID.
func (m *Model) UpdateStatus(id, status string) {
	for i := range m.agents {
		if m.agents[i].ID == id {
			m.agents[i].Status = status
			return
		}
	}
}

// statusIcon returns a styled icon for the given status.
func statusIcon(status string) string {
	switch status {
	case "active", "running":
		return lipgloss.NewStyle().Foreground(shared.ColorWarning).Render("●")
	case "thinking":
		return lipgloss.NewStyle().Foreground(shared.ColorWarning).Render("◌")
	case "completed", "ready", "":
		return lipgloss.NewStyle().Foreground(shared.ColorSuccess).Render("●")
	case "failed", "error":
		return lipgloss.NewStyle().Foreground(shared.ColorDanger).Render("●")
	case "starting", "created":
		return lipgloss.NewStyle().Foreground(shared.ColorWarning).Render("○")
	default:
		return lipgloss.NewStyle().Foreground(shared.ColorSuccess).Render("●")
	}
}

// View renders the agent list.
func (m Model) View() string {
	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render("AGENTS"))
	b.WriteString("\n")

	if len(m.agents) == 0 {
		b.WriteString(shared.MutedStyle.Render("  (none)"))
		return b.String()
	}

	for i, a := range m.agents {
		cursor := "  "
		if i == m.selected {
			cursor = "▸ "
		}

		name := a.Name
		// Truncate name to fit width.
		maxName := m.width - 6 // cursor(2) + space(1) + icon(~2) + padding
		if maxName < 4 {
			maxName = 4
		}
		if len(name) > maxName {
			name = name[:maxName-1] + "…"
		}

		icon := statusIcon(a.Status)

		line := fmt.Sprintf("%s%-*s %s", cursor, maxName, name, icon)
		if i == m.selected {
			line = shared.SelectedStyle.Render(line)
		}
		b.WriteString(line)
		if i < len(m.agents)-1 {
			b.WriteString("\n")
		}
	}

	// Truncate to allocated height so the panel never overflows
	result := b.String()
	if m.height > 0 {
		lines := strings.Split(result, "\n")
		if len(lines) > m.height {
			lines = lines[:m.height]
			result = strings.Join(lines, "\n")
		}
	}
	return result
}
