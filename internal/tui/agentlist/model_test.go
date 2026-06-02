package agentlist

import (
	"strings"
	"testing"
	"time"

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

func TestClickNearestUsesSameVisibleRangeAsView(t *testing.T) {
	m := New()
	m.SetWidth(24)
	m.SetHeight(4)
	agents := make([]vault.Agent, 8)
	for i := range agents {
		agents[i] = vault.Agent{
			ID:     string(rune('a' + i)),
			Name:   "Agent " + string(rune('A'+i)),
			Status: "active",
		}
	}
	m.SetAgents(agents)
	m.SelectIndex(6)

	item, ok := m.ClickNearest(12, 10)
	if !ok {
		t.Fatal("ClickNearest returned no agent")
	}
	if item.Name != "Agent F" {
		t.Fatalf("clicked item = %q, want first rendered row Agent F", item.Name)
	}
}

func TestUpdateLastSelectedAt(t *testing.T) {
	m := New()
	m.SetAgents([]vault.Agent{{ID: "agent-1", Name: "Agent", Status: "created"}})
	selectedAt := time.Now().Truncate(time.Second)

	if !m.UpdateLastSelectedAt("agent-1", selectedAt) {
		t.Fatal("UpdateLastSelectedAt returned false for existing agent")
	}
	item, ok := m.Agent("agent-1")
	if !ok {
		t.Fatal("agent missing after update")
	}
	if !item.LastSelectedAt.Equal(selectedAt) {
		t.Fatalf("LastSelectedAt = %s, want %s", item.LastSelectedAt, selectedAt)
	}
	if m.UpdateLastSelectedAt("missing", selectedAt) {
		t.Fatal("UpdateLastSelectedAt returned true for missing agent")
	}
}
