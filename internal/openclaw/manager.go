package openclaw

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// Manager manages multiple OpenClaw agent subprocesses.
type Manager struct {
	agents   map[string]*Agent
	statusCh chan AgentStatus
	mu       sync.RWMutex
	binary   string
}

// NewManager creates a new agent manager.
func NewManager(binary string) *Manager {
	if binary == "" {
		binary = "openclaw"
	}
	return &Manager{
		agents:   make(map[string]*Agent),
		statusCh: make(chan AgentStatus, 64),
		binary:   binary,
	}
}

// StatusChan returns the channel for agent status updates.
func (m *Manager) StatusChan() <-chan AgentStatus {
	return m.statusCh
}

// Spawn creates and starts a new agent bound to a browser context.
func (m *Manager) Spawn(contextID string, sopFile string, extraArgs ...string) (string, error) {
	id := uuid.New().String()[:8]

	args := []string{
		"--context-id", contextID,
	}
	if sopFile != "" {
		args = append(args, "--sop", sopFile)
	}
	args = append(args, extraArgs...)

	agent := newAgent(id, contextID, m.statusCh)
	if err := agent.start(m.binary, args); err != nil {
		return "", fmt.Errorf("spawn agent: %w", err)
	}

	m.mu.Lock()
	m.agents[id] = agent
	m.mu.Unlock()

	// Auto-cleanup when agent exits
	go func() {
		agent.Wait()
		m.mu.Lock()
		delete(m.agents, id)
		m.mu.Unlock()
	}()

	return id, nil
}

// Kill stops an agent by ID.
func (m *Manager) Kill(agentID string) error {
	m.mu.RLock()
	agent, ok := m.agents[agentID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("agent %s not found", agentID)
	}
	return agent.Stop()
}

// List returns the status of all active agents.
func (m *Manager) List() []AgentStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]AgentStatus, 0, len(m.agents))
	for _, agent := range m.agents {
		statuses = append(statuses, agent.Status())
	}
	return statuses
}

// Count returns the number of active agents.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.agents)
}

// KillAll stops all agents.
func (m *Manager) KillAll() {
	m.mu.RLock()
	agents := make([]*Agent, 0, len(m.agents))
	for _, a := range m.agents {
		agents = append(agents, a)
	}
	m.mu.RUnlock()

	for _, a := range agents {
		a.Stop()
	}
}

// Dispose kills all agents and closes the status channel.
func (m *Manager) Dispose() {
	m.KillAll()
	close(m.statusCh)
}
