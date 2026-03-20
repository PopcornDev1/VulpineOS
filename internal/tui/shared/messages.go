package shared

import (
	"time"
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
	CPUPercent         float64
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

// TickMsg is the periodic refresh tick.
type TickMsg struct{}

// ErrorMsg carries an error to display.
type ErrorMsg struct {
	Err error
}
