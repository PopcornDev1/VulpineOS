package agents

import (
	"fmt"
	"strings"

	"vulpineos/internal/tui/shared"
)

// Model is the agents panel showing OpenClaw agent status.
type Model struct {
	width    int
	agents   []agent
	selected int
	totalTokens int
}

type agent struct {
	agentID   string
	contextID string
	status    string
	objective string
	tokens    int
}

func New() Model {
	return Model{selected: 0}
}

func (m Model) Update(msg interface{}) Model {
	switch msg := msg.(type) {
	case shared.AgentStatusMsg:
		found := false
		for i, a := range m.agents {
			if a.agentID == msg.AgentID {
				m.agents[i].status = msg.Status
				m.agents[i].objective = msg.Objective
				m.agents[i].tokens = msg.Tokens
				found = true
				break
			}
		}
		if !found {
			m.agents = append(m.agents, agent{
				agentID:   msg.AgentID,
				contextID: msg.ContextID,
				status:    msg.Status,
				objective: msg.Objective,
				tokens:    msg.Tokens,
			})
		}
		// Recalculate total tokens
		m.totalTokens = 0
		for _, a := range m.agents {
			m.totalTokens += a.tokens
		}
	}
	return m
}

func (m Model) Remove(agentID string) Model {
	for i, a := range m.agents {
		if a.agentID == agentID {
			m.agents = append(m.agents[:i], m.agents[i+1:]...)
			if m.selected >= len(m.agents) && m.selected > 0 {
				m.selected--
			}
			break
		}
	}
	return m
}

func (m Model) SelectedAgent() (agentID, contextID string) {
	if m.selected < len(m.agents) {
		return m.agents[m.selected].agentID, m.agents[m.selected].contextID
	}
	return "", ""
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
	if m.selected < len(m.agents)-1 {
		m.selected++
	}
	return m
}

func (m Model) TotalTokens() int {
	return m.totalTokens
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render("AGENTS"))
	b.WriteString(shared.MutedStyle.Render(fmt.Sprintf(" (%d)", len(m.agents))))
	if m.totalTokens > 0 {
		b.WriteString(shared.MutedStyle.Render(fmt.Sprintf("  Tokens: %dk", m.totalTokens/1000)))
	}
	b.WriteString("\n")

	if len(m.agents) == 0 {
		b.WriteString(shared.MutedStyle.Render("  No active agents"))
		return b.String()
	}

	b.WriteString(shared.HeaderStyle.Render(fmt.Sprintf("  %-10s %-8s %-8s %s", "AGENT", "STATUS", "TOKENS", "OBJECTIVE")))
	b.WriteString("\n")

	for i, a := range m.agents {
		statusStyle := shared.MutedStyle
		switch a.status {
		case "running":
			statusStyle = shared.RunningStyle
		case "thinking":
			statusStyle = shared.WarmingStyle
		case "error":
			statusStyle = shared.StoppedStyle
		}

		obj := a.objective
		if len(obj) > 35 {
			obj = obj[:32] + "..."
		}

		line := fmt.Sprintf("  %-10s %s %-8s %s",
			truncateStr(a.agentID, 10),
			statusStyle.Render(fmt.Sprintf("%-8s", a.status)),
			fmt.Sprintf("%dk", a.tokens/1000),
			obj,
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

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
