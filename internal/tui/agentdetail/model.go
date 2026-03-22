package agentdetail

import (
	"fmt"
	"strings"
	"time"

	"vulpineos/internal/tui/shared"
)

// Model shows the selected agent's details and controls in the top-center area.
type Model struct {
	agentID        string
	agentName      string
	agentTask      string
	agentStatus    string
	tokens         int
	fingerprint    string // summary line
	proxyInfo      string // summary line
	browserContext string // active browser context info
	createdAt      time.Time
	width          int
	height         int
}

// New creates a new agent detail panel.
func New() Model {
	return Model{
		width:  40,
		height: 8,
	}
}

// SetAgent loads agent data into the detail panel.
func (m *Model) SetAgent(id, name, task, status string, tokens int, fpSummary, proxyInfo string, createdAt time.Time) {
	m.agentID = id
	m.agentName = name
	m.agentTask = task
	m.agentStatus = status
	m.tokens = tokens
	m.fingerprint = fpSummary
	m.proxyInfo = proxyInfo
	m.createdAt = createdAt
}

// SetBrowserContext sets the active browser context info for the agent.
func (m *Model) SetBrowserContext(info string) {
	m.browserContext = info
}

// Clear resets the panel to no agent selected.
func (m *Model) Clear() {
	m.agentID = ""
	m.agentName = ""
	m.agentTask = ""
	m.agentStatus = ""
	m.tokens = 0
	m.fingerprint = ""
	m.proxyInfo = ""
	m.browserContext = ""
}

// SetSize sets the render dimensions.
func (m *Model) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// HasAgent returns whether an agent is loaded.
func (m Model) HasAgent() bool {
	return m.agentID != ""
}

// statusIndicator returns a styled status with icon.
func statusIndicator(status string) string {
	switch status {
	case "active", "running":
		return shared.WarmingStyle.Render("● working...")
	case "thinking":
		return shared.WarmingStyle.Render("◌ thinking...")
	case "completed", "ready", "":
		return shared.RunningStyle.Render("● ready")
	case "failed", "error":
		return shared.StoppedStyle.Render("● error")
	case "starting", "created":
		return shared.WarmingStyle.Render("○ starting...")
	default:
		return shared.RunningStyle.Render("● ready")
	}
}

// formatAge formats a duration since creation as a human-readable string.
func formatAge(t time.Time) string {
	if t.IsZero() {
		return "just now"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// formatTokens formats a token count with commas.
func formatTokens(n int) string {
	if n == 0 {
		return "0"
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// View renders the agent detail panel.
func (m Model) View() string {
	var b strings.Builder

	if m.agentID == "" {
		b.WriteString("\n")
		b.WriteString(shared.MutedStyle.Render("  Press "))
		b.WriteString(shared.KeyStyle.Render("n"))
		b.WriteString(shared.MutedStyle.Render(" to create a new agent"))
		return b.String()
	}

	// Line 1: Agent name
	b.WriteString(shared.TitleStyle.Render("AGENT: "))
	b.WriteString(shared.HeaderStyle.Render(m.agentName))
	b.WriteString("\n")

	// Line 2: Status | Tokens | Created
	b.WriteString("Status: ")
	b.WriteString(statusIndicator(m.agentStatus))
	b.WriteString(shared.MutedStyle.Render(" | "))
	b.WriteString("Tokens: ")
	b.WriteString(formatTokens(m.tokens))
	b.WriteString(shared.MutedStyle.Render(" | "))
	b.WriteString("Created: ")
	b.WriteString(formatAge(m.createdAt))
	b.WriteString("\n")

	// Line 3: Task
	task := m.agentTask
	maxTask := m.width - 8
	if maxTask < 10 {
		maxTask = 10
	}
	if len(task) > maxTask {
		task = task[:maxTask-1] + "..."
	}
	b.WriteString("Task: ")
	b.WriteString(task)
	b.WriteString("\n")

	// Line 4: Fingerprint profile
	if m.fingerprint != "" {
		b.WriteString("Profile: ")
		b.WriteString(m.fingerprint)
		b.WriteString("\n")
	}

	// Line 5: Proxy info
	if m.proxyInfo != "" {
		b.WriteString("Proxy: ")
		b.WriteString(m.proxyInfo)
		b.WriteString("\n")
	}

	// Line 6: Browser context
	if m.browserContext != "" {
		b.WriteString("Browser: ")
		b.WriteString(shared.RunningStyle.Render(m.browserContext))
		b.WriteString("\n")
	} else if m.agentStatus == "active" || m.agentStatus == "thinking" {
		b.WriteString("Browser: ")
		b.WriteString(shared.MutedStyle.Render("no context"))
		b.WriteString("\n")
	}

	// Controls
	b.WriteString("\n")
	b.WriteString(shared.MutedStyle.Render("[Enter] chat  [x] delete"))

	// Truncate to allocated height so the panel never overflows
	result := b.String()
	if m.height > 0 {
		lines := strings.Split(result, "\n")
		if len(lines) > m.height {
			lines = lines[:m.height]
			result = strings.Join(lines, "\n")
		}
	}
	return result
}
