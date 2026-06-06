// Package agentmsg holds the runtime-neutral value types the agent runtime emits
// to its consumers (TUI, web panel, remote broadcasts): conversation messages
// and agent status. Keeping them in their own package lets the orchestrator,
// UI, and remote layers depend on a stable contract independent of any specific
// agent backend.
package agentmsg

// ConversationMsg represents a captured conversation message from an agent.
type ConversationMsg struct {
	AgentID      string
	Role         string
	Content      string
	Tokens       int
	StreamActive bool
}

// AgentStatus represents an agent's current state.
type AgentStatus struct {
	AgentID   string `json:"agent_id"`
	ParentID  string `json:"parent_id"`
	ContextID string `json:"context_id"`
	Status    string `json:"status"`    // starting, running, thinking, paused, completed, error, failed, interrupted
	Objective string `json:"objective"` // current task description
	Tokens    int    `json:"tokens"`    // tokens consumed
}
