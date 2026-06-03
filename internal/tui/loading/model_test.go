package loading

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestLoadingViewFitsTinyTerminal(t *testing.T) {
	m := New("Starting browser runtime with a long status")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 2})
	m = model.(Model)

	lines := strings.Split(m.View(), "\n")
	if len(lines) > m.height {
		t.Fatalf("line count = %d, want <= %d:\n%s", len(lines), m.height, m.View())
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width > m.width {
			t.Fatalf("line %d width = %d, want <= %d:\n%s", i+1, width, m.width, m.View())
		}
	}
}

func TestLoadingStatusMsgUpdatesSecondaryLine(t *testing.T) {
	m := New("Launching VulpineOS")
	model, _ := m.Update(StatusMsg("Preparing NanoClaw agents..."))
	m = model.(Model)

	view := m.View()
	if !strings.Contains(view, "Preparing NanoClaw agents...") {
		t.Fatalf("view missing updated status:\n%s", view)
	}
	if strings.Contains(view, "Starting VulpineOS kernel...") {
		t.Fatalf("view still contains default status:\n%s", view)
	}
}

func TestLoadingStatusMsgIgnoresBlankStatus(t *testing.T) {
	m := New("Launching VulpineOS")
	model, _ := m.Update(StatusMsg("   "))
	m = model.(Model)

	view := m.View()
	if !strings.Contains(view, "Starting VulpineOS kernel...") {
		t.Fatalf("view should keep default status for blank update:\n%s", view)
	}
}
