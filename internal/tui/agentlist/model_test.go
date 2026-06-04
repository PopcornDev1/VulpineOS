package agentlist

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/tui/shared"
	"vulpineos/internal/vault"
)

func TestStatusIconDistinguishesPausedAndInterrupted(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   string
	}{
		{status: "paused", want: "Ⅱ"},
		{status: "interrupted", want: "×"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			if got := statusIcon(tc.status); !strings.Contains(got, tc.want) {
				t.Fatalf("statusIcon(%q) = %q, want marker %q", tc.status, got, tc.want)
			}
		})
	}
}

func TestViewBudgetsUnreadBadgeWidth(t *testing.T) {
	m := New()
	m.SetWidth(14)
	m.SetAgents([]vault.Agent{{
		ID:     "agent-1",
		Name:   "very-long-agent-name",
		Status: "active",
	}})
	m.agents[0].Unread = 12

	lines := strings.Split(m.View(), "\n")
	if len(lines) < 2 {
		t.Fatalf("view lines = %d, want agent row", len(lines))
	}
	if got := lipgloss.Width(lines[1]); got > m.width {
		t.Fatalf("agent row width = %d, want <= %d: %q", got, m.width, lines[1])
	}
}

func TestStatusEventWithoutTokensPreservesExistingCount(t *testing.T) {
	m := New()
	m.SetAgents([]vault.Agent{{
		ID:          "agent-1",
		Name:        "Agent",
		Status:      "active",
		TotalTokens: 42,
	}})

	updated, _ := m.Update(shared.AgentStatusMsg{AgentID: "agent-1", Status: "paused", Tokens: 0})

	if got := updated.agents[0].Tokens; got != 42 {
		t.Fatalf("tokens = %d, want preserved 42", got)
	}
}

func TestViewKeepsSelectedAgentVisibleWhenListOverflows(t *testing.T) {
	m := New()
	m.SetWidth(24)
	m.SetHeight(4)
	agents := make([]vault.Agent, 8)
	for i := range agents {
		agents[i] = vault.Agent{
			ID:     "agent-visible",
			Name:   "Agent " + string(rune('A'+i)),
			Status: "active",
		}
	}
	m.SetAgents(agents)
	for i := 0; i < 6; i++ {
		m.MoveDown()
	}

	view := m.View()
	if !strings.Contains(view, "Agent G") {
		t.Fatalf("selected agent hidden in overflowed view:\n%s", view)
	}
	if strings.Contains(view, "Agent A") {
		t.Fatalf("overflowed view stayed pinned to top:\n%s", view)
	}
}

func TestClickNearestSelectsRowUnderClick(t *testing.T) {
	m := New()
	m.SetHeight(10)
	m.SetAgents([]vault.Agent{
		{ID: "a0", Name: "Agent0", Status: "ready"},
		{ID: "a1", Name: "Agent1", Status: "ready"},
		{ID: "a2", Name: "Agent2", Status: "ready"},
	})

	// Panel top at screen row 5: border=5, title=6, first agent=7, second=8.
	panelY := 5
	item, ok := m.ClickNearest(panelY+2+1, panelY) // click the second agent row
	if !ok {
		t.Fatal("ClickNearest returned not-ok with agents present")
	}
	if item.ID != "a1" {
		t.Fatalf("clicked agent = %q, want a1", item.ID)
	}
	if got := m.SelectedAgentID(); got != "a1" {
		t.Fatalf("selected after click = %q, want a1", got)
	}
}

func TestClickNearestEmptyListReturnsFalse(t *testing.T) {
	m := New()
	m.SetHeight(10)
	if _, ok := m.ClickNearest(7, 5); ok {
		t.Fatal("ClickNearest should return false with no agents")
	}
}

func TestScrollMovesSelectedAgent(t *testing.T) {
	m := New()
	m.SetHeight(4)
	m.SetAgents([]vault.Agent{
		{ID: "a0", Name: "Agent0", Status: "ready"},
		{ID: "a1", Name: "Agent1", Status: "ready"},
		{ID: "a2", Name: "Agent2", Status: "ready"},
	})

	if item, ok := m.Scroll(1); !ok || item.ID != "a1" {
		t.Fatalf("scroll down selected %#v ok=%v, want a1/true", item, ok)
	}
	if item, ok := m.Scroll(10); !ok || item.ID != "a2" {
		t.Fatalf("scroll past end selected %#v ok=%v, want clamped a2/true", item, ok)
	}
	if item, ok := m.Scroll(-10); !ok || item.ID != "a0" {
		t.Fatalf("scroll past start selected %#v ok=%v, want clamped a0/true", item, ok)
	}
}

func TestScrollEmptyListReturnsFalse(t *testing.T) {
	m := New()
	if _, ok := m.Scroll(1); ok {
		t.Fatal("Scroll should return false with no agents")
	}
}
