package tui

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/config"
	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
	"vulpineos/internal/orchestrator"
	"vulpineos/internal/tui/agentlist"
	"vulpineos/internal/tui/contextlist"
	"vulpineos/internal/tui/conversation"
	"vulpineos/internal/tui/poolstats"
	"vulpineos/internal/tui/shared"
	"vulpineos/internal/tui/systeminfo"
	"vulpineos/internal/vault"
)

// Focus panel identifiers.
const (
	FocusAgentList    = 0
	FocusConversation = 1
	FocusContextList  = 2
	FocusPanelCount   = 3
)

// statusNotice is a transient message shown in the status bar.
type statusNotice struct {
	text string
}

// App is the root Bubbletea model for the 3-column agent workbench.
type App struct {
	kernel *kernel.Kernel
	client *juggler.Client
	orch   *orchestrator.Orchestrator
	vault  *vault.DB
	cfg    *config.Config

	width, height  int
	leftWidth      int // adjustable left sidebar width
	rightWidth     int // adjustable right sidebar width
	focus          int // 0=agentlist, 1=conversation, 2=contextlist

	// Panels
	systemInfo   systeminfo.Model
	agentList    agentlist.Model
	conversation conversation.Model
	contextList  contextlist.Model
	poolStats    poolstats.Model

	// State
	selectedAgentID string
	inputMode       string // "" | "new-agent-name" | "new-agent-task" | "chat"
	newAgentName    string // temp storage during agent creation
	notice          string

	// Text inputs
	nameInput textinput.Model
	taskInput textinput.Model

	eventCh chan tea.Msg
}

// NewApp creates the root TUI model.
func NewApp(k *kernel.Kernel, client *juggler.Client, orch *orchestrator.Orchestrator, v *vault.DB, cfg *config.Config) App {
	eventCh := make(chan tea.Msg, 64)

	nameIn := textinput.New()
	nameIn.Placeholder = "Agent name..."
	nameIn.CharLimit = 64
	nameIn.Width = 40

	taskIn := textinput.New()
	taskIn.Placeholder = "Describe what the agent should do..."
	taskIn.CharLimit = 500
	taskIn.Width = 60

	app := App{
		kernel:       k,
		client:       client,
		orch:         orch,
		vault:        v,
		cfg:          cfg,
		leftWidth:    18,
		rightWidth:   18,
		nameInput:    nameIn,
		taskInput:    taskIn,
		systemInfo:   systeminfo.New(),
		agentList:    agentlist.New(),
		conversation: conversation.New(),
		contextList:  contextlist.New(),
		poolStats:    poolstats.New(),
		eventCh:      eventCh,
	}

	// Load existing agents from vault
	if v != nil {
		agents, err := v.ListAgents()
		if err != nil {
			log.Printf("tui: failed to load agents from vault: %v", err)
		} else {
			app.agentList.SetAgents(agents)
			// Select first agent if any
			if len(agents) > 0 {
				app.selectedAgentID = agents[0].ID
				app.conversation.SetAgentID(agents[0].ID)
				msgs, err := v.GetMessages(agents[0].ID)
				if err == nil {
					app.conversation.LoadMessages(msgs)
				}
			}
		}
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

		// Forward conversation messages from orchestrator
		go func() {
			for msg := range orch.Agents.ConversationChan() {
				eventCh <- shared.ConversationEntryMsg{
					AgentID:   msg.AgentID,
					Role:      msg.Role,
					Content:   msg.Content,
					Tokens:    msg.Tokens,
					Timestamp: time.Now(),
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
		// Handle input modes first
		switch a.inputMode {
		case "new-agent-name":
			return a.updateNameInput(msg)
		case "new-agent-task":
			return a.updateTaskInput(msg)
		case "chat":
			return a.updateChatInput(msg)
		}

		// Normal keybinds
		switch msg.String() {
		case "q", "ctrl+c":
			return a, tea.Quit
		case "tab":
			a.focus = (a.focus + 1) % FocusPanelCount
		case "j", "down":
			switch a.focus {
			case FocusAgentList:
				a.agentList.MoveDown()
				cmds = append(cmds, a.selectCurrentAgent())
			case FocusContextList:
				a.contextList.MoveDown()
			}
		case "k", "up":
			switch a.focus {
			case FocusAgentList:
				a.agentList.MoveUp()
				cmds = append(cmds, a.selectCurrentAgent())
			case FocusContextList:
				a.contextList.MoveUp()
			}
		case "n":
			if a.orch != nil {
				a.inputMode = "new-agent-name"
				a.nameInput.Focus()
				return a, textinput.Blink
			}
			a.notice = "No orchestrator available"
		case "p":
			// Pause selected agent
			if a.orch != nil && a.selectedAgentID != "" {
				cmds = append(cmds, a.pauseAgent(a.selectedAgentID))
			}
		case "r":
			// Resume selected agent
			if a.orch != nil && a.selectedAgentID != "" {
				cmds = append(cmds, a.resumeAgent(a.selectedAgentID))
			}
		case "x":
			// Delete selected agent
			if a.selectedAgentID != "" {
				cmds = append(cmds, a.deleteAgent(a.selectedAgentID))
			}
		case "enter":
			switch a.focus {
			case FocusAgentList:
				// Focus conversation input
				if a.selectedAgentID != "" {
					a.focus = FocusConversation
					a.inputMode = "chat"
					cmd := a.conversation.Focus()
					return a, cmd
				}
			}
		case "esc":
			a.conversation.Blur()
			a.inputMode = ""
			a.focus = FocusAgentList
		case "[":
			if a.leftWidth > 12 {
				a.leftWidth -= 2
				a.updatePanelSizes()
			}
		case "]":
			if a.leftWidth < 30 {
				a.leftWidth += 2
				a.updatePanelSizes()
			}
		case "{":
			if a.rightWidth > 12 {
				a.rightWidth -= 2
				a.updatePanelSizes()
			}
		case "}":
			if a.rightWidth < 30 {
				a.rightWidth += 2
				a.updatePanelSizes()
			}
		case "c":
			// Quit TUI to re-run setup
			return a, tea.Quit
		}

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.updatePanelSizes()

	case shared.TickMsg:
		a.notice = "" // clear transient notice
		if a.kernel != nil {
			ksMsg := shared.KernelStatusMsg{
				Running: a.kernel.Running(),
				PID:     a.kernel.PID(),
				Uptime:  a.kernel.Uptime(),
			}
			a.systemInfo, _ = a.systemInfo.Update(ksMsg)
		}
		if a.orch != nil {
			avail, active, total := a.orch.Pool.Stats()
			a.poolStats.SetStats(avail, active, total)
		}
		cmds = append(cmds, a.tick())

	// Juggler events
	case shared.TargetAttachedMsg:
		a.contextList, _ = a.contextList.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.TargetDetachedMsg:
		a.contextList, _ = a.contextList.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.NavigationMsg:
		a.contextList, _ = a.contextList.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.FrameAttachedMsg:
		a.contextList, _ = a.contextList.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.ExecContextCreatedMsg:
		cmds = append(cmds, a.waitForEvent())
	case shared.PageLoadMsg:
		cmds = append(cmds, a.waitForEvent())
	case shared.TelemetryMsg:
		a.systemInfo, _ = a.systemInfo.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.TrustWarmMsg:
		cmds = append(cmds, a.waitForEvent())
	case shared.AlertMsg:
		cmds = append(cmds, a.waitForEvent())

	case shared.AgentStatusMsg:
		a.agentList, _ = a.agentList.Update(msg)
		// Update vault status
		if a.vault != nil {
			a.vault.UpdateAgentStatus(msg.AgentID, msg.Status)
			if msg.Tokens > 0 {
				a.vault.UpdateAgentTokens(msg.AgentID, msg.Tokens)
			}
		}
		cmds = append(cmds, a.waitForEvent())

	case shared.ConversationEntryMsg:
		// Save to vault always
		if a.vault != nil {
			a.vault.AppendMessage(msg.AgentID, msg.Role, msg.Content, msg.Tokens)
		}
		// If matches selected agent, add to conversation panel
		if msg.AgentID == a.selectedAgentID {
			a.conversation.AddEntry(msg.Role, msg.Content)
		}
		cmds = append(cmds, a.waitForEvent())

	case shared.PoolStatsMsg:
		a.poolStats, _ = a.poolStats.Update(msg)
		cmds = append(cmds, a.waitForEvent())

	case shared.AgentCreatedMsg:
		a.agentList, _ = a.agentList.Update(msg)
		cmds = append(cmds, a.waitForEvent())

	case statusNotice:
		a.notice = msg.text
	}

	return a, tea.Batch(cmds...)
}

// updateNameInput handles keystrokes in "new-agent-name" mode.
func (a App) updateNameInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(a.nameInput.Value())
		if name != "" {
			a.newAgentName = name
			a.inputMode = "new-agent-task"
			a.nameInput.Blur()
			a.nameInput.Reset()
			a.taskInput.Focus()
			return a, textinput.Blink
		}
		return a, nil
	case "esc":
		a.inputMode = ""
		a.newAgentName = ""
		a.nameInput.Blur()
		a.nameInput.Reset()
		return a, nil
	default:
		var cmd tea.Cmd
		a.nameInput, cmd = a.nameInput.Update(msg)
		return a, cmd
	}
}

// updateTaskInput handles keystrokes in "new-agent-task" mode.
func (a App) updateTaskInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		task := strings.TrimSpace(a.taskInput.Value())
		if task != "" {
			cmd := a.createAndSpawnAgent(a.newAgentName, task)
			a.inputMode = ""
			a.newAgentName = ""
			a.taskInput.Blur()
			a.taskInput.Reset()
			return a, cmd
		}
		return a, nil
	case "esc":
		a.inputMode = ""
		a.newAgentName = ""
		a.taskInput.Blur()
		a.taskInput.Reset()
		return a, nil
	default:
		var cmd tea.Cmd
		a.taskInput, cmd = a.taskInput.Update(msg)
		return a, cmd
	}
}

// updateChatInput handles keystrokes in "chat" mode.
func (a App) updateChatInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		text := a.conversation.InputValue()
		if text != "" && a.selectedAgentID != "" {
			// Add to conversation view
			a.conversation.AddEntry("user", text)
			// Save to vault
			if a.vault != nil {
				a.vault.AppendMessage(a.selectedAgentID, "user", text, 0)
			}
			// Send to agent stdin
			cmd := a.sendMessageToAgent(a.selectedAgentID, text)
			return a, cmd
		}
		return a, nil
	case "esc":
		a.conversation.Blur()
		a.inputMode = ""
		a.focus = FocusAgentList
		return a, nil
	default:
		ti := a.conversation.TextInput()
		var cmd tea.Cmd
		*ti, cmd = ti.Update(msg)
		return a, cmd
	}
}

func (a App) View() string {
	if a.width == 0 {
		return "Loading..."
	}

	leftWidth := a.leftWidth
	rightWidth := a.rightWidth
	centerWidth := a.width - leftWidth - rightWidth - 6 // borders + spacing
	if centerWidth < 20 {
		centerWidth = 20
	}

	bodyHeight := a.height - 2 // status bar

	// Left column: systemInfo on top, agentList below
	sysView := a.renderPanel(FocusAgentList, a.systemInfo.View(), leftWidth, bodyHeight/2)
	agentView := a.renderFocusPanel(FocusAgentList, a.agentList.View(), leftWidth, bodyHeight-bodyHeight/2)
	leftColumn := lipgloss.JoinVertical(lipgloss.Left, sysView, agentView)

	// Center column: conversation (+ input area if in input mode)
	var centerContent string
	switch a.inputMode {
	case "new-agent-name":
		centerContent = a.conversation.View() + "\n\n" +
			shared.TitleStyle.Render("NEW AGENT — NAME") + "\n" +
			a.nameInput.View() + "\n" +
			shared.MutedStyle.Render("[Enter] confirm  [Esc] cancel")
	case "new-agent-task":
		centerContent = a.conversation.View() + "\n\n" +
			shared.TitleStyle.Render("NEW AGENT — TASK for "+a.newAgentName) + "\n" +
			a.taskInput.View() + "\n" +
			shared.MutedStyle.Render("[Enter] spawn  [Esc] cancel")
	default:
		centerContent = a.conversation.View()
	}
	centerView := a.renderFocusPanel(FocusConversation, centerContent, centerWidth, bodyHeight)

	// Right column: contextList on top, poolStats below
	ctxView := a.renderFocusPanel(FocusContextList, a.contextList.View(), rightWidth, bodyHeight-5)
	poolView := a.renderPanel(FocusContextList, a.poolStats.View(), rightWidth, 5)
	rightColumn := lipgloss.JoinVertical(lipgloss.Left, ctxView, poolView)

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftColumn, centerView, rightColumn)

	// Status bar
	statusBar := a.renderStatusBar()

	// Notice
	if a.notice != "" {
		statusBar = shared.WarmingStyle.Render("  "+a.notice) + "\n" + statusBar
	}

	return lipgloss.JoinVertical(lipgloss.Left, body, statusBar)
}

// renderPanel renders content in a panel box without focus highlight.
func (a App) renderPanel(_ int, content string, width, height int) string {
	return shared.PanelStyle.
		Width(width).
		Height(height).
		Render(content)
}

// renderFocusPanel renders content in a panel box, highlighted if focused.
func (a App) renderFocusPanel(panel int, content string, width, height int) string {
	style := shared.PanelStyle
	if panel == a.focus {
		style = shared.ActivePanelStyle
	}
	return style.
		Width(width).
		Height(height).
		Render(content)
}

// renderStatusBar renders the bottom status bar.
func (a App) renderStatusBar() string {
	mode := "local"
	if a.client != nil && a.kernel == nil {
		mode = "remote"
	}

	bar := shared.TitleStyle.Render("VULPINE") +
		shared.MutedStyle.Render(" │ ") +
		shared.RunningStyle.Render("● "+mode) +
		shared.MutedStyle.Render("  n:new  p:pause  r:resume  x:del  Enter:chat  Tab:focus  []:resize  q:quit")

	return lipgloss.NewStyle().Width(a.width).Render(bar)
}

// updatePanelSizes recalculates panel dimensions after a resize.
func (a *App) updatePanelSizes() {
	leftWidth := a.leftWidth
	rightWidth := a.rightWidth
	centerWidth := a.width - leftWidth - rightWidth - 6
	if centerWidth < 20 {
		centerWidth = 20
	}
	bodyHeight := a.height - 2

	a.systemInfo.SetWidth(leftWidth)
	a.agentList.SetWidth(leftWidth)
	a.conversation.SetSize(centerWidth, bodyHeight)
	a.contextList.SetWidth(rightWidth)
	a.poolStats.SetWidth(rightWidth)
}

// selectCurrentAgent loads the currently highlighted agent's data.
func (a *App) selectCurrentAgent() tea.Cmd {
	newID := a.agentList.SelectedAgentID()
	if newID == a.selectedAgentID || newID == "" {
		return nil
	}
	a.selectedAgentID = newID
	a.conversation.SetAgentID(newID)

	if a.vault != nil {
		msgs, err := a.vault.GetMessages(newID)
		if err == nil {
			a.conversation.LoadMessages(msgs)
		}
	}
	return nil
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

// createAndSpawnAgent creates an agent in the vault and spawns it via orchestrator.
func (a *App) createAndSpawnAgent(name, task string) tea.Cmd {
	return func() tea.Msg {
		// Generate fingerprint
		fp, err := vault.GenerateFingerprint(name)
		if err != nil {
			return statusNotice{text: "Fingerprint failed: " + err.Error()}
		}

		if a.vault == nil {
			return statusNotice{text: "No vault available"}
		}

		// Create in vault
		agent, err := a.vault.CreateAgent(name, task, fp)
		if err != nil {
			return statusNotice{text: "Create agent failed: " + err.Error()}
		}

		// Save initial task message
		a.vault.AppendMessage(agent.ID, "user", task, 0)

		// Spawn via orchestrator
		if a.orch != nil {
			sessionName := "vulpine-" + agent.ID
			_, err := a.orch.Agents.SpawnWithSession(agent.ID, task, sessionName, config.OpenClawConfigPath())
			if err != nil {
				return statusNotice{text: "Spawn failed: " + err.Error()}
			}
		}

		return shared.AgentCreatedMsg{Agent: *agent}
	}
}

// pauseAgent pauses an agent.
func (a App) pauseAgent(agentID string) tea.Cmd {
	return func() tea.Msg {
		if a.orch == nil {
			return statusNotice{text: "No orchestrator"}
		}
		if err := a.orch.Agents.PauseAgent(agentID); err != nil {
			return statusNotice{text: "Pause failed: " + err.Error()}
		}
		if a.vault != nil {
			a.vault.UpdateAgentStatus(agentID, "paused")
		}
		return statusNotice{text: "Agent paused: " + agentID}
	}
}

// resumeAgent resumes an agent from saved session.
func (a App) resumeAgent(agentID string) tea.Cmd {
	return func() tea.Msg {
		if a.orch == nil {
			return statusNotice{text: "No orchestrator"}
		}
		sessionName := "vulpine-" + agentID
		_, err := a.orch.Agents.ResumeWithSession(agentID, sessionName, config.OpenClawConfigPath())
		if err != nil {
			return statusNotice{text: "Resume failed: " + err.Error()}
		}
		if a.vault != nil {
			a.vault.UpdateAgentStatus(agentID, "active")
		}
		return statusNotice{text: "Agent resumed: " + agentID}
	}
}

// deleteAgent removes an agent.
func (a *App) deleteAgent(agentID string) tea.Cmd {
	return func() tea.Msg {
		// Kill if running
		if a.orch != nil {
			a.orch.Agents.Kill(agentID)
		}
		// Remove from vault
		if a.vault != nil {
			a.vault.DeleteAgent(agentID)
		}
		return statusNotice{text: "Agent deleted: " + agentID}
	}
}

// sendMessageToAgent sends a text message to a running agent's stdin.
func (a App) sendMessageToAgent(agentID, text string) tea.Cmd {
	return func() tea.Msg {
		if a.orch == nil {
			return statusNotice{text: "No orchestrator"}
		}
		if err := a.orch.Agents.SendMessage(agentID, text); err != nil {
			return statusNotice{text: "Send failed: " + err.Error()}
		}
		return nil
	}
}

// Header renders the VulpineOS header.
func Header() string {
	var b strings.Builder
	b.WriteString(shared.TitleStyle.Render("VulpineOS"))
	b.WriteString(shared.MutedStyle.Render(" — Sovereign Agent Runtime"))
	return b.String()
}
