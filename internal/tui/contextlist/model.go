package contextlist

import (
	"fmt"
	"net/url"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/tui/shared"
)

// ContextItem represents a browser context/target entry.
type ContextItem struct {
	SessionID string
	TargetID  string
	ContextID string
	URL       string
	FrameID   string
	Loading   bool   // true while navigating
	Title     string // page title if known
}

// Model holds the context list state.
type Model struct {
	items    []ContextItem
	selected int
	width    int
	height   int
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
	case shared.TargetAttachedMsg:
		for i := range m.items {
			if m.items[i].SessionID == msg.SessionID || (msg.TargetID != "" && m.items[i].TargetID == msg.TargetID) {
				m.items[i].SessionID = msg.SessionID
				m.items[i].TargetID = msg.TargetID
				m.items[i].ContextID = msg.ContextID
				m.items[i].URL = msg.URL
				return m, nil
			}
		}
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
				m.items[i].Loading = true
				if m.items[i].FrameID == "" {
					m.items[i].FrameID = msg.FrameID
				}
			}
		}
	case shared.PageLoadMsg:
		for i := range m.items {
			if m.items[i].SessionID == msg.SessionID {
				m.items[i].Loading = false
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

// PageCount returns the number of tracked targets (pages/tabs).
func (m Model) PageCount() int {
	return len(m.items)
}

// ContextCount returns the number of distinct browser contexts across all
// tracked targets.
func (m Model) ContextCount() int {
	seen := make(map[string]struct{}, len(m.items))
	for _, item := range m.items {
		seen[item.ContextID] = struct{}{}
	}
	return len(seen)
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

// SelectedContextID returns the browser context ID of the selected item.
func (m Model) SelectedContextID() string {
	if len(m.items) == 0 || m.selected >= len(m.items) {
		return ""
	}
	return m.items[m.selected].ContextID
}

// SelectedURL returns the URL of the selected context.
func (m Model) SelectedURL() string {
	if len(m.items) == 0 || m.selected >= len(m.items) {
		return ""
	}
	return m.items[m.selected].URL
}

// SafeDisplayURL redacts credentials and common secret query params for UI output.
func SafeDisplayURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	if parsed.User != nil {
		parsed.User = url.UserPassword("redacted", "redacted")
	}
	query := parsed.Query()
	redacted := false
	for key := range query {
		if sensitiveURLKey(key) {
			query.Set(key, "[redacted]")
			redacted = true
		}
	}
	if redacted {
		parsed.RawQuery = query.Encode()
	}
	return parsed.String()
}

func sensitiveURLKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
	for _, marker := range []string{"api_key", "apikey", "token", "secret", "password", "credential", "authorization", "cookie", "session"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
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

	b.WriteString(fitLine(shared.TitleStyle.Render("CONTEXTS"), m.width))
	b.WriteString("\n")

	if len(m.items) == 0 {
		b.WriteString(shared.MutedStyle.Render(" (none)"))
		return b.String()
	}

	maxURL := m.width - 4
	start, end := visibleContextRange(len(m.items), m.selected, m.height-1)
	for i := start; i < end; i++ {
		item := m.items[i]
		cursor := " "
		if i == m.selected {
			cursor = "▸"
		}

		// Status indicator
		status := shared.RunningStyle.Render("●") // green = idle
		if item.Loading {
			status = shared.WarmingStyle.Render("◌") // amber = loading
		}
		if item.URL == "" || item.URL == "about:blank" {
			status = shared.MutedStyle.Render("○") // gray = blank
		}

		url := item.URL
		if url == "" {
			url = "about:blank"
		}
		url = SafeDisplayURL(url)
		url = truncateURL(url, maxURL)

		line := fmt.Sprintf("%s %s %s", cursor, status, url)
		if i == m.selected {
			line = shared.SelectedStyle.Render(line)
		}
		line = fitLine(line, m.width)
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

func visibleContextRange(total, selected, capacity int) (int, int) {
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
