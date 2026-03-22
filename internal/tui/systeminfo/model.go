package systeminfo

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/tui/shared"
)

// Model holds compact system metrics for the left sidebar.
type Model struct {
	running        bool
	pid            int
	uptime         time.Duration
	memoryMB       float64
	eventLoopLag   float64
	detectionRisk  float64
	activeContexts int
	activePages    int
	poolAvailable  int
	poolActive     int
	poolTotal      int
	width          int
	height         int
}

// New creates a new system info panel.
func New() Model {
	return Model{
		width: 14,
	}
}

// SetWidth sets the render width.
func (m *Model) SetWidth(w int) {
	m.width = w
}

// SetHeight sets the render height.
func (m *Model) SetHeight(h int) {
	m.height = h
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case shared.KernelStatusMsg:
		m.running = msg.Running
		m.pid = msg.PID
		m.uptime = msg.Uptime
	case shared.TelemetryMsg:
		m.memoryMB = msg.MemoryMB
		m.eventLoopLag = msg.EventLoopLagMs
		m.detectionRisk = msg.DetectionRiskScore
		m.activeContexts = msg.ActiveContexts
		m.activePages = msg.ActivePages
	case shared.PoolStatsMsg:
		m.poolAvailable = msg.Available
		m.poolActive = msg.Active
		m.poolTotal = msg.Total
	}
	return m, nil
}

// meterBar renders a compact bar like "██░ 312" within roughly 10 chars.
func meterBar(value, max float64, label string) string {
	const barWidth = 3
	filled := int((value / max) * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	var color lipgloss.Color
	ratio := value / max
	switch {
	case ratio > 0.8:
		color = shared.ColorDanger
	case ratio > 0.5:
		color = shared.ColorWarning
	default:
		color = shared.ColorSuccess
	}

	barStyled := lipgloss.NewStyle().Foreground(color).Render(bar)
	return fmt.Sprintf("%s %s", barStyled, label)
}

// SetPoolStats directly updates pool statistics.
func (m *Model) SetPoolStats(available, active, total int) {
	m.poolAvailable = available
	m.poolActive = active
	m.poolTotal = total
}

// View renders the system info panel.
func (m Model) View() string {
	var b strings.Builder

	title := shared.TitleStyle.Render("SYSTEM")
	b.WriteString(title)
	b.WriteString("\n")

	if m.running {
		b.WriteString(shared.RunningStyle.Render("● RUNNING"))
	} else {
		b.WriteString(shared.StoppedStyle.Render("● STOPPED"))
	}
	b.WriteString("\n")

	b.WriteString(shared.MutedStyle.Render(fmt.Sprintf("PID %d", m.pid)))
	b.WriteString("\n")

	upStr := formatDuration(m.uptime)
	b.WriteString(shared.MutedStyle.Render(fmt.Sprintf("Up %s", upStr)))
	b.WriteString("\n")
	b.WriteString("\n")

	b.WriteString(fmt.Sprintf("MEM %s\n", meterBar(m.memoryMB, 1024, fmt.Sprintf("%.0f", m.memoryMB))))
	b.WriteString(fmt.Sprintf("LAG %s\n", meterBar(m.eventLoopLag, 100, fmt.Sprintf("%.0fms", m.eventLoopLag))))
	b.WriteString(fmt.Sprintf("RISK %s\n", meterBar(m.detectionRisk, 100, fmt.Sprintf("%.0f", m.detectionRisk))))
	b.WriteString("\n")

	b.WriteString(shared.MutedStyle.Render(fmt.Sprintf("Pool: %d/%d/%d", m.poolAvailable, m.poolActive, m.poolTotal)))
	b.WriteString("\n")
	b.WriteString(shared.MutedStyle.Render(fmt.Sprintf("Ctx: %d Pg: %d", m.activeContexts, m.activePages)))

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

// formatDuration formats a duration compactly (e.g., "12m", "1h3m").
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}
