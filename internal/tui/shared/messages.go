package shared

import (
	"time"

	"vulpineos/internal/vault"
)

// Custom tea.Msg types for the TUI.

// KernelStatusMsg updates kernel process status.
type KernelStatusMsg struct {
	Running bool
	PID     int
	Uptime  time.Duration
}

// TelemetryMsg carries engine telemetry data.
type TelemetryMsg struct {
	MemoryMB           float64
	EventLoopLagMs     float64
	DetectionRiskScore float64
	ActiveContexts     int
	ActivePages        int
}

// ContextInfo describes a browser context.
type ContextInfo struct {
	ContextID  string
	Identity   string
	PageCount  int
	TrustState string
	URLs       []string
}

// ContextUpdateMsg updates the context list.
type ContextUpdateMsg struct {
	Contexts []ContextInfo
}

// TargetAttachedMsg fires when a new page target appears.
type TargetAttachedMsg struct {
	SessionID  string
	TargetID   string
	ContextID  string
	URL        string
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

// AgentStatusMsg carries OpenClaw agent status.
type AgentStatusMsg struct {
	AgentID   string
	ContextID string
	Status    string
	Objective string
	Tokens    int
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
	AgentID   string
	Role      string
	Content   string
	Tokens    int
	Timestamp time.Time
}

// AgentSelectedMsg fires when the user selects a different agent.
type AgentSelectedMsg struct {
	AgentID string
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

// TickMsg is the periodic refresh tick.
type TickMsg struct{}

// ErrorMsg carries an error to display.
type ErrorMsg struct {
	Err error
}

// ProxyTestedMsg carries the result of a proxy latency test.
type ProxyTestedMsg struct {
	ProxyID string
	Latency string // "45ms" or "error: ..."
	ExitIP  string
}

// ProxyImportedMsg fires after proxies are imported.
type ProxyImportedMsg struct {
	Count int
}

// SettingsClosedMsg fires when the settings panel is closed.
type SettingsClosedMsg struct{}
