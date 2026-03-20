package alerts

import (
	"fmt"
	"strings"

	"vulpineos/internal/tui/shared"
)

const maxAlerts = 50

// Model is the alerts panel showing injection attempt alerts.
type Model struct {
	width  int
	alerts []shared.AlertMsg
}

func New() Model {
	return Model{}
}

func (m Model) Update(msg interface{}) Model {
	switch msg := msg.(type) {
	case shared.AlertMsg:
		m.alerts = append([]shared.AlertMsg{msg}, m.alerts...)
		if len(m.alerts) > maxAlerts {
			m.alerts = m.alerts[:maxAlerts]
		}
	}
	return m
}

func (m Model) SetWidth(w int) Model {
	m.width = w
	return m
}

func (m Model) Count() int {
	return len(m.alerts)
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render("ALERTS"))
	if len(m.alerts) > 0 {
		b.WriteString(shared.AlertStyle.Render(fmt.Sprintf(" (%d)", len(m.alerts))))
	}
	b.WriteString("\n")

	if len(m.alerts) == 0 {
		b.WriteString(shared.MutedStyle.Render("  No injection attempts detected"))
		return b.String()
	}

	// Show latest 8 alerts
	limit := 8
	if len(m.alerts) < limit {
		limit = len(m.alerts)
	}

	for _, a := range m.alerts[:limit] {
		ts := a.Timestamp.Format("15:04:05")
		blockedStr := shared.RunningStyle.Render("BLOCKED")
		if !a.Blocked {
			blockedStr = shared.StoppedStyle.Render("PASSED")
		}

		url := a.URL
		if len(url) > 30 {
			url = url[:27] + "..."
		}

		b.WriteString(fmt.Sprintf("  %s %s %-7s %s %s\n",
			shared.MutedStyle.Render(ts),
			blockedStr,
			shared.WarmingStyle.Render(a.Type),
			url,
			shared.MutedStyle.Render(a.Details),
		))
	}

	if len(m.alerts) > limit {
		b.WriteString(shared.MutedStyle.Render(fmt.Sprintf("  ... and %d more", len(m.alerts)-limit)))
	}

	return b.String()
}
