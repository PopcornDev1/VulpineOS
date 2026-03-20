package openclaw

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"

	"vulpineos/internal/config"
)

// Manager manages multiple OpenClaw agent subprocesses.
type Manager struct {
	agents         map[string]*Agent
	statusCh       chan AgentStatus
	conversationCh chan ConversationMsg
	mu             sync.RWMutex
	binary         string
}

// NewManager creates a new agent manager.
func NewManager(binary string) *Manager {
	if binary == "" {
		binary = "openclaw"
	}
	return &Manager{
		agents:         make(map[string]*Agent),
		statusCh:       make(chan AgentStatus, 64),
		conversationCh: make(chan ConversationMsg, 64),
		binary:         binary,
	}
}

// StatusChan returns the channel for agent status updates.
func (m *Manager) StatusChan() <-chan AgentStatus {
	return m.statusCh
}

// ConversationChan returns the channel for agent conversation messages.
func (m *Manager) ConversationChan() <-chan ConversationMsg {
	return m.conversationCh
}

// SpawnWithSession creates and starts a new agent with a named session for state persistence.
func (m *Manager) SpawnWithSession(agentID, task, sessionName, configPath string) (string, error) {
	openclawBin := m.findOpenClaw()
	if openclawBin == "" {
		return "", fmt.Errorf("OpenClaw not found. Run ./scripts/bundle-openclaw.sh or install globally: npm install -g openclaw")
	}

	args := []string{
		"run",
		"--config", configPath,
		"--session-name", sessionName,
		"--message", task,
	}

	agent := newAgent(agentID, "openclaw", m.statusCh)
	if err := agent.start(openclawBin, args); err != nil {
		return "", fmt.Errorf("spawn agent with session: %w", err)
	}

	m.mu.Lock()
	m.agents[agentID] = agent
	m.mu.Unlock()

	// Wire agent conversation channel to manager's channel
	go m.forwardConversation(agent)

	// Auto-cleanup when agent exits
	go func() {
		agent.Wait()
		m.mu.Lock()
		delete(m.agents, agentID)
		m.mu.Unlock()
	}()

	return agentID, nil
}

// ResumeWithSession resumes an agent from a saved session.
func (m *Manager) ResumeWithSession(agentID, sessionName, configPath string) (string, error) {
	openclawBin := m.findOpenClaw()
	if openclawBin == "" {
		return "", fmt.Errorf("OpenClaw not found. Run ./scripts/bundle-openclaw.sh or install globally: npm install -g openclaw")
	}

	args := []string{
		"run",
		"--config", configPath,
		"--session-name", sessionName,
		"--message", "/resume",
	}

	agent := newAgent(agentID, "openclaw", m.statusCh)
	if err := agent.start(openclawBin, args); err != nil {
		return "", fmt.Errorf("resume agent with session: %w", err)
	}

	m.mu.Lock()
	m.agents[agentID] = agent
	m.mu.Unlock()

	go m.forwardConversation(agent)

	go func() {
		agent.Wait()
		m.mu.Lock()
		delete(m.agents, agentID)
		m.mu.Unlock()
	}()

	return agentID, nil
}

// PauseAgent saves state and stops an agent.
func (m *Manager) PauseAgent(agentID string) error {
	m.mu.RLock()
	agent, ok := m.agents[agentID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("agent %s not found", agentID)
	}

	if err := agent.SendMessage("/savestate"); err != nil {
		// Best effort — continue to stop even if send fails
		_ = err
	}
	time.Sleep(1 * time.Second)

	return agent.Stop()
}

// forwardConversation reads from an agent's conversationCh and sends to the manager's channel.
func (m *Manager) forwardConversation(agent *Agent) {
	for msg := range agent.conversationCh {
		select {
		case m.conversationCh <- msg:
		default:
			// Manager channel full, drop
		}
	}
}

// SendMessage sends a message to a running agent's stdin.
func (m *Manager) SendMessage(agentID, text string) error {
	m.mu.RLock()
	agent, ok := m.agents[agentID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("agent %s not found", agentID)
	}
	return agent.SendMessage(text)
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

// SpawnOpenClaw spawns a real OpenClaw agent using the VulpineOS-generated config.
// It sends a task message to OpenClaw's gateway to start an agent run.
func (m *Manager) SpawnOpenClaw(task string, agentSkills []config.SkillEntry) (string, error) {
	// Find OpenClaw binary
	openclawBin := m.findOpenClaw()
	if openclawBin == "" {
		return "", fmt.Errorf("OpenClaw not found. Run ./scripts/bundle-openclaw.sh or install globally: npm install -g openclaw")
	}

	id := uuid.New().String()[:8]

	// Build per-agent skill dirs if needed
	agentSkillDir := config.AgentSkillDir(id)

	// OpenClaw args: run with our config, send a task
	args := []string{
		"run",
		"--config", config.OpenClawConfigPath(),
		"--message", task,
	}

	// Add per-agent skill directory if there are agent-specific skills
	if len(agentSkills) > 0 {
		args = append(args, "--skills-dir", agentSkillDir)
	}

	agent := newAgent(id, "openclaw", m.statusCh)
	if err := agent.start(openclawBin, args); err != nil {
		return "", fmt.Errorf("spawn openclaw: %w", err)
	}

	m.mu.Lock()
	m.agents[id] = agent
	m.mu.Unlock()

	go func() {
		agent.Wait()
		m.mu.Lock()
		delete(m.agents, id)
		m.mu.Unlock()
	}()

	return id, nil
}

// findOpenClaw looks for the OpenClaw binary in common locations.
func (m *Manager) findOpenClaw() string {
	// Check repo-level node_modules (from npm install in VulpineOS root)
	repoLocal := []string{
		"./node_modules/.bin/openclaw",
		"./node_modules/openclaw/bin/openclaw.js",
	}
	for _, c := range repoLocal {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}

	// Check bundled openclaw directory
	bundled := []string{
		"./openclaw/start.sh",
		"./openclaw/node_modules/.bin/openclaw",
	}
	for _, c := range bundled {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}

	// Check global install
	if path, err := exec.LookPath("openclaw"); err == nil {
		return path
	}

	return ""
}

// OpenClawInstalled returns true if OpenClaw is available.
func (m *Manager) OpenClawInstalled() bool {
	return m.findOpenClaw() != ""
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

// Dispose kills all agents and closes channels.
func (m *Manager) Dispose() {
	m.KillAll()
	close(m.statusCh)
	close(m.conversationCh)
}
