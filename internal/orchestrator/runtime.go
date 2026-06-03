package orchestrator

import (
	"vulpineos/internal/agentcore"
	"vulpineos/internal/agentmsg"
	"vulpineos/internal/runtimeaudit"
)

// AgentRuntime is the agent-execution backend the orchestrator drives. The sole
// implementation is *agentcore.Manager — the native in-process runtime that runs
// the model<->tool loop and drives the host Camoufox directly through the
// MCP/Juggler tools. The interface is retained so the orchestrator depends on a
// behavioral contract rather than a concrete type (and so tests can substitute a
// fake). It emits agentmsg.ConversationMsg / agentmsg.AgentStatus value types so
// the TUI, web panel, and remote broadcasts work unchanged.
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

// Compile-time check that the native manager satisfies the runtime contract.
var _ AgentRuntime = (*agentcore.Manager)(nil)
