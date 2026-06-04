package agentcore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"

	"vulpineos/internal/agentmsg"
	"vulpineos/internal/juggler"
	"vulpineos/internal/runtimeaudit"
	"vulpineos/internal/vault"
)

// agentStore is the minimal vault surface the native runtime needs to make chat
// stateful across turns: resolving the agent's pooled browser context so each
// turn reuses the same identity-applied context instead of a throwaway one.
type agentStore interface {
	GetAgent(id string) (*vault.Agent, error)
}

// Manager is the native, in-process agent runtime. It implements the method
// surface the orchestrator, TUI, and remote API use for agent execution, while
// running the model<->tool loop in-process and driving the host Camoufox
// directly through the MCP/Juggler tools.
//
// It emits the existing agentmsg.ConversationMsg / agentmsg.AgentStatus value
// types so existing consumers (TUI, web panel, remote broadcasts) work
// unchanged.
type Manager struct {
	client *juggler.Client
	cfg    Config

	mu     sync.RWMutex
	agents map[string]*nativeAgent
	closed bool
	audit  *runtimeaudit.Manager
	store  agentStore
	wg     sync.WaitGroup
	fanout sync.WaitGroup
	// toolsets holds one persistent browser toolset (one reused page/tab + its
	// MCP execution-context tracker) per agent id, so successive chat turns reuse
	// the same tab instead of opening a new one each time. Closed on Kill.
	toolsets map[string]*BrowserToolset

	statusSource       chan agentmsg.AgentStatus
	conversationSource chan agentmsg.ConversationMsg
	statusSubs         map[chan agentmsg.AgentStatus]struct{}
	conversationSubs   map[chan agentmsg.ConversationMsg]struct{}
}

type nativeAgent struct {
	id        string
	contextID string
	cancel    context.CancelFunc
	cleanup   func()
	status    string
	terminal  string
	objective string
	tokens    int // cumulative tokens consumed this run
}

// NewManager creates a native runtime bound to a juggler client and model
// config. The client drives the browser; cfg selects the provider/model.
func NewManager(client *juggler.Client, cfg Config) *Manager {
	m := &Manager{
		client:             client,
		cfg:                cfg,
		agents:             make(map[string]*nativeAgent),
		toolsets:           make(map[string]*BrowserToolset),
		statusSource:       make(chan agentmsg.AgentStatus, 64),
		conversationSource: make(chan agentmsg.ConversationMsg, 64),
		statusSubs:         make(map[chan agentmsg.AgentStatus]struct{}),
		conversationSubs:   make(map[chan agentmsg.ConversationMsg]struct{}),
	}
	m.fanout.Add(2)
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

// SetVault attaches the vault so chat turns reuse the agent's pooled, identity-
// applied browser context (resolved from agent metadata) instead of a fresh
// throwaway context each turn.
func (m *Manager) SetVault(store agentStore) {
	m.mu.Lock()
	m.store = store
	m.mu.Unlock()
}

// resolveContextID returns the agent's stored browser context id (set by the
// orchestrator's EnsureAgentBrowserContext), or "" when unknown.
func (m *Manager) resolveContextID(agentID string) string {
	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return ""
	}
	a, err := store.GetAgent(agentID)
	if err != nil || a == nil {
		return ""
	}
	meta, err := vault.ParseAgentMetadata(a.Metadata)
	if err != nil {
		return ""
	}
	return meta.ContextID
}

// StatusChan returns a new subscriber channel for agent status updates.
func (m *Manager) StatusChan() <-chan agentmsg.AgentStatus {
	ch := make(chan agentmsg.AgentStatus, 64)
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
func (m *Manager) ConversationChan() <-chan agentmsg.ConversationMsg {
	ch := make(chan agentmsg.ConversationMsg, 64)
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
// (or extraArgs) provides the task. configPath is ignored by the native runtime.
func (m *Manager) SpawnIsolated(contextID string, sopFile string, configPath string, cleanup func(), extraArgs ...string) (string, error) {
	id := uuid.New().String()[:8]
	task := readTask(sopFile, extraArgs)
	return m.spawn(id, contextID, task, false, cleanup)
}

// SpawnWithSessionIsolated starts/continues a native agent turn. agentID/session
// identify the agent; task is the user message (which already carries recent
// visible chat history via the caller's turn prompt). The turn runs on the
// agent's pooled, identity-applied context when one is known, so cookies,
// storage, and identity persist across turns — matching the prior runtime.
func (m *Manager) SpawnWithSessionIsolated(agentID, task, sessionName, configPath string, cleanup func()) (string, error) {
	return m.spawn(agentID, m.resolveContextID(agentID), task, true, cleanup)
}

// ResumeWithSessionIsolated re-activates an agent by running a fresh turn seeded
// with a continue instruction, on the agent's pooled context.
func (m *Manager) ResumeWithSessionIsolated(agentID, sessionName, configPath string, cleanup func()) (string, error) {
	return m.spawn(agentID, m.resolveContextID(agentID), "Continue from the saved session and resume the current task.", true, cleanup)
}

// spawn runs one agent turn in a goroutine, streaming events to subscribers.
// reusePage keeps a persistent per-agent tab (and MCP tracker) so successive
// turns reuse the same page instead of opening a new tab each time.
func (m *Manager) spawn(agentID, contextID, task string, reusePage bool, cleanup func()) (string, error) {
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
		existing.terminal = "interrupted"
		existing.cancel()
	}
	m.agents[agentID] = ag
	m.wg.Add(1)
	m.mu.Unlock()

	ev := &managerEvents{m: m, agentID: agentID}
	m.emitStatus(agentID, contextID, "running", task)

	go func() {
		defer m.wg.Done()
		var err error
		switch {
		case contextID != "" && reusePage:
			// Reuse the agent's single tab + tracker across turns; the agent
			// navigates that tab wherever the task needs.
			var toolset *BrowserToolset
			toolset, err = m.acquireToolset(ctx, agentID, contextID)
			if err == nil {
				_, err = RunBrowserAgentWithToolset(ctx, toolset, m.cfg, task, ev)
			}
		case contextID != "":
			_, err = RunBrowserAgentInContext(ctx, m.client, contextID, m.cfg, task, ev)
		default:
			_, err = RunBrowserAgent(ctx, m.client, m.cfg, task, ev)
		}
		final := "completed"
		if err != nil {
			final = "error"
			m.mu.RLock()
			terminal := ag.terminal
			m.mu.RUnlock()
			if terminal != "" && errors.Is(err, context.Canceled) {
				m.finish(agentID, ag)
				return
			}
			if terminal != "" {
				final = terminal
			} else {
				m.emitConversation(agentID, "system", "agent error: "+err.Error())
				m.logRuntimeEvent("error", "native_agent_failed", err.Error(), map[string]string{"agent_id": agentID})
			}
		}
		m.emitStatus(agentID, contextID, final, task)
		m.finish(agentID, ag)
	}()

	return agentID, nil
}

// acquireToolset returns the agent's persistent browser toolset (one reused
// tab + MCP tracker), creating it on first use by opening a page in the agent's
// context. Kept alive across turns so the page and its execution contexts are
// reused rather than recreated each task.
func (m *Manager) acquireToolset(ctx context.Context, agentID, contextID string) (*BrowserToolset, error) {
	m.mu.RLock()
	existing := m.toolsets[agentID]
	m.mu.RUnlock()
	if existing != nil {
		return existing, nil
	}
	sid, err := openPageSession(ctx, m.client, contextID)
	if err != nil {
		return nil, fmt.Errorf("open page in context: %w", err)
	}
	ts := NewBrowserToolset(m.client, contextID, sid)
	m.mu.Lock()
	if raced := m.toolsets[agentID]; raced != nil {
		m.mu.Unlock()
		ts.Close()
		return raced, nil
	}
	m.toolsets[agentID] = ts
	m.mu.Unlock()
	return ts, nil
}

// closeToolset closes and forgets an agent's persistent toolset (e.g. on kill,
// when the orchestrator releases the underlying context).
func (m *Manager) closeToolset(agentID string) {
	m.mu.Lock()
	ts := m.toolsets[agentID]
	delete(m.toolsets, agentID)
	m.mu.Unlock()
	if ts != nil {
		ts.Close()
	}
}

// Kill cancels a running agent's loop and releases its reused tab. Idempotent:
// safe to call when the agent is idle between turns.
func (m *Manager) Kill(agentID string) error {
	m.mu.Lock()
	ag, ok := m.agents[agentID]
	if ok {
		ag.terminal = "interrupted"
	}
	m.mu.Unlock()
	if ok {
		ag.cancel()
		m.emitStatus(agentID, ag.contextID, "interrupted", ag.objective)
		m.finish(agentID, ag)
	}
	m.closeToolset(agentID)
	return nil
}

// PauseAgent stops the current turn. Native turns are request/response, so a
// pause is a cancel; the conversation history persists for the next turn.
func (m *Manager) PauseAgent(agentID string) error {
	m.mu.Lock()
	ag, ok := m.agents[agentID]
	if ok {
		ag.terminal = "paused"
	}
	m.mu.Unlock()
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
func (m *Manager) List() []agentmsg.AgentStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]agentmsg.AgentStatus, 0, len(m.agents))
	for _, ag := range m.agents {
		out = append(out, agentmsg.AgentStatus{AgentID: ag.id, ContextID: ag.contextID, Status: ag.status, Objective: ag.objective})
	}
	return out
}

// KillAll cancels all running agents and closes their reused tabs.
func (m *Manager) KillAll() {
	m.mu.Lock()
	agents := make([]*nativeAgent, 0, len(m.agents))
	for _, ag := range m.agents {
		ag.terminal = "interrupted"
		agents = append(agents, ag)
	}
	toolsets := make([]*BrowserToolset, 0, len(m.toolsets))
	for id, ts := range m.toolsets {
		toolsets = append(toolsets, ts)
		delete(m.toolsets, id)
	}
	m.mu.Unlock()
	for _, ag := range agents {
		ag.cancel()
		m.finish(ag.id, ag)
	}
	for _, ts := range toolsets {
		ts.Close()
	}
}

// Dispose cancels all agents and closes subscriber channels.
func (m *Manager) Dispose() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	agents := make([]*nativeAgent, 0, len(m.agents))
	for _, ag := range m.agents {
		ag.terminal = "interrupted"
		agents = append(agents, ag)
	}
	toolsets := make([]*BrowserToolset, 0, len(m.toolsets))
	for id, ts := range m.toolsets {
		toolsets = append(toolsets, ts)
		delete(m.toolsets, id)
	}
	close(m.statusSource)
	close(m.conversationSource)
	m.mu.Unlock()

	for _, ag := range agents {
		ag.cancel()
	}
	for _, ts := range toolsets {
		ts.Close()
	}
	m.wg.Wait()
	m.fanout.Wait()

	m.mu.Lock()
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
	var cleanup func()
	m.mu.Lock()
	if cur, ok := m.agents[agentID]; ok && cur == ag {
		delete(m.agents, agentID)
	}
	if ag.cleanup != nil {
		cleanup = ag.cleanup
		ag.cleanup = nil
	}
	m.mu.Unlock()
	if cleanup != nil {
		cleanup()
	}
}

func (m *Manager) emitStatus(agentID, contextID, status, objective string) {
	m.mu.Lock()
	tokens := 0
	if ag, ok := m.agents[agentID]; ok {
		ag.status = status
		ag.objective = objective
		tokens = ag.tokens
	}
	m.mu.Unlock()
	m.safeSendStatus(agentmsg.AgentStatus{AgentID: agentID, ContextID: contextID, Status: status, Objective: objective, Tokens: tokens})
}

func (m *Manager) emitConversation(agentID, role, content string) {
	m.safeSendConversation(agentmsg.ConversationMsg{AgentID: agentID, Role: role, Content: content})
}

// addTokens accumulates a turn's token usage onto the agent's running total.
func (m *Manager) addTokens(agentID string, turn Usage) {
	m.mu.Lock()
	if ag, ok := m.agents[agentID]; ok {
		ag.tokens += turn.TotalTokens
	}
	m.mu.Unlock()
}

// agentTokens returns the agent's cumulative token count.
func (m *Manager) agentTokens(agentID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if ag, ok := m.agents[agentID]; ok {
		return ag.tokens
	}
	return 0
}

func (m *Manager) safeSendStatus(s agentmsg.AgentStatus) {
	defer func() { _ = recover() }()
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return
	}
	select {
	case m.statusSource <- s:
	default:
	}
}

func (m *Manager) safeSendConversation(c agentmsg.ConversationMsg) {
	defer func() { _ = recover() }()
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return
	}
	select {
	case m.conversationSource <- c:
	default:
	}
}

func (m *Manager) fanOutStatus() {
	defer m.fanout.Done()
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
	defer m.fanout.Done()
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
// stream accumulates the in-progress assistant text so each "stream" message
// carries the full text-so-far (consumers replace the live entry in place),
// rather than a lone token; it is reset when an assistant message finalizes.
type managerEvents struct {
	m       *Manager
	agentID string
	stream  strings.Builder
}

func (e *managerEvents) OnTextDelta(delta string) {
	e.stream.WriteString(delta)
	e.m.safeSendConversation(agentmsg.ConversationMsg{AgentID: e.agentID, Role: "stream", Content: e.stream.String(), StreamActive: true})
}
func (e *managerEvents) OnAssistant(text string) {
	e.stream.Reset()
	if strings.TrimSpace(text) != "" {
		e.m.safeSendConversation(agentmsg.ConversationMsg{AgentID: e.agentID, Role: "assistant", Content: text, Tokens: e.m.agentTokens(e.agentID)})
	}
}
func (e *managerEvents) OnToolCall(name, args string) {
	e.m.emitConversation(e.agentID, "system", "Running tool: "+toolCallSummary(name, args))
}
func (e *managerEvents) OnToolResult(name, result string, isErr bool) {
	if isErr {
		e.m.emitConversation(e.agentID, "system", fmt.Sprintf("Tool failed: %s — %s", name, traceSnippet(result)))
		return
	}
	e.m.emitConversation(e.agentID, "system", fmt.Sprintf("Tool completed: %s", name))
}
func (e *managerEvents) OnStatus(status string) {}
func (e *managerEvents) OnUsage(turn Usage)     { e.m.addTokens(e.agentID, turn) }
func (e *managerEvents) OnWarning(text string) {
	e.m.emitConversation(e.agentID, "system", "Warning: "+text)
}

// toolCallSummary renders a concise "name {key args}" label for the trace.
func toolCallSummary(name, args string) string {
	args = strings.TrimSpace(args)
	if args == "" || args == "{}" || args == "null" {
		return name
	}
	return name + " " + traceSnippet(args)
}

// traceSnippet collapses a value to a single short line for operator trace rows.
func traceSnippet(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " "))
	const max = 160
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
