package orchestrator

import (
	"vulpineos/internal/agentmsg"
	"vulpineos/internal/nanoclaw"
	"vulpineos/internal/runtimeaudit"
)

// AgentRuntime is the agent-execution backend the orchestrator drives. Two
// implementations satisfy it:
//
//   - *nanoclaw.Manager — runs each agent in a per-turn Docker container via the
//     NanoClaw daemon (the legacy/default backend).
//   - *agentcore.Manager — runs the model<->tool loop in-process and drives the
//     host Camoufox directly through the MCP/Juggler tools (the native backend).
//
// The backend is chosen by the caller via Opts.AgentRuntime; when unset the
// orchestrator defaults to NanoClaw. Both emit the same agentmsg.ConversationMsg
// / agentmsg.AgentStatus value types so the TUI, web panel, and remote
// broadcasts work unchanged regardless of backend.
type AgentRuntime interface {
	SetRuntimeAudit(*runtimeaudit.Manager)
	StatusChan() <-chan agentmsg.AgentStatus
	ConversationChan() <-chan agentmsg.ConversationMsg
	SpawnIsolated(contextID, sopFile, configPath string, cleanup func(), extraArgs ...string) (string, error)
	SpawnWithSessionIsolated(agentID, task, sessionName, configPath string, cleanup func()) (string, error)
	ResumeWithSessionIsolated(agentID, sessionName, configPath string, cleanup func()) (string, error)
	Kill(agentID string) error
	PauseAgent(agentID string) error
	KillAll()
	Dispose()
	Count() int
	List() []agentmsg.AgentStatus
}

// Compile-time check that the NanoClaw manager satisfies the runtime contract.
// (The native agentcore.Manager is checked at its construction site in cmd to
// keep this package free of an agentcore import.)
var _ AgentRuntime = (*nanoclaw.Manager)(nil)
