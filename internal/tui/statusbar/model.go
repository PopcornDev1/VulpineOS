package statusbar

import (
	"fmt"
	"strings"

	"vulpineos/internal/tui/shared"
)

// Model is the bottom status bar.
type Model struct {
	width      int
	mode       string // "local", "remote", "serve"
	connected  bool
	remoteAddr string
	activePanel string
}

func New() Model {
	return Model{
		mode:        "local",
		connected:   false,
		activePanel: "dashboard",
	}
}

func (m Model) SetWidth(w int) Model {
	m.width = w
	return m
}

func (m Model) SetMode(mode string) Model {
	m.mode = mode
	return m
}

func (m Model) SetConnected(connected bool) Model {
	m.connected = connected
	return m
}

func (m Model) SetActivePanel(panel string) Model {
	m.activePanel = panel
	return m
}

func (m Model) View() string {
	// Left: mode + connection
	var left strings.Builder
	left.WriteString(shared.KeyStyle.Render(" VULPINE"))
	left.WriteString(shared.MutedStyle.Render(" │ "))

	switch m.mode {
	case "local":
		if m.connected {
			left.WriteString(shared.RunningStyle.Render("● local"))
		} else {
			left.WriteString(shared.StoppedStyle.Render("● disconnected"))
		}
	case "remote":
		if m.connected {
			left.WriteString(shared.RunningStyle.Render(fmt.Sprintf("● remote (%s)", m.remoteAddr)))
		} else {
			left.WriteString(shared.StoppedStyle.Render("● connecting..."))
		}
	case "serve":
		left.WriteString(shared.WarmingStyle.Render("● serving"))
	}

	// Right: keybind hints
	hints := []struct{ key, desc string }{
		{"Tab", "panel"},
		{"j/k", "nav"},
		{"s", "spawn"},
		{"a", "agent"},
		{"d", "destroy"},
		{"r", "restart"},
		{"q", "quit"},
	}

	var right strings.Builder
	for i, h := range hints {
		if i > 0 {
			right.WriteString("  ")
		}
		right.WriteString(shared.KeyStyle.Render(h.key))
		right.WriteString(shared.MutedStyle.Render(":" + h.desc))
	}

	// Pad between left and right
	leftStr := left.String()
	rightStr := right.String()

	return leftStr + "  " + rightStr
}
