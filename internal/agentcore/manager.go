package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

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
// running the model<->tool loop in-process and driving the host Vulpine browser
// directly through the MCP/Juggler tools.
//
// It emits the existing agentmsg.ConversationMsg / agentmsg.AgentStatus value
// types so existing consumers (TUI, web panel, remote broadcasts) work
// unchanged.
type Manager struct {
	client *juggler.Client
	cfg    Config
	cfgMu  sync.RWMutex

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
	// results holds completed sub-agent outputs so they survive finish()
	// deletion and remain readable by the lead agent across turns.
	results  map[string]string
	finished map[string]finishedAgent
}

type nativeAgent struct {
	id           string
	parentID     string // empty for lead agents, set for sub-agents
	contextID    string
	cancel       context.CancelFunc
	cleanup      func()
	status       string
	terminal     string
	objective    string
	tokens       int      // cumulative tokens consumed this run
	inbox        []string // steering messages from lead agent
	result       string   // final output text, set when sub-agent completes
	errText      string   // terminal error text, set when a sub-agent fails
	phase        string   // processing, waiting_on_tool, idle, finalizing
	turn         int      // current iteration in the agent loop
	maxTurns     int      // max iterations before forced stop
	lastActivity int64    // unix timestamp of last recorded activity
	observation  ObservationState
}

type finishedAgent struct {
	status agentmsg.AgentStatus
	result string
	err    string
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

// Reconfigure updates the provider/model config. Next agent spawns will use
// the new settings; already-running agents are unaffected.
func (m *Manager) Reconfigure(cfg Config) {
	m.cfgMu.Lock()
	m.cfg = cfg
	m.cfgMu.Unlock()
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
	// Prepend sub-agent status context for cross-turn reconnection so the lead
	// agent can see its prior sub-agents when starting a new turn.
	if s := m.buildSubAgentContext(agentID); s != "" {
		task = s + "\n\n" + task
	}

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
	m.logRuntimeEvent("info", "native_agent_started", "native agent started", map[string]string{
		"agent_id":   agentID,
		"context_id": contextID,
		"reuse_page": fmt.Sprintf("%t", reusePage),
		"task_bytes": fmt.Sprintf("%d", len(task)),
	})

	// Snapshot config so the goroutine is not affected by a concurrent
	// Reconfigure call.
	m.cfgMu.RLock()
	cfg := m.cfg
	m.cfgMu.RUnlock()

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
				_, err = RunBrowserAgentWithToolset(ctx, toolset, cfg, task, ev)
				if closeErr := toolset.CloseExtraTabs(); closeErr != nil {
					m.logRuntimeEvent("warn", "native_agent_extra_tabs_close_failed", closeErr.Error(), map[string]string{"agent_id": agentID})
				}
			}
		case contextID != "":
			_, err = RunBrowserAgentInContext(ctx, m.client, contextID, cfg, task, ev)
		default:
			_, err = RunBrowserAgent(ctx, m.client, cfg, task, ev)
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
		} else {
			m.logRuntimeEvent("info", "native_agent_completed", "native agent completed", map[string]string{"agent_id": agentID})
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
	ts := NewBrowserToolset(m.client, contextID, "")
	ts.SetDelegateManagerForParent(m, agentID)
	if _, err := openPageInContextWithToolset(ctx, m.client, contextID, ts); err != nil {
		ts.Close()
		return nil, fmt.Errorf("open page in context: %w", err)
	}
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

// Kill cancels a running agent's loop and releases its reused tab. When the
// target is a lead agent, its sub-agents are also cancelled (cascade kill).
// Idempotent: safe to call when the agent is idle between turns.
func (m *Manager) Kill(agentID string) error {
	m.mu.Lock()
	// Collect sub-agents to cascade kill.
	var subs []*nativeAgent
	for _, a := range m.agents {
		if a.parentID == agentID {
			a.terminal = "interrupted"
			subs = append(subs, a)
		}
	}
	ag, ok := m.agents[agentID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("agent %s not found", agentID)
	}
	ag.terminal = "interrupted"
	m.mu.Unlock()

	// Cancel sub-agents first (their goroutines clean up resources via defer).
	for _, sa := range subs {
		sa.cancel()
	}

	ag.cancel()
	m.emitStatus(agentID, ag.contextID, "interrupted", ag.objective)
	m.finish(agentID, ag)
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
		out = append(out, agentStatusFromNative(ag))
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
		// Save terminal sub-agent state so status/snapshot/result retrieval
		// remains diagnosable after browser context cleanup removes the live
		// agent entry.
		if m.finished == nil {
			m.finished = make(map[string]finishedAgent)
		}
		m.finished[agentID] = finishedAgent{
			status: agentStatusFromNative(ag),
			result: ag.result,
			err:    ag.errText,
		}
		if ag.status == "completed" && ag.result != "" {
			if m.results == nil {
				m.results = make(map[string]string)
			}
			m.results[agentID] = ag.result
		}
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
	out := agentmsg.AgentStatus{
		AgentID:   agentID,
		ContextID: contextID,
		Status:    status,
		Objective: objective,
	}
	if ag, ok := m.agents[agentID]; ok {
		ag.status = status
		ag.objective = objective
		out = agentStatusFromNative(ag)
	}
	m.mu.Unlock()
	m.safeSendStatus(out)
}

func agentStatusFromNative(ag *nativeAgent) agentmsg.AgentStatus {
	if ag == nil {
		return agentmsg.AgentStatus{}
	}
	status := agentmsg.AgentStatus{
		AgentID:      ag.id,
		ParentID:     ag.parentID,
		ContextID:    ag.contextID,
		Status:       ag.status,
		Objective:    ag.objective,
		Tokens:       ag.tokens,
		Phase:        ag.phase,
		Turn:         ag.turn,
		MaxTurns:     ag.maxTurns,
		LastActivity: ag.lastActivity,
	}
	if ag.observation.Confidence != "" {
		status.ObservationConfidence = string(ag.observation.Confidence)
		status.ObservationSummary = ag.observation.LastObservedSummary
		status.ObservationURL = ag.observation.URL
		status.LastFailedTool = ag.observation.LastFailedTool
	}
	return status
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

// Delegate spawns a sub-agent with a composed prompt from the given mission.
// Returns the new agent's ID. The sub-agent runs asynchronously.
func (m *Manager) Delegate(mission Mission) (string, error) {
	return m.DelegateForParentMission(mission, "")
}

func missionMaxTurns(mission Mission) int {
	if mission.MaxTurns > 0 {
		return mission.MaxTurns
	}
	return defaultMissionMaxTurns
}

// DelegateForParentMission spawns a sub-agent with a known parent lead agent ID.
func (m *Manager) DelegateForParentMission(mission Mission, parentID string) (string, error) {
	id := uuid.New().String()[:8]
	task := composeSubAgentTask(mission)

	ctx, cancel := context.WithCancel(context.Background())
	maxTurns := missionMaxTurns(mission)
	ag := &nativeAgent{
		id:           id,
		parentID:     parentID,
		cancel:       cancel,
		status:       "running",
		objective:    task,
		inbox:        make([]string, 0),
		maxTurns:     maxTurns,
		lastActivity: time.Now().Unix(),
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		return "", fmt.Errorf("manager closed")
	}
	m.agents[id] = ag
	m.wg.Add(1)
	m.mu.Unlock()

	ev := &managerEvents{m: m, agentID: id}
	m.emitStatus(id, "", "running", task)
	m.logRuntimeEvent("info", "sub_agent_started", "sub-agent started", map[string]string{
		"agent_id":  id,
		"parent_id": parentID,
		"objective": truncateString(task, 80),
	})

	m.cfgMu.RLock()
	cfg := m.cfg
	m.cfgMu.RUnlock()

	// Create an isolated browser context for the sub-agent.
	var subCtxID string
	var subToolset *BrowserToolset
	if m.client != nil {
		var subErr error
		sctx, scancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer scancel()
		subCtxID, _, subToolset, subErr = newSubAgentContext(sctx, m.client)
		if subErr != nil {
			m.mu.Lock()
			delete(m.agents, id)
			m.mu.Unlock()
			cancel()
			return "", fmt.Errorf("create sub-agent browser context: %w", subErr)
		}
		m.mu.Lock()
		m.toolsets[id] = subToolset
		m.mu.Unlock()
	}

	go func() {
		defer m.wg.Done()

		// Ensure the sub-agent's isolated browser context and toolset are
		// cleaned up no matter how this goroutine exits (normal completion,
		// error, or kill/interrupt).
		defer func() {
			if subCtxID != "" {
				m.mu.Lock()
				delete(m.toolsets, id)
				m.mu.Unlock()
				cleanupSubAgentContext(m.client, subCtxID, subToolset)
			}
		}()

		var err error

		prompt := ComposeSubAgentPrompt(mission)
		loop := NewLoop(newCompleter(cfg), subToolset, ev, LoopConfig{
			Models:        cfg.modelChain(),
			SystemPrompt:  prompt,
			Tools:         subAgentTools(),
			MaxIterations: maxTurns,
			ModelTimeout:  120 * time.Second, // prevent model API hang from blocking the sub-agent indefinitely
			InboxReader: func() []string {
				m.mu.Lock()
				defer m.mu.Unlock()
				if cur, ok := m.agents[id]; ok && cur == ag {
					msgs := cur.inbox
					cur.inbox = nil
					return msgs
				}
				return nil
			},
		})
		var subResult string
		subResult, err = loop.Run(ctx, task, nil)

		final := "completed"
		if err != nil {
			final = "error"
			m.mu.Lock()
			if cur, ok := m.agents[id]; ok && cur == ag {
				ag.errText = err.Error()
			}
			m.mu.Unlock()
			m.emitConversation(id, "system", "sub-agent error: "+err.Error())
			m.logRuntimeEvent("error", "sub_agent_failed", err.Error(), map[string]string{"agent_id": id})
		} else {
			m.mu.Lock()
			if cur, ok := m.agents[id]; ok && cur == ag {
				ag.result = subResult
			}
			m.mu.Unlock()
		}
		m.mu.Lock()
		ag.status = final
		m.mu.Unlock()
		m.emitStatus(id, subCtxID, final, task)
		m.finish(id, ag)
	}()

	return id, nil
}

// SteerAgent sends a mid-task guidance message to a running sub-agent.
func (m *Manager) SteerAgent(agentID, message string) error {
	m.mu.Lock()
	ag, ok := m.agents[agentID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("agent %s not found", agentID)
	}
	ag.inbox = append(ag.inbox, message)
	m.mu.Unlock()
	return nil
}

// AgentStatus returns the current status of an agent.
func (m *Manager) AgentStatus(agentID string) (string, error) {
	m.mu.RLock()
	ag, ok := m.agents[agentID]
	if ok {
		status := ag.status
		m.mu.RUnlock()
		return status, nil
	}
	if done, ok := m.finished[agentID]; ok {
		status := done.status.Status
		m.mu.RUnlock()
		return status, nil
	}
	if _, ok := m.results[agentID]; ok {
		m.mu.RUnlock()
		return "completed", nil
	}
	m.mu.RUnlock()
	return "", fmt.Errorf("agent %s not found", agentID)
}

// AgentResult returns the final output of a completed sub-agent. It checks live
// agents first, then falls back to the results cache (which outlives finished
// agents after finish() removes them from the map).
func (m *Manager) AgentResult(agentID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if ag, ok := m.agents[agentID]; ok {
		if ag.status != "completed" {
			return "", fmt.Errorf("agent %s has status %q, not completed", agentID, ag.status)
		}
		return ag.result, nil
	}
	if done, ok := m.finished[agentID]; ok {
		if done.status.Status != "completed" {
			if done.err != "" {
				return "", fmt.Errorf("agent %s has status %q: %s", agentID, done.status.Status, done.err)
			}
			return "", fmt.Errorf("agent %s has status %q, not completed", agentID, done.status.Status)
		}
		return done.result, nil
	}
	if m.results != nil {
		if result, ok := m.results[agentID]; ok {
			return result, nil
		}
	}
	return "", fmt.Errorf("agent %s not found", agentID)
}

// AgentSnapshot returns a detailed JSON snapshot of an agent's current state
// for diagnostic and debugging use by lead agents. Unlike AgentStatus (a single
// word), the snapshot includes phase, turn, last activity, and run metadata so
// the lead agent can distinguish active agents from stuck/idle ones.
func (m *Manager) AgentSnapshot(agentID string) (string, error) {
	m.mu.RLock()
	ag, ok := m.agents[agentID]
	if !ok {
		if done, ok := m.finished[agentID]; ok {
			m.mu.RUnlock()
			return formatAgentSnapshot(done.status, done.result != "", done.err), nil
		}
		// Check results cache for completed agents that were cleaned up.
		wasCached := false
		if m.results != nil {
			_, wasCached = m.results[agentID]
		}
		m.mu.RUnlock()
		if wasCached {
			return fmt.Sprintf(`{"agent_id":%q,"status":"completed","result_available":true}`, agentID), nil
		}
		return "", fmt.Errorf("agent %s not found", agentID)
	}
	status := agentStatusFromNative(ag)
	hasResult := ag.result != ""
	m.mu.RUnlock()

	return formatAgentSnapshot(status, hasResult, ""), nil
}

func formatAgentSnapshot(status agentmsg.AgentStatus, resultAvailable bool, errText string) string {
	out := struct {
		AgentID               string `json:"agent_id"`
		ParentID              string `json:"parent_id,omitempty"`
		ContextID             string `json:"context_id,omitempty"`
		Status                string `json:"status"`
		Phase                 string `json:"phase,omitempty"`
		Objective             string `json:"objective,omitempty"`
		Turn                  int    `json:"turn,omitempty"`
		MaxTurns              int    `json:"max_turns,omitempty"`
		LastActivity          int64  `json:"last_activity_at,omitempty"`
		Tokens                int    `json:"tokens,omitempty"`
		ResultAvailable       bool   `json:"result_available"`
		Error                 string `json:"error,omitempty"`
		ObservationConfidence string `json:"observation_confidence,omitempty"`
		ObservationSummary    string `json:"observation_summary,omitempty"`
		ObservationURL        string `json:"observation_url,omitempty"`
		LastFailedTool        string `json:"last_failed_tool,omitempty"`
	}{
		AgentID:               status.AgentID,
		ParentID:              status.ParentID,
		ContextID:             status.ContextID,
		Status:                status.Status,
		Phase:                 status.Phase,
		Objective:             status.Objective,
		Turn:                  status.Turn,
		MaxTurns:              status.MaxTurns,
		LastActivity:          status.LastActivity,
		Tokens:                status.Tokens,
		ResultAvailable:       resultAvailable,
		Error:                 errText,
		ObservationConfidence: status.ObservationConfidence,
		ObservationSummary:    status.ObservationSummary,
		ObservationURL:        status.ObservationURL,
		LastFailedTool:        status.LastFailedTool,
	}
	data, err := json.Marshal(out)
	if err != nil {
		return fmt.Sprintf(`{"agent_id":%q,"status":%q,"result_available":%t}`, status.AgentID, status.Status, resultAvailable)
	}
	return string(data)
}

// ReleaseAgent terminates a sub-agent and cleans up its resources.
func (m *Manager) ReleaseAgent(agentID string) error {
	return m.Kill(agentID)
}

// buildSubAgentContext returns a formatted string describing the sub-agents of
// the given lead agent that are still tracked in the Manager, or "" when none.
// Used for cross-turn reconnection — the next turn of the lead agent receives
// this context so it can steer/collect/release its existing sub-agents.
func (m *Manager) buildSubAgentContext(agentID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var lines []string
	for _, ag := range m.agents {
		if ag.parentID == agentID {
			status := ag.status
			obj := truncateString(ag.objective, 80)
			lines = append(lines, fmt.Sprintf("  - %s: %s — %s", ag.id, status, obj))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "Your sub-agents from previous turns:\n" + strings.Join(lines, "\n") + "\n\nTake stock of their state and decide whether to collect results, steer, release, or delegate new work."
}

// truncateString truncates a string to max runes for logging.
func truncateString(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

// composeSubAgentTask builds the user task string for a sub-agent.
func composeSubAgentTask(m Mission) string {
	return m.Objective
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
	metadata := map[string]string{"agent_id": e.agentID, "tool": name}
	if args = safeToolCallArgsSummary(name, args); args != "" {
		metadata["args"] = args
	}
	e.m.logRuntimeEvent("info", "native_agent_tool_call", "tool call: "+name, metadata)
}
func (e *managerEvents) OnToolResult(name, result string, isErr bool) {
	if isErr {
		e.m.emitConversation(e.agentID, "system", fmt.Sprintf("Tool failed: %s — %s", name, traceSnippet(result)))
		e.m.logRuntimeEvent("warn", "native_agent_tool_failed", "tool failed: "+name, map[string]string{
			"agent_id": e.agentID,
			"tool":     name,
			"result":   traceSnippet(result),
		})
		return
	}
	e.m.emitConversation(e.agentID, "system", toolCompletionSummary(name, result))
	e.m.logRuntimeEvent("info", "native_agent_tool_completed", "tool completed: "+name, map[string]string{
		"agent_id": e.agentID,
		"tool":     name,
	})
}
func (e *managerEvents) OnStatus(status string) {}
func (e *managerEvents) OnUsage(turn Usage)     { e.m.addTokens(e.agentID, turn) }
func (e *managerEvents) OnWarning(text string) {
	e.m.emitConversation(e.agentID, "system", "Warning: "+text)
}
func (e *managerEvents) OnObservationState(state ObservationState) {
	state = normalizeObservationState(state)
	status := e.m.updateObservationState(e.agentID, state)
	if status.AgentID != "" {
		e.m.safeSendStatus(status)
	}

	event, level, message := observationRuntimeEvent(state)
	metadata := map[string]string{
		"agent_id":   e.agentID,
		"confidence": state.Confidence,
	}
	if state.LastObservedTool != "" {
		metadata["last_observed_tool"] = state.LastObservedTool
	}
	if state.LastFailedTool != "" {
		metadata["last_failed_tool"] = state.LastFailedTool
	}
	if state.URL != "" {
		metadata["url"] = state.URL
	}
	if state.Title != "" {
		metadata["title"] = state.Title
	}
	if state.LastObservedSummary != "" {
		metadata["summary"] = state.LastObservedSummary
	}
	if state.LastFailure != "" {
		metadata["failure"] = traceSnippet(state.LastFailure)
	}
	e.m.logRuntimeEvent(level, event, message, metadata)

	if state.Confidence == ObservationUnverified || state.Confidence == ObservationLost {
		e.m.emitConversation(e.agentID, "system", observationConversationWarning(state))
	}
}
func (e *managerEvents) OnPhase(phase string) {
	e.m.mu.Lock()
	if ag, ok := e.m.agents[e.agentID]; ok {
		ag.phase = phase
		ag.lastActivity = time.Now().Unix()
	}
	e.m.mu.Unlock()
}

func (m *Manager) updateObservationState(agentID string, state ObservationState) agentmsg.AgentStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	ag, ok := m.agents[agentID]
	if !ok {
		return agentmsg.AgentStatus{}
	}
	ag.observation = state
	ag.lastActivity = time.Now().Unix()
	return agentStatusFromNative(ag)
}

func normalizeObservationState(state ObservationState) ObservationState {
	state.Confidence = strings.TrimSpace(state.Confidence)
	switch state.Confidence {
	case ObservationObserved, ObservationUnverified, ObservationLost:
	default:
		state.Confidence = ObservationUnverified
	}
	state.LastObservedTool = strings.TrimSpace(state.LastObservedTool)
	state.LastObservedSummary = strings.TrimSpace(state.LastObservedSummary)
	state.LastFailedTool = strings.TrimSpace(state.LastFailedTool)
	state.LastFailure = strings.TrimSpace(state.LastFailure)
	state.URL = strings.TrimSpace(state.URL)
	state.Title = strings.TrimSpace(state.Title)
	if state.Confidence == ObservationLost {
		state.Lost = true
	}
	return state
}

func observationRuntimeEvent(state ObservationState) (event, level, message string) {
	switch state.Confidence {
	case ObservationObserved:
		return "browser_observation_observed", "info", "browser observation refreshed"
	case ObservationLost:
		return "browser_observation_lost", "warn", "browser observation lost"
	default:
		return "browser_observation_unverified", "warn", "browser observation unverified after tool failure"
	}
}

func observationConversationWarning(state ObservationState) string {
	if state.Confidence == ObservationLost {
		summary := state.LastObservedSummary
		if summary == "" && state.URL != "" {
			summary = "url=" + state.URL
		}
		if summary == "" {
			summary = "browser page appears blank or unavailable"
		}
		return "Observation warning: browser state appears lost (" + summary + "). Retry observation, use visual recovery, or ask the user to take over."
	}
	parts := []string{"Observation warning: browser state is unverified"}
	if state.LastFailedTool != "" {
		parts = append(parts, "after "+state.LastFailedTool+" failed")
	}
	if state.LastObservedSummary != "" {
		parts = append(parts, "last confirmed: "+state.LastObservedSummary)
	}
	return strings.Join(parts, "; ") + "."
}
func (e *managerEvents) OnTurn(turn int) {
	e.m.mu.Lock()
	if ag, ok := e.m.agents[e.agentID]; ok {
		ag.turn = turn
		ag.lastActivity = time.Now().Unix()
	}
	e.m.mu.Unlock()
}

// toolCallSummary renders a concise "name {key args}" label for the trace.
func toolCallSummary(name, args string) string {
	args = strings.TrimSpace(args)
	if args == "" || args == "{}" || args == "null" {
		return name
	}
	summary := safeToolCallArgsSummary(name, args)
	if summary == "" {
		return name
	}
	return name + " " + summary
}

func toolCompletionSummary(name, result string) string {
	result = traceSnippet(result)
	switch name {
	case "vulpine_click_ref", "vulpine_type_ref", "vulpine_hover_ref", "vulpine_scroll_into_view", "vulpine_click_label":
		if result != "" {
			return fmt.Sprintf("Tool completed: %s - %s", name, result)
		}
	}
	return fmt.Sprintf("Tool completed: %s", name)
}

// traceSnippet collapses a value to a single short line for operator trace rows.
func traceSnippet(s string) string {
	s = redactTraceText(s)
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " "))
	const max = 160
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

func safeToolCallArgsSummary(name, args string) string {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return ""
	}
	out := map[string]interface{}{}
	copyStringArg := func(key string) {
		if v, ok := raw[key].(string); ok && strings.TrimSpace(v) != "" {
			out[key] = traceSnippet(v)
		}
	}
	copyNumberArg := func(key string) {
		switch v := raw[key].(type) {
		case float64, int, int64:
			out[key] = v
		}
	}

	switch name {
	case "vulpine_navigate":
		if v, ok := raw["url"].(string); ok && strings.TrimSpace(v) != "" {
			out["url"] = redactTraceURL(v)
		}
	case "vulpine_click_ref", "vulpine_type_ref", "vulpine_hover_ref", "vulpine_scroll_into_view", "vulpine_element_status":
		copyStringArg("ref")
	case "vulpine_click_label":
		copyStringArg("label")
	case "vulpine_click":
		copyNumberArg("x")
		copyNumberArg("y")
	case "vulpine_scroll", "vulpine_human_scroll":
		copyNumberArg("deltaY")
	case "vulpine_wait":
		copyStringArg("condition")
		copyStringArg("ref")
	case "vulpine_page_settled":
		copyNumberArg("timeout")
	default:
		return ""
	}
	if len(out) == 0 {
		return ""
	}
	data, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(data)
}

func redactTraceURL(raw string) string {
	raw = strings.TrimSpace(redactTraceText(raw))
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return traceSnippet(raw)
	}
	if parsed.User != nil {
		parsed.User = url.UserPassword("redacted", "redacted")
	}
	query := parsed.Query()
	redacted := false
	for key := range query {
		if sensitiveTraceKey(key) {
			query.Set(key, "[redacted]")
			redacted = true
		}
	}
	if redacted {
		parsed.RawQuery = query.Encode()
	}
	return traceSnippet(parsed.String())
}

var traceRedactors = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`), `${1}redacted:redacted@`},
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`), `${1}[redacted]`},
	{regexp.MustCompile(`(?i)([?&][^=\s&]*(?:api[_-]?key|apikey|token|secret|password|passwd|credential|authorization|cookie|session)[^=\s&]*=)[^&\s]+`), `${1}[redacted]`},
	{regexp.MustCompile(`(?i)(\b(?:api[_-]?key|apikey|token|secret|password|passwd|credential|authorization|cookie|session)\b\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,}]+)`), `${1}[redacted]`},
}

func redactTraceText(value string) string {
	for _, redactor := range traceRedactors {
		value = redactor.re.ReplaceAllString(value, redactor.repl)
	}
	return value
}

func sensitiveTraceKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
	for _, marker := range []string{"api_key", "apikey", "token", "secret", "password", "passwd", "credential", "authorization", "cookie", "session"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
