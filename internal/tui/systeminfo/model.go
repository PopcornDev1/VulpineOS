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
	headless       bool
	browserRoute   string
	browserWindow  string
	memoryMB       float64
	eventLoopLag   float64
	runtimeRisk    float64
	activeContexts int
	activePages    int
	poolAvailable  int
	poolActive     int
	poolTotal      int
	runtimeEvents  []sharedRuntimeEvent
	width          int
	height         int
}

type sharedRuntimeEvent struct {
	component string
	event     string
	level     string
	at        time.Time
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
		m.headless = msg.Headless
		m.browserRoute = msg.BrowserRoute
		m.browserWindow = msg.BrowserWindow
	case shared.TelemetryMsg:
		m.memoryMB = msg.MemoryMB
		m.eventLoopLag = msg.EventLoopLagMs
		m.runtimeRisk = msg.RuntimeRiskScore
		m.activeContexts = msg.ActiveContexts
		m.activePages = msg.ActivePages
	case shared.PoolStatsMsg:
		m.poolAvailable = msg.Available
		m.poolActive = msg.Active
		m.poolTotal = msg.Total
	case shared.RuntimeEventMsg:
		m.runtimeEvents = append([]sharedRuntimeEvent{{
			component: msg.Event.Component,
			event:     msg.Event.Event,
			level:     msg.Event.Level,
			at:        msg.Event.Timestamp,
		}}, m.runtimeEvents...)
		if len(m.runtimeEvents) > 3 {
			m.runtimeEvents = m.runtimeEvents[:3]
		}
	}
	return m, nil
}

// SetRuntimeEvents seeds the recent runtime event list.
func (m *Model) SetRuntimeEvents(events []shared.RuntimeEventMsg) {
	m.runtimeEvents = m.runtimeEvents[:0]
	for _, event := range events {
		m.runtimeEvents = append(m.runtimeEvents, sharedRuntimeEvent{
			component: event.Event.Component,
			event:     event.Event.Event,
			level:     event.Event.Level,
			at:        event.Event.Timestamp,
		})
	}
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
	b.WriteString(shared.MutedStyle.Render(fmt.Sprintf("Pool: %d/%d/%d", m.poolAvailable, m.poolActive, m.poolTotal)))
	b.WriteString("\n")
	b.WriteString(shared.MutedStyle.Render(fmt.Sprintf("Ctx: %d Pg: %d", m.activeContexts, m.activePages)))
	b.WriteString("\n")

	if len(m.runtimeEvents) > 0 {
		b.WriteString("\n\n")
		b.WriteString(shared.MutedStyle.Render("Runtime"))
		for _, event := range m.runtimeEvents {
			b.WriteString("\n")
			b.WriteString(shared.MutedStyle.Render(formatRuntimeEvent(event)))
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("MEM %s\n", meterBar(m.memoryMB, 1024, fmt.Sprintf("%.0f", m.memoryMB))))
	b.WriteString(fmt.Sprintf("LAG %s\n", meterBar(m.eventLoopLag, 100, fmt.Sprintf("%.0fms", m.eventLoopLag))))
	b.WriteString(fmt.Sprintf("RISK %s", meterBar(m.runtimeRisk, 100, fmt.Sprintf("%.0f", m.runtimeRisk))))

	// Truncate to allocated height so the panel never overflows
	result := b.String()
	if m.width > 0 {
		lines := strings.Split(result, "\n")
		for i, line := range lines {
			lines[i] = fitLine(line, m.width)
		}
		result = strings.Join(lines, "\n")
	}
	if m.height > 0 {
		lines := strings.Split(result, "\n")
		if len(lines) > m.height {
			lines = lines[:m.height]
			result = strings.Join(lines, "\n")
		}
	}
	return result
}

func fitLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	fitted := lipgloss.NewStyle().MaxWidth(width).Render(line)
	lines := strings.Split(fitted, "\n")
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func formatRuntimeEvent(event sharedRuntimeEvent) string {
	component := strings.ToUpper(event.component)
	if len(component) > 4 {
		component = component[:4]
	}
	return fmt.Sprintf("%-4s %s %s", component, event.event, event.at.Format("15:04"))
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
