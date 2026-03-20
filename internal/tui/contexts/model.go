package contexts

import (
	"fmt"
	"strings"

	"vulpineos/internal/tui/shared"
)

// Model is the contexts panel showing active browser contexts.
type Model struct {
	width    int
	targets  []target
	selected int
}

type target struct {
	sessionID string
	targetID  string
	contextID string
	url       string
}

func New() Model {
	return Model{selected: 0}
}

func (m Model) Update(msg interface{}) Model {
	switch msg := msg.(type) {
	case shared.TargetAttachedMsg:
		m.targets = append(m.targets, target{
			sessionID: msg.SessionID,
			targetID:  msg.TargetID,
			contextID: msg.ContextID,
			url:       msg.URL,
		})
	case shared.TargetDetachedMsg:
		for i, t := range m.targets {
			if t.targetID == msg.TargetID || t.sessionID == msg.SessionID {
				m.targets = append(m.targets[:i], m.targets[i+1:]...)
				break
			}
		}
		if m.selected >= len(m.targets) && m.selected > 0 {
			m.selected--
		}
	}
	return m
}

func (m Model) SetWidth(w int) Model {
	m.width = w
	return m
}

func (m Model) MoveUp() Model {
	if m.selected > 0 {
		m.selected--
	}
	return m
}

func (m Model) MoveDown() Model {
	if m.selected < len(m.targets)-1 {
		m.selected++
	}
	return m
}

func (m Model) SelectedTarget() (sessionID, targetID string) {
	if m.selected < len(m.targets) {
		return m.targets[m.selected].sessionID, m.targets[m.selected].targetID
	}
	return "", ""
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render("TARGETS"))
	b.WriteString(shared.MutedStyle.Render(fmt.Sprintf(" (%d)", len(m.targets))))
	b.WriteString("\n")

	if len(m.targets) == 0 {
		b.WriteString(shared.MutedStyle.Render("  No active targets"))
		return b.String()
	}

	b.WriteString(shared.HeaderStyle.Render(fmt.Sprintf("  %-12s %-12s %s", "TARGET", "CONTEXT", "URL")))
	b.WriteString("\n")

	for i, t := range m.targets {
		url := t.url
		if len(url) > 40 {
			url = url[:37] + "..."
		}
		line := fmt.Sprintf("  %-12s %-12s %s",
			truncate(t.targetID, 12),
			truncate(t.contextID, 12),
			url,
		)
		if i == m.selected {
			b.WriteString(shared.SelectedStyle.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
