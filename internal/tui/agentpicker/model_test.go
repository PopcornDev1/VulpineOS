package agentpicker

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"vulpineos/internal/tui/commandpalette"
	"vulpineos/internal/tui/shared"
)

func sampleAgents() []commandpalette.Agent {
	return []commandpalette.Agent{
		{ID: "a1", Name: "worker_1", Status: "active"},
		{ID: "a2", Name: "worker_2", Status: "paused"},
		{ID: "a3", Name: "researcher", Status: "active"},
		{ID: "a4", Name: "tester", Status: "completed"},
		{ID: "a5", Name: "auditor", Status: "error"},
	}
}

func pressKey(m *Model, key tea.KeyMsg) *Model {
	out, _ := m.Update(key)
	return out.(*Model)
}

func TestNewEmptyShowsAllAgents(t *testing.T) {
	for _, n := range []int{0, 1, 5} {
		agents := sampleAgents()[:n]
		m := New(agents)
		if got := len(m.filtered()); got != n {
			t.Fatalf("New(%d) filtered = %d, want %d", n, got, n)
		}
	}
}

func TestFilterNarrowsResults(t *testing.T) {
	m := New(sampleAgents())
	m = pressKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	filtered := m.filtered()
	if len(filtered) != 2 {
		t.Fatalf("filter 'w' returned %d agents, want 2: %v", len(filtered), filtered)
	}
}

func TestArrowKeysMoveSelectionThroughFilteredList(t *testing.T) {
	m := New(sampleAgents())
	m = pressKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if m.selected != 0 {
		t.Fatalf("initial selected = %d, want 0", m.selected)
	}
	m = pressKey(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.selected != 1 {
		t.Fatalf("after down selected = %d, want 1", m.selected)
	}
	m = pressKey(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.selected != 1 {
		t.Fatalf("down past end selected = %d, want 1 (clamped)", m.selected)
	}
	m = pressKey(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.selected != 0 {
		t.Fatalf("after up selected = %d, want 0", m.selected)
	}
	m = pressKey(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.selected != 0 {
		t.Fatalf("up past start selected = %d, want 0 (clamped)", m.selected)
	}
}

func TestEnterDispatchesPickedMessage(t *testing.T) {
	m := New(sampleAgents())
	m.SetSize(80, 30)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should dispatch a picked message")
	}
	msg := cmd()
	picked, ok := msg.(shared.AgentPickerPickedMsg)
	if !ok {
		t.Fatalf("enter dispatched %T, want AgentPickerPickedMsg", msg)
	}
	if picked.AgentID != "a1" {
		t.Fatalf("picked AgentID = %q, want a1 (first in list)", picked.AgentID)
	}
	if picked.AgentName != "worker_1" {
		t.Fatalf("picked AgentName = %q, want worker_1", picked.AgentName)
	}
}

func TestEscDispatchesCancelledMessage(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyEsc} {
		t.Run(key.String(), func(t *testing.T) {
			m := New(sampleAgents())
			_, cmd := m.Update(tea.KeyMsg{Type: key})
			if cmd == nil {
				t.Fatalf("%s should dispatch a cancelled message", key)
			}
			msg := cmd()
			if _, ok := msg.(shared.AgentPickerCancelledMsg); !ok {
				t.Fatalf("%s dispatched %T, want AgentPickerCancelledMsg", key, msg)
			}
		})
	}
}

func TestBackspacePopsFilter(t *testing.T) {
	m := New(sampleAgents())
	for _, r := range "wo" {
		m = pressKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.query != "wo" {
		t.Fatalf("query after typing = %q, want wo", m.query)
	}
	m = pressKey(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.query != "w" {
		t.Fatalf("query after backspace = %q, want w", m.query)
	}
}

func TestViewRendersTitleAndFilter(t *testing.T) {
	m := New(sampleAgents())
	m.SetSize(80, 30)
	view := m.View()
	if !strings.Contains(view, "Select an agent") {
		t.Fatalf("view should contain title, got:\n%s", view)
	}
	if !strings.Contains(view, "Filter:") {
		t.Fatalf("view should contain filter label, got:\n%s", view)
	}
	for _, a := range sampleAgents() {
		if !strings.Contains(view, a.Name) {
			t.Fatalf("view should contain %q, got:\n%s", a.Name, view)
		}
	}
}
