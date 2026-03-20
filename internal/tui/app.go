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

// statusNotice is a transient message shown in the status bar.
type statusNotice struct {
	text string
}

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

	notice  string // transient status notice
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
		client.Subscribe("Browser.attachedToTarget", func(sid string, params json.RawMessage) {
			var e juggler.AttachedToTarget
			json.Unmarshal(params, &e)
			eventCh <- shared.TargetAttachedMsg{
				SessionID: e.SessionID,
				TargetID:  e.TargetInfo.TargetID,
				ContextID: e.TargetInfo.BrowserContextID,
				URL:       e.TargetInfo.URL,
			}
		})
		client.Subscribe("Browser.detachedFromTarget", func(sid string, params json.RawMessage) {
			var e juggler.DetachedFromTarget
			json.Unmarshal(params, &e)
			eventCh <- shared.TargetDetachedMsg{
				SessionID: e.SessionID,
				TargetID:  e.TargetID,
			}
		})
		client.Subscribe("Browser.trustWarmingStateChanged", func(sid string, params json.RawMessage) {
			var e juggler.TrustWarmingState
			json.Unmarshal(params, &e)
			eventCh <- shared.TrustWarmMsg{State: e.State, CurrentSite: e.CurrentSite}
		})
		client.Subscribe("Browser.telemetryUpdate", func(sid string, params json.RawMessage) {
			var e juggler.TelemetryUpdate
			json.Unmarshal(params, &e)
			eventCh <- shared.TelemetryMsg{
				MemoryMB:           e.MemoryMB,
				EventLoopLagMs:     e.EventLoopLagMs,
				DetectionRiskScore: e.DetectionRiskScore,
				ActiveContexts:     e.ActiveContexts,
				ActivePages:        e.ActivePages,
			}
		})
		client.Subscribe("Browser.injectionAttemptDetected", func(sid string, params json.RawMessage) {
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
		// Page-session events — sessionId identifies which target they belong to
		client.Subscribe("Page.navigationCommitted", func(sid string, params json.RawMessage) {
			var e struct {
				FrameID string `json:"frameId"`
				URL     string `json:"url"`
			}
			json.Unmarshal(params, &e)
			eventCh <- shared.NavigationMsg{
				SessionID: sid,
				FrameID:   e.FrameID,
				URL:       e.URL,
			}
		})
		client.Subscribe("Page.eventFired", func(sid string, params json.RawMessage) {
			var e struct {
				FrameID string `json:"frameId"`
				Name    string `json:"name"`
			}
			json.Unmarshal(params, &e)
			eventCh <- shared.PageLoadMsg{
				SessionID: sid,
				FrameID:   e.FrameID,
				Name:      e.Name,
			}
		})
		client.Subscribe("Page.frameAttached", func(sid string, params json.RawMessage) {
			var e struct {
				FrameID       string `json:"frameId"`
				ParentFrameID string `json:"parentFrameId"`
			}
			json.Unmarshal(params, &e)
			eventCh <- shared.FrameAttachedMsg{
				SessionID:     sid,
				FrameID:       e.FrameID,
				ParentFrameID: e.ParentFrameID,
			}
		})
		client.Subscribe("Runtime.executionContextCreated", func(sid string, params json.RawMessage) {
			var e struct {
				ExecutionContextID string `json:"executionContextId"`
				AuxData            struct {
					FrameID string `json:"frameId"`
				} `json:"auxData"`
			}
			json.Unmarshal(params, &e)
			eventCh <- shared.ExecContextCreatedMsg{
				SessionID:          sid,
				ExecutionContextID: e.ExecutionContextID,
				FrameID:            e.AuxData.FrameID,
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
		case "s":
			// Spawn a new browser context + page
			if a.client != nil {
				cmds = append(cmds, a.spawnContext())
			} else {
				a.notice = "No kernel connected"
			}
		case "d":
			// Destroy selected target
			if a.client != nil {
				sessionID, _ := a.contexts.SelectedTarget()
				if sessionID != "" {
					cmds = append(cmds, a.destroyTarget(sessionID))
				} else {
					a.notice = "No target selected"
				}
			}
		case "a":
			// Navigate selected context to a page (agent placeholder)
			if a.client != nil {
				sessionID, _ := a.contexts.SelectedTarget()
				frameID := a.contexts.SelectedFrameID()
				if sessionID != "" && frameID != "" {
					cmds = append(cmds, a.navigateTarget(sessionID, frameID, "https://www.google.com/"))
				} else if sessionID != "" {
					a.notice = "Target has no frame yet — try again in a moment"
				} else {
					a.notice = "No target selected — press 's' to spawn one first"
				}
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
		a.notice = "" // clear transient notice
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
	case shared.NavigationMsg:
		a.contexts = a.contexts.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.PageLoadMsg:
		a.contexts = a.contexts.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.FrameAttachedMsg:
		a.contexts = a.contexts.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.ExecContextCreatedMsg:
		a.contexts = a.contexts.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.AgentStatusMsg:
		a.agents = a.agents.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case statusNotice:
		a.notice = msg.text
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

	// Status bar + notice
	statusView := a.statusbar.View()
	if a.notice != "" {
		statusView = shared.WarmingStyle.Render("  "+a.notice) + "\n" + statusView
	}

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

// spawnContext creates a new browser context and opens a page in it.
func (a App) spawnContext() tea.Cmd {
	return func() tea.Msg {
		// Create a browser context
		result, err := a.client.Call("", "Browser.createBrowserContext", map[string]interface{}{
			"removeOnDetach": true,
		})
		if err != nil {
			return statusNotice{text: "Spawn failed: " + err.Error()}
		}

		var ctx struct {
			BrowserContextID string `json:"browserContextId"`
		}
		if err := json.Unmarshal(result, &ctx); err != nil {
			return statusNotice{text: "Parse error: " + err.Error()}
		}

		// Open a new page in this context
		_, err = a.client.Call("", "Browser.newPage", map[string]interface{}{
			"browserContextId": ctx.BrowserContextID,
		})
		if err != nil {
			return statusNotice{text: "New page failed: " + err.Error()}
		}

		return statusNotice{text: "Context spawned: " + ctx.BrowserContextID[:8]}
	}
}

// destroyTarget closes a page target by session ID.
func (a App) destroyTarget(sessionID string) tea.Cmd {
	return func() tea.Msg {
		_, err := a.client.Call(sessionID, "Page.close", nil)
		if err != nil {
			return statusNotice{text: "Close failed: " + err.Error()}
		}
		return statusNotice{text: "Target closed"}
	}
}

// navigateTarget navigates a page target to a URL using Page.navigate with the correct frameId.
func (a App) navigateTarget(sessionID, frameID, url string) tea.Cmd {
	return func() tea.Msg {
		_, err := a.client.Call(sessionID, "Page.navigate", map[string]interface{}{
			"url":     url,
			"frameId": frameID,
		})
		if err != nil {
			return statusNotice{text: "Navigate failed: " + err.Error()}
		}
		return statusNotice{text: "Navigating to " + url}
	}
}

// Header renders the VulpineOS header.
func Header() string {
	var b strings.Builder
	b.WriteString(shared.TitleStyle.Render("VulpineOS"))
	b.WriteString(shared.MutedStyle.Render(" — Sovereign Agent Runtime"))
	return b.String()
}
