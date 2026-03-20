package poolstats

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"vulpineos/internal/tui/shared"
)

// Model holds context pool statistics.
type Model struct {
	available int
	active    int
	total     int
	width     int
}

// New creates a new pool stats panel.
func New() Model {
	return Model{
		width: 14,
	}
}

// SetWidth sets the render width.
func (m *Model) SetWidth(w int) {
	m.width = w
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case shared.PoolStatsMsg:
		m.available = msg.Available
		m.active = msg.Active
		m.total = msg.Total
	}
	return m, nil
}

// SetStats directly updates pool statistics.
func (m *Model) SetStats(available, active, total int) {
	m.available = available
	m.active = active
	m.total = total
}

// View renders the pool stats panel.
func (m Model) View() string {
	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render("POOL"))
	b.WriteString("\n")

	b.WriteString(shared.MutedStyle.Render(fmt.Sprintf("Avail: %d", m.available)))
	b.WriteString("\n")
	b.WriteString(shared.MutedStyle.Render(fmt.Sprintf("Active: %d", m.active)))
	b.WriteString("\n")
	b.WriteString(shared.MutedStyle.Render(fmt.Sprintf("Total: %d", m.total)))

	return b.String()
}
