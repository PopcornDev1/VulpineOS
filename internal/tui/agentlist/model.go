package agentlist

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/tui/shared"
	"vulpineos/internal/vault"
)

// AgentListItem represents one agent in the list.
type AgentListItem struct {
	ID          string
	Name        string
	Task        string
	Status      string
	Tokens      int
	Fingerprint string
	ProxyConfig string
	Metadata    string
	CreatedAt   time.Time
	Unread      int
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
				if msg.Tokens > 0 {
					m.agents[i].Tokens = msg.Tokens
				}
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
			ID:          a.ID,
			Name:        a.Name,
			Task:        a.Task,
			Status:      a.Status,
			Tokens:      a.TotalTokens,
			Fingerprint: a.Fingerprint,
			ProxyConfig: a.ProxyConfig,
			Metadata:    a.Metadata,
			CreatedAt:   a.CreatedAt,
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

// SelectedAgent returns the currently selected list item.
func (m Model) SelectedAgent() (AgentListItem, bool) {
	if len(m.agents) == 0 || m.selected >= len(m.agents) {
		return AgentListItem{}, false
	}
	return m.agents[m.selected], true
}

// Agent returns an item by ID.
func (m Model) Agent(id string) (AgentListItem, bool) {
	for _, agent := range m.agents {
		if agent.ID == id {
			return agent, true
		}
	}
	return AgentListItem{}, false
}

// SelectAgentID selects an agent by ID.
func (m *Model) SelectAgentID(id string) bool {
	for i := range m.agents {
		if m.agents[i].ID == id {
			m.selected = i
			return true
		}
	}
	return false
}

// AddAgent adds a new agent to the list.
func (m *Model) AddAgent(a vault.Agent) {
	m.agents = append(m.agents, AgentListItem{
		ID:          a.ID,
		Name:        a.Name,
		Task:        a.Task,
		Status:      a.Status,
		Tokens:      a.TotalTokens,
		Fingerprint: a.Fingerprint,
		ProxyConfig: a.ProxyConfig,
		Metadata:    a.Metadata,
		CreatedAt:   a.CreatedAt,
	})
}

// RemoveAgent removes an agent by ID.
func (m *Model) RemoveAgent(id string) {
	for i, a := range m.agents {
		if a.ID == id {
			m.agents = append(m.agents[:i], m.agents[i+1:]...)
			if i <= m.selected && m.selected > 0 {
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

// MarkUnread increments the unread count for an agent.
func (m *Model) MarkUnread(id string) {
	for i := range m.agents {
		if m.agents[i].ID == id {
			m.agents[i].Unread++
			return
		}
	}
}

// ClearUnread clears unread count for an agent.
func (m *Model) ClearUnread(id string) {
	for i := range m.agents {
		if m.agents[i].ID == id {
			m.agents[i].Unread = 0
			return
		}
	}
}

// UnreadCount returns the unread count for an agent.
func (m Model) UnreadCount(id string) int {
	for _, agent := range m.agents {
		if agent.ID == id {
			return agent.Unread
		}
	}
	return 0
}

// Status returns the current status for an agent.
func (m Model) Status(id string) string {
	for _, agent := range m.agents {
		if agent.ID == id {
			return agent.Status
		}
	}
	return ""
}

// IDsByStatus returns IDs whose current status is in the supplied set.
func (m Model) IDsByStatus(statuses map[string]bool) []string {
	ids := make([]string, 0)
	for _, agent := range m.agents {
		if statuses[agent.Status] {
			ids = append(ids, agent.ID)
		}
	}
	return ids
}

// statusIcon returns a styled icon for the given status.
func statusIcon(status string) string {
	switch status {
	case "active", "running":
		return lipgloss.NewStyle().Foreground(shared.ColorWarning).Render("●")
	case "thinking":
		return lipgloss.NewStyle().Foreground(shared.ColorWarning).Render("◌")
	case "paused":
		return lipgloss.NewStyle().Foreground(shared.ColorMuted).Render("Ⅱ")
	case "completed", "ready", "created", "":
		return lipgloss.NewStyle().Foreground(shared.ColorSuccess).Render("●")
	case "failed", "error", "interrupted":
		return lipgloss.NewStyle().Foreground(shared.ColorDanger).Render("×")
	case "starting":
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

	start, end := visibleAgentRange(len(m.agents), m.selected, m.height-1)
	for i := start; i < end; i++ {
		a := m.agents[i]
		cursor := "  "
		if i == m.selected {
			cursor = "▸ "
		}

		icon := statusIcon(a.Status)
		unreadText := ""
		if a.Unread > 0 {
			if a.Unread > 9 {
				unreadText = " 9+"
			} else {
				unreadText = fmt.Sprintf(" %d", a.Unread)
			}
		}
		unread := ""
		if unreadText != "" {
			unread = lipgloss.NewStyle().Foreground(shared.ColorWarning).Render(unreadText)
		}

		// Context indicator: a small browser-window glyph when this agent owns an
		// open browser context (so `v` can show/hide that agent's window).
		ctxText, ctxMark := "", ""
		if hasOpenContext(a.Metadata) {
			ctxText = " ◗"
			ctxMark = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED")).Render(ctxText)
		}

		maxName := m.width - lipgloss.Width(cursor) - 1 - lipgloss.Width(icon) - lipgloss.Width(unreadText) - lipgloss.Width(ctxText)
		if maxName < 1 {
			maxName = 1
		}
		name := padVisible(fitVisible(a.Name, maxName), maxName)

		line := fitAgentRow(fmt.Sprintf("%s%s%s %s%s", cursor, name, ctxMark, icon, unread), m.width)
		if i == m.selected {
			line = shared.SelectedStyle.Render(line)
		}
		b.WriteString(line)
		if i < end-1 {
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

// hasOpenContext reports whether the agent's metadata records an open browser
// context (and thus a window that `v` can show/hide).
func hasOpenContext(metadata string) bool {
	meta, err := vault.ParseAgentMetadata(metadata)
	if err != nil {
		return false
	}
	return strings.TrimSpace(meta.ContextID) != ""
}

func visibleAgentRange(total, selected, capacity int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if capacity <= 0 || capacity >= total {
		return 0, total
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= total {
		selected = total - 1
	}
	start := selected - capacity/2
	if start < 0 {
		start = 0
	}
	if start+capacity > total {
		start = total - capacity
	}
	return start, start + capacity
}

func fitVisible(text string, width int) string {
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

func padVisible(text string, width int) string {
	if width <= 0 {
		return ""
	}
	for lipgloss.Width(text) < width {
		text += " "
	}
	return text
}

func fitAgentRow(line string, width int) string {
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
