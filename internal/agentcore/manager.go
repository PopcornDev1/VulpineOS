package agentcore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"

	"vulpineos/internal/juggler"
	"vulpineos/internal/nanoclaw"
	"vulpineos/internal/runtimeaudit"
)

// Manager is the native, in-process agent runtime. It implements the same
// method surface the orchestrator/TUI/remote use on the NanoClaw manager, so it
// is a drop-in alternative behind the agent_provider flag — but instead of
// spawning Docker containers via a daemon, it runs the model<->tool loop
// in-process and drives the host Camoufox directly through the MCP/Juggler
// tools.
//
// It emits the existing nanoclaw.ConversationMsg / nanoclaw.AgentStatus value
// types so existing consumers (TUI, web panel, remote broadcasts) work
// unchanged. That dependency is transitional and can move to a neutral package
// once NanoClaw is removed.
type Manager struct {
	client *juggler.Client
	cfg    Config

	mu     sync.RWMutex
	agents map[string]*nativeAgent
	closed bool
	audit  *runtimeaudit.Manager

	statusSource       chan nanoclaw.AgentStatus
	conversationSource chan nanoclaw.ConversationMsg
	statusSubs         map[chan nanoclaw.AgentStatus]struct{}
	conversationSubs   map[chan nanoclaw.ConversationMsg]struct{}
}

type nativeAgent struct {
	id        string
	contextID string
	cancel    context.CancelFunc
	cleanup   func()
	status    string
	objective string
}

// NewManager creates a native runtime bound to a juggler client and model
// config. The client drives the browser; cfg selects the provider/model.
func NewManager(client *juggler.Client, cfg Config) *Manager {
	m := &Manager{
		client:             client,
		cfg:                cfg,
		agents:             make(map[string]*nativeAgent),
		statusSource:       make(chan nanoclaw.AgentStatus, 64),
		conversationSource: make(chan nanoclaw.ConversationMsg, 64),
		statusSubs:         make(map[chan nanoclaw.AgentStatus]struct{}),
		conversationSubs:   make(map[chan nanoclaw.ConversationMsg]struct{}),
	}
	go m.fanOutStatus()
	go m.fanOutConversation()
	return m
}

// SetRuntimeAudit attaches a runtime audit manager.
func (m *Manager) SetRuntimeAudit(audit *runtimeaudit.Manager) {
	m.mu.Lock()
	m.audit = audit
	m.mu.Unlock()
}

// StatusChan returns a new subscriber channel for agent status updates.
func (m *Manager) StatusChan() <-chan nanoclaw.AgentStatus {
	ch := make(chan nanoclaw.AgentStatus, 64)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		close(ch)
		return ch
	}
	m.statusSubs[ch] = struct{}{}
	m.mu.Unlock()
	return ch
}

// ConversationChan returns a new subscriber channel for conversation messages.
func (m *Manager) ConversationChan() <-chan nanoclaw.ConversationMsg {
	ch := make(chan nanoclaw.ConversationMsg, 64)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		close(ch)
		return ch
	}
	m.conversationSubs[ch] = struct{}{}
	m.mu.Unlock()
	return ch
}

// SpawnIsolated starts a native agent on a pooled, caller-owned context. sopFile
// (or extraArgs) provides the task. configPath is ignored (no NanoClaw config).
func (m *Manager) SpawnIsolated(contextID string, sopFile string, configPath string, cleanup func(), extraArgs ...string) (string, error) {
	id := uuid.New().String()[:8]
	task := readTask(sopFile, extraArgs)
	return m.spawn(id, contextID, task, cleanup)
}

// SpawnWithSessionIsolated starts/continues a native agent turn. agentID/session
// identify the agent; task is the user message. The agent runs on contextID
// resolved from prior state, or a fresh page when none.
func (m *Manager) SpawnWithSessionIsolated(agentID, task, sessionName, configPath string, cleanup func()) (string, error) {
	return m.spawn(agentID, "", task, cleanup)
}

// ResumeWithSessionIsolated re-activates a saved session by re-running the loop
// with a continue instruction. (Native turns are request/response; resume is a
// fresh turn seeded with the continue prompt.)
func (m *Manager) ResumeWithSessionIsolated(agentID, sessionName, configPath string, cleanup func()) (string, error) {
	return m.spawn(agentID, "", "Continue from the saved session and resume the current task.", cleanup)
}

// spawn runs one agent turn in a goroutine, streaming events to subscribers.
func (m *Manager) spawn(agentID, contextID, task string, cleanup func()) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	ag := &nativeAgent{id: agentID, contextID: contextID, cancel: cancel, cleanup: cleanup, status: "running", objective: task}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		if cleanup != nil {
			cleanup()
		}
		return "", fmt.Errorf("manager closed")
	}
	if existing, ok := m.agents[agentID]; ok {
		existing.cancel()
	}
	m.agents[agentID] = ag
	m.mu.Unlock()

	ev := &managerEvents{m: m, agentID: agentID}
	m.emitStatus(agentID, contextID, "running", task)

	go func() {
		var err error
		if contextID != "" {
			_, err = RunBrowserAgentInContext(ctx, m.client, contextID, m.cfg, task, ev)
		} else {
			_, err = RunBrowserAgent(ctx, m.client, m.cfg, task, ev)
		}
		final := "completed"
		if err != nil {
			final = "error"
			m.emitConversation(agentID, "system", "agent error: "+err.Error())
			m.logRuntimeEvent("error", "native_agent_failed", err.Error(), map[string]string{"agent_id": agentID})
		}
		m.emitStatus(agentID, contextID, final, task)
		m.finish(agentID, ag)
	}()

	return agentID, nil
}

// Kill cancels a running agent's loop.
func (m *Manager) Kill(agentID string) error {
	m.mu.RLock()
	ag, ok := m.agents[agentID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("agent %s not found", agentID)
	}
	ag.cancel()
	m.emitStatus(agentID, ag.contextID, "interrupted", ag.objective)
	m.finish(agentID, ag)
	return nil
}

// PauseAgent stops the current turn. Native turns are request/response, so a
// pause is a cancel; the conversation history persists for the next turn.
func (m *Manager) PauseAgent(agentID string) error {
	m.mu.RLock()
	ag, ok := m.agents[agentID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("agent %s not found", agentID)
	}
	ag.cancel()
	m.emitStatus(agentID, ag.contextID, "paused", ag.objective)
	m.finish(agentID, ag)
	return nil
}

// Count returns the number of active agents.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.agents)
}

// List returns the status of active agents.
func (m *Manager) List() []nanoclaw.AgentStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]nanoclaw.AgentStatus, 0, len(m.agents))
	for _, ag := range m.agents {
		out = append(out, nanoclaw.AgentStatus{AgentID: ag.id, ContextID: ag.contextID, Status: ag.status, Objective: ag.objective})
	}
	return out
}

// KillAll cancels all running agents.
func (m *Manager) KillAll() {
	m.mu.Lock()
	agents := make([]*nativeAgent, 0, len(m.agents))
	for _, ag := range m.agents {
		agents = append(agents, ag)
	}
	m.mu.Unlock()
	for _, ag := range agents {
		ag.cancel()
		m.finish(ag.id, ag)
	}
}

// Dispose cancels all agents and closes subscriber channels.
func (m *Manager) Dispose() {
	m.KillAll()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.statusSource)
	close(m.conversationSource)
	for ch := range m.statusSubs {
		close(ch)
		delete(m.statusSubs, ch)
	}
	for ch := range m.conversationSubs {
		close(ch)
		delete(m.conversationSubs, ch)
	}
	m.mu.Unlock()
}

func (m *Manager) finish(agentID string, ag *nativeAgent) {
	m.mu.Lock()
	if cur, ok := m.agents[agentID]; ok && cur == ag {
		delete(m.agents, agentID)
	}
	m.mu.Unlock()
	if ag.cleanup != nil {
		ag.cleanup()
		ag.cleanup = nil
	}
}

func (m *Manager) emitStatus(agentID, contextID, status, objective string) {
	m.mu.Lock()
	if ag, ok := m.agents[agentID]; ok {
		ag.status = status
		ag.objective = objective
	}
	m.mu.Unlock()
	m.safeSendStatus(nanoclaw.AgentStatus{AgentID: agentID, ContextID: contextID, Status: status, Objective: objective})
}

func (m *Manager) emitConversation(agentID, role, content string) {
	m.safeSendConversation(nanoclaw.ConversationMsg{AgentID: agentID, Role: role, Content: content})
}

func (m *Manager) safeSendStatus(s nanoclaw.AgentStatus) {
	defer func() { _ = recover() }()
	select {
	case m.statusSource <- s:
	default:
	}
}

func (m *Manager) safeSendConversation(c nanoclaw.ConversationMsg) {
	defer func() { _ = recover() }()
	select {
	case m.conversationSource <- c:
	default:
	}
}

func (m *Manager) fanOutStatus() {
	for s := range m.statusSource {
		m.mu.RLock()
		for ch := range m.statusSubs {
			select {
			case ch <- s:
			default:
			}
		}
		m.mu.RUnlock()
	}
}

func (m *Manager) fanOutConversation() {
	for c := range m.conversationSource {
		m.mu.RLock()
		for ch := range m.conversationSubs {
			select {
			case ch <- c:
			default:
			}
		}
		m.mu.RUnlock()
	}
}

func (m *Manager) logRuntimeEvent(level, event, message string, metadata map[string]string) {
	m.mu.RLock()
	audit := m.audit
	m.mu.RUnlock()
	if audit == nil {
		return
	}
	_, _ = audit.Log("agentcore", level, event, message, metadata)
}

func readTask(sopFile string, extraArgs []string) string {
	if sopFile != "" {
		if data, err := os.ReadFile(sopFile); err == nil {
			if s := strings.TrimSpace(string(data)); s != "" {
				return s
			}
		}
	}
	if len(extraArgs) > 0 {
		return strings.Join(extraArgs, " ")
	}
	return "Start."
}

// managerEvents adapts agentcore loop Events to the manager's streaming channels.
type managerEvents struct {
	m       *Manager
	agentID string
}

func (e *managerEvents) OnTextDelta(delta string) {
	e.m.safeSendConversation(nanoclaw.ConversationMsg{AgentID: e.agentID, Role: "stream", Content: delta, StreamActive: true})
}
func (e *managerEvents) OnAssistant(text string) {
	if strings.TrimSpace(text) != "" {
		e.m.emitConversation(e.agentID, "assistant", text)
	}
}
func (e *managerEvents) OnToolCall(name, args string) {
	e.m.emitConversation(e.agentID, "tool", name)
}
func (e *managerEvents) OnToolResult(name, result string, isErr bool) {}
func (e *managerEvents) OnStatus(status string)                       {}
