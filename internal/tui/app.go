package tui

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
	"vulpineos/internal/orchestrator"
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
	orch      *orchestrator.Orchestrator
	width     int
	height    int
	activePanel int

	dashboard dashboard.Model
	contexts  contexts.Model
	agents    agents.Model
	alerts    alerts.Model
	statusbar statusbar.Model

	notice    string // transient status notice
	inputMode string // "" = normal, "task" = entering agent task
	taskInput textinput.Model
	eventCh   chan tea.Msg
}

// NewApp creates the root TUI model.
func NewApp(k *kernel.Kernel, client *juggler.Client, orch *orchestrator.Orchestrator) App {
	eventCh := make(chan tea.Msg, 64)

	ti := textinput.New()
	ti.Placeholder = "Describe what the agent should do..."
	ti.CharLimit = 500
	ti.Width = 60

	app := App{
		kernel:    k,
		client:    client,
		orch:      orch,
		taskInput: ti,
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

	// Forward agent status updates from orchestrator to TUI
	if orch != nil {
		go func() {
			for status := range orch.Agents.StatusChan() {
				eventCh <- shared.AgentStatusMsg{
					AgentID:   status.AgentID,
					ContextID: status.ContextID,
					Status:    status.Status,
					Objective: status.Objective,
					Tokens:    status.Tokens,
				}
			}
		}()
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
		// Handle task input mode
		if a.inputMode == "task" {
			switch msg.String() {
			case "enter":
				task := strings.TrimSpace(a.taskInput.Value())
				if task != "" {
					cmds = append(cmds, a.spawnOpenClawAgent(task))
				}
				a.inputMode = ""
				a.taskInput.Blur()
				a.taskInput.Reset()
			case "esc":
				a.inputMode = ""
				a.taskInput.Blur()
				a.taskInput.Reset()
			default:
				var cmd tea.Cmd
				a.taskInput, cmd = a.taskInput.Update(msg)
				return a, cmd
			}
			return a, tea.Batch(cmds...)
		}

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
			// Enter task input mode to spawn an OpenClaw agent
			if a.orch != nil {
				a.inputMode = "task"
				a.taskInput.Focus()
				return a, textinput.Blink
			} else {
				a.notice = "No orchestrator — launch with a browser"
			}
		case "g":
			// Navigate selected target to Google (quick test)
			if a.client != nil {
				sessionID, _ := a.contexts.SelectedTarget()
				frameID := a.contexts.SelectedFrameID()
				if sessionID != "" && frameID != "" {
					cmds = append(cmds, a.navigateTarget(sessionID, frameID, "https://www.google.com/"))
				} else if sessionID != "" {
					a.notice = "Target has no frame yet — try again"
				} else {
					a.notice = "No target selected"
				}
			}
		case "x":
			// Kill selected agent
			if a.orch != nil {
				agentID, _ := a.agents.SelectedAgent()
				if agentID != "" {
					cmds = append(cmds, a.killAgent(agentID))
				} else {
					a.notice = "No agent selected"
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
		if msg.Status == "completed" || msg.Status == "error" {
			// Keep the agent visible for one more render cycle with final status
			a.agents = a.agents.Update(msg)
		} else {
			a.agents = a.agents.Update(msg)
		}
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

	// Task input bar (when in input mode)
	var inputBar string
	if a.inputMode == "task" {
		inputBar = shared.ActivePanelStyle.Width(a.width - 4).Render(
			shared.TitleStyle.Render("AGENT TASK") + "\n" +
				a.taskInput.View() + "\n" +
				shared.MutedStyle.Render("[Enter] spawn  [Esc] cancel"),
		)
	}

	// Status bar + notice
	statusView := a.statusbar.View()
	if a.notice != "" {
		statusView = shared.WarmingStyle.Render("  "+a.notice) + "\n" + statusView
	}

	parts := []string{body}
	if inputBar != "" {
		parts = append(parts, inputBar)
	}
	parts = append(parts, statusView)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
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

// spawnOpenClawAgent spawns a real OpenClaw agent with the given task.
func (a App) spawnOpenClawAgent(task string) tea.Cmd {
	return func() tea.Msg {
		agentID, err := a.orch.Agents.SpawnOpenClaw(task, nil)
		if err != nil {
			return statusNotice{text: "Agent failed: " + err.Error()}
		}
		return statusNotice{text: "Agent spawned: " + agentID + " — " + task}
	}
}

// killAgent kills an agent by ID.
func (a App) killAgent(agentID string) tea.Cmd {
	return func() tea.Msg {
		if err := a.orch.Agents.Kill(agentID); err != nil {
			return statusNotice{text: "Kill failed: " + err.Error()}
		}
		return statusNotice{text: "Agent killed: " + agentID}
	}
}

// Header renders the VulpineOS header.
func Header() string {
	var b strings.Builder
	b.WriteString(shared.TitleStyle.Render("VulpineOS"))
	b.WriteString(shared.MutedStyle.Render(" — Sovereign Agent Runtime"))
	return b.String()
}
