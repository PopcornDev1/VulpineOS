package contextlist

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"vulpineos/internal/tui/shared"
)

// ContextItem represents a browser context/target entry.
type ContextItem struct {
	SessionID string
	TargetID  string
	ContextID string
	URL       string
	FrameID   string
}

// Model holds the context list state.
type Model struct {
	items    []ContextItem
	selected int
	width    int
}

// New creates a new context list panel.
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
	case shared.TargetAttachedMsg:
		m.items = append(m.items, ContextItem{
			SessionID: msg.SessionID,
			TargetID:  msg.TargetID,
			ContextID: msg.ContextID,
			URL:       msg.URL,
		})
	case shared.TargetDetachedMsg:
		for i, item := range m.items {
			if item.SessionID == msg.SessionID || item.TargetID == msg.TargetID {
				m.items = append(m.items[:i], m.items[i+1:]...)
				if m.selected >= len(m.items) && m.selected > 0 {
					m.selected--
				}
				break
			}
		}
	case shared.NavigationMsg:
		for i := range m.items {
			if m.items[i].SessionID == msg.SessionID {
				m.items[i].URL = msg.URL
				if m.items[i].FrameID == "" {
					m.items[i].FrameID = msg.FrameID
				}
			}
		}
	case shared.FrameAttachedMsg:
		for i := range m.items {
			if m.items[i].SessionID == msg.SessionID && m.items[i].FrameID == "" {
				m.items[i].FrameID = msg.FrameID
			}
		}
	}
	return m, nil
}

// MoveUp moves selection up.
func (m *Model) MoveUp() {
	if m.selected > 0 {
		m.selected--
	}
}

// MoveDown moves selection down.
func (m *Model) MoveDown() {
	if m.selected < len(m.items)-1 {
		m.selected++
	}
}

// SelectedTarget returns the session ID and target ID of the selected item.
func (m Model) SelectedTarget() (string, string) {
	if len(m.items) == 0 || m.selected >= len(m.items) {
		return "", ""
	}
	return m.items[m.selected].SessionID, m.items[m.selected].TargetID
}

// truncateURL shortens a URL for display.
func truncateURL(url string, maxLen int) string {
	if maxLen < 4 {
		maxLen = 4
	}
	if len(url) <= maxLen {
		return url
	}
	return url[:maxLen-1] + "…"
}

// View renders the context list.
func (m Model) View() string {
	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render("CONTEXTS"))
	b.WriteString("\n")

	if len(m.items) == 0 {
		b.WriteString(shared.MutedStyle.Render(" (none)"))
		return b.String()
	}

	maxURL := m.width - 4
	for i, item := range m.items {
		cursor := " "
		if i == m.selected {
			cursor = "▸"
		}

		url := item.URL
		if url == "" {
			url = "about:blank"
		}
		url = truncateURL(url, maxURL)

		line := fmt.Sprintf("%s %s", cursor, url)
		if i == m.selected {
			line = shared.SelectedStyle.Render(line)
		}
		b.WriteString(line)
		if i < len(m.items)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}
