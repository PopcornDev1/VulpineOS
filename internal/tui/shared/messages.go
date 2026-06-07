package shared

import (
	"time"

	"vulpineos/internal/vault"
)

// Custom tea.Msg types for the TUI.

// KernelStatusMsg updates kernel process status.
type KernelStatusMsg struct {
	Running       bool
	PID           int
	Uptime        time.Duration
	Headless      bool
	BrowserRoute  string
	BrowserWindow string
}

// TelemetryMsg carries engine telemetry data.
type TelemetryMsg struct {
	MemoryMB         float64
	EventLoopLagMs   float64
	RuntimeRiskScore float64
	ActiveContexts   int
	ActivePages      int
}

// TargetAttachedMsg fires when a new page target appears.
type TargetAttachedMsg struct {
	SessionID string
	TargetID  string
	ContextID string
	URL       string
}

// FrameAttachedMsg fires when a frame is attached to a page.
type FrameAttachedMsg struct {
	SessionID     string
	FrameID       string
	ParentFrameID string
}

// ExecContextCreatedMsg fires when an execution context is created.
type ExecContextCreatedMsg struct {
	SessionID          string
	ExecutionContextID string
	FrameID            string
}

// TargetDetachedMsg fires when a page target disappears.
type TargetDetachedMsg struct {
	SessionID string
	TargetID  string
}

// AgentStatusMsg carries agent runtime status.
type AgentStatusMsg struct {
	AgentID      string
	ParentID     string
	ContextID    string
	Status       string
	Objective    string
	Tokens       int
	Phase        string
	Turn         int
	MaxTurns     int
	LastActivity int64
}

// AlertMsg carries an injection attempt alert.
type AlertMsg struct {
	Timestamp time.Time
	Type      string
	URL       string
	Details   string
	Blocked   bool
}

// TrustWarmMsg carries trust warming state changes.
type TrustWarmMsg struct {
	State       string
	CurrentSite string
}

// NavigationMsg fires when a page navigates to a new URL.
type NavigationMsg struct {
	SessionID string
	FrameID   string
	URL       string
}

// PageLoadMsg fires when a page fires load or DOMContentLoaded.
type PageLoadMsg struct {
	SessionID string
	FrameID   string
	Name      string // "load" or "DOMContentLoaded"
}

// ConversationEntryMsg is a new message in an agent's conversation.
type ConversationEntryMsg struct {
	AgentID        string
	Role           string
	Content        string
	DisplayContent string
	Tokens         int
	Timestamp      time.Time
	StreamActive   bool
}

// AgentSelectedMsg fires when the user selects a different agent.
type AgentSelectedMsg struct {
	AgentID string
}

// AgentDeletedMsg fires when an agent has been deleted from vault.
type AgentDeletedMsg struct {
	AgentID string
}

// BulkAgentStatusMsg updates multiple agent statuses at once.
type BulkAgentStatusMsg struct {
	AgentIDs []string
	Status   string
	Notice   string
}

// AgentCreatedMsg fires when a new agent is created.
type AgentCreatedMsg struct {
	Agent vault.Agent
}

// PoolStatsMsg carries context pool statistics.
type PoolStatsMsg struct {
	Available int
	Active    int
	Total     int
}

// RuntimeEventMsg carries a runtime lifecycle audit event.
type RuntimeEventMsg struct {
	Event vault.RuntimeEvent
}

// TickMsg is the periodic refresh tick.
type TickMsg struct{}

// ProxyTestedMsg carries the result of a proxy latency test.
type ProxyTestedMsg struct {
	ProxyID string
	Latency string // "45ms" or "error: ..."
	ExitIP  string
	Country string
}

// SettingsNoticeMsg displays a notice in the settings panel.
type SettingsNoticeMsg struct {
	Message string
}

// SettingsClosedMsg fires when the settings panel is closed.
type SettingsClosedMsg struct{}

// ReconfigureRequestedMsg requests launching the setup wizard inside the TUI.
type ReconfigureRequestedMsg struct{}

// ReconfigureProviderMsg requests launching the setup wizard inside the TUI.
type ReconfigureProviderMsg struct{}

// ResizeModeToggleMsg toggles arrow-key resize mode in the main TUI.
type ResizeModeToggleMsg struct {
	Enabled bool
}

// ProxyAddMsg requests adding a proxy to the vault.
type ProxyAddMsg struct {
	URL string // raw proxy URL to parse and save
}

// ProxyDeleteMsg requests deleting a proxy from the vault.
type ProxyDeleteMsg struct {
	ProxyID string
}

// SkillToggleMsg requests toggling a global skill.
type SkillToggleMsg struct {
	Name    string
	Enabled bool
}

// ProxyTestRequestMsg requests testing a proxy's latency and geo.
type ProxyTestRequestMsg struct {
	ProxyID string
	Config  string // JSON ProxyConfig
}

// AgentPickerPickedMsg fires when the user selects an agent in the
// agent picker modal.
type AgentPickerPickedMsg struct {
	AgentID   string
	AgentName string
}

// AgentPickerCancelledMsg fires when the user dismisses the agent picker.
type AgentPickerCancelledMsg struct{}
