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

func TestLoadingScreenOnlyCtrlCQuits(t *testing.T) {
	m := New("Starting browser runtime")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("plain q should not quit loading screen")
		}
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c should quit loading screen")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c did not return tea.QuitMsg")
	}
}
