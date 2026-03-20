package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/tui/shared"
)

// Model is the dashboard panel showing kernel status and resource meters.
type Model struct {
	width          int
	running        bool
	pid            int
	uptime         time.Duration
	memoryMB       float64
	eventLoopLag   float64
	detectionRisk  float64
	activeContexts int
	activePages    int
	trustState     string
	trustSite      string
}

func New() Model {
	return Model{}
}

func (m Model) Update(msg interface{}) Model {
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
	case shared.TrustWarmMsg:
		m.trustState = msg.State
		m.trustSite = msg.CurrentSite
	}
	return m
}

func (m Model) SetWidth(w int) Model {
	m.width = w
	return m
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString(shared.TitleStyle.Render("KERNEL"))
	b.WriteString("\n")

	// Status line
	if m.running {
		b.WriteString(shared.RunningStyle.Render("● RUNNING"))
		b.WriteString(shared.MutedStyle.Render(fmt.Sprintf("  PID %d  Up %s", m.pid, formatDuration(m.uptime))))
	} else {
		b.WriteString(shared.StoppedStyle.Render("● STOPPED"))
	}
	b.WriteString("\n\n")

	// Resource meters
	b.WriteString(renderMeter("MEM", m.memoryMB, 2048, shared.ColorSecondary))
	b.WriteString("\n")
	b.WriteString(renderMeter("LAG", m.eventLoopLag, 50, shared.ColorSecondary))
	b.WriteString("\n\n")

	// Detection risk
	riskColor := shared.ColorSuccess
	if m.detectionRisk > 60 {
		riskColor = shared.ColorDanger
	} else if m.detectionRisk > 30 {
		riskColor = shared.ColorWarning
	}
	b.WriteString(renderMeter("RISK", m.detectionRisk, 100, riskColor))
	b.WriteString("\n\n")

	// Counts
	b.WriteString(shared.MutedStyle.Render("Contexts: "))
	b.WriteString(fmt.Sprintf("%d", m.activeContexts))
	b.WriteString(shared.MutedStyle.Render("  Pages: "))
	b.WriteString(fmt.Sprintf("%d", m.activePages))
	b.WriteString("\n")

	// Trust warming
	if m.trustState != "" && m.trustState != "stopped" {
		b.WriteString(shared.WarmingStyle.Render(fmt.Sprintf("Trust: %s", m.trustState)))
		if m.trustSite != "" {
			b.WriteString(shared.MutedStyle.Render(fmt.Sprintf(" → %s", m.trustSite)))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func renderMeter(label string, value, max float64, color lipgloss.Color) string {
	barWidth := 20
	filled := int(value / max * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}

	bar := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled))
	empty := shared.MutedStyle.Render(strings.Repeat("░", barWidth-filled))

	return fmt.Sprintf("%-4s %s%s %s",
		shared.MutedStyle.Render(label),
		bar, empty,
		shared.MutedStyle.Render(fmt.Sprintf("%.0f", value)),
	)
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
