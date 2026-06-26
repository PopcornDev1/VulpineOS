package commandpalette

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestInlineViewShowsSectionsAndScrollbar(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")

	m := New()
	m.SetAgents([]Agent{
		{Name: "Alpha", Status: "ready"},
		{Name: "Beta", Status: "paused"},
	})
	m.Activate()

	view := m.InlineView(48, 8)
	if view == "" {
		t.Fatal("inline command palette view should not be empty")
	}
	if height := lipgloss.Height(view); height > 8 {
		t.Fatalf("inline command palette height = %d, want <= 8\n%s", height, view)
	}
	if !strings.Contains(view, "Commands") {
		t.Fatalf("inline command palette should contain header, got:\n%s", view)
	}
	if !strings.Contains(view, "Agents") || !strings.Contains(view, "Alpha") {
		t.Fatalf("inline command palette should contain agent section, got:\n%s", view)
	}
	if !strings.Contains(view, "█") {
		t.Fatalf("inline command palette should contain scrollbar thumb, got:\n%s", view)
	}
	if strings.Contains(view, "type a command") {
		t.Fatalf("inline command palette should not render its own input, got:\n%s", view)
	}
	if !strings.Contains(view, "▌") {
		t.Fatalf("inline command palette should use rail shell, got:\n%s", view)
	}
	for _, border := range []string{"╭", "╮", "╰", "╯"} {
		if strings.Contains(view, border) {
			t.Fatalf("inline command palette should not use rounded border %q, got:\n%s", border, view)
		}
	}
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 48 {
			t.Fatalf("inline command palette line %d width = %d, want <= 48:\n%s", i+1, width, view)
		}
	}
}

func TestInlineViewUsesSolidRailInAppleTerminal(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")

	m := New()
	m.SetAgents([]Agent{{Name: "Alpha", Status: "ready"}})
	m.Activate()

	view := m.InlineView(48, 8)
	if view == "" {
		t.Fatal("inline command palette view should not be empty")
	}
	if strings.Contains(view, "▌") {
		t.Fatalf("Apple Terminal command palette should use solid rail cell, not glyph rail:\n%s", view)
	}
	if !strings.Contains(view, "Commands") || !strings.Contains(view, "Alpha") {
		t.Fatalf("inline command palette should keep content, got:\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 48 {
			t.Fatalf("inline command palette line %d width = %d, want <= 48:\n%s", i+1, width, view)
		}
	}
}

func TestSlashPrefixedAgentNameDispatchesSwitchCommand(t *testing.T) {
	m := New()
	m.SetAgents([]Agent{{ID: "agent-fox", Name: "fox", Status: "ready"}})
	m.Activate()
	m.SetQuery("/fox")

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should dispatch the selected command")
	}

	dispatched := cmd()
	msg, ok := dispatched.(ExecuteCommandMsg)
	if !ok {
		t.Fatalf("enter dispatched %T, want ExecuteCommandMsg", dispatched)
	}
	if msg.Name != "switch" {
		t.Fatalf("command name = %q, want switch", msg.Name)
	}
	if msg.RawInput != "agent-fox" {
		t.Fatalf("raw input = %q, want agent id", msg.RawInput)
	}
}

func TestDefaultCommandsDispatchByTypedName(t *testing.T) {
	for _, command := range defaultCommands() {
		t.Run(command.Name, func(t *testing.T) {
			m := New()
			m.Activate()
			m.SetQuery("/" + command.Name)

			var cmd tea.Cmd
			m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("enter should dispatch a command")
			}

			dispatched := cmd()
			msg, ok := dispatched.(ExecuteCommandMsg)
			if !ok {
				t.Fatalf("enter dispatched %T, want ExecuteCommandMsg", dispatched)
			}
			if msg.Name != command.Name {
				t.Fatalf("command name = %q, want %q", msg.Name, command.Name)
			}
		})
	}
}

func TestCommandWithArbitraryArgsDispatchesTypedInput(t *testing.T) {
	m := New()
	m.Activate()
	m.SetQuery("/model ollama/qwen3")

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should dispatch model command with args")
	}

	dispatched := cmd()
	msg, ok := dispatched.(ExecuteCommandMsg)
	if !ok {
		t.Fatalf("enter dispatched %T, want ExecuteCommandMsg", dispatched)
	}
	if msg.Name != "model" || msg.RawInput != "model ollama/qwen3" {
		t.Fatalf("dispatched command = %+v, want model raw input", msg)
	}
}

func TestQuitCommandMatchesExitAndCloseAliases(t *testing.T) {
	for _, query := range []string{"/exit", "/close"} {
		t.Run(query, func(t *testing.T) {
			m := New()
			m.Activate()
			m.SetQuery(query)

			var cmd tea.Cmd
			m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("enter should dispatch the quit command")
			}

			dispatched := cmd()
			msg, ok := dispatched.(ExecuteCommandMsg)
			if !ok {
				t.Fatalf("enter dispatched %T, want ExecuteCommandMsg", dispatched)
			}
			if msg.Name != "quit" {
				t.Fatalf("command name = %q, want quit", msg.Name)
			}
		})
	}
}

func TestQuitAliasesDoNotAddDefaultCommands(t *testing.T) {
	for _, command := range defaultCommands() {
		if command.Name == "exit" || command.Name == "close" {
			t.Fatalf("default commands should not include alias command %q", command.Name)
		}
	}
}

func TestDefaultCommandsIncludeRecoveryCommands(t *testing.T) {
	seen := map[string]bool{}
	for _, command := range defaultCommands() {
		seen[command.Name] = true
	}
	for _, name := range []string{"recover", "clear-warning"} {
		if !seen[name] {
			t.Fatalf("default commands missing %q", name)
		}
	}
}

func TestPartialCloseAliasDoesNotPreferQuit(t *testing.T) {
	m := New()
	m.Activate()
	m.SetQuery("/c")

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should dispatch a matching command")
	}

	dispatched := cmd()
	msg, ok := dispatched.(ExecuteCommandMsg)
	if !ok {
		t.Fatalf("enter dispatched %T, want ExecuteCommandMsg", dispatched)
	}
	if msg.Name == "quit" {
		t.Fatal("partial /c query should not prefer quit via close alias")
	}
}

func TestExactCommandQueryPrefersCommandOverAgentMatches(t *testing.T) {
	m := New()
	// Agent whose display name contains the query, so the exact command name
	// ("delete") must still outrank it.
	m.SetAgents([]Agent{{ID: "agent-1", Name: "Deleted Worker", Status: "ready"}})
	m.Activate()
	m.SetQuery("/delete")

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should dispatch the delete command")
	}

	dispatched := cmd()
	msg, ok := dispatched.(ExecuteCommandMsg)
	if !ok {
		t.Fatalf("enter dispatched %T, want ExecuteCommandMsg", dispatched)
	}
	if msg.Name != "delete" {
		t.Fatalf("command name = %q, want delete", msg.Name)
	}
}

func TestNamePrefixOutranksDescriptionSubstring(t *testing.T) {
	for _, query := range []string{"/mod", "/mo", "/model", "/models"} {
		t.Run(query, func(t *testing.T) {
			m := New()
			m.Activate()
			m.SetQuery(query)

			if len(m.filtered) == 0 {
				t.Fatalf("query %q produced no matches", query)
			}
			if got := m.filtered[0].Name; got != "model" {
				names := make([]string, len(m.filtered))
				for i, c := range m.filtered {
					names[i] = c.Name
				}
				t.Fatalf("query %q top match = %q, want model; full list: %v", query, got, names)
			}
		})
	}
}
