package tui

import (
	"encoding/json"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
	"vulpineos/internal/tui/agents"
	"vulpineos/internal/tui/alerts"
	"vulpineos/internal/tui/contexts"
	"vulpineos/internal/tui/dashboard"
	"vulpineos/internal/tui/shared"
	"vulpineos/internal/tui/statusbar"
)

// Panel identifiers.
const (
	PanelDashboard = 0
	PanelContexts  = 1
	PanelAgents    = 2
	PanelAlerts    = 3
	PanelCount     = 4
)

var panelNames = []string{"dashboard", "contexts", "agents", "alerts"}

// App is the root Bubbletea model.
type App struct {
	kernel    *kernel.Kernel
	client    *juggler.Client
	width     int
	height    int
	activePanel int

	dashboard dashboard.Model
	contexts  contexts.Model
	agents    agents.Model
	alerts    alerts.Model
	statusbar statusbar.Model

	eventCh chan tea.Msg
}

// NewApp creates the root TUI model.
func NewApp(k *kernel.Kernel, client *juggler.Client) App {
	eventCh := make(chan tea.Msg, 64)

	app := App{
		kernel:    k,
		client:    client,
		dashboard: dashboard.New(),
		contexts:  contexts.New(),
		agents:    agents.New(),
		alerts:    alerts.New(),
		statusbar: statusbar.New().SetMode("local"),
		eventCh:   eventCh,
	}

	// Subscribe to Juggler events
	if client != nil {
		client.Subscribe("Browser.attachedToTarget", func(params json.RawMessage) {
			var e juggler.AttachedToTarget
			json.Unmarshal(params, &e)
			eventCh <- shared.TargetAttachedMsg{
				SessionID: e.SessionID,
				TargetID:  e.TargetInfo.TargetID,
				ContextID: e.TargetInfo.BrowserContextID,
				URL:       e.TargetInfo.URL,
			}
		})
		client.Subscribe("Browser.detachedFromTarget", func(params json.RawMessage) {
			var e juggler.DetachedFromTarget
			json.Unmarshal(params, &e)
			eventCh <- shared.TargetDetachedMsg{
				SessionID: e.SessionID,
				TargetID:  e.TargetID,
			}
		})
		client.Subscribe("Browser.trustWarmingStateChanged", func(params json.RawMessage) {
			var e juggler.TrustWarmingState
			json.Unmarshal(params, &e)
			eventCh <- shared.TrustWarmMsg{State: e.State, CurrentSite: e.CurrentSite}
		})
		client.Subscribe("Browser.telemetryUpdate", func(params json.RawMessage) {
			var e juggler.TelemetryUpdate
			json.Unmarshal(params, &e)
			eventCh <- shared.TelemetryMsg{
				MemoryMB:           e.MemoryMB,
				CPUPercent:         e.CPUPercent,
				DetectionRiskScore: e.DetectionRiskScore,
				ActiveContexts:     e.ActiveContexts,
				ActivePages:        e.ActivePages,
			}
		})
		client.Subscribe("Browser.injectionAttemptDetected", func(params json.RawMessage) {
			var e juggler.InjectionAttempt
			json.Unmarshal(params, &e)
			eventCh <- shared.AlertMsg{
				Timestamp: time.Now(),
				Type:      e.AttemptType,
				URL:       e.URL,
				Details:   e.Details,
				Blocked:   e.Blocked,
			}
		})
	}

	return app
}

func (a App) Init() tea.Cmd {
	return tea.Batch(
		a.waitForEvent(),
		a.tick(),
	)
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return a, tea.Quit
		case "tab":
			a.activePanel = (a.activePanel + 1) % PanelCount
			a.statusbar = a.statusbar.SetActivePanel(panelNames[a.activePanel])
		case "shift+tab":
			a.activePanel = (a.activePanel - 1 + PanelCount) % PanelCount
			a.statusbar = a.statusbar.SetActivePanel(panelNames[a.activePanel])
		case "j", "down":
			switch a.activePanel {
			case PanelContexts:
				a.contexts = a.contexts.MoveDown()
			case PanelAgents:
				a.agents = a.agents.MoveDown()
			}
		case "k", "up":
			switch a.activePanel {
			case PanelContexts:
				a.contexts = a.contexts.MoveUp()
			case PanelAgents:
				a.agents = a.agents.MoveUp()
			}
		}

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.dashboard = a.dashboard.SetWidth(msg.Width)
		a.contexts = a.contexts.SetWidth(msg.Width)
		a.agents = a.agents.SetWidth(msg.Width)
		a.alerts = a.alerts.SetWidth(msg.Width)
		a.statusbar = a.statusbar.SetWidth(msg.Width)

	case shared.TickMsg:
		if a.kernel != nil {
			a.dashboard = a.dashboard.Update(shared.KernelStatusMsg{
				Running: a.kernel.Running(),
				PID:     a.kernel.PID(),
				Uptime:  a.kernel.Uptime(),
			})
			a.statusbar = a.statusbar.SetConnected(a.kernel.Running())
		}
		cmds = append(cmds, a.tick())

	// Juggler events
	case shared.TargetAttachedMsg:
		a.contexts = a.contexts.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.TargetDetachedMsg:
		a.contexts = a.contexts.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.TelemetryMsg:
		a.dashboard = a.dashboard.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.TrustWarmMsg:
		a.dashboard = a.dashboard.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.AlertMsg:
		a.alerts = a.alerts.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.AgentStatusMsg:
		a.agents = a.agents.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	}

	return a, tea.Batch(cmds...)
}

func (a App) View() string {
	if a.width == 0 {
		return "Loading..."
	}

	// Layout: left column (dashboard) | right column (contexts + agents + alerts)
	leftWidth := 36
	rightWidth := a.width - leftWidth - 5 // borders + padding

	if rightWidth < 30 {
		rightWidth = a.width - 4
		leftWidth = 0
	}

	// Left panel
	dashView := a.renderPanel(PanelDashboard, a.dashboard.View(), leftWidth)

	// Right panels stacked
	ctxView := a.renderPanel(PanelContexts, a.contexts.View(), rightWidth)
	agentView := a.renderPanel(PanelAgents, a.agents.View(), rightWidth)
	alertView := a.renderPanel(PanelAlerts, a.alerts.View(), rightWidth)

	rightColumn := lipgloss.JoinVertical(lipgloss.Left, ctxView, agentView, alertView)

	var body string
	if leftWidth > 0 {
		body = lipgloss.JoinHorizontal(lipgloss.Top, dashView, " ", rightColumn)
	} else {
		body = lipgloss.JoinVertical(lipgloss.Left, dashView, rightColumn)
	}

	// Status bar
	statusView := a.statusbar.View()

	return lipgloss.JoinVertical(lipgloss.Left, body, statusView)
}

func (a App) renderPanel(panel int, content string, width int) string {
	style := shared.PanelStyle
	if panel == a.activePanel {
		style = shared.ActivePanelStyle
	}
	return style.Width(width).Render(content)
}

func (a App) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		return <-a.eventCh
	}
}

func (a App) tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return shared.TickMsg{}
	})
}

// Header renders the VulpineOS header.
func Header() string {
	var b strings.Builder
	b.WriteString(shared.TitleStyle.Render("VulpineOS"))
	b.WriteString(shared.MutedStyle.Render(" — Sovereign Agent Runtime"))
	return b.String()
}
