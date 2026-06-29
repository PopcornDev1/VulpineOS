package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/agentcore"
	"vulpineos/internal/agentprompt"
	"vulpineos/internal/config"
	"vulpineos/internal/juggler"
	"vulpineos/internal/kernel"
	"vulpineos/internal/monitor"
	"vulpineos/internal/orchestrator"
	"vulpineos/internal/proxy"
	"vulpineos/internal/runtimeaudit"
	"vulpineos/internal/tui/agentdetail"
	"vulpineos/internal/tui/agentlist"
	"vulpineos/internal/tui/agentpicker"
	"vulpineos/internal/tui/commandpalette"
	"vulpineos/internal/tui/contextlist"
	"vulpineos/internal/tui/conversation"
	"vulpineos/internal/tui/settings"
	"vulpineos/internal/tui/setup"
	"vulpineos/internal/tui/shared"
	"vulpineos/internal/tui/systeminfo"
	"vulpineos/internal/vault"
)

var startExternalCommand = func(name string, args ...string) error {
	return exec.Command(name, args...).Start()
}

var lookExternalCommand = exec.LookPath

func openExternalTarget(target string) error {
	candidates := [][]string{
		{"open", target},
		{"xdg-open", target},
		{"rundll32", "url.dll,FileProtocolHandler", target},
	}
	var lastErr error
	for _, candidate := range candidates {
		if _, err := lookExternalCommand(candidate[0]); err != nil {
			continue
		}
		if err := startExternalCommand(candidate[0], candidate[1:]...); err != nil {
			lastErr = fmt.Errorf("%s: %w", candidate[0], err)
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no opener available")
}

// Focus panel identifiers.
const (
	FocusAgentList    = 0
	FocusConversation = 1
	FocusAgentDetail  = 2 // deprecated: agent-detail panel removed from the UI
	FocusContextList  = 3 // deprecated: contexts panel removed from the UI
	FocusSettings     = 4
	FocusNormalCount  = 2 // Tab cycles AgentList <-> Conversation only
)

// statusNotice is a transient message shown in the status bar.
type statusNotice struct {
	text string
}

type browserContextWindow interface {
	IsContextVisible(contextID string) bool
	HideContext(contextID string) error
	ShowContext(contextID string) error
}

type browserAllWindow interface {
	HideAll() error
}

// eventNotice is delivered through the event channel and must re-arm
// waitForEvent after it is displayed.
type eventNotice struct {
	text    string
	agentID string
}

type remoteStatusMsg struct {
	Err                string             `json:"-"`
	KernelRunning      bool               `json:"kernel_running"`
	KernelPID          int                `json:"kernel_pid"`
	KernelHeadless     bool               `json:"kernel_headless"`
	BrowserRoute       string             `json:"browser_route"`
	BrowserWindow      string             `json:"browser_window"`
	PoolAvailable      int                `json:"pool_available"`
	PoolActive         int                `json:"pool_active"`
	PoolTotal          int                `json:"pool_total"`
	ActiveContexts     int                `json:"active_contexts"`
	ActivePages        int                `json:"active_pages"`
	BrowserObservation vault.RuntimeEvent `json:"browser_observation"`
}

// ControlClient sends TUI control commands over a remote connection.
type ControlClient interface {
	ControlCall(ctx context.Context, method string, params any, result any) error
}

type remoteAgentsLoadedMsg struct {
	Agents          []vault.Agent
	SelectedAgentID string
	Notice          string
}

type remoteMessagesLoadedMsg struct {
	AgentID  string
	Messages []vault.AgentMessage
}

type remoteSettingsLoadedMsg struct {
	Err     string
	Config  remoteConfigSummary
	Proxies []remoteProxySummary
	Skills  []remoteSkillSummary
}

type remoteProxiesLoadedMsg struct {
	Err     string
	Notice  string
	Proxies []remoteProxySummary
}

type remoteSkillsLoadedMsg struct {
	Err    string
	Notice string
	Skills []remoteSkillSummary
}

type remoteConfigSummary struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	APIKeySet     bool   `json:"apiKeySet"`
	SetupComplete bool   `json:"setupComplete"`
}

type remoteProxySummary struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Type    string `json:"type"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Country string `json:"country"`
	Latency string `json:"latency"`
}

type remoteSkillSummary struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

const (
	defaultAgentName        = "Agent 1"
	remoteAPIKeyPlaceholder = "__vulpine_remote_api_key_set__"
)

type selectionAutoScrollMsg struct{}

type queuedChatTurn struct {
	AgentID        string
	Content        string
	DisplayContent string
}

var hostGOOS = runtime.GOOS

// App is the root Bubbletea model for the 3-column agent workbench.
type App struct {
	kernel           *kernel.Kernel
	client           *juggler.Client
	orch             *orchestrator.Orchestrator
	vault            *vault.DB
	cfg              *config.Config
	monitor          *monitor.Monitor
	control          ControlClient
	foxbridgeRunning func() bool

	width, height int
	leftWidth     int // adjustable left sidebar width
	rightWidth    int // adjustable right sidebar width
	leftSplit     int // height of system info in left (agent list gets remainder)
	rightSplit    int // height of agent detail in right (contexts gets remainder)
	focus         int // 0=agentlist, 1=conversation, 2=agentdetail, 3=contexts

	// Panels
	systemInfo     systeminfo.Model
	agentList      agentlist.Model
	agentDetail    agentdetail.Model
	conversation   conversation.Model
	contextList    contextlist.Model
	settings       settings.Model
	setupWizard    *setup.Model
	setupActive    bool
	commandPalette commandpalette.Model
	agentPicker    *agentpicker.Model

	// State
	selectedAgentID            string
	inputMode                  string // "" | "new-agent-name" | "new-agent-desc" | "chat" | "rename"
	newAgentName               string // temp storage during agent creation
	newAgentContext            string
	renameAgentID              string // agent ID being renamed
	notice                     string
	noticeTTL                  int // number of ticks before notice is cleared
	persistentWarning          string
	persistentWarningAgent     string
	quitConfirmArmed           bool
	selectionAutoScrollDir     int
	selectionAutoScrollCol     int
	pendingChatFocusAgentID    string
	pendingTerminalMouseReport string
	queuedChatTurns            map[string][]queuedChatTurn
	liveAgentContexts          map[string]string
	agentTokens                map[string]int
	observationWarnings        map[string]vault.RuntimeEvent
	dismissedWarnings          map[string]string
	clipboardWrite             func(string) error

	// Text inputs
	nameInput   textinput.Model
	taskInput   textinput.Model
	renameInput textinput.Model

	eventCh  chan tea.Msg
	eventIn  chan tea.Msg
	stopCh   chan struct{}
	stopOnce *sync.Once
}

// NewApp creates the root TUI model.
func NewApp(k *kernel.Kernel, client *juggler.Client, orch *orchestrator.Orchestrator, v *vault.DB, cfg *config.Config, audit *runtimeaudit.Manager) App {
	return NewAppWithControl(k, client, orch, v, cfg, audit, nil)
}

// SetFoxbridgeRunning wires the local embedded foxbridge liveness check used
// to avoid displaying a stale CDP route.
func (a *App) SetFoxbridgeRunning(fn func() bool) {
	a.foxbridgeRunning = fn
}

// NewAppWithControl creates the root TUI model with an optional remote control client.
func NewAppWithControl(k *kernel.Kernel, client *juggler.Client, orch *orchestrator.Orchestrator, v *vault.DB, cfg *config.Config, audit *runtimeaudit.Manager, control ControlClient) App {
	eventCh := make(chan tea.Msg, 64)
	eventIn := make(chan tea.Msg, 256)
	stopCh := make(chan struct{})

	nameIn := textinput.New()
	nameIn.Placeholder = "Agent name..."
	nameIn.CharLimit = 64
	nameIn.Width = 40

	taskIn := textinput.New()
	taskIn.Placeholder = "Brief description of this agent's purpose..."
	taskIn.CharLimit = 500
	taskIn.Width = 60

	renameIn := textinput.New()
	renameIn.Placeholder = "New agent name..."
	renameIn.CharLimit = 64
	renameIn.Width = 40

	mon := monitor.New()

	app := App{
		kernel:              k,
		client:              client,
		orch:                orch,
		vault:               v,
		cfg:                 cfg,
		monitor:             mon,
		control:             control,
		leftWidth:           24,
		rightWidth:          0,
		leftSplit:           13, // system info height (includes pool stats now)
		rightSplit:          10, // agent detail height in right column
		nameInput:           nameIn,
		taskInput:           taskIn,
		renameInput:         renameIn,
		systemInfo:          systeminfo.New(),
		agentList:           agentlist.New(),
		agentDetail:         agentdetail.New(),
		conversation:        conversation.New(),
		contextList:         contextlist.New(),
		settings:            settings.New(),
		commandPalette:      commandpalette.New(),
		queuedChatTurns:     make(map[string][]queuedChatTurn),
		liveAgentContexts:   make(map[string]string),
		agentTokens:         make(map[string]int),
		observationWarnings: make(map[string]vault.RuntimeEvent),
		dismissedWarnings:   make(map[string]string),
		clipboardWrite:      clipboard.WriteAll,
		eventCh:             eventCh,
		eventIn:             eventIn,
		stopCh:              stopCh,
		stopOnce:            &sync.Once{},
	}
	go forwardTUIEvents(stopCh, eventIn, eventCh)
	if control != nil {
		app.agentDetail.SetRemote(true)
	}
	emitEvent := app.emitEvent
	if audit != nil {
		if events, err := audit.List(vault.RuntimeEventFilter{Limit: 100}); err == nil {
			seed := make([]shared.RuntimeEventMsg, 0, len(events))
			for i, event := range events {
				if i < 3 {
					seed = append(seed, shared.RuntimeEventMsg{Event: event})
				}
			}
			app.systemInfo.SetRuntimeEvents(seed)
			for i := len(events) - 1; i >= 0; i-- {
				app.applyRuntimeObservationEvent(events[i])
			}
		}
		sub := audit.Subscribe()
		go func() {
			defer audit.Unsubscribe(sub)
			for {
				select {
				case <-stopCh:
					return
				case event, ok := <-sub:
					if !ok {
						return
					}
					emitEvent(shared.RuntimeEventMsg{Event: event})
				}
			}
		}()
	}

	// Load existing agents from vault and reconcile status
	if v != nil {
		agents, err := v.ListAgents()
		if err != nil {
			log.Printf("tui: failed to load agents from vault: %v", err)
		} else {
			// Seed per-agent token totals from vault
			var total int64
			for _, agent := range agents {
				app.agentTokens[agent.ID] = agent.TotalTokens
				total += int64(agent.TotalTokens)
			}
			app.systemInfo.SetSessionTokens(total)
			// Reconcile status: agents that were live when the previous process exited
			// are now "paused" since no process is running on startup
			for i := range agents {
				if isLiveAgentStatus(agents[i].Status) {
					agents[i].Status = "paused"
					v.UpdateAgentStatus(agents[i].ID, "paused")
				}
			}
			app.agentList.SetAgents(agents)
			// Select first agent if any and auto-enter chat mode
			if len(agents) > 0 {
				app.selectedAgentID = agents[0].ID
				app.conversation.SetAgentID(agents[0].ID)
				app.conversation.SetAgentName(agents[0].Name)
				msgs, err := v.GetMessages(agents[0].ID)
				if err == nil {
					app.conversation.LoadMessages(msgs)
				}
				app.applyConversationStatus(agents[0].Status)
				app.updateAgentDetail(&agents[0])
				app.focus = FocusConversation
				app.inputMode = "chat"
				app.conversation.SetAwake(true)
				app.conversation.Focus()
			} else {
				app.prepareDefaultChatPlaceholder()
			}
		}
	}

	// Subscribe to Juggler events
	if client != nil {
		client.Subscribe("Browser.attachedToTarget", func(sid string, params json.RawMessage) {
			var e juggler.AttachedToTarget
			json.Unmarshal(params, &e)
			emitEvent(shared.TargetAttachedMsg{
				SessionID: e.SessionID,
				TargetID:  e.TargetInfo.TargetID,
				ContextID: e.TargetInfo.BrowserContextID,
				URL:       e.TargetInfo.URL,
			})
		})
		client.Subscribe("Browser.detachedFromTarget", func(sid string, params json.RawMessage) {
			var e juggler.DetachedFromTarget
			json.Unmarshal(params, &e)
			emitEvent(shared.TargetDetachedMsg{
				SessionID: e.SessionID,
				TargetID:  e.TargetID,
			})
		})
		client.Subscribe("Browser.trustWarmingStateChanged", func(sid string, params json.RawMessage) {
			var e juggler.TrustWarmingState
			json.Unmarshal(params, &e)
			emitEvent(shared.TrustWarmMsg{State: e.State, CurrentSite: e.CurrentSite})
		})
		client.Subscribe("Browser.telemetryUpdate", func(sid string, params json.RawMessage) {
			var e juggler.TelemetryUpdate
			json.Unmarshal(params, &e)
			riskScore := e.RuntimeRiskScore
			if riskScore == 0 {
				riskScore = e.DetectionRiskScore
			}
			emitEvent(shared.TelemetryMsg{
				MemoryMB:         e.MemoryMB,
				EventLoopLagMs:   e.EventLoopLagMs,
				RuntimeRiskScore: riskScore,
				ActiveContexts:   e.ActiveContexts,
				ActivePages:      e.ActivePages,
			})
		})
		client.Subscribe("Browser.injectionAttemptDetected", func(sid string, params json.RawMessage) {
			var e juggler.InjectionAttempt
			json.Unmarshal(params, &e)
			emitEvent(shared.AlertMsg{
				Timestamp: time.Now(),
				Type:      e.AttemptType,
				URL:       e.URL,
				Details:   e.Details,
				Blocked:   e.Blocked,
			})
		})
		client.Subscribe("Page.navigationCommitted", func(sid string, params json.RawMessage) {
			var e struct {
				FrameID string `json:"frameId"`
				URL     string `json:"url"`
			}
			json.Unmarshal(params, &e)
			emitEvent(shared.NavigationMsg{
				SessionID: sid,
				FrameID:   e.FrameID,
				URL:       e.URL,
			})
		})
		client.Subscribe("Page.eventFired", func(sid string, params json.RawMessage) {
			var e struct {
				FrameID string `json:"frameId"`
				Name    string `json:"name"`
			}
			json.Unmarshal(params, &e)
			emitEvent(shared.PageLoadMsg{
				SessionID: sid,
				FrameID:   e.FrameID,
				Name:      e.Name,
			})
		})
		client.Subscribe("Page.frameAttached", func(sid string, params json.RawMessage) {
			var e struct {
				FrameID       string `json:"frameId"`
				ParentFrameID string `json:"parentFrameId"`
			}
			json.Unmarshal(params, &e)
			emitEvent(shared.FrameAttachedMsg{
				SessionID:     sid,
				FrameID:       e.FrameID,
				ParentFrameID: e.ParentFrameID,
			})
		})
		client.Subscribe("Runtime.executionContextCreated", func(sid string, params json.RawMessage) {
			var e struct {
				ExecutionContextID string `json:"executionContextId"`
				AuxData            struct {
					FrameID string `json:"frameId"`
				} `json:"auxData"`
			}
			json.Unmarshal(params, &e)
			emitEvent(shared.ExecContextCreatedMsg{
				SessionID:          sid,
				ExecutionContextID: e.ExecutionContextID,
				FrameID:            e.AuxData.FrameID,
			})
		})
		client.Subscribe("Vulpine.agentStatus", func(sid string, params json.RawMessage) {
			var e struct {
				AgentID               string `json:"agentId"`
				ParentID              string `json:"parentId"`
				ContextID             string `json:"contextId"`
				Status                string `json:"status"`
				Objective             string `json:"objective"`
				Tokens                int    `json:"tokens"`
				Phase                 string `json:"phase"`
				Turn                  int    `json:"turn"`
				MaxTurns              int    `json:"maxTurns"`
				LastActivity          int64  `json:"lastActivity"`
				ObservationConfidence string `json:"observationConfidence"`
				ObservationSummary    string `json:"observationSummary"`
				ObservationURL        string `json:"observationUrl"`
				LastFailedTool        string `json:"lastFailedTool"`
			}
			if err := json.Unmarshal(params, &e); err != nil {
				return
			}
			emitEvent(shared.AgentStatusMsg{
				AgentID:               e.AgentID,
				ParentID:              e.ParentID,
				ContextID:             e.ContextID,
				Status:                e.Status,
				Objective:             e.Objective,
				Tokens:                e.Tokens,
				Phase:                 e.Phase,
				Turn:                  e.Turn,
				MaxTurns:              e.MaxTurns,
				LastActivity:          e.LastActivity,
				ObservationConfidence: e.ObservationConfidence,
				ObservationSummary:    e.ObservationSummary,
				ObservationURL:        e.ObservationURL,
				LastFailedTool:        e.LastFailedTool,
			})
		})
		client.Subscribe("Vulpine.conversation", func(sid string, params json.RawMessage) {
			var e struct {
				AgentID        string `json:"agentId"`
				Role           string `json:"role"`
				Content        string `json:"content"`
				DisplayContent string `json:"displayContent"`
				Tokens         int    `json:"tokens"`
			}
			if err := json.Unmarshal(params, &e); err != nil {
				return
			}
			emitEvent(shared.ConversationEntryMsg{
				AgentID:        e.AgentID,
				Role:           e.Role,
				Content:        e.Content,
				DisplayContent: e.DisplayContent,
				Tokens:         e.Tokens,
				Timestamp:      time.Now(),
			})
		})
		client.Subscribe("Vulpine.runtimeEvent", func(sid string, params json.RawMessage) {
			var event vault.RuntimeEvent
			if err := json.Unmarshal(params, &event); err != nil {
				return
			}
			emitEvent(shared.RuntimeEventMsg{Event: event})
		})
	}

	// Forward agent status updates from orchestrator to TUI
	if orch != nil {
		statusCh := orch.Agents.StatusChan()
		go func() {
			for {
				select {
				case <-stopCh:
					return
				case status, ok := <-statusCh:
					if !ok {
						return
					}
					emitEvent(shared.AgentStatusMsg{
						AgentID:               status.AgentID,
						ParentID:              status.ParentID,
						ContextID:             status.ContextID,
						Status:                status.Status,
						Objective:             status.Objective,
						Tokens:                status.Tokens,
						Phase:                 status.Phase,
						Turn:                  status.Turn,
						MaxTurns:              status.MaxTurns,
						LastActivity:          status.LastActivity,
						ObservationConfidence: status.ObservationConfidence,
						ObservationSummary:    status.ObservationSummary,
						ObservationURL:        status.ObservationURL,
						LastFailedTool:        status.LastFailedTool,
					})
				}
			}
		}()

		// Forward conversation messages from orchestrator
		conversationCh := orch.Agents.ConversationChan()
		go func() {
			for {
				select {
				case <-stopCh:
					return
				case msg, ok := <-conversationCh:
					if !ok {
						return
					}
					emitEvent(shared.ConversationEntryMsg{
						AgentID:      msg.AgentID,
						Role:         msg.Role,
						Content:      msg.Content,
						Tokens:       msg.Tokens,
						Timestamp:    time.Now(),
						StreamActive: msg.StreamActive,
					})
				}
			}
		}()
	}

	// Forward rate limit monitor alerts to TUI
	go func() {
		alertCh := mon.AlertChan()
		for {
			select {
			case <-stopCh:
				return
			case alert, ok := <-alertCh:
				if !ok {
					return
				}
				emitEvent(eventNotice{
					text:    fmt.Sprintf("WARNING %s: %s on agent %s", alert.Type, alert.Details, alert.AgentID),
					agentID: alert.AgentID,
				})
			}
		}
	}()

	if cfg != nil {
		app.conversation.SetModelLabel(cfg.Model)
	}

	return app
}

func (a App) Init() tea.Cmd {
	cmds := []tea.Cmd{
		a.waitForEvent(),
		a.tick(),
		conversation.InputPulseTick(),
		a.replayBrowserTargets(),
	}
	if a.shouldCreateDefaultAgent() {
		cmds = append(cmds, a.createDefaultAgent())
	}
	if a.control != nil {
		cmds = append(cmds, a.loadRemoteAgents())
		cmds = append(cmds, a.loadRemoteStatus())
	}
	return tea.Batch(cmds...)
}

func (a App) shouldCreateDefaultAgent() bool {
	return a.control == nil && a.vault != nil && len(a.agentList.AllAgents()) == 0
}

func (a *App) prepareDefaultChatPlaceholder() {
	a.selectedAgentID = ""
	a.conversation.SetAgentID("")
	a.conversation.SetAgentName(defaultAgentName)
	a.conversation.SetAwake(true)
	a.agentDetail.Clear()
	a.focus = FocusConversation
	a.inputMode = "chat"
	a.conversation.Focus()
}

func (a *App) focusChatComposer() tea.Cmd {
	a.focus = FocusConversation
	a.inputMode = "chat"
	return a.conversation.Focus()
}

func (a *App) cancelChatSelectionAndFocus() tea.Cmd {
	a.conversation.ClearSelection()
	a.stopConversationSelectionAutoScroll()
	return a.focusChatComposer()
}

func (a *App) createDefaultAgent() tea.Cmd {
	return a.createAgent(defaultAgentName, defaultAgentName, "")
}

func (a App) replayBrowserTargets() tea.Cmd {
	if a.client == nil {
		return nil
	}
	return func() tea.Msg {
		_, _ = a.client.Call("", "Browser.enable", map[string]interface{}{
			"attachToDefaultContext": true,
		})
		return nil
	}
}

type remoteAgentSummary struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Status             string `json:"status"`
	Task               string `json:"task"`
	TotalTokens        int    `json:"totalTokens"`
	Fingerprint        string `json:"fingerprint"`
	FingerprintSummary string `json:"fingerprintSummary"`
	ContextID          string `json:"contextId"`
}

func (a App) loadRemoteAgents() tea.Cmd {
	if a.control == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		agents, err := a.fetchRemoteAgents(ctx)
		if err != nil {
			return statusNotice{text: "Remote agents failed: " + err.Error()}
		}
		return remoteAgentsLoadedMsg{Agents: agents}
	}
}

func (a App) loadRemoteStatus() tea.Cmd {
	if a.control == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var result remoteStatusMsg
		if err := a.control.ControlCall(ctx, "status.get", map[string]any{}, &result); err != nil {
			return remoteStatusMsg{Err: err.Error()}
		}
		return result
	}
}

func (a App) loadRemoteMessages(agentID string) tea.Cmd {
	if a.control == nil || agentID == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var result struct {
			Messages []vault.AgentMessage `json:"messages"`
		}
		if err := a.control.ControlCall(ctx, "agents.getMessages", map[string]any{"agentId": agentID}, &result); err != nil {
			return statusNotice{text: "Remote messages failed: " + err.Error()}
		}
		return remoteMessagesLoadedMsg{AgentID: agentID, Messages: result.Messages}
	}
}

func (a App) loadRemoteSettings() tea.Cmd {
	if a.control == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var result struct {
			Config  remoteConfigSummary  `json:"config"`
			Proxies []remoteProxySummary `json:"proxies"`
			Skills  []remoteSkillSummary `json:"skills"`
		}
		if err := a.control.ControlCall(ctx, "settings.get", map[string]any{}, &result); err != nil {
			return remoteSettingsLoadedMsg{Err: err.Error()}
		}
		return remoteSettingsLoadedMsg{Config: result.Config, Proxies: result.Proxies, Skills: result.Skills}
	}
}

func configFromRemoteSummary(item remoteConfigSummary) *config.Config {
	cfg := &config.Config{
		Provider:      item.Provider,
		Model:         item.Model,
		SetupComplete: item.SetupComplete,
	}
	if item.APIKeySet {
		cfg.APIKey = remoteAPIKeyPlaceholder
	}
	return cfg
}

func settingsProxiesFromRemote(items []remoteProxySummary) []settings.ProxyItem {
	proxies := make([]settings.ProxyItem, 0, len(items))
	for _, item := range items {
		latency := item.Latency
		if latency == "" {
			latency = "untested"
		}
		proxies = append(proxies, settings.ProxyItem{
			ID:      item.ID,
			Label:   item.Label,
			Type:    item.Type,
			Host:    item.Host,
			Port:    item.Port,
			Country: item.Country,
			Latency: latency,
		})
	}
	return proxies
}

func settingsSkillsFromRemote(items []remoteSkillSummary) []settings.SkillItem {
	skills := make([]settings.SkillItem, 0, len(items))
	for _, item := range items {
		skills = append(skills, settings.SkillItem{Name: item.Name, Enabled: item.Enabled})
	}
	return skills
}

func (a App) fetchRemoteAgents(ctx context.Context) ([]vault.Agent, error) {
	var result struct {
		Agents []remoteAgentSummary `json:"agents"`
	}
	if err := a.control.ControlCall(ctx, "agents.list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	agents := make([]vault.Agent, 0, len(result.Agents))
	for _, item := range result.Agents {
		agent := remoteSummaryToAgent(item)
		agents = append(agents, agent)
	}
	return agents, nil
}

func remoteSummaryToAgent(item remoteAgentSummary) vault.Agent {
	fingerprint := item.Fingerprint
	if fingerprint == "" {
		fingerprint = item.FingerprintSummary
	}
	agent := vault.Agent{
		ID:          item.ID,
		Name:        item.Name,
		Task:        item.Task,
		Status:      item.Status,
		TotalTokens: item.TotalTokens,
		Fingerprint: fingerprint,
	}
	if item.ContextID != "" {
		agent.Metadata = vault.MarshalAgentMetadata(vault.AgentMetadata{ContextID: item.ContextID})
	}
	return agent
}

func agentListItemToAgent(item agentlist.AgentListItem) vault.Agent {
	return vault.Agent{
		ID:          item.ID,
		Name:        item.Name,
		Task:        item.Task,
		Status:      item.Status,
		TotalTokens: item.Tokens,
		Fingerprint: item.Fingerprint,
		ProxyConfig: item.ProxyConfig,
		Metadata:    item.Metadata,
		CreatedAt:   item.CreatedAt,
	}
}

func (a App) emitEvent(msg tea.Msg) {
	if a.eventCh == nil {
		return
	}
	if a.eventIn != nil {
		if a.stopCh == nil {
			a.eventIn <- msg
			return
		}
		select {
		case <-a.stopCh:
		case a.eventIn <- msg:
		}
		return
	}
	if a.stopCh == nil {
		select {
		case a.eventCh <- msg:
		default:
		}
		return
	}
	select {
	case <-a.stopCh:
	case a.eventCh <- msg:
	default:
	}
}

func forwardTUIEvents(stopCh <-chan struct{}, in <-chan tea.Msg, out chan<- tea.Msg) {
	pending := make([]tea.Msg, 0)
	for {
		if len(pending) == 0 {
			select {
			case <-stopCh:
				return
			case msg := <-in:
				pending = append(pending, msg)
			}
			continue
		}

		select {
		case <-stopCh:
			return
		case msg := <-in:
			pending = append(pending, msg)
		case out <- pending[0]:
			pending[0] = nil
			pending = pending[1:]
		}
	}
}

func (a App) stopForwarders() {
	if a.stopOnce == nil || a.stopCh == nil {
		if a.monitor != nil {
			a.monitor.Dispose()
		}
		return
	}
	a.stopOnce.Do(func() {
		close(a.stopCh)
		if a.monitor != nil {
			a.monitor.Dispose()
		}
	})
}

func (a *App) shutdown() tea.Cmd {
	a.gracefulShutdown()
	a.stopForwarders()
	return tea.Quit
}

func (a App) handleCtrlC() (tea.Model, tea.Cmd) {
	if a.quitConfirmArmed {
		return a, a.shutdown()
	}
	a.notice = "Press Ctrl+Shift+C again to quit"
	a.noticeTTL = 4
	a.quitConfirmArmed = true
	return a, nil
}

func (a App) handleKillAgent() (tea.Model, tea.Cmd) {
	if a.selectedAgentID == "" {
		return a, nil
	}
	if a.control != nil {
		if !isLiveAgentStatus(a.selectedAgentStatus()) {
			a.notice = "Agent is not running"
			a.noticeTTL = 3
			return a, nil
		}
		return a, a.remoteAgentStatusCommand("agents.kill", a.selectedAgentID, "interrupted", "Remote agent killed: ")
	}
	if a.orch != nil && isLiveAgentStatus(a.selectedAgentStatus()) {
		if err := a.orch.KillAgent(a.selectedAgentID); err != nil && !strings.Contains(err.Error(), "not found") {
			a.notice = "Kill failed: " + err.Error()
			a.noticeTTL = 4
			return a, nil
		}
		a.quitConfirmArmed = false
		a.conversation.AddEntry("system", "Agent interrupted")
		a.conversation.SetThinking(false)
		a.notice = "Agent interrupted"
		a.noticeTTL = 3
	} else {
		a.notice = "No running agent to kill"
		a.noticeTTL = 3
	}
	return a, nil
}

func (a App) handleCopySelection() (tea.Model, tea.Cmd) {
	selected := a.conversation.SelectedText()
	if strings.TrimSpace(selected) == "" {
		return a, nil
	}
	a.conversation.ClearSelection()
	a.quitConfirmArmed = false
	return a, a.copyTextCommand(selected, "Copied selected chat text to clipboard")
}

func (a App) copySelectedChatTextCommand(success string) tea.Cmd {
	selected := a.conversation.SelectedText()
	if strings.TrimSpace(selected) == "" {
		return nil
	}
	return a.copyTextCommand(selected, success)
}

func (a App) copyTextCommand(content, success string) tea.Cmd {
	return func() tea.Msg {
		write := a.clipboardWrite
		if write == nil {
			write = clipboard.WriteAll
		}
		if err := write(content); err != nil {
			return statusNotice{text: "Copy failed: " + err.Error()}
		}
		return statusNotice{text: success}
	}
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if key, ok := msg.(tea.KeyMsg); ok {
		filtered, consumed := a.filterLeakedTerminalMouseReportKey(key)
		if consumed {
			return a, nil
		}
		msg = filtered
	}
	if isCopySelectionMsg(msg) {
		return a.handleCopySelection()
	}
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+c" {
		if hostGOOS != "darwin" && strings.TrimSpace(a.conversation.SelectedText()) != "" {
			return a.handleCopySelection()
		}
		return a.handleCtrlC()
	}

	if a.setupActive {
		switch msg.(type) {
		case tea.KeyMsg, tea.WindowSizeMsg:
			return a.updateEmbeddedSetup(msg)
		}
		// Forward the wizard's own async messages (e.g. OAuth flow steps) so the
		// embedded sign-in state machine advances; otherwise it stalls before the
		// auth URL is shown.
		if setup.IsAsyncMsg(msg) {
			return a.updateEmbeddedSetup(msg)
		}
	}

	// Agent picker modal owns key/size input while open; it emits
	// AgentPickerPickedMsg / AgentPickerCancelledMsg (handled in the switch below).
	if a.agentPicker != nil {
		switch msg.(type) {
		case tea.KeyMsg, tea.WindowSizeMsg:
			model, cmd := a.agentPicker.Update(msg)
			if p, ok := model.(*agentpicker.Model); ok {
				a.agentPicker = p
			}
			return a, cmd
		}
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// The slash-command palette owns key input whenever it is open.
		if a.commandPalette.Active() {
			return a.updateCommandPaletteInput(msg)
		}
		// Handle input modes first
		switch a.inputMode {
		case "new-agent-name":
			return a.updateNameInput(msg)
		case "new-agent-desc":
			return a.updateDescInput(msg)
		case "rename":
			return a.updateRenameInput(msg)
		case "chat":
			return a.updateChatInput(msg)
		}

		// Route to settings panel when active. Text capture owns printable keys
		// that overlap global lifecycle shortcuts, such as proxy URLs with p/r.
		if a.focus == FocusSettings && msg.String() != "ctrl+c" && (a.settings.CapturingText() || !isGlobalLifecycleKey(msg)) {
			var cmd tea.Cmd
			a.settings, cmd = a.settings.Update(msg)
			// If settings closed itself (via Esc or Tab cycling out)
			if !a.settings.IsActive() {
				cmds = append(cmds, a.focusChatComposer())
			}
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return a, tea.Batch(cmds...)
		}

		// Global controls are intentionally minimal: Ctrl+C copies selected chat
		// text, otherwise confirms quit. Cmd/Meta+C also copies selected chat text
		// where the terminal reports it. Esc returns to the always-active chat
		// composer, and "/" opens the command palette.
		switch msg.String() {
		case "ctrl+c":
			return a.handleCtrlC()
		case "esc":
			cmds = append(cmds, a.cancelChatSelectionAndFocus())
		case "/":
			if a.selectedAgentID != "" {
				a.focus = FocusConversation
				a.inputMode = "chat"
				a.conversation.Focus()
			}
			a.syncCommandPaletteAgents()
			a.commandPalette.Activate()
			return a, nil
		}

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.updatePanelSizes()

	case commandpalette.ExecuteCommandMsg:
		return a, a.dispatchCommand(msg.Name, msg.RawInput)

	case shared.AgentPickerPickedMsg:
		a.agentPicker = nil
		if msg.AgentID != "" {
			a.agentList.SelectAgentID(msg.AgentID)
			cmds = append(cmds, a.selectCurrentAgent())
			a.markAgentSelected(msg.AgentID)
		}

	case shared.AgentPickerCancelledMsg:
		a.agentPicker = nil

	case shared.TickMsg:
		// Decrement notice TTL; only clear when it reaches 0
		if a.noticeTTL > 0 {
			a.noticeTTL--
			if a.noticeTTL == 0 {
				a.notice = ""
				a.quitConfirmArmed = false
			}
		}
		if a.kernel != nil {
			ksMsg := shared.KernelStatusMsg{
				Running:       a.kernel.Running(),
				PID:           a.kernel.PID(),
				Uptime:        a.kernel.Uptime(),
				Headless:      a.kernel.IsHeadless(),
				BrowserRoute:  a.browserRouteLabel(),
				BrowserWindow: a.browserWindowLabel(),
			}
			a.systemInfo, _ = a.systemInfo.Update(ksMsg)
		}
		if a.control != nil && a.kernel == nil {
			cmds = append(cmds, a.loadRemoteStatus())
		}
		cmds = append(cmds, a.tick())

	// Juggler events
	case shared.TargetAttachedMsg:
		a.contextList, _ = a.contextList.Update(msg)
		a.systemInfo.SetBrowserCounts(a.contextList.ContextCount(), a.contextList.PageCount())
		cmds = append(cmds, a.waitForEvent())
	case shared.TargetDetachedMsg:
		a.contextList, _ = a.contextList.Update(msg)
		a.systemInfo.SetBrowserCounts(a.contextList.ContextCount(), a.contextList.PageCount())
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
		a.contextList, _ = a.contextList.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.TelemetryMsg:
		a.systemInfo, _ = a.systemInfo.Update(msg)
		cmds = append(cmds, a.waitForEvent())
	case shared.RuntimeEventMsg:
		a.systemInfo, _ = a.systemInfo.Update(msg)
		a.applyRuntimeObservationEvent(msg.Event)
		cmds = append(cmds, a.waitForEvent())
	case shared.TrustWarmMsg:
		state := strings.ToUpper(strings.TrimSpace(msg.State))
		if state == "" {
			state = "UNKNOWN"
		}
		site := strings.TrimSpace(msg.CurrentSite)
		if site != "" {
			a.notice = fmt.Sprintf("Trust warming %s: %s", state, site)
		} else {
			a.notice = "Trust warming " + state
		}
		a.noticeTTL = 3
		cmds = append(cmds, a.waitForEvent())
	case shared.AlertMsg:
		alertType := strings.TrimSpace(msg.Type)
		if alertType == "" {
			alertType = "alert"
		}
		details := strings.TrimSpace(msg.Details)
		if details == "" {
			details = strings.TrimSpace(msg.URL)
		}
		if details == "" {
			details = "injection attempt detected"
		}
		if msg.Blocked {
			a.notice = fmt.Sprintf("WARNING %s blocked: %s", alertType, details)
		} else {
			a.notice = fmt.Sprintf("WARNING %s: %s", alertType, details)
		}
		a.noticeTTL = 5
		cmds = append(cmds, a.waitForEvent())

	case eventNotice:
		a.notice = a.noticeTextWithAgentName(msg.text, msg.agentID)
		a.noticeTTL = 3
		cmds = append(cmds, a.waitForEvent())

	case shared.AgentStatusMsg:
		// Auto-create agent entry for unknown agents (sub-agents, remote agents
		// not yet synced). The agent list stores vault.Agent records; when we get
		// a status for an unseen ID we create a minimal in-memory entry so the
		// user sees it in the side panel.
		if _, known := a.agentList.Agent(msg.AgentID); !known {
			a.agentList.AddAgent(vault.Agent{
				ID:     msg.AgentID,
				Name:   msg.AgentID[:min(len(msg.AgentID), 8)],
				Task:   msg.Objective,
				Status: msg.Status,
			})
		}
		a.agentList, _ = a.agentList.Update(msg)
		// Update vault status (no-op when agent doesn't exist in vault — SQL
		// UPDATE with no matching row is harmless).
		if a.vault != nil {
			a.vault.UpdateAgentStatus(msg.AgentID, msg.Status)
			if msg.Tokens > 0 {
				a.vault.UpdateAgentTokens(msg.AgentID, msg.Tokens)
			}
		}
		// Accumulate per-agent tokens for session total
		if msg.Tokens > 0 {
			a.agentTokens[msg.AgentID] = msg.Tokens
			var total int64
			for _, t := range a.agentTokens {
				total += int64(t)
			}
			a.systemInfo.SetSessionTokens(total)
		}
		a.updateLiveAgentContext(msg)
		pendingTerminalStatus := a.pendingChatFocusAgentID == msg.AgentID && !isLiveAgentStatus(msg.Status)
		if pendingTerminalStatus {
			a.pendingChatFocusAgentID = ""
		}
		// Update agent detail if this is the selected agent
		if msg.AgentID == a.selectedAgentID {
			if !isLiveAgentStatus(msg.Status) {
				a.conversation.SetThinking(false)
			}
			a.conversation.SetAgentStatus(msg.Status)
			if pendingTerminalStatus {
				a.focus = FocusConversation
				a.inputMode = "chat"
				a.conversation.SetAwake(true)
				cmds = append(cmds, a.conversation.Focus())
			}
			if a.control != nil && a.vault == nil {
				a.updateRemoteAgentDetailFromList(msg.AgentID)
			} else {
				a.refreshAgentDetail(msg.AgentID)
			}
			if a.focus == FocusConversation && a.inputMode == "chat" && !a.conversation.Focused() {
				cmds = append(cmds, a.conversation.Focus())
			}
		}
		if cmd := a.flushQueuedChatTurnAfterStatus(msg.AgentID, msg.Status); cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, a.waitForEvent())

	case shared.ConversationEntryMsg:
		// Persist real conversation turns, but NOT transient streaming deltas
		// (Role "stream") — they update one live entry and would otherwise be
		// stored token-by-token and reload as fragmented one-word messages.
		if a.vault != nil && msg.Role != "stream" {
			a.vault.AppendMessageWithDisplay(msg.AgentID, msg.Role, msg.Content, msg.DisplayContent, msg.Tokens)
		}
		// Check for rate limit / captcha / block patterns
		if a.monitor != nil && (msg.Role == "assistant" || msg.Role == "system") {
			a.monitor.CheckMessage(msg.AgentID, msg.Content)
		}
		pendingAssistantReply := msg.Role == "assistant" && a.pendingChatFocusAgentID == msg.AgentID
		if pendingAssistantReply {
			a.pendingChatFocusAgentID = ""
		}
		// If matches selected agent, add to conversation panel. Tool and stream
		// updates keep the loading indicator active until the final assistant
		// reply or a terminal status arrives.
		if msg.AgentID == a.selectedAgentID {
			if msg.Role == "assistant" {
				a.conversation.SetThinking(false)
			}
			if msg.Role == "stream" {
				a.conversation.UpdateLastAssistant(msg.Content, true)
			} else if msg.Role == "assistant" && a.conversation.IsLastEntryStreaming() {
				// Final authoritative message from outbound DB —
				// replace the streaming entry with the complete message
				a.conversation.UpdateLastAssistant(msg.Content, false)
			} else {
				a.conversation.AddEntryWithDisplay(msg.Role, msg.Content, msg.DisplayContent)
			}
			if msg.Role == "assistant" {
				a.conversation.ForceScrollToBottom()
			}
			a.agentList.ClearUnread(msg.AgentID)
			if pendingAssistantReply {
				a.focus = FocusConversation
				a.inputMode = "chat"
				a.conversation.SetAwake(true)
				cmds = append(cmds, a.conversation.Focus())
			} else if a.focus == FocusConversation && a.inputMode == "chat" && !a.conversation.Focused() {
				cmds = append(cmds, a.conversation.Focus())
			}
		} else {
			a.agentList.MarkUnread(msg.AgentID)
		}
		cmds = append(cmds, a.waitForEvent())

	case remoteAgentsLoadedMsg:
		a.agentList.SetAgents(msg.Agents)
		// Seed per-agent token totals from loaded agents
		a.agentTokens = make(map[string]int, len(msg.Agents))
		total := int64(0)
		for _, agent := range msg.Agents {
			a.agentTokens[agent.ID] = agent.TotalTokens
			total += int64(agent.TotalTokens)
		}
		a.systemInfo.SetSessionTokens(total)
		if len(msg.Agents) == 0 {
			a.selectedAgentID = ""
			a.conversation.SetAgentID("")
			a.agentDetail.Clear()
			break
		}
		selectedID := msg.SelectedAgentID
		if selectedID == "" {
			selectedID = a.selectedAgentID
		}
		if selectedID == "" {
			selectedID = msg.Agents[0].ID
		}
		if !a.agentList.SelectAgentID(selectedID) {
			selectedID = msg.Agents[0].ID
			a.agentList.SelectAgentID(selectedID)
		}
		a.selectedAgentID = selectedID
		if msg.Notice != "" {
			a.notice = msg.Notice
			a.noticeTTL = 3
		}
		for i := range msg.Agents {
			if msg.Agents[i].ID == selectedID {
				a.conversation.SetAgentID(msg.Agents[i].ID)
				a.conversation.SetAgentName(msg.Agents[i].Name)
				if isLiveAgentStatus(msg.Agents[i].Status) {
					a.conversation.SetThinking(true)
					cmds = append(cmds, conversation.ThinkingTick())
				} else {
					a.conversation.SetThinking(false)
				}
				a.applyConversationStatus(msg.Agents[i].Status)
				a.updateAgentDetail(&msg.Agents[i])
				cmds = append(cmds, a.loadRemoteMessages(selectedID))
				break
			}
		}

	case remoteStatusMsg:
		if msg.Err != "" {
			a.notice = "Remote status failed: " + msg.Err
			a.noticeTTL = 3
			break
		}
		a.systemInfo, _ = a.systemInfo.Update(shared.KernelStatusMsg{
			Running:       msg.KernelRunning,
			PID:           msg.KernelPID,
			Headless:      msg.KernelHeadless,
			BrowserRoute:  msg.BrowserRoute,
			BrowserWindow: msg.BrowserWindow,
		})
		a.systemInfo.SetBrowserCounts(msg.ActiveContexts, msg.ActivePages)
		if strings.TrimSpace(msg.BrowserObservation.Event) != "" {
			a.applyRuntimeObservationEvent(msg.BrowserObservation)
		}

	case remoteMessagesLoadedMsg:
		if msg.AgentID == a.selectedAgentID {
			a.conversation.LoadMessages(msg.Messages)
			if item, ok := a.agentList.Agent(msg.AgentID); ok {
				a.applyConversationStatus(item.Status)
			}
		}

	case remoteSettingsLoadedMsg:
		if msg.Err != "" {
			a.notice = "Remote settings failed: " + msg.Err
			a.noticeTTL = 3
			break
		}
		a.cfg = configFromRemoteSummary(msg.Config)
		a.focus = FocusSettings
		a.settings.SetActive(true)
		a.settings.SetConfig(a.cfg)
		a.settings.SetProxies(settingsProxiesFromRemote(msg.Proxies))
		a.settings.SetSkills(settingsSkillsFromRemote(msg.Skills))
		a.notice = "Remote settings loaded"
		a.noticeTTL = 3

	case remoteProxiesLoadedMsg:
		if msg.Err != "" {
			a.notice = msg.Err
			a.noticeTTL = 3
			break
		}
		a.settings.SetProxies(settingsProxiesFromRemote(msg.Proxies))
		if msg.Notice != "" {
			a.notice = msg.Notice
			a.noticeTTL = 3
		}

	case remoteSkillsLoadedMsg:
		if msg.Err != "" {
			a.notice = msg.Err
			a.noticeTTL = 3
			break
		}
		a.settings.SetSkills(settingsSkillsFromRemote(msg.Skills))
		if a.cfg != nil {
			a.cfg.GlobalSkills = nil
			for _, skill := range msg.Skills {
				a.cfg.GlobalSkills = append(a.cfg.GlobalSkills, config.SkillEntry{Name: skill.Name, Enabled: skill.Enabled})
			}
			a.settings.SetConfig(a.cfg)
		}
		if msg.Notice != "" {
			a.notice = msg.Notice
			a.noticeTTL = 3
		}

	case shared.AgentCreatedMsg:
		a.agentList, _ = a.agentList.Update(msg)
		// Auto-select the newly created agent
		a.agentList.SelectAgentID(msg.Agent.ID)
		a.selectedAgentID = msg.Agent.ID
		a.conversation.SetAgentID(msg.Agent.ID)
		a.conversation.SetAgentName(msg.Agent.Name)
		// Select the new agent, load any messages (includes errors or "starting")
		if a.vault != nil {
			msgs, err := a.vault.GetMessages(msg.Agent.ID)
			if err == nil {
				a.conversation.LoadMessages(msgs)
			}
		}
		// Show thinking if agent is active (not error)
		if msg.Agent.Status == "active" {
			a.conversation.SetThinking(true)
			cmds = append(cmds, conversation.ThinkingTick())
			a.notice = "Agent starting — waiting for response..."
			a.pendingChatFocusAgentID = msg.Agent.ID
		} else if msg.Agent.Status == "error" {
			a.conversation.SetThinking(false)
			a.notice = "Agent created with errors — check conversation"
			a.pendingChatFocusAgentID = ""
		} else {
			a.conversation.SetThinking(false)
			a.pendingChatFocusAgentID = ""
		}
		agentCopy := msg.Agent
		a.updateAgentDetail(&agentCopy)
		a.focus = FocusConversation
		a.inputMode = "chat"
		a.conversation.SetAwake(msg.Agent.Status != "active")
		a.applyConversationStatus(msg.Agent.Status)
		a.noticeTTL = 3
		cmds = append(cmds, a.conversation.Focus())
		cmds = append(cmds, a.waitForEvent())

	case shared.AgentDeletedMsg:
		a.agentList.RemoveAgent(msg.AgentID)
		// Remove tokens for deleted agent
		delete(a.agentTokens, msg.AgentID)
		var total int64
		for _, t := range a.agentTokens {
			total += int64(t)
		}
		a.systemInfo.SetSessionTokens(total)
		// If deleted agent was selected, clear selection
		if a.selectedAgentID == msg.AgentID {
			a.selectedAgentID = ""
			a.conversation.SetAgentID("")
			a.agentDetail.Clear()
			// Select next agent if any
			newID := a.agentList.SelectedAgentID()
			if newID != "" {
				a.selectedAgentID = newID
				a.conversation.SetAgentID(newID)
				if a.vault != nil {
					status := ""
					if agent, err := a.vault.GetAgent(newID); err == nil {
						a.conversation.SetAgentName(agent.Name)
						status = agent.Status
					}
					msgs, err := a.vault.GetMessages(newID)
					if err == nil {
						a.conversation.LoadMessages(msgs)
					}
					if status != "" {
						a.applyConversationStatus(status)
					}
				}
				a.refreshAgentDetail(newID)
			}
		}
		if a.shouldCreateDefaultAgent() {
			a.prepareDefaultChatPlaceholder()
			cmds = append(cmds, a.createDefaultAgent())
		}
		a.notice = "Agent deleted"
		a.noticeTTL = 3

	case shared.BulkAgentStatusMsg:
		flushedQueuedTurn := false
		for _, agentID := range msg.AgentIDs {
			a.agentList.UpdateStatus(agentID, msg.Status)
			if a.vault != nil {
				a.vault.UpdateAgentStatus(agentID, msg.Status)
			}
			if agentID == a.selectedAgentID {
				if isLiveAgentStatus(msg.Status) {
					a.conversation.SetThinking(true)
					cmds = append(cmds, conversation.ThinkingTick())
				} else {
					a.conversation.SetThinking(false)
				}
				if a.control != nil && a.vault == nil {
					a.updateRemoteAgentDetailFromList(agentID)
				} else {
					a.refreshAgentDetail(agentID)
				}
			}
			if cmd := a.flushQueuedChatTurnAfterStatus(agentID, msg.Status); cmd != nil {
				flushedQueuedTurn = true
				cmds = append(cmds, cmd)
			}
		}
		if !flushedQueuedTurn {
			a.notice = msg.Notice
			a.noticeTTL = 3
		}

	case shared.SettingsClosedMsg:
		cmds = append(cmds, a.focusChatComposer())
	case shared.SettingsNoticeMsg:
		a.notice = msg.Message
		a.noticeTTL = 3
	case shared.ReconfigureRequestedMsg:
		return a, a.startEmbeddedReconfigure()

	case shared.ProxyAddMsg:
		if a.control != nil {
			cmds = append(cmds, a.remoteProxyAdd(msg.URL))
			break
		}
		if a.vault == nil {
			a.notice = "Proxy changes unavailable without local vault access"
			a.noticeTTL = 3
			break
		}
		pc, err := proxy.ParseProxyURL(msg.URL)
		if err != nil {
			a.notice = "Invalid proxy: " + err.Error()
			a.noticeTTL = 3
		} else {
			configJSON, _ := json.Marshal(pc)
			a.vault.AddProxy(string(configJSON), "", pc.String())
			a.notice = "Proxy added: " + pc.String()
			a.noticeTTL = 3
		}
		a.reloadSettingsProxies()

	case shared.ProxyDeleteMsg:
		if a.control != nil {
			cmds = append(cmds, a.remoteProxyDelete(msg.ProxyID))
			break
		}
		if a.vault == nil {
			a.notice = "Proxy changes unavailable without local vault access"
			a.noticeTTL = 3
			break
		}
		a.vault.DeleteProxy(msg.ProxyID)
		a.notice = "Proxy deleted"
		a.noticeTTL = 3
		a.reloadSettingsProxies()

	case shared.SkillToggleMsg:
		if a.control != nil {
			cmds = append(cmds, a.remoteSkillSet(msg.Name, msg.Enabled))
			break
		}
		if a.cfg != nil {
			if msg.Enabled {
				a.cfg.AddGlobalSkill(msg.Name, nil)
			} else {
				a.cfg.RemoveGlobalSkill(msg.Name)
			}
			a.cfg.Save()
			state := "disabled"
			if msg.Enabled {
				state = "enabled"
			}
			a.notice = "Skill " + msg.Name + " " + state
			a.noticeTTL = 3
		}

	case shared.ProxyTestRequestMsg:
		if a.control != nil {
			cmds = append(cmds, a.remoteProxyTest(msg.ProxyID))
			break
		}
		cmds = append(cmds, a.testProxy(msg.ProxyID, msg.Config))

	case shared.ProxyTestedMsg:
		a.settings, _ = a.settings.Update(msg)

	case tea.MouseMsg:
		// Left-click on an agent row selects that agent.
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			rx, ry, rw, rh := a.agentListPanelRect()
			if rw > 0 && rh > 0 &&
				msg.X >= rx && msg.X < rx+rw &&
				msg.Y >= ry && msg.Y < ry+rh {
				if item, ok := a.agentList.ClickNearest(msg.Y, ry); ok {
					a.markAgentSelected(item.ID)
					cmds = append(cmds, a.selectCurrentAgent())
				}
				return a, tea.Batch(cmds...)
			}
		}
		if msg.Action == tea.MouseActionPress && (msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown) {
			rx, ry, rw, rh := a.agentListPanelRect()
			if rw > 0 && rh > 0 &&
				msg.X >= rx && msg.X < rx+rw &&
				msg.Y >= ry && msg.Y < ry+rh {
				delta := 1
				if msg.Button == tea.MouseButtonWheelUp {
					delta = -1
				}
				if item, ok := a.agentList.Scroll(delta); ok {
					a.markAgentSelected(item.ID)
					cmds = append(cmds, a.selectCurrentAgent())
				}
				return a, tea.Batch(cmds...)
			}
		}
		if handled, cmd := a.handleConversationSelectionMouse(msg); handled {
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return a, tea.Batch(cmds...)
		}
		// Forward mouse events (e.g. wheel scroll) to the conversation.
		if a.focus == FocusConversation || a.inputMode == "chat" {
			var cmd tea.Cmd
			a.conversation, cmd = a.conversation.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case conversation.ThinkingTickMsg:
		var cmd tea.Cmd
		a.conversation, cmd = a.conversation.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

	case selectionAutoScrollMsg:
		if a.selectionAutoScrollDir == 0 || !a.conversation.SelectionDragging() {
			return a, tea.Batch(cmds...)
		}
		a.extendConversationSelectionAtEdge(a.selectionAutoScrollDir, a.selectionAutoScrollCol)
		return a, selectionAutoScrollTick()

	case conversation.InputPulseTickMsg:
		var cmd tea.Cmd
		a.conversation, cmd = a.conversation.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

	case statusNotice:
		a.notice = msg.text
		a.noticeTTL = 3
	}

	return a, tea.Batch(cmds...)
}

func (a App) noticeTextWithAgentName(text, agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return text
	}
	display := a.agentDisplayName(agentID)
	if display == "" || display == agentID {
		return text
	}
	return strings.ReplaceAll(text, agentID, display)
}

func (a App) agentDisplayName(agentID string) string {
	if item, ok := a.agentList.Agent(agentID); ok && strings.TrimSpace(item.Name) != "" {
		return strings.TrimSpace(item.Name)
	}
	if a.vault != nil {
		if agent, err := a.vault.GetAgent(agentID); err == nil && agent != nil && strings.TrimSpace(agent.Name) != "" {
			return strings.TrimSpace(agent.Name)
		}
	}
	return agentID
}

func (a App) statusNoticeText() string {
	if strings.TrimSpace(a.notice) != "" {
		return a.notice
	}
	return a.persistentWarning
}

func (a *App) clearPersistentWarning() {
	if len(a.observationWarnings) > 0 {
		if a.dismissedWarnings == nil {
			a.dismissedWarnings = make(map[string]string)
		}
		for key, event := range a.observationWarnings {
			a.dismissedWarnings[key] = runtimeObservationEventSignature(event)
		}
	}
	a.persistentWarning = ""
	a.persistentWarningAgent = ""
	if a.observationWarnings != nil {
		clear(a.observationWarnings)
	}
}

func (a *App) applyRuntimeObservationEvent(event vault.RuntimeEvent) {
	if !strings.EqualFold(strings.TrimSpace(event.Component), "agentcore") {
		return
	}
	if a.observationWarnings == nil {
		a.observationWarnings = make(map[string]vault.RuntimeEvent)
	}
	if a.dismissedWarnings == nil {
		a.dismissedWarnings = make(map[string]string)
	}
	key := runtimeObservationEventKey(event)
	switch event.Event {
	case "browser_observation_observed":
		if key == "" {
			clear(a.observationWarnings)
			clear(a.dismissedWarnings)
		} else {
			delete(a.observationWarnings, key)
			delete(a.dismissedWarnings, key)
		}
	case "browser_observation_unverified", "browser_observation_lost":
		signature := runtimeObservationEventSignature(event)
		if a.dismissedWarnings[key] == signature {
			a.refreshPersistentObservationWarning()
			return
		}
		delete(a.dismissedWarnings, key)
		a.observationWarnings[key] = event
	}
	a.refreshPersistentObservationWarning()
}

func runtimeObservationEventKey(event vault.RuntimeEvent) string {
	if agentID := strings.TrimSpace(event.Metadata["agent_id"]); agentID != "" {
		return agentID
	}
	return "__global__"
}

func runtimeObservationEventSignature(event vault.RuntimeEvent) string {
	if event.ID > 0 {
		return fmt.Sprintf("id:%d", event.ID)
	}
	metadata, _ := json.Marshal(event.Metadata)
	return fmt.Sprintf("event:%s|message:%s|timestamp:%s|metadata:%s", event.Event, event.Message, event.Timestamp.Format(time.RFC3339Nano), metadata)
}

func (a *App) refreshPersistentObservationWarning() {
	var latest vault.RuntimeEvent
	found := false
	for _, event := range a.observationWarnings {
		if !found || event.Timestamp.After(latest.Timestamp) || (event.Timestamp.Equal(latest.Timestamp) && event.ID > latest.ID) {
			latest = event
			found = true
		}
	}
	if !found {
		a.persistentWarning = ""
		a.persistentWarningAgent = ""
		return
	}
	a.persistentWarningAgent = strings.TrimSpace(latest.Metadata["agent_id"])
	a.persistentWarning = a.runtimeObservationWarningText(latest)
}

func (a App) runtimeObservationWarningText(event vault.RuntimeEvent) string {
	agentID := strings.TrimSpace(event.Metadata["agent_id"])
	name := a.agentDisplayName(agentID)
	if name == "" {
		name = "Agent"
	}
	state := "observation unverified"
	if event.Event == "browser_observation_lost" {
		state = "observation lost"
	}
	var detail string
	if tool := strings.TrimSpace(event.Metadata["last_failed_tool"]); tool != "" {
		detail = " after " + tool
	}
	if url := strings.TrimSpace(event.Metadata["url"]); url != "" {
		if detail != "" {
			detail += " at " + url
		} else {
			detail = " at " + url
		}
	} else if summary := strings.TrimSpace(event.Metadata["summary"]); summary != "" {
		detail = ": " + summary
	}
	return fmt.Sprintf("%s: browser %s%s", name, state, detail)
}

// updateNameInput handles keystrokes in "new-agent-name" mode.
func (a App) updateNameInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return a.handleCtrlC()
	case "enter":
		name := strings.TrimSpace(a.nameInput.Value())
		if name != "" {
			// Agents no longer need a separate task — create immediately and go
			// straight to chat. The name doubles as the task label.
			cmd := a.createAgent(name, name, a.newAgentContext)
			a.inputMode = ""
			a.newAgentName = ""
			a.newAgentContext = ""
			a.nameInput.Blur()
			a.nameInput.Reset()
			return a, cmd
		}
		return a, nil
	case "esc":
		a.inputMode = ""
		a.newAgentName = ""
		a.newAgentContext = ""
		a.nameInput.Blur()
		a.nameInput.Reset()
		return a, nil
	default:
		var cmd tea.Cmd
		a.nameInput, cmd = a.nameInput.Update(msg)
		return a, cmd
	}
}

func isTerminalAgentStatus(status string) bool {
	switch status {
	case "completed", "error", "failed", "interrupted":
		return true
	default:
		return false
	}
}

func isLiveAgentStatus(status string) bool {
	switch status {
	case "active", "running", "thinking", "starting":
		return true
	default:
		return false
	}
}

func isReadyChatStatus(status string) bool {
	switch status {
	case "ready", "created":
		return true
	default:
		return false
	}
}

func (a *App) applyConversationStatus(status string) {
	a.conversation.SetAgentStatus(status)
	switch {
	case isLiveAgentStatus(status):
		a.conversation.SetThinking(true)
	case isReadyChatStatus(status):
		a.conversation.SetThinking(false)
		a.conversation.SetAwake(true)
	case status == "paused" || isTerminalAgentStatus(status):
		a.conversation.SetThinking(false)
	default:
		a.conversation.SetThinking(false)
	}
}

func isGlobalLifecycleKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "ctrl+c":
		return true
	default:
		return false
	}
}

func isCopySelectionMsg(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return isCopySelectionKey(msg)
	case fmt.Stringer:
		return hostGOOS == "darwin" && isMacCommandCSequence(msg.String())
	default:
		return false
	}
}

func isCopySelectionKey(msg tea.KeyMsg) bool {
	if msg.Paste {
		return false
	}
	if hostGOOS != "darwin" {
		return false
	}
	switch msg.String() {
	case "cmd+c", "super+c", "meta+c":
		return true
	}
	return msg.Type == tea.KeyRunes && isMacCommandCSequence(string(msg.Runes))
}

func isMacCommandCSequence(raw string) bool {
	if decoded, ok := decodeBubbleTeaUnknownCSI(raw); ok {
		raw = decoded
	}
	raw = strings.TrimPrefix(raw, "\x1b")
	raw = strings.TrimPrefix(raw, "[")
	if raw == "" {
		return false
	}
	final := raw[len(raw)-1]
	if final != 'u' && final != '~' {
		return false
	}
	body := raw[:len(raw)-1]
	parts := strings.Split(body, ";")
	if len(parts) >= 3 && parts[0] == "27" {
		modifier, err := strconv.Atoi(parts[1])
		if err != nil {
			return false
		}
		keyCode, err := strconv.Atoi(parts[2])
		if err != nil {
			return false
		}
		return isCKeyCode(keyCode) && modifierHasSuper(modifier)
	}
	if len(parts) >= 2 {
		keyCode, err := strconv.Atoi(parts[0])
		if err != nil {
			return false
		}
		modifier, err := strconv.Atoi(parts[1])
		if err != nil {
			return false
		}
		return isCKeyCode(keyCode) && modifierHasSuper(modifier)
	}
	return false
}

func decodeBubbleTeaUnknownCSI(raw string) (string, bool) {
	const prefix = "?CSI["
	const suffix = "]?"
	if !strings.HasPrefix(raw, prefix) || !strings.HasSuffix(raw, suffix) {
		return "", false
	}
	fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(raw, prefix), suffix))
	if len(fields) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil || value < 0 || value > 255 {
			return "", false
		}
		b.WriteByte(byte(value))
	}
	return b.String(), true
}

func isCKeyCode(code int) bool {
	return code == 'c' || code == 'C'
}

func modifierHasSuper(modifier int) bool {
	const superBit = 8
	return modifier > 1 && (modifier-1)&superBit != 0
}

func isLeakedTerminalMouseReportKey(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes && isOnlyTerminalMouseReports(string(msg.Runes))
}

func (a *App) filterLeakedTerminalMouseReportKey(msg tea.KeyMsg) (tea.KeyMsg, bool) {
	if msg.Type != tea.KeyRunes {
		a.pendingTerminalMouseReport = ""
		return msg, false
	}
	raw := string(msg.Runes)
	if raw == "" {
		return msg, false
	}
	if msg.Paste && a.pendingTerminalMouseReport == "" && !isOnlyTerminalMouseReports(raw) && !isTerminalMouseReportPrefix(raw) {
		return msg, false
	}

	combined := raw
	if a.pendingTerminalMouseReport != "" {
		combined = a.pendingTerminalMouseReport + raw
		a.pendingTerminalMouseReport = ""
	}

	if rest, ok := consumeLeadingTerminalMouseReports(combined); ok {
		if rest == "" {
			return msg, true
		}
		msg.Runes = []rune(rest)
		msg.Paste = false
		return msg, false
	}

	if isTerminalMouseReportPrefix(combined) {
		a.pendingTerminalMouseReport = combined
		return msg, true
	}

	if combined != raw {
		msg.Runes = []rune(combined)
		msg.Paste = false
	}
	return msg, false
}

func isOnlyTerminalMouseReports(s string) bool {
	if s == "" {
		return false
	}
	for s != "" {
		next, ok := consumeTerminalMouseReport(s)
		if !ok {
			return false
		}
		s = next
	}
	return true
}

func consumeLeadingTerminalMouseReports(s string) (string, bool) {
	consumed := false
	for s != "" {
		next, ok := consumeTerminalMouseReport(s)
		if !ok {
			break
		}
		consumed = true
		s = next
	}
	return s, consumed
}

func consumeTerminalMouseReport(s string) (string, bool) {
	if next, ok := consumeSGRMouseReport(s); ok {
		return next, true
	}
	return consumeLegacyMouseReport(s)
}

func isTerminalMouseReportPrefix(s string) bool {
	return isSGRMouseReportPrefix(s) || isLegacyMouseReportPrefix(s)
}

func isSGRMouseReportPrefix(s string) bool {
	const minBracketPrefixLen = len("[<")
	markers := []string{"\x1b[<", "\u009b<", "[<"}
	for _, marker := range markers {
		if len(s) < len(marker) {
			if marker == "[<" && len(s) < minBracketPrefixLen {
				continue
			}
			if strings.HasPrefix(marker, s) {
				return true
			}
			continue
		}
		if !strings.HasPrefix(s, marker) {
			continue
		}
		rest := s[len(marker):]
		for field := 0; field < 3; field++ {
			if rest == "" {
				return true
			}
			digits := 0
			for rest != "" && isASCIIDigit(rest[0]) {
				rest = rest[1:]
				digits++
			}
			if digits == 0 {
				return false
			}
			if field < 2 {
				if rest == "" {
					return true
				}
				if rest[0] != ';' {
					return false
				}
				rest = rest[1:]
				continue
			}
			if rest == "" {
				return true
			}
			return false
		}
	}
	return false
}

func isLegacyMouseReportPrefix(s string) bool {
	markers := []string{"\x1b[M", "\u009bM", "[M"}
	for _, marker := range markers {
		if len(s) < len(marker) {
			if marker == "[M" && len(s) < len("[M") {
				continue
			}
			if strings.HasPrefix(marker, s) {
				return true
			}
			continue
		}
		if !strings.HasPrefix(s, marker) {
			continue
		}
		return len([]rune(s[len(marker):])) < 3
	}
	return false
}

func consumeSGRMouseReport(s string) (string, bool) {
	switch {
	case strings.HasPrefix(s, "\x1b["):
		s = s[2:]
	case strings.HasPrefix(s, "\u009b"):
		s = strings.TrimPrefix(s, "\u009b")
	case strings.HasPrefix(s, "["):
		s = s[1:]
	default:
		return "", false
	}
	if !strings.HasPrefix(s, "<") {
		return "", false
	}
	s = s[1:]
	for field := 0; field < 3; field++ {
		if s == "" || !isASCIIDigit(s[0]) {
			return "", false
		}
		for s != "" && isASCIIDigit(s[0]) {
			s = s[1:]
		}
		if field < 2 {
			if s == "" || s[0] != ';' {
				return "", false
			}
			s = s[1:]
		}
	}
	if s == "" || (s[0] != 'M' && s[0] != 'm') {
		return "", false
	}
	return s[1:], true
}

func consumeLegacyMouseReport(s string) (string, bool) {
	switch {
	case strings.HasPrefix(s, "\x1b[M"):
		s = s[3:]
	case strings.HasPrefix(s, "\u009bM"):
		s = strings.TrimPrefix(s, "\u009bM")
	case strings.HasPrefix(s, "[M"):
		s = s[2:]
	default:
		return "", false
	}
	runes := []rune(s)
	if len(runes) < 3 {
		return "", false
	}
	return string(runes[3:]), true
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// updateDescInput handles keystrokes in "new-agent-desc" mode.
func (a App) updateDescInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return a.handleCtrlC()
	case "enter":
		desc := strings.TrimSpace(a.taskInput.Value())
		if desc == "" {
			desc = a.newAgentName // use name as description if empty
		}
		cmd := a.createAgent(a.newAgentName, desc, a.newAgentContext)
		a.inputMode = ""
		a.newAgentName = ""
		a.newAgentContext = ""
		a.taskInput.Blur()
		a.taskInput.Reset()
		return a, cmd
	case "esc":
		a.inputMode = ""
		a.newAgentName = ""
		a.newAgentContext = ""
		a.taskInput.Blur()
		a.taskInput.Reset()
		return a, nil
	default:
		var cmd tea.Cmd
		a.taskInput, cmd = a.taskInput.Update(msg)
		return a, cmd
	}
}

func (a App) updateRenameInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return a.handleCtrlC()
	case "enter":
		newName := strings.TrimSpace(a.renameInput.Value())
		renameAgentID := a.renameAgentID
		if newName != "" && renameAgentID != "" {
			if err := a.vault.UpdateAgentName(renameAgentID, newName); err != nil {
				a.notice = "Failed to rename agent: " + err.Error()
				a.noticeTTL = 3
			} else {
				a.agentList.UpdateName(renameAgentID, newName)
				if renameAgentID == a.selectedAgentID {
					a.conversation.SetAgentName(newName)
				}
				a.notice = "Agent renamed to \"" + newName + "\""
				a.noticeTTL = 3
			}
		}
		a.inputMode = ""
		a.renameAgentID = ""
		a.renameInput.Blur()
		a.renameInput.Reset()
		return a, nil
	case "esc":
		a.inputMode = ""
		a.renameAgentID = ""
		a.renameInput.Blur()
		a.renameInput.Reset()
		return a, nil
	default:
		var cmd tea.Cmd
		a.renameInput, cmd = a.renameInput.Update(msg)
		return a, cmd
	}
}

// updateChatInput handles keystrokes in "chat" mode.
func (a App) updateChatInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The command palette owns input while open (it filters from the chat box).
	if a.commandPalette.Active() {
		return a.updateCommandPaletteInput(msg)
	}

	if msg.Type == tea.KeyRunes && msg.Paste {
		if !a.conversation.Focused() {
			a.conversation.Focus()
		}
		a.conversation.InsertPastedContent(string(msg.Runes))
		return a, nil
	}

	// "/" on an empty input opens the slash-command palette.
	if msg.String() == "/" && strings.TrimSpace(a.conversation.DraftValue()) == "" {
		a.syncCommandPaletteAgents()
		a.commandPalette.Activate()
		return a.updateCommandPaletteInput(msg)
	}

	switch msg.String() {
	case "ctrl+c":
		return a.handleCtrlC()
	case "enter":
		text, displayText := a.conversation.InputPayloadAndDisplay()
		if text != "" && a.selectedAgentID != "" {
			displayContent := operatorDisplayContent(text, displayText)
			if a.selectedAgentBusy() {
				a.appendQueuedChatTurn(a.selectedAgentID, text, displayContent)
				a.conversation.SetThinking(true)
				a.notice = a.queuedChatNotice(a.selectedAgentID)
				a.noticeTTL = 4
				return a, conversation.ThinkingTick()
			}
			// Add to conversation view + show thinking with animation
			a.conversation.AddEntryWithDisplay("user", text, displayContent)
			a.conversation.ForceScrollToBottom()
			a.conversation.SetThinking(true)
			// Save to vault
			if a.vault != nil {
				a.vault.AppendMessageWithDisplay(a.selectedAgentID, "user", text, displayContent, 0)
			}
			// Run one agent turn
			cmd := a.sendMessageToAgent(a.selectedAgentID, text, displayContent)
			return a, tea.Batch(cmd, conversation.ThinkingTick())
		}
		return a, nil
	case "esc":
		if a.selectedAgentID != "" && a.hasQueuedChatTurn(a.selectedAgentID) {
			return a.handleQueuedChatInterrupt()
		}
		return a, a.cancelChatSelectionAndFocus()
	default:
		if !a.conversation.Focused() {
			a.conversation.Focus()
		}
		return a, a.conversation.UpdateDraftInput(msg)
	}
}

// updateCommandPaletteInput routes keys while the slash-command palette is open.
// The chat draft keeps the typed query (so the user sees "/foo") and each
// keystroke re-filters the palette; navigation/selection keys drive the palette.
func (a App) updateCommandPaletteInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return a.handleCtrlC()
	case "esc", "enter":
		var cmd tea.Cmd
		a.commandPalette, cmd = a.commandPalette.Update(msg)
		a.conversation.ResetDraft()
		return a, cmd
	case "up", "down":
		var cmd tea.Cmd
		a.commandPalette, cmd = a.commandPalette.Update(msg)
		return a, cmd
	}

	if !a.conversation.Focused() {
		a.conversation.Focus()
	}
	cmd := a.conversation.UpdateDraftInput(msg)
	if strings.TrimSpace(a.conversation.DraftValue()) == "" {
		a.commandPalette.Deactivate()
		return a, cmd
	}
	a.commandPalette.SetQuery(a.conversation.DraftValue())
	return a, cmd
}

// syncCommandPaletteAgents loads the most-recently-selected agents into the
// palette as quick-switch shortcuts (top 3 by last-selected time).
func (a *App) syncCommandPaletteAgents() {
	a.commandPalette.SetAgents(a.topRecentAgents(3))
}

// topRecentAgents returns up to n agents ordered by most-recently selected.
func (a App) topRecentAgents(n int) []commandpalette.Agent {
	items := a.agentList.Agents()
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].LastSelectedAt.After(items[j].LastSelectedAt)
	})
	if len(items) > n {
		items = items[:n]
	}
	out := make([]commandpalette.Agent, 0, len(items))
	for _, it := range items {
		out = append(out, commandpalette.Agent{ID: it.ID, Name: it.Name, Status: it.Status})
	}
	return out
}

// markAgentSelected records a switch to the given agent for recency ranking.
func (a *App) markAgentSelected(agentID string) {
	if a.vault != nil {
		_ = a.vault.MarkAgentSelected(agentID, time.Now())
	}
	a.syncCommandPaletteAgents()
}

// openAgentPicker opens the full agent-picker modal over all agents.
func (a *App) openAgentPicker() {
	items := a.agentList.Agents()
	agents := make([]commandpalette.Agent, 0, len(items))
	for _, it := range items {
		agents = append(agents, commandpalette.Agent{ID: it.ID, Name: it.Name, Status: it.Status})
	}
	picker := agentpicker.New(agents)
	if a.width > 0 && a.height > 0 {
		picker.SetSize(a.width, a.height)
	}
	a.agentPicker = picker
}

// openSettings opens the settings panel (remote or local) and loads proxies.
func (a *App) openSettings() tea.Cmd {
	if a.control != nil {
		a.notice = "Loading remote settings..."
		a.noticeTTL = 3
		return a.loadRemoteSettings()
	}
	a.focus = FocusSettings
	a.settings.SetActive(true)
	a.settings.SetConfig(a.cfg)
	if a.vault != nil {
		if storedProxies, err := a.vault.ListProxies(); err == nil {
			items := make([]settings.ProxyItem, len(storedProxies))
			for i, sp := range storedProxies {
				items[i] = settings.ProxyItem{
					ID:      sp.ID,
					Label:   safeProxyLabel(sp.Label),
					Config:  sp.Config,
					Latency: "untested",
				}
				var pc struct {
					Type string `json:"type"`
					Host string `json:"host"`
					Port int    `json:"port"`
				}
				if json.Unmarshal([]byte(sp.Config), &pc) == nil {
					items[i].Type = pc.Type
					items[i].Host = pc.Host
					items[i].Port = pc.Port
				}
				var geo struct {
					Country string `json:"country"`
				}
				if json.Unmarshal([]byte(sp.Geo), &geo) == nil {
					items[i].Country = geo.Country
				}
			}
			a.settings.SetProxies(items)
		}
	}
	return nil
}

// dispatchCommand runs a command selected from the slash-command palette,
// mapping each name to the same action as its keyboard shortcut.
func (a *App) dispatchCommand(name, rawInput string) tea.Cmd {
	switch name {
	case "help":
		a.syncCommandPaletteAgents()
		a.commandPalette.Activate()
	case "quit":
		return a.shutdown()
	case "new":
		if a.orch != nil || a.control != nil {
			a.newAgentContext = ""
			a.inputMode = "new-agent-name"
			a.nameInput.Focus()
			return textinput.Blink
		}
		a.notice = "No orchestrator available"
		a.noticeTTL = 3
	case "rename":
		if a.selectedAgentID == "" {
			a.notice = "No agent selected"
			a.noticeTTL = 3
			break
		}
		if a.vault == nil {
			a.notice = "Rename unavailable without local vault access"
			a.noticeTTL = 3
			break
		}
		agent, err := a.vault.GetAgent(a.selectedAgentID)
		if err != nil {
			a.notice = "Failed to get agent: " + err.Error()
			a.noticeTTL = 3
			break
		}
		a.renameAgentID = a.selectedAgentID
		a.renameInput.SetValue(agent.Name)
		a.inputMode = "rename"
		a.renameInput.Focus()
		return textinput.Blink
	case "delete":
		if a.selectedAgentID == "" {
			a.notice = "No agent selected"
			a.noticeTTL = 3
			break
		}
		if a.control != nil && !isLiveAgentStatus(a.selectedAgentStatus()) {
			a.notice = "Remote kill is only available for live agents"
			a.noticeTTL = 3
			break
		}
		return a.deleteAgent(a.selectedAgentID)
	case "copy":
		return a.handleYankResponse()
	case "view":
		return a.handleBrowserToggle()
	case "hide":
		return a.handleHideAll()
	case "log":
		return a.handleOpenSessionLog()
	case "trace":
		a.handleTraceToggle()
	case "recover":
		return a.handleObservationRecover()
	case "clear-warning":
		a.clearPersistentWarning()
		a.notice = "Warning cleared"
		a.noticeTTL = 3
	case "settings":
		return a.openSettings()
	case "config", "model":
		return a.startEmbeddedReconfigure()
	case "agents":
		a.openAgentPicker()
	}
	return nil
}

func operatorDisplayContent(content, displayContent string) string {
	displayContent = strings.TrimSpace(displayContent)
	if displayContent == "" || displayContent == strings.TrimSpace(content) {
		return ""
	}
	return displayContent
}

func (a *App) handleBrowserToggle() tea.Cmd {
	if a.kernel == nil || a.kernel.Window() == nil {
		a.notice = "Browser not available"
		a.noticeTTL = 3
		return nil
	}

	contextID, notice := a.browserToggleContextID()
	if contextID == "" {
		a.notice = notice
		a.noticeTTL = 3
		return nil
	}

	a.notice = "Updating context window..."
	a.noticeTTL = 3
	return browserToggleCmd(a.kernel.Window(), contextID)
}

func (a *App) handleObservationRecover() tea.Cmd {
	if strings.TrimSpace(a.selectedAgentID) == "" {
		a.notice = "Select an agent first"
		a.noticeTTL = 3
		return nil
	}
	var cmds []tea.Cmd
	if a.kernel != nil && a.kernel.Window() != nil {
		if contextID := a.selectedAgentBrowserContextID(); contextID != "" {
			cmds = append(cmds, browserShowCmd(a.kernel.Window(), contextID))
		}
	}
	if isLiveAgentStatus(a.selectedAgentStatus()) {
		a.notice = "Browser shown; recover after the current turn finishes"
		a.noticeTTL = 4
		return tea.Batch(cmds...)
	}
	a.notice = "Recovering browser observation..."
	a.noticeTTL = 3
	prompt := observationRecoverPrompt()
	display := "/recover: observe current browser state"
	if a.control == nil {
		a.recordLocalOperatorMessage(a.selectedAgentID, prompt, display)
		cmds = append(cmds, conversation.ThinkingTick())
	}
	cmds = append(cmds, a.sendMessageToAgent(a.selectedAgentID, prompt, display))
	return tea.Batch(cmds...)
}

func (a *App) recordLocalOperatorMessage(agentID, text, displayContent string) {
	if strings.TrimSpace(agentID) == "" || strings.TrimSpace(text) == "" {
		return
	}
	if agentID == a.selectedAgentID {
		a.conversation.AddEntryWithDisplay("user", text, displayContent)
		a.conversation.ForceScrollToBottom()
		a.conversation.SetThinking(true)
	}
	if a.vault != nil {
		a.vault.AppendMessageWithDisplay(agentID, "user", text, displayContent, 0)
	}
}

func (a App) selectedAgentBusy() bool {
	return a.conversation.IsThinking() || isLiveAgentStatus(a.selectedAgentStatus())
}

func (a *App) appendQueuedChatTurn(agentID, text, displayContent string) {
	if strings.TrimSpace(agentID) == "" || strings.TrimSpace(text) == "" {
		return
	}
	if a.queuedChatTurns == nil {
		a.queuedChatTurns = make(map[string][]queuedChatTurn)
	}
	a.queuedChatTurns[agentID] = append(a.queuedChatTurns[agentID], queuedChatTurn{
		AgentID:        agentID,
		Content:        text,
		DisplayContent: displayContent,
	})
}

func (a App) queuedChatCount(agentID string) int {
	return len(a.queuedChatTurns[agentID])
}

func (a App) hasQueuedChatTurn(agentID string) bool {
	return a.queuedChatCount(agentID) > 0
}

func (a *App) popQueuedChatTurn(agentID string) (queuedChatTurn, bool) {
	queue := a.queuedChatTurns[agentID]
	if len(queue) == 0 {
		return queuedChatTurn{}, false
	}
	turn := queue[0]
	if len(queue) == 1 {
		delete(a.queuedChatTurns, agentID)
	} else {
		a.queuedChatTurns[agentID] = append([]queuedChatTurn(nil), queue[1:]...)
	}
	return turn, true
}

func (a App) queuedChatNotice(agentID string) string {
	count := a.queuedChatCount(agentID)
	if count <= 1 {
		return "Message queued - press Esc to interrupt and send it now"
	}
	return fmt.Sprintf("%d messages queued - press Esc to interrupt and send them now", count)
}

func (a App) queuedChatSentNotice(agentID string) string {
	remaining := a.queuedChatCount(agentID)
	if remaining > 0 {
		return fmt.Sprintf("Queued message sent (%d remaining)", remaining)
	}
	return "Queued message sent"
}

func (a *App) flushNextQueuedChatTurn(agentID string) tea.Cmd {
	turn, ok := a.popQueuedChatTurn(agentID)
	if !ok {
		return nil
	}
	a.recordLocalOperatorMessage(agentID, turn.Content, turn.DisplayContent)
	return tea.Batch(a.sendMessageToAgent(agentID, turn.Content, turn.DisplayContent), conversation.ThinkingTick())
}

func (a *App) flushQueuedChatTurnAfterStatus(agentID, status string) tea.Cmd {
	if isLiveAgentStatus(status) || !a.hasQueuedChatTurn(agentID) {
		return nil
	}
	cmd := a.flushNextQueuedChatTurn(agentID)
	if cmd != nil {
		a.notice = a.queuedChatSentNotice(agentID)
		a.noticeTTL = 3
	}
	return cmd
}

func (a App) handleQueuedChatInterrupt() (tea.Model, tea.Cmd) {
	agentID := a.selectedAgentID
	if agentID == "" || !a.hasQueuedChatTurn(agentID) {
		return a, a.cancelChatSelectionAndFocus()
	}
	if a.control != nil && isLiveAgentStatus(a.selectedAgentStatus()) {
		a.notice = "Interrupting agent - queued message will send next"
		a.noticeTTL = 4
		return a, a.remoteAgentStatusCommand("agents.kill", agentID, "interrupted", "Agent interrupted: ")
	}
	if a.orch != nil && isLiveAgentStatus(a.selectedAgentStatus()) {
		if err := a.orch.KillAgent(agentID); err != nil && !strings.Contains(err.Error(), "not found") {
			a.notice = "Interrupt failed: " + err.Error()
			a.noticeTTL = 4
			return a, nil
		}
		a.conversation.AddEntry("system", "Agent interrupted")
	}
	cmd := a.flushNextQueuedChatTurn(agentID)
	if cmd != nil {
		a.quitConfirmArmed = false
		a.notice = a.queuedChatSentNotice(agentID)
		a.noticeTTL = 3
	}
	return a, cmd
}

func (a App) browserToggleContextID() (string, string) {
	if a.focus == FocusContextList {
		if contextID := strings.TrimSpace(a.contextList.SelectedContextID()); contextID != "" {
			return contextID, ""
		}
		if contextID := a.selectedAgentBrowserContextID(); contextID != "" {
			return contextID, ""
		}
		return "", "Select a context first"
	}

	if a.selectedAgentID == "" && a.agentList.SelectedAgentID() == "" {
		return "", "Select an agent first"
	}
	if contextID := a.selectedAgentBrowserContextID(); contextID != "" {
		return contextID, ""
	}
	return "", "Selected agent has no active browser context"
}

func (a App) selectedAgentBrowserContextID() string {
	agentID := strings.TrimSpace(a.selectedAgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(a.agentList.SelectedAgentID())
	}
	if agentID == "" {
		return ""
	}

	if contextID := strings.TrimSpace(a.liveAgentContexts[agentID]); contextID != "" {
		return contextID
	}
	if a.vault != nil {
		if agent, err := a.vault.GetAgent(agentID); err == nil {
			if contextID := contextIDFromAgentMetadata(agent.Metadata); contextID != "" {
				return contextID
			}
		}
	}
	if item, ok := a.agentList.Agent(agentID); ok {
		if contextID := contextIDFromAgentMetadata(item.Metadata); contextID != "" {
			return contextID
		}
	}
	return ""
}

func contextIDFromAgentMetadata(metadata string) string {
	meta, err := vault.ParseAgentMetadata(metadata)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(meta.ContextID)
}

func browserToggleCmd(window browserContextWindow, contextID string) tea.Cmd {
	return func() tea.Msg {
		visible := window.IsContextVisible(contextID)
		if visible {
			if err := window.HideContext(contextID); err != nil {
				return statusNotice{text: "Failed to hide context: " + err.Error()}
			}
			return statusNotice{text: "Context hidden"}
		}
		if err := window.ShowContext(contextID); err != nil {
			return statusNotice{text: "Failed to show context: " + err.Error()}
		}
		return statusNotice{text: "Context shown"}
	}
}

func browserShowCmd(window browserContextWindow, contextID string) tea.Cmd {
	return func() tea.Msg {
		if window.IsContextVisible(contextID) {
			return statusNotice{text: "Context already visible"}
		}
		if err := window.ShowContext(contextID); err != nil {
			return statusNotice{text: "Failed to show context: " + err.Error()}
		}
		return statusNotice{text: "Context shown"}
	}
}

func observationRecoverPrompt() string {
	return `Recover browser state now. First call vulpine_observe with visual:true and profile:"compact". If the report is lost or unverified, retry navigation/observation or ask the user to take over. Do not infer success from failed tools.`
}

func (a *App) handleHideAll() tea.Cmd {
	if a.kernel == nil || a.kernel.Window() == nil {
		a.notice = "Browser not available"
		a.noticeTTL = 3
		return nil
	}

	a.notice = "Hiding all contexts..."
	a.noticeTTL = 3
	return browserHideAllCmd(a.kernel.Window())
}

func browserHideAllCmd(window browserAllWindow) tea.Cmd {
	return func() tea.Msg {
		if err := window.HideAll(); err != nil {
			return statusNotice{text: "Failed to hide all: " + err.Error()}
		}
		return statusNotice{text: "All contexts hidden"}
	}
}

func (a *App) handleOpenSessionLog() tea.Cmd {
	if a.selectedAgentID == "" {
		a.notice = "No agent selected"
		a.noticeTTL = 3
		return nil
	}
	if a.control != nil {
		agentID := a.selectedAgentID
		a.notice = "Loading remote session log..."
		a.noticeTTL = 3
		return a.remoteOpenSessionLog(agentID)
	}
	if a.vault == nil {
		a.notice = "Session log unavailable"
		a.noticeTTL = 4
		return nil
	}
	content, exists, err := a.vault.RenderSessionLog(a.selectedAgentID)
	if err != nil {
		a.notice = "Failed to read session log: " + err.Error()
		a.noticeTTL = 4
		return nil
	}
	if !exists {
		a.notice = "No session log yet for selected agent"
		a.noticeTTL = 4
		return nil
	}
	logPath := filepath.Join(os.TempDir(), "vulpineos-session-"+safeTempAgentID(a.selectedAgentID)+".jsonl")
	if err := os.WriteFile(logPath, content, 0600); err != nil {
		a.notice = "Failed to write session log: " + err.Error()
		a.noticeTTL = 4
		return nil
	}
	if err := openExternalTarget(logPath); err != nil {
		a.notice = "Failed to open session log: " + err.Error()
		a.noticeTTL = 4
		return nil
	}
	a.notice = "Opened session log"
	a.noticeTTL = 3
	return nil
}

func (a App) remoteOpenSessionLog(agentID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var result struct {
			Content string `json:"content"`
			Exists  bool   `json:"exists"`
		}
		if err := a.control.ControlCall(ctx, "agents.getSessionLog", map[string]any{
			"agentId":    agentID,
			"tail":       true,
			"limitBytes": int64(65536),
		}, &result); err != nil {
			return statusNotice{text: "Remote session log failed: " + err.Error()}
		}
		if !result.Exists && result.Content == "" {
			return statusNotice{text: "No session log yet for selected agent"}
		}
		path := filepath.Join(os.TempDir(), "vulpineos-remote-session-"+safeTempAgentID(agentID)+".jsonl")
		if err := os.WriteFile(path, []byte(result.Content), 0600); err != nil {
			return statusNotice{text: "Remote session log write failed: " + err.Error()}
		}
		if err := openExternalTarget(path); err != nil {
			return statusNotice{text: "Failed to open remote session log: " + err.Error()}
		}
		return statusNotice{text: "Opened remote session log"}
	}
}

func safeTempAgentID(agentID string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	id := strings.Trim(replacer.Replace(strings.TrimSpace(agentID)), "._")
	if id == "" {
		return "agent"
	}
	if len(id) > 80 {
		return id[:80]
	}
	return id
}

func (a *App) handleTraceToggle() {
	enabled := !a.conversation.TraceOnly()
	a.conversation.SetTraceOnly(enabled)
	if enabled {
		a.notice = "Trace mode enabled — showing tool actions and results"
	} else {
		a.notice = "Trace mode disabled — showing full conversation"
	}
	a.noticeTTL = 3
}

func (a *App) handleYankResponse() tea.Cmd {
	content := a.conversation.LatestAssistantContent()
	if content == "" {
		return func() tea.Msg {
			return statusNotice{text: "No agent response to copy"}
		}
	}
	return a.copyTextCommand(content, "Copied latest agent response")
}

func (a *App) browserWindowLabel() string {
	if a.kernel == nil {
		return ""
	}
	if a.kernel.IsHeadless() {
		return "HEADLESS"
	}
	w := a.kernel.Window()
	if w == nil {
		return "N/A"
	}
	visible, found := w.CachedStatus()
	if !found {
		return "N/A"
	}
	if visible {
		return "VISIBLE"
	}
	return "HIDDEN"
}

// detailHeight is the fixed height for the agent detail area.
// Min/max constraints for panel sizes
const (
	minSplit      = 5
	maxSplitRatio = 80 // percent of column height

	minCenterWidth        = 20
	panelHorizontalChrome = 2 // Lipgloss Width includes horizontal padding; the border adds 2 columns.
	workbenchPanelCount   = 2 // left + center (contexts/agent-detail right column removed)
)

func clampVerticalSplit(split, bodyHeight int) int {
	const minPanelHeight = 3
	maxTop := bodyHeight - minPanelHeight - 4
	if maxTop < minPanelHeight {
		return minPanelHeight
	}
	if split < minPanelHeight {
		return minPanelHeight
	}
	if split > maxTop {
		return maxTop
	}
	return split
}

func panelContentWidth(panelWidth int) int {
	width := panelWidth - 2
	if width < 1 {
		return 1
	}
	return width
}

func compactPanelWidth(terminalWidth int) int {
	width := terminalWidth - 4
	if width < 1 {
		return max(1, terminalWidth)
	}
	return width
}

func compactContentHeight(terminalHeight int) int {
	bodyHeight := workbenchBodyHeight(terminalHeight)
	contentHeight := bodyHeight - 2
	if contentHeight < 1 {
		return 1
	}
	return contentHeight
}

func workbenchBodyHeight(terminalHeight int) int {
	bodyHeight := terminalHeight - 1
	if bodyHeight < 1 {
		return 1
	}
	return bodyHeight
}

type workbenchWidths struct {
	left   int
	center int
	right  int
}

// agentListPanelRect returns the screen rectangle (x, y, w, h) of the agent-list
// panel in the full workbench layout, mirroring View()'s layout math. Returns a
// zero-width rect when the agent list isn't a clickable panel (compact layout).
func (a App) agentListPanelRect() (int, int, int, int) {
	if a.width <= 0 || a.height <= 0 {
		return 0, 0, 0, 0
	}
	widths := resolveWorkbenchWidths(a.width, a.leftWidth, a.rightWidth)
	bodyHeight := workbenchBodyHeight(a.height)
	if a.useCompactWorkbench(widths, bodyHeight) {
		return 0, 0, 0, 0
	}
	leftTop := a.leftSplit
	leftBottom := bodyHeight - leftTop - 4
	if leftBottom < 3 {
		leftBottom = 3
		leftTop = bodyHeight - leftBottom - 4
	}
	if leftTop < 3 {
		leftTop = 3
	}
	// System-info panel occupies leftTop+2 rows (content + top/bottom border);
	// the agent-list panel starts right below it.
	ry := leftTop + 2
	rh := leftBottom + 2
	return 0, ry, widths.left + 2, rh
}

func (a App) conversationContentRect() (int, int, int, int) {
	if a.width <= 0 || a.height <= 0 {
		return 0, 0, 0, 0
	}
	widths := resolveWorkbenchWidths(a.width, a.leftWidth, a.rightWidth)
	bodyHeight := workbenchBodyHeight(a.height)
	if a.useCompactWorkbench(widths, bodyHeight) {
		if a.focus != FocusConversation {
			return 0, 0, 0, 0
		}
		panelWidth := compactPanelWidth(a.width)
		return 1, 1, panelContentWidth(panelWidth), compactContentHeight(a.height)
	}
	if a.focus == FocusSettings && a.settings.IsActive() {
		return 0, 0, 0, 0
	}
	x := widths.left + 3 // left panel including border, then center panel left border
	y := 1
	return x, y, panelContentWidth(widths.center), bodyHeight - 2
}

func selectionAutoScrollTick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg {
		return selectionAutoScrollMsg{}
	})
}

func (a *App) stopConversationSelectionAutoScroll() {
	a.selectionAutoScrollDir = 0
	a.selectionAutoScrollCol = 0
}

func (a *App) startConversationSelectionAutoScroll(dir, col int) tea.Cmd {
	if dir == 0 {
		a.stopConversationSelectionAutoScroll()
		return nil
	}
	wasStopped := a.selectionAutoScrollDir == 0
	a.selectionAutoScrollDir = dir
	a.selectionAutoScrollCol = col
	a.extendConversationSelectionAtEdge(dir, col)
	if wasStopped {
		return selectionAutoScrollTick()
	}
	return nil
}

func (a *App) extendConversationSelectionAtEdge(dir, col int) bool {
	if dir == 0 {
		return false
	}
	a.conversation.ScrollBy(dir)
	first, last, ok := a.conversation.VisibleMessageRowBounds()
	if !ok {
		return false
	}
	row := first
	if dir > 0 {
		row = last
	}
	return a.conversation.ExtendSelectionAtViewCell(row, col)
}

func (a *App) handleConversationSelectionMouse(msg tea.MouseMsg) (bool, tea.Cmd) {
	if a.commandPalette.Active() || a.inputMode == "new-agent-name" || a.inputMode == "new-agent-desc" || a.inputMode == "rename" {
		return false, nil
	}
	rx, ry, rw, rh := a.conversationContentRect()
	if rw <= 0 || rh <= 0 {
		return false, nil
	}
	inside := msg.X >= rx && msg.X < rx+rw && msg.Y >= ry && msg.Y < ry+rh
	verticalInside := msg.Y >= ry && msg.Y < ry+rh
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft || !inside {
			return false, nil
		}
		a.stopConversationSelectionAutoScroll()
		if msg.Shift {
			a.quitConfirmArmed = false
			return true, nil
		}
		if !a.conversation.BeginSelectionAtViewCell(msg.Y-ry, msg.X-rx) {
			return false, nil
		}
		a.quitConfirmArmed = false
		return true, nil
	case tea.MouseActionMotion:
		if msg.Button != tea.MouseButtonLeft || !a.conversation.SelectionDragging() {
			return false, nil
		}
		if verticalInside {
			a.stopConversationSelectionAutoScroll()
			a.conversation.ExtendSelectionAtViewCell(msg.Y-ry, msg.X-rx)
			return true, nil
		}
		if msg.Y < ry {
			return true, a.startConversationSelectionAutoScroll(-1, msg.X-rx)
		}
		if msg.Y >= ry+rh {
			return true, a.startConversationSelectionAutoScroll(1, msg.X-rx)
		}
		return true, nil
	case tea.MouseActionRelease:
		if !a.conversation.SelectionDragging() {
			return false, nil
		}
		a.stopConversationSelectionAutoScroll()
		if verticalInside && a.conversation.HasSelection() {
			a.conversation.ExtendSelectionAtViewCell(msg.Y-ry, msg.X-rx)
		} else if msg.Y < ry && a.conversation.HasSelection() {
			a.extendConversationSelectionAtEdge(-1, msg.X-rx)
		} else if msg.Y >= ry+rh && a.conversation.HasSelection() {
			a.extendConversationSelectionAtEdge(1, msg.X-rx)
		}
		a.conversation.EndSelection()
		if hostGOOS == "darwin" {
			return true, a.copySelectedChatTextCommand("Copied selected chat text to clipboard")
		}
		return true, nil
	default:
		return false, nil
	}
}

// conversationView renders the conversation, overlaying the inline slash-command
// palette above the input when it is open.
func (a App) conversationView(width, height int) string {
	if !a.commandPalette.Active() {
		return a.conversation.View()
	}
	paletteWidth := width - 4
	if paletteWidth < 6 {
		paletteWidth = width
	}
	maxHeight := height / 2
	if maxHeight < 4 {
		maxHeight = 4
	}
	return a.conversation.ViewWithCommandPalette(a.commandPalette.InlineView(paletteWidth, maxHeight))
}

func (a App) View() string {
	if a.width == 0 {
		return "Loading..."
	}
	if a.height <= 0 {
		return ""
	}
	if a.setupActive {
		wizard := a.setupWizard
		model, _ := wizard.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height})
		if updated, ok := model.(*setup.Model); ok {
			wizard = updated
		}
		a.setupWizard = wizard
		return fitTerminalBlock(wizard.View(), a.width, a.height)
	}
	if a.agentPicker != nil {
		return fitTerminalBlock(a.agentPicker.View(), a.width, a.height)
	}
	if a.height < 4 {
		if notice := a.statusNoticeText(); notice != "" {
			return fitTerminalLine(shared.WarmingStyle.Render("  "+notice), a.width)
		}
		return fitTerminalLine(a.renderStatusBar(), a.width)
	}

	widths := resolveWorkbenchWidths(a.width, a.leftWidth, a.rightWidth)
	leftWidth := widths.left
	centerWidth := widths.center

	bodyHeight := workbenchBodyHeight(a.height)
	if a.useCompactWorkbench(widths, bodyHeight) {
		return a.renderCompactWorkbench()
	}

	// Left column: systemInfo (with pool stats) on top, agentList below
	leftTop := a.leftSplit
	leftBottom := bodyHeight - leftTop - 4 // subtract borders
	if leftBottom < 3 {
		leftBottom = 3
		leftTop = bodyHeight - leftBottom - 4
	}
	if leftTop < 3 {
		leftTop = 3
	}
	sysView := a.renderPanel(FocusAgentList, a.systemInfo.View(), leftWidth, leftTop)
	agentView := a.renderFocusPanel(FocusAgentList, a.agentList.View(), leftWidth, leftBottom)
	leftColumn := lipgloss.JoinVertical(lipgloss.Left, sysView, agentView)

	// Center column: settings panel OR full-height conversation
	var centerContent string
	if a.focus == FocusSettings && a.settings.IsActive() {
		settingsView := a.settings.View()
		maxContentLines := bodyHeight - 2
		settingsLines := strings.Split(settingsView, "\n")
		if maxContentLines > 0 && len(settingsLines) > maxContentLines {
			settingsLines = settingsLines[:maxContentLines]
			settingsView = strings.Join(settingsLines, "\n")
		}
		centerContent = shared.ActivePanelStyle.Width(centerWidth).Height(bodyHeight - 2).Render(settingsView)
	} else {
		// Check if we need to show agent creation inputs overlaid on conversation
		var convView string
		switch a.inputMode {
		case "new-agent-name", "new-agent-desc":
			convView = a.newAgentInputView()
		case "rename":
			convView = a.renameAgentInputView()
		default:
			convView = a.conversationView(centerWidth, bodyHeight)
		}

		// Full-height conversation panel
		convStyle := shared.PanelStyle
		if a.focus == FocusConversation {
			convStyle = shared.ActivePanelStyle
		}

		// Hard-truncate conversation content to prevent overflow
		maxContentLines := bodyHeight - 2 // subtract panel borders
		convLines := strings.Split(convView, "\n")
		if len(convLines) > maxContentLines {
			convLines = convLines[:maxContentLines]
			convView = strings.Join(convLines, "\n")
		}

		centerContent = convStyle.Width(centerWidth).Height(bodyHeight - 2).Render(convView)
	}
	centerView := centerContent

	// Hard-truncate the left column to bodyHeight lines
	leftLines := strings.Split(leftColumn, "\n")
	if len(leftLines) > bodyHeight {
		leftColumn = strings.Join(leftLines[:bodyHeight], "\n")
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftColumn, centerView)

	// Status bar (notice replaces status bar content when present)
	var statusBar string
	if notice := a.statusNoticeText(); notice != "" {
		statusBar = fitTerminalLine(shared.WarmingStyle.Render("  "+notice), a.width)
	} else {
		statusBar = fitTerminalLine(a.renderStatusBar(), a.width)
	}

	output := lipgloss.JoinVertical(lipgloss.Left, body, statusBar)

	// Final safety: hard-truncate to terminal height.
	// Keep the last line (status bar) and trim overflow from the top of the body
	// so the status bar is never cut off.
	outputLines := strings.Split(output, "\n")
	if len(outputLines) > a.height {
		excess := len(outputLines) - a.height
		outputLines = append(outputLines[excess:len(outputLines)-1], outputLines[len(outputLines)-1])
		output = strings.Join(outputLines, "\n")
	}
	return fitTerminalBlock(output, a.width, a.height)
}

func (a App) useCompactWorkbench(widths workbenchWidths, bodyHeight int) bool {
	if a.width < 48 || bodyHeight < 10 {
		return true
	}
	return widths.left < 6 || widths.center < minCenterWidth
}

func (a App) renderCompactWorkbench() string {
	bodyHeight := workbenchBodyHeight(a.height)
	if bodyHeight < 1 {
		return fitTerminalLine(a.renderStatusBar(), a.width)
	}
	panelWidth := compactPanelWidth(a.width)
	contentWidth := panelContentWidth(panelWidth)
	contentHeight := compactContentHeight(a.height)

	var content string
	panel := a.focus
	switch {
	case a.inputMode == "new-agent-name" || a.inputMode == "new-agent-desc":
		content = a.newAgentInputView()
	case a.inputMode == "rename":
		content = a.renameAgentInputView()
	case a.focus == FocusSettings && a.settings.IsActive():
		a.settings.SetSize(contentWidth, contentHeight)
		content = a.settings.View()
	case a.focus == FocusContextList:
		a.contextList.SetWidth(contentWidth)
		a.contextList.SetHeight(contentHeight)
		content = a.contextList.View()
	case a.focus == FocusAgentDetail:
		a.agentDetail.SetSize(contentWidth, contentHeight)
		content = a.agentDetail.View()
	case a.focus == FocusConversation:
		a.conversation.SetSize(contentWidth, contentHeight)
		content = a.conversationView(contentWidth, contentHeight)
	default:
		panel = FocusAgentList
		a.agentList.SetWidth(contentWidth)
		a.agentList.SetHeight(contentHeight)
		content = a.agentList.View()
	}

	contentLines := strings.Split(content, "\n")
	if len(contentLines) > contentHeight {
		content = strings.Join(contentLines[:contentHeight], "\n")
	}
	body := a.renderFocusPanel(panel, content, panelWidth, contentHeight)
	statusBar := fitTerminalLine(a.renderStatusBar(), a.width)
	if notice := a.statusNoticeText(); notice != "" {
		statusBar = fitTerminalLine(shared.WarmingStyle.Render("  "+notice), a.width)
	}
	return fitTerminalBlock(lipgloss.JoinVertical(lipgloss.Left, body, statusBar), a.width, a.height)
}

func (a App) newAgentInputView() string {
	switch a.inputMode {
	case "new-agent-name":
		return shared.TitleStyle.Render("NEW AGENT — NAME") + "\n\n" +
			a.nameInput.View() + "\n\n" +
			a.newAgentContextNotice() +
			shared.MutedStyle.Render("[Enter] create  [Esc] cancel")
	case "new-agent-desc":
		return shared.TitleStyle.Render("NEW AGENT — DESCRIPTION for "+a.newAgentName) + "\n\n" +
			a.taskInput.View() + "\n\n" +
			a.newAgentContextNotice() +
			shared.MutedStyle.Render("[Enter] create  [Esc] cancel")
	default:
		return ""
	}
}

func (a App) renameAgentInputView() string {
	return shared.TitleStyle.Render("RENAME AGENT") + "\n\n" +
		a.renameInput.View() + "\n\n" +
		shared.MutedStyle.Render("[Enter] save  [Esc] cancel")
}

func fitTerminalBlock(output string, width, height int) string {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		lines[i] = fitTerminalLine(line, width)
	}
	if height > 0 && len(lines) > height {
		excess := len(lines) - height
		lines = append(lines[excess:len(lines)-1], lines[len(lines)-1])
	}
	return strings.Join(lines, "\n")
}

func fitTerminalLine(line string, width int) string {
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

	prefix := shared.TitleStyle.Render("VULPINE") +
		shared.MutedStyle.Render(" | ") +
		shared.RunningStyle.Render("* "+mode)

	return fitTerminalLine(prefix, a.width)
}

// updatePanelSizes recalculates panel dimensions after a resize.
func (a *App) updatePanelSizes() {
	widths := resolveWorkbenchWidths(a.width, a.leftWidth, a.rightWidth)
	leftWidth := widths.left
	centerWidth := widths.center
	rightWidth := widths.right
	bodyHeight := workbenchBodyHeight(a.height)

	if a.useCompactWorkbench(widths, bodyHeight) {
		contentWidth := panelContentWidth(compactPanelWidth(a.width))
		contentHeight := compactContentHeight(a.height)
		a.agentList.SetWidth(contentWidth)
		a.agentList.SetHeight(contentHeight)
		a.agentDetail.SetSize(contentWidth, contentHeight)
		a.conversation.SetSize(contentWidth, contentHeight)
		a.settings.SetSize(contentWidth, contentHeight)
		a.contextList.SetWidth(contentWidth)
		a.contextList.SetHeight(contentHeight)
		a.nameInput.Width = max(10, contentWidth-4)
		a.taskInput.Width = max(10, contentWidth-4)
		return
	}

	// Center is full-height conversation (minus panel border)
	convHeight := bodyHeight - 2
	if convHeight < minSplit {
		convHeight = minSplit
	}

	// Left column splits
	a.leftSplit = clampVerticalSplit(a.leftSplit, bodyHeight)
	leftTop := a.leftSplit
	leftBottom := bodyHeight - leftTop - 4
	if leftBottom < 3 {
		leftBottom = 3
		leftTop = bodyHeight - leftBottom - 4
	}
	if leftTop < 3 {
		leftTop = 3
	}

	// Right column splits
	a.rightSplit = clampVerticalSplit(a.rightSplit, bodyHeight)
	rightTop := a.rightSplit
	rightBottom := bodyHeight - rightTop - 4
	if rightBottom < 3 {
		rightBottom = 3
		rightTop = bodyHeight - rightBottom - 4
	}
	if rightTop < 3 {
		rightTop = 3
	}

	leftContentWidth := panelContentWidth(leftWidth)
	centerContentWidth := panelContentWidth(centerWidth)
	rightContentWidth := panelContentWidth(rightWidth)

	a.systemInfo.SetWidth(leftContentWidth)
	a.systemInfo.SetHeight(leftTop)
	a.agentList.SetWidth(leftContentWidth)
	a.agentList.SetHeight(leftBottom)
	a.agentDetail.SetSize(rightContentWidth, rightTop)
	a.conversation.SetSize(centerContentWidth, convHeight)
	a.settings.SetSize(centerContentWidth, max(1, bodyHeight-2))
	a.contextList.SetWidth(rightContentWidth)
	a.contextList.SetHeight(rightBottom)

	// Update text input widths to fit center panel
	inputWidth := centerContentWidth - 6
	if inputWidth < 10 {
		inputWidth = 10
	}
	a.nameInput.Width = inputWidth
	a.taskInput.Width = inputWidth
}

func resolveWorkbenchWidths(totalWidth, preferredLeft, preferredRight int) workbenchWidths {
	// Two-column workbench: left sidebar + center conversation. The right column
	// (agent detail + contexts) was removed; preferredRight is ignored and the
	// center absorbs the freed space.
	_ = preferredRight
	available := totalWidth - workbenchPanelCount*panelHorizontalChrome
	if available <= 0 {
		return workbenchWidths{}
	}
	left := max(0, preferredLeft)
	if left+minCenterWidth > available {
		left = max(0, available-minCenterWidth)
	}
	return workbenchWidths{
		left:   left,
		center: max(0, available-left),
		right:  0,
	}
}

func shrinkSideWidths(left, right, budget int) (int, int) {
	if budget <= 0 {
		return 0, 0
	}
	if left+right <= budget {
		return left, right
	}
	total := left + right
	if total <= 0 {
		return budget / 2, budget - budget/2
	}
	left = budget * left / total
	if left < 0 {
		left = 0
	}
	if left > budget {
		left = budget
	}
	return left, budget - left
}

func (a *App) startEmbeddedReconfigure() tea.Cmd {
	cfg := a.cfg
	if cfg == nil {
		cfg = &config.Config{}
		a.cfg = cfg
	}
	a.setupWizard = setup.NewWithConfig(cfg)
	a.setupActive = true
	a.focus = FocusSettings
	if a.width > 0 && a.height > 0 {
		model, _ := a.setupWizard.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height})
		if wizard, ok := model.(*setup.Model); ok {
			a.setupWizard = wizard
		}
	}
	return nil
}

func (a App) updateEmbeddedSetup(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.updatePanelSizes()
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			a.cancelEmbeddedReconfigure()
			return a, nil
		case "ctrl+c":
			return a.handleCtrlC()
		}
	}

	model, cmd := a.setupWizard.Update(msg)
	if wizard, ok := model.(*setup.Model); ok {
		a.setupWizard = wizard
	}
	if a.setupWizard.Done() {
		a.completeEmbeddedReconfigure()
		return a, nil
	}
	return a, cmd
}

func (a *App) cancelEmbeddedReconfigure() {
	a.setupActive = false
	a.focus = FocusConversation
	a.inputMode = "chat"
	a.notice = "Reconfigure cancelled"
	a.noticeTTL = 3
}

func (a *App) completeEmbeddedReconfigure() {
	if err := a.applySetupConfig(a.setupWizard.Config()); err != nil {
		a.notice = "Failed to save configuration: " + err.Error()
		a.noticeTTL = 4
		a.setupActive = false
		a.focus = FocusConversation
		a.inputMode = "chat"
		return
	}
	_ = config.ClearReconfigureRequest()
	a.setupActive = false
	a.focus = FocusConversation
	a.inputMode = "chat"
	a.notice = "Configuration updated"
	a.noticeTTL = 3
}

// nativeFallbackModels returns the rate-limit fallback chain for a provider.
// Mirrors the helper in cmd/vulpineos/main.go.
func nativeFallbackModels(provider string) []string {
	if override := strings.TrimSpace(os.Getenv("OPENCODE_FALLBACK_MODELS")); override != "" {
		var out []string
		for _, m := range strings.Split(override, ",") {
			if m = strings.TrimSpace(m); m != "" {
				out = append(out, m)
			}
		}
		return out
	}
	if strings.EqualFold(strings.TrimSpace(provider), "openrouter") {
		return agentcore.DefaultFallbackModels()
	}
	return nil
}

func (a *App) applySetupConfig(updated *config.Config) error {
	if updated == nil {
		return fmt.Errorf("setup returned no configuration")
	}
	if a.control != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var result remoteConfigSummary
		apiKey := strings.TrimSpace(updated.APIKey)
		keepAPIKey := apiKey == "" || apiKey == remoteAPIKeyPlaceholder
		if keepAPIKey {
			apiKey = ""
		}
		params := map[string]any{
			"provider":      updated.Provider,
			"model":         updated.Model,
			"apiKey":        apiKey,
			"keepApiKey":    keepAPIKey,
			"setupComplete": updated.SetupComplete,
		}
		if err := a.control.ControlCall(ctx, "config.set", params, &result); err != nil {
			return err
		}
		a.cfg = configFromRemoteSummary(result)
		a.conversation.SetModelLabel(a.cfg.Model)
		return nil
	}
	if a.cfg == nil {
		a.cfg = updated
		return a.cfg.Save()
	}
	a.cfg.Provider = updated.Provider
	a.cfg.APIKey = updated.APIKey
	a.cfg.Model = updated.Model
	a.conversation.SetModelLabel(updated.Model)
	a.cfg.SetupComplete = updated.SetupComplete
	a.cfg.BinaryPath = updated.BinaryPath
	a.cfg.GlobalSkills = append([]config.SkillEntry(nil), updated.GlobalSkills...)
	if len(updated.AgentSkills) > 0 {
		a.cfg.AgentSkills = make(map[string][]config.SkillEntry, len(updated.AgentSkills))
		for agentID, skills := range updated.AgentSkills {
			a.cfg.AgentSkills[agentID] = append([]config.SkillEntry(nil), skills...)
		}
	} else {
		a.cfg.AgentSkills = nil
	}
	if a.orch != nil {
		if mgr, ok := a.orch.Agents.(*agentcore.Manager); ok {
			fallbackModels := nativeFallbackModels(updated.Provider)
			mgr.Reconfigure(agentcore.Config{
				Provider:       updated.Provider,
				Model:          updated.Model,
				APIKey:         updated.APIKey,
				FallbackModels: fallbackModels,
			})
		}
	}
	return a.cfg.Save()
}

func (a *App) browserRouteLabel() string {
	if a.kernel == nil || !a.kernel.Running() {
		return ""
	}
	if a.activeFoxbridgeCDPURL() != "" {
		return "VULPINE"
	}
	if a.kernel != nil && a.kernel.IsHeadless() {
		return "HEADLESS"
	}
	if a.kernel != nil {
		return "DIRECT"
	}
	return ""
}

func (a *App) activeFoxbridgeCDPURL() string {
	if a.cfg == nil {
		return ""
	}
	cdpURL := strings.TrimSpace(a.cfg.FoxbridgeCDPURL)
	if cdpURL == "" {
		return ""
	}
	if a.foxbridgeRunning != nil && !a.foxbridgeRunning() {
		return ""
	}
	return cdpURL
}

// updateAgentDetail populates the agent detail panel from an Agent struct.
func (a *App) updateAgentDetail(agent *vault.Agent) {
	if agent == nil {
		a.agentDetail.Clear()
		return
	}
	fpSummary := vault.FingerprintSummary(agent.Fingerprint)
	proxyInfo := ""
	if agent.ProxyConfig != "" {
		proxyInfo = agent.ProxyConfig // simplified display
	}
	a.agentDetail.SetAgent(
		agent.ID, agent.Name, agent.Task, agent.Status,
		agent.TotalTokens, fpSummary, proxyInfo, agent.CreatedAt,
	)
	if liveContextID := strings.TrimSpace(a.liveAgentContexts[agent.ID]); liveContextID != "" {
		a.agentDetail.SetBrowserContext("live " + shortContextID(liveContextID))
	} else if meta, err := vault.ParseAgentMetadata(agent.Metadata); err == nil && meta.ContextID != "" {
		a.agentDetail.SetBrowserContext("pinned " + shortContextID(meta.ContextID))
	} else {
		a.agentDetail.SetBrowserContext("")
	}
}

func (a *App) updateLiveAgentContext(msg shared.AgentStatusMsg) {
	if a.liveAgentContexts == nil {
		a.liveAgentContexts = make(map[string]string)
	}
	if msg.Status != "" && !isLiveAgentStatus(msg.Status) {
		delete(a.liveAgentContexts, msg.AgentID)
		return
	}
	if contextID := strings.TrimSpace(msg.ContextID); contextID != "" {
		a.liveAgentContexts[msg.AgentID] = contextID
	}
}

// refreshAgentDetail reloads agent detail from vault.
func (a *App) refreshAgentDetail(agentID string) {
	if a.vault == nil {
		return
	}
	agent, err := a.vault.GetAgent(agentID)
	if err != nil {
		return
	}
	a.updateAgentDetail(agent)
}

func (a *App) updateRemoteAgentDetailFromList(agentID string) {
	item, ok := a.agentList.Agent(agentID)
	if !ok {
		return
	}
	agent := agentListItemToAgent(item)
	a.updateAgentDetail(&agent)
}

// selectCurrentAgent loads the currently highlighted agent's data.
func (a *App) selectCurrentAgent() tea.Cmd {
	newID := a.agentList.SelectedAgentID()
	if newID == a.selectedAgentID || newID == "" {
		return nil
	}
	a.selectedAgentID = newID
	a.conversation.SetAgentID(newID)
	a.agentList.ClearUnread(newID)
	a.focus = FocusConversation
	a.inputMode = "chat"
	a.conversation.Focus()

	if a.control != nil && a.vault == nil {
		if item, ok := a.agentList.SelectedAgent(); ok {
			a.conversation.SetAgentName(item.Name)
			agent := agentListItemToAgent(item)
			a.updateAgentDetail(&agent)
		}
		return a.loadRemoteMessages(newID)
	}

	if a.vault != nil {
		status := ""
		agent, err := a.vault.GetAgent(newID)
		if err == nil {
			a.conversation.SetAgentName(agent.Name)
			status = agent.Status
		}
		msgs, err := a.vault.GetMessages(newID)
		if err == nil {
			a.conversation.LoadMessages(msgs)
		}
		if status != "" {
			a.applyConversationStatus(status)
		}
		a.refreshAgentDetail(newID)
	}
	return nil
}

func (a App) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		if a.stopCh == nil {
			return <-a.eventCh
		}
		select {
		case msg := <-a.eventCh:
			return msg
		case <-a.stopCh:
			return nil
		}
	}
}

func (a App) tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return shared.TickMsg{}
	})
}

// createAgent creates an agent profile and opens an unlocked chat. The first
// user message starts the native agent turn, matching a normal CLI chat flow.
func (a *App) createAgent(name, description, contextID string) tea.Cmd {
	if a.control != nil {
		return a.createRemoteAgent(name, description, contextID)
	}
	return func() tea.Msg {
		// Pre-creation checks — show errors as notices since there's no agent yet
		if a.vault == nil {
			return statusNotice{text: "ERROR: No vault available — cannot create agent"}
		}

		// Generate fingerprint seeded by a unique id, NOT the display name:
		// names have no uniqueness constraint and the seed is deterministic, so
		// two same-named agents would otherwise get identical fingerprints.
		agentID := vault.NewID()
		fp, err := vault.GenerateFingerprint(agentID)
		if err != nil {
			fp = "{}" // use empty fingerprint as fallback, don't block creation
		}

		// Create in vault — this MUST succeed for anything else to work
		agent, err := a.vault.CreateAgentWithID(agentID, name, description, fp)
		if err != nil {
			return statusNotice{text: "ERROR: Failed to create agent: " + err.Error()}
		}
		if contextID != "" {
			metadata := vault.MarshalAgentMetadata(vault.AgentMetadata{ContextID: contextID})
			if err := a.vault.UpdateAgentMetadata(agent.ID, metadata); err == nil {
				agent.Metadata = metadata
			}
		}

		// If agent has a proxy assigned, sync fingerprint geo
		if agent.ProxyConfig != "" {
			var pc proxy.ProxyConfig
			if json.Unmarshal([]byte(agent.ProxyConfig), &pc) == nil {
				geo, geoErr := proxy.ResolveGeo(pc)
				if geoErr == nil {
					synced, syncErr := proxy.SyncFingerprintToProxy(agent.Fingerprint, geo)
					if syncErr == nil {
						agent.Fingerprint = synced
						a.vault.UpdateAgentFingerprint(agent.ID, synced)
					}
				}
			}
		}

		agent.Status = "ready"
		a.vault.UpdateAgentStatus(agent.ID, "ready")

		// ALWAYS return AgentCreatedMsg so the agent shows up in the list
		return shared.AgentCreatedMsg{Agent: *agent}
	}
}

func (a *App) createRemoteAgent(name, description, contextID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var result struct {
			AgentID string `json:"agentId"`
		}
		params := map[string]any{
			"name": name,
			"task": description,
		}
		if contextID != "" {
			params["contextId"] = contextID
		}
		if err := a.control.ControlCall(ctx, "agents.create", params, &result); err != nil {
			agents, listErr := a.fetchRemoteAgents(ctx)
			if listErr != nil {
				return statusNotice{text: "Remote agent failed: " + err.Error() + "; reload failed: " + listErr.Error()}
			}
			return remoteAgentsLoadedMsg{
				Agents: agents,
				Notice: "Remote agent failed: " + err.Error(),
			}
		}
		agents, err := a.fetchRemoteAgents(ctx)
		if err != nil {
			return statusNotice{text: "Remote agents failed: " + err.Error()}
		}
		return remoteAgentsLoadedMsg{Agents: agents, SelectedAgentID: result.AgentID}
	}
}

func (a App) remoteAgentStatusCommand(method, agentID, status, noticePrefix string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var result map[string]any
		if err := a.control.ControlCall(ctx, method, map[string]any{"agentId": agentID}, &result); err != nil {
			return statusNotice{text: "Remote command failed: " + err.Error()}
		}
		return shared.BulkAgentStatusMsg{
			AgentIDs: []string{agentID},
			Status:   status,
			Notice:   noticePrefix + agentID,
		}
	}
}

// deleteAgent removes an agent.
func (a *App) deleteAgent(agentID string) tea.Cmd {
	if a.control != nil {
		if !isLiveAgentStatus(a.selectedAgentStatus()) {
			return statusNoticeCmd("Remote kill is only available for live agents")
		}
		return a.remoteAgentStatusCommand("agents.kill", agentID, "interrupted", "Remote agent killed: ")
	}
	return func() tea.Msg {
		if a.orch != nil && isLiveAgentStatus(a.selectedAgentStatus()) {
			if err := a.orch.KillAgent(agentID); err != nil && !strings.Contains(err.Error(), "not found") {
				return statusNotice{text: "Kill failed: " + err.Error()}
			}
		}
		if a.orch != nil {
			a.orch.ReleaseAgentContext(agentID)
		}
		// Remove from vault
		if a.vault != nil {
			a.vault.DeleteAgent(agentID)
		}
		return shared.AgentDeletedMsg{AgentID: agentID}
	}
}

// sendMessageToAgent runs one native agent turn for a conversation message.
// The browser context and visible chat history provide continuity between
// turns; no idle per-agent process is kept alive between messages.
func (a App) sendMessageToAgent(agentID, text, displayText string) tea.Cmd {
	if a.control != nil {
		return a.sendRemoteMessageToAgent(agentID, text, displayText)
	}
	return func() tea.Msg {
		if a.orch == nil {
			return shared.ConversationEntryMsg{
				AgentID: agentID,
				Role:    "system",
				Content: "Error: No orchestrator available. Is the browser running?",
			}
		}
		if a.vault == nil {
			return shared.ConversationEntryMsg{
				AgentID: agentID,
				Role:    "system",
				Content: "Error: No vault available.",
			}
		}

		sessionName := "vulpine-" + agentID
		agent, err := a.vault.GetAgent(agentID)
		if err != nil {
			return shared.ConversationEntryMsg{
				AgentID: agentID,
				Role:    "system",
				Content: "Error: " + err.Error(),
			}
		}
		configPath, cleanup, err := a.agentRuntimeConfig(agent)
		if err != nil {
			return shared.ConversationEntryMsg{
				AgentID: agentID,
				Role:    "system",
				Content: "Error: " + err.Error(),
			}
		}
		turnText := a.agentTurnPrompt(agentID, text)
		_, err = a.orch.Agents.SpawnWithSessionIsolated(agentID, turnText, sessionName, configPath, cleanup)
		if err != nil {
			return shared.ConversationEntryMsg{
				AgentID: agentID,
				Role:    "system",
				Content: "Error: " + err.Error(),
			}
		}

		if a.vault != nil {
			a.vault.UpdateAgentStatus(agentID, "active")
		}

		return nil
	}
}

func (a App) agentTurnPrompt(agentID, text string) string {
	if a.vault == nil {
		return text
	}
	history, err := a.vault.GetRecentMessages(agentID, 16)
	if err != nil {
		return text
	}
	return agentprompt.FormatTurnPrompt(history, text)
}

func (a App) sendRemoteMessageToAgent(agentID, text, displayText string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var result struct {
			AgentID string `json:"agentId"`
		}
		params := map[string]any{
			"agentId": agentID,
			"message": text,
		}
		if strings.TrimSpace(displayText) != "" && displayText != text {
			params["displayContent"] = displayText
		}
		if err := a.control.ControlCall(ctx, "agents.resume", params, &result); err != nil {
			return shared.ConversationEntryMsg{
				AgentID: agentID,
				Role:    "system",
				Content: "Error: " + err.Error(),
			}
		}
		return shared.BulkAgentStatusMsg{
			AgentIDs: []string{agentID},
			Status:   "active",
			Notice:   "Remote agent running: " + agentID,
		}
	}
}

func (a App) newAgentContextNotice() string {
	if a.newAgentContext == "" {
		return ""
	}
	return shared.MutedStyle.Render("Pinned browser context: "+shortContextID(a.newAgentContext)) + "\n\n"
}

func (a *App) agentRuntimeConfig(agent *vault.Agent) (string, func(), error) {
	if agent == nil {
		return "", nil, fmt.Errorf("agent not found")
	}
	if a.orch == nil {
		return "", nil, fmt.Errorf("orchestrator not available")
	}
	contextID, err := a.orch.EnsureAgentBrowserContext(agent)
	if err != nil {
		return "", nil, err
	}
	cleanup, err := a.orch.AgentRuntimeConfig(contextID)
	if err != nil {
		return "", nil, err
	}
	return "", cleanup, nil
}

func shortContextID(contextID string) string {
	if len(contextID) <= 12 {
		return contextID
	}
	return contextID[:12]
}

func (a App) selectedAgentStatus() string {
	if a.selectedAgentID == "" {
		return ""
	}
	if a.control != nil && a.vault == nil {
		return a.agentList.Status(a.selectedAgentID)
	}
	if a.vault == nil {
		return ""
	}
	agent, err := a.vault.GetAgent(a.selectedAgentID)
	if err != nil {
		return ""
	}
	return agent.Status
}

func (a App) remoteBulkAgentStatusCommand(method string, agentIDs []string, status string, noticeFormat string) tea.Cmd {
	return func() tea.Msg {
		if len(agentIDs) == 0 {
			return statusNotice{text: "No remote agents matched"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var result struct {
			Failures map[string]string `json:"failures"`
		}
		if err := a.control.ControlCall(ctx, method, map[string]any{"agentIds": agentIDs}, &result); err != nil {
			return statusNotice{text: "Remote command failed: " + err.Error()}
		}
		successful := make([]string, 0, len(agentIDs))
		for _, agentID := range agentIDs {
			if _, failed := result.Failures[agentID]; failed {
				continue
			}
			successful = append(successful, agentID)
		}
		if len(successful) == 0 {
			return statusNotice{text: fmt.Sprintf("Remote command failed for %d agents", len(agentIDs))}
		}
		notice := fmt.Sprintf(noticeFormat, len(successful))
		if len(result.Failures) > 0 {
			notice = fmt.Sprintf("%s (%d failed)", notice, len(result.Failures))
		}
		return shared.BulkAgentStatusMsg{
			AgentIDs: successful,
			Status:   status,
			Notice:   notice,
		}
	}
}

func statusNoticeCmd(text string) tea.Cmd {
	return func() tea.Msg {
		return statusNotice{text: text}
	}
}

func (a App) remoteProxyAdd(rawURL string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var result struct {
			Proxies []remoteProxySummary `json:"proxies"`
		}
		if err := a.control.ControlCall(ctx, "proxies.add", map[string]any{"url": rawURL}, &result); err != nil {
			return statusNotice{text: "Remote proxy add failed: " + err.Error()}
		}
		return remoteProxiesLoadedMsg{Notice: "Remote proxy added", Proxies: result.Proxies}
	}
}

func (a App) remoteProxyDelete(proxyID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var result struct {
			Proxies []remoteProxySummary `json:"proxies"`
		}
		if err := a.control.ControlCall(ctx, "proxies.delete", map[string]any{"proxyId": proxyID}, &result); err != nil {
			return statusNotice{text: "Remote proxy delete failed: " + err.Error()}
		}
		return remoteProxiesLoadedMsg{Notice: "Remote proxy deleted", Proxies: result.Proxies}
	}
}

func (a App) remoteProxyTest(proxyID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var result struct {
			ProxyID string `json:"proxyId"`
			Latency string `json:"latency"`
			ExitIP  string `json:"exitIp"`
			Country string `json:"country"`
		}
		if err := a.control.ControlCall(ctx, "proxies.test", map[string]any{"proxyId": proxyID}, &result); err != nil {
			return shared.ProxyTestedMsg{ProxyID: proxyID, Latency: "error: " + err.Error()}
		}
		return shared.ProxyTestedMsg{ProxyID: result.ProxyID, Latency: result.Latency, ExitIP: result.ExitIP, Country: result.Country}
	}
}

func (a App) remoteSkillSet(name string, enabled bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var result struct {
			Skills []remoteSkillSummary `json:"skills"`
		}
		if err := a.control.ControlCall(ctx, "skills.set", map[string]any{"name": name, "enabled": enabled}, &result); err != nil {
			return statusNotice{text: "Remote skill update failed: " + err.Error()}
		}
		return remoteSkillsLoadedMsg{Notice: "Remote skill updated", Skills: result.Skills}
	}
}

// reloadSettingsProxies loads proxies from vault into the settings panel.
func (a *App) reloadSettingsProxies() {
	if a.vault == nil {
		return
	}
	storedProxies, err := a.vault.ListProxies()
	if err != nil {
		return
	}
	items := make([]settings.ProxyItem, len(storedProxies))
	for i, sp := range storedProxies {
		items[i] = settings.ProxyItem{
			ID:      sp.ID,
			Label:   safeProxyLabel(sp.Label),
			Config:  sp.Config,
			Latency: "untested",
		}
		var pc struct {
			Type string `json:"type"`
			Host string `json:"host"`
			Port int    `json:"port"`
		}
		if json.Unmarshal([]byte(sp.Config), &pc) == nil {
			items[i].Type = pc.Type
			items[i].Host = pc.Host
			items[i].Port = pc.Port
		}
		var geo struct {
			Country string `json:"country"`
		}
		if json.Unmarshal([]byte(sp.Geo), &geo) == nil {
			items[i].Country = geo.Country
		}
	}
	a.settings.SetProxies(items)
}

func safeProxyLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	if pc, err := proxy.ParseProxyURL(label); err == nil {
		return pc.String()
	}
	return label
}

// testProxy spawns a goroutine to test proxy latency and resolve geo.
func (a App) testProxy(proxyID, configJSON string) tea.Cmd {
	return func() tea.Msg {
		var pc proxy.ProxyConfig
		if err := json.Unmarshal([]byte(configJSON), &pc); err != nil {
			return shared.ProxyTestedMsg{ProxyID: proxyID, Latency: "error: invalid config"}
		}
		latency, err := proxy.TestProxy(pc)
		if err != nil {
			return shared.ProxyTestedMsg{ProxyID: proxyID, Latency: "error: " + err.Error()}
		}
		result := shared.ProxyTestedMsg{
			ProxyID: proxyID,
			Latency: fmt.Sprintf("%dms", latency),
		}
		// Also resolve geo
		geo, err := proxy.ResolveGeo(pc)
		if err == nil {
			result.ExitIP = geo.IP
			result.Country = geo.Country
			// Update vault with geo info
			if a.vault != nil {
				geoJSON, _ := json.Marshal(geo)
				a.vault.UpdateProxyGeo(proxyID, string(geoJSON))
			}
		}
		return result
	}
}

// gracefulShutdown pauses all running agents so they save state before exit.
func (a *App) gracefulShutdown() {
	if a.control != nil && a.vault == nil {
		a.pauseRemoteAgentsOnShutdown()
		return
	}
	if a.orch == nil || a.vault == nil {
		return
	}

	// Get all running agents and pause them
	agents := a.orch.Agents.List()
	for _, status := range agents {
		if shouldPauseOnShutdown(status.Status) {
			// Send /savestate and mark as paused in vault
			if err := a.orch.Agents.PauseAgent(status.AgentID); err != nil {
				log.Printf("pause agent during shutdown %s: %v", status.AgentID, err)
				continue
			}
			a.vault.UpdateAgentStatus(status.AgentID, "paused")
		}
	}
}

func (a *App) pauseRemoteAgentsOnShutdown() {
	ids := a.agentList.IDsByStatus(map[string]bool{
		"running": true, "thinking": true, "starting": true, "active": true,
	})
	if len(ids) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var result struct {
		Failures map[string]string `json:"failures"`
	}
	if err := a.control.ControlCall(ctx, "agents.pauseMany", map[string]any{"agentIds": ids}, &result); err != nil {
		log.Printf("pause remote agents during shutdown: %v", err)
		return
	}
	if len(result.Failures) > 0 {
		log.Printf("pause remote agents during shutdown: %d failed", len(result.Failures))
	}
}

func shouldPauseOnShutdown(status string) bool {
	switch status {
	case "running", "thinking", "starting", "active":
		return true
	default:
		return false
	}
}

// Header renders the VulpineOS header.
func Header() string {
	var b strings.Builder
	b.WriteString(shared.TitleStyle.Render("VulpineOS"))
	b.WriteString(shared.MutedStyle.Render(" -- Sovereign Agent Runtime"))
	return b.String()
}
