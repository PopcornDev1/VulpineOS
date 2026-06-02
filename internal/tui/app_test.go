package tui

import (
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/agentmsg"
	"vulpineos/internal/config"
	"vulpineos/internal/juggler"
	"vulpineos/internal/orchestrator"
	"vulpineos/internal/pool"
	"vulpineos/internal/runtimeaudit"
	"vulpineos/internal/testutil"
	"vulpineos/internal/tui/settings"
	"vulpineos/internal/tui/shared"
	"vulpineos/internal/vault"
)

type fakeControlCall struct {
	method string
	params json.RawMessage
}

type fakeControlClient struct {
	responses map[string]any
	errors    map[string]error
	calls     []fakeControlCall
}

func (f *fakeControlClient) ControlCall(ctx context.Context, method string, params any, result any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	f.calls = append(f.calls, fakeControlCall{method: method, params: data})
	if err := f.errors[method]; err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	response := f.responses[method]
	if response == nil {
		response = map[string]any{}
	}
	data, err = json.Marshal(response)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}

func openTestVault(t *testing.T) *vault.DB {
	t.Helper()
	db, err := vault.OpenPath(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestNewAppReconcilesNonTerminalAgentsToPaused(t *testing.T) {
	db := openTestVault(t)

	active, err := db.CreateAgent("active-agent", "task", "{}")
	if err != nil {
		t.Fatalf("create active agent: %v", err)
	}
	if err := db.UpdateAgentStatus(active.ID, "active"); err != nil {
		t.Fatalf("set active status: %v", err)
	}

	running, err := db.CreateAgent("running-agent", "task", "{}")
	if err != nil {
		t.Fatalf("create running agent: %v", err)
	}
	if err := db.UpdateAgentStatus(running.ID, "running"); err != nil {
		t.Fatalf("set running status: %v", err)
	}

	thinking, err := db.CreateAgent("thinking-agent", "task", "{}")
	if err != nil {
		t.Fatalf("create thinking agent: %v", err)
	}
	if err := db.UpdateAgentStatus(thinking.ID, "thinking"); err != nil {
		t.Fatalf("set thinking status: %v", err)
	}

	completed, err := db.CreateAgent("completed-agent", "task", "{}")
	if err != nil {
		t.Fatalf("create completed agent: %v", err)
	}
	if err := db.UpdateAgentStatus(completed.ID, "completed"); err != nil {
		t.Fatalf("set completed status: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)

	if got := app.agentList.SelectedAgentID(); got == "" {
		t.Fatal("expected initial selection after loading agents")
	}

	activeAgent, err := db.GetAgent(active.ID)
	if err != nil {
		t.Fatalf("get active agent: %v", err)
	}
	if activeAgent.Status != "paused" {
		t.Fatalf("active agent status = %q, want paused", activeAgent.Status)
	}

	runningAgent, err := db.GetAgent(running.ID)
	if err != nil {
		t.Fatalf("get running agent: %v", err)
	}
	if runningAgent.Status != "paused" {
		t.Fatalf("running agent status = %q, want paused", runningAgent.Status)
	}

	thinkingAgent, err := db.GetAgent(thinking.ID)
	if err != nil {
		t.Fatalf("get thinking agent: %v", err)
	}
	if thinkingAgent.Status != "paused" {
		t.Fatalf("thinking agent status = %q, want paused", thinkingAgent.Status)
	}

	completedAgent, err := db.GetAgent(completed.ID)
	if err != nil {
		t.Fatalf("get completed agent: %v", err)
	}
	if completedAgent.Status != "completed" {
		t.Fatalf("completed agent status = %q, want completed", completedAgent.Status)
	}
}

func TestBrowserRouteLabelIgnoresStoppedFoxbridge(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, &config.Config{FoxbridgeCDPURL: "ws://127.0.0.1:9222"}, nil)
	app.SetFoxbridgeRunning(func() bool { return false })

	if got := app.browserRouteLabel(); got != "" {
		t.Fatalf("browser route = %q, want empty route for stopped kernel/foxbridge", got)
	}
}

func TestAgentRuntimeConfigCreatesScopedAgentContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondJSON("Browser.createBrowserContext", map[string]string{"browserContextId": "ctx-agent-runtime"})
	fake.RespondJSON("Browser.enable", map[string]any{})
	fake.RespondJSON("Browser.setUserAgentOverride", map[string]any{})
	fake.RespondJSON("Browser.setPlatformOverride", map[string]any{})
	fake.RespondJSON("Browser.setDefaultViewport", map[string]any{})
	fake.RespondJSON("Browser.setLocaleOverride", map[string]any{})
	fake.RespondJSON("Browser.setTimezoneOverride", map[string]any{})
	fake.RespondJSON("Browser.setExtraHTTPHeaders", map[string]any{})

	client := juggler.NewClient(fake)
	defer client.Close()

	db := openTestVault(t)
	fp, err := vault.GenerateFingerprint("agent-runtime")
	if err != nil {
		t.Fatalf("GenerateFingerprint: %v", err)
	}
	agent, err := db.CreateAgent("agent-runtime", "use browser", fp)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	orch := orchestrator.New(nil, client, db, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 1})
	if err := orch.Pool.Start(); err != nil {
		t.Fatalf("pool start: %v", err)
	}
	defer orch.Close()

	app := NewApp(nil, nil, orch, db, &config.Config{FoxbridgeCDPURL: "ws://127.0.0.1:9222"}, nil)
	app.SetFoxbridgeRunning(func() bool { return false })
	path, cleanup, err := app.agentRuntimeConfig(agent)
	if err != nil {
		t.Fatalf("agentRuntimeConfig: %v", err)
	}
	defer cleanup()

	// The native runtime drives the pooled context directly and needs no config file.
	if path != "" {
		t.Fatalf("native runtime needs no config file, got path %q", path)
	}

	persisted, err := db.GetAgent(agent.ID)
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	meta, err := vault.ParseAgentMetadata(persisted.Metadata)
	if err != nil {
		t.Fatalf("ParseAgentMetadata: %v", err)
	}
	if meta.ContextID != "ctx-agent-runtime" {
		t.Fatalf("contextID = %q, want ctx-agent-runtime", meta.ContextID)
	}
}

func TestNewAppLoadsPersistedConversationAndSelectionSwitch(t *testing.T) {
	db := openTestVault(t)

	older, err := db.CreateAgent("older-agent", "first task", "{}")
	if err != nil {
		t.Fatalf("create first agent: %v", err)
	}
	if err := db.AppendMessage(older.ID, "assistant", "older persisted reply", 5); err != nil {
		t.Fatalf("append first message: %v", err)
	}

	newer, err := db.CreateAgent("newer-agent", "second task", "{}")
	if err != nil {
		t.Fatalf("create second agent: %v", err)
	}
	if err := db.AppendMessage(newer.ID, "assistant", "newer persisted reply", 6); err != nil {
		t.Fatalf("append second message: %v", err)
	}
	if _, err := db.Conn().Exec(`UPDATE agents SET last_active = ? WHERE id = ?`, time.Now().Unix()+10, newer.ID); err != nil {
		t.Fatalf("set newer last_active: %v", err)
	}
	if _, err := db.Conn().Exec(`UPDATE agents SET last_active = ? WHERE id = ?`, time.Now().Unix()-10, older.ID); err != nil {
		t.Fatalf("set older last_active: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.conversation.SetSize(80, 20)

	if app.selectedAgentID != newer.ID {
		t.Fatalf("selected agent = %q, want newest %q", app.selectedAgentID, newer.ID)
	}
	if !strings.Contains(app.conversation.View(), "newer persisted reply") {
		t.Fatalf("expected initial conversation to include newest persisted message, got:\n%s", app.conversation.View())
	}

	app.agentList.MoveDown()
	if cmd := app.selectCurrentAgent(); cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("selectCurrentAgent returned unexpected msg: %#v", msg)
		}
	}

	if app.selectedAgentID != older.ID {
		t.Fatalf("selected agent after move = %q, want %q", app.selectedAgentID, older.ID)
	}
	if !strings.Contains(app.conversation.View(), "older persisted reply") {
		t.Fatalf("expected conversation to reload older persisted message, got:\n%s", app.conversation.View())
	}
}

func TestConversationUnreadCountsTrackNonSelectedAgents(t *testing.T) {
	db := openTestVault(t)

	first, err := db.CreateAgent("first-agent", "first task", "{}")
	if err != nil {
		t.Fatalf("create first agent: %v", err)
	}
	second, err := db.CreateAgent("second-agent", "second task", "{}")
	if err != nil {
		t.Fatalf("create second agent: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.selectedAgentID = first.ID
	app.conversation.SetAgentID(first.ID)

	model, _ := app.Update(shared.ConversationEntryMsg{
		AgentID: second.ID,
		Role:    "assistant",
		Content: "background reply",
	})
	app = model.(App)

	if got := app.agentList.UnreadCount(second.ID); got != 1 {
		t.Fatalf("second unread = %d, want 1", got)
	}

	app.agentList.MoveDown()
	if cmd := app.selectCurrentAgent(); cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("selectCurrentAgent returned unexpected msg: %#v", msg)
		}
	}

	if got := app.agentList.UnreadCount(second.ID); got != 0 {
		t.Fatalf("second unread after selection = %d, want 0", got)
	}
	if !strings.Contains(app.conversation.View(), "background reply") {
		t.Fatalf("expected conversation to include unread message after selection, got:\n%s", app.conversation.View())
	}
}

func TestDeletingSelectedAgentLoadsNextPersistedConversation(t *testing.T) {
	db := openTestVault(t)

	first, err := db.CreateAgent("first-agent", "first task", "{}")
	if err != nil {
		t.Fatalf("create first agent: %v", err)
	}
	if err := db.AppendMessage(first.ID, "assistant", "first persisted reply", 1); err != nil {
		t.Fatalf("append first message: %v", err)
	}

	second, err := db.CreateAgent("second-agent", "second task", "{}")
	if err != nil {
		t.Fatalf("create second agent: %v", err)
	}
	if err := db.AppendMessage(second.ID, "assistant", "second persisted reply", 2); err != nil {
		t.Fatalf("append second message: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.conversation.SetSize(80, 20)
	app.selectedAgentID = first.ID
	app.conversation.SetAgentID(first.ID)
	app.conversation.LoadMessages([]vault.AgentMessage{
		{Role: "assistant", Content: "first persisted reply"},
	})

	model, _ := app.Update(shared.AgentDeletedMsg{AgentID: first.ID})
	app = model.(App)

	if app.selectedAgentID != second.ID {
		t.Fatalf("selected agent = %q, want %q", app.selectedAgentID, second.ID)
	}
	if !strings.Contains(app.conversation.View(), "second persisted reply") {
		t.Fatalf("expected conversation to reload second agent messages, got:\n%s", app.conversation.View())
	}
	if app.notice != "Agent deleted" {
		t.Fatalf("notice = %q, want %q", app.notice, "Agent deleted")
	}
}

func TestDeletingOnlySelectedAgentClearsWorkbenchState(t *testing.T) {
	db := openTestVault(t)

	agent, err := db.CreateAgent("solo-agent", "solo task", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := db.AppendMessage(agent.ID, "assistant", "solo persisted reply", 1); err != nil {
		t.Fatalf("append message: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.conversation.SetSize(80, 20)
	app.agentDetail.SetSize(40, 8)
	app.selectedAgentID = agent.ID
	app.conversation.SetAgentID(agent.ID)
	app.conversation.LoadMessages([]vault.AgentMessage{
		{Role: "assistant", Content: "solo persisted reply"},
	})
	app.agentDetail.SetAgent(agent.ID, "solo-agent", "solo task", "paused", 0, "", "", time.Now())

	model, _ := app.Update(shared.AgentDeletedMsg{AgentID: agent.ID})
	app = model.(App)

	if app.selectedAgentID != "" {
		t.Fatalf("selected agent = %q, want empty", app.selectedAgentID)
	}
	if app.conversation.AgentID() != "" {
		t.Fatalf("conversation agent id = %q, want empty", app.conversation.AgentID())
	}
	if app.agentDetail.HasAgent() {
		t.Fatal("expected agent detail to clear after deleting final agent")
	}
	conversationView := app.conversation.View()
	if !strings.Contains(conversationView, "to create a new agent") {
		t.Fatalf("expected empty conversation prompt, got:\n%s", conversationView)
	}
	detailView := app.agentDetail.View()
	if !strings.Contains(detailView, "to create a new agent") {
		t.Fatalf("expected empty detail prompt, got:\n%s", detailView)
	}
}

func TestAgentCreatedKeepsChatLockedUntilAgentResponds(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	agent.Status = "active"

	model, _ := app.Update(shared.AgentCreatedMsg{Agent: *agent})
	app = model.(App)

	if app.focus != FocusConversation {
		t.Fatalf("focus = %d, want conversation", app.focus)
	}
	if app.inputMode != "chat" {
		t.Fatalf("inputMode = %q, want chat", app.inputMode)
	}
	if app.conversation.IsAwake() {
		t.Fatal("conversation should stay locked while a new active agent is still starting")
	}
	if app.pendingChatFocusAgentID != agent.ID {
		t.Fatalf("pendingChatFocusAgentID = %q, want %q", app.pendingChatFocusAgentID, agent.ID)
	}
	if view := app.conversation.View(); !strings.Contains(view, "Chat available after agent responds") {
		t.Fatalf("expected startup lock message, got:\n%s", view)
	}
}

func TestCreateAgentOpensReadyChatWithoutStartingTurn(t *testing.T) {
	db := openTestVault(t)
	app := NewApp(nil, nil, nil, db, &config.Config{}, nil)
	app.conversation.SetSize(80, 20)

	msg := app.createAgent("Scraper", "Scrape prices", "")()
	created, ok := msg.(shared.AgentCreatedMsg)
	if !ok {
		t.Fatalf("createAgent returned %#v, want AgentCreatedMsg", msg)
	}
	if created.Agent.Status != "ready" {
		t.Fatalf("created status = %q, want ready", created.Agent.Status)
	}
	msgs, err := db.GetMessages(created.Agent.ID)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("created agent messages = %#v, want none before first user message", msgs)
	}

	model, _ := app.Update(created)
	app = model.(App)
	if app.focus != FocusConversation || app.inputMode != "chat" {
		t.Fatalf("focus/input = %d/%q, want conversation/chat", app.focus, app.inputMode)
	}
	if !app.conversation.IsAwake() {
		t.Fatal("newly created ready agent should accept chat immediately")
	}
	if app.conversation.IsThinking() {
		t.Fatal("newly created ready agent should not be thinking before first user message")
	}
	if app.pendingChatFocusAgentID != "" {
		t.Fatalf("pendingChatFocusAgentID = %q, want empty", app.pendingChatFocusAgentID)
	}

	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	app = model.(App)
	if got := app.conversation.TextInput().Value(); got != "h" {
		t.Fatalf("chat input value = %q, want first typed user message", got)
	}
}

func TestExistingReadyAgentWithNoMessagesOpensUnlockedChat(t *testing.T) {
	db := openTestVault(t)
	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := db.UpdateAgentStatus(agent.ID, "ready"); err != nil {
		t.Fatalf("UpdateAgentStatus: %v", err)
	}

	app := NewApp(nil, nil, nil, db, &config.Config{}, nil)
	app.conversation.SetSize(80, 20)

	if app.selectedAgentID != agent.ID {
		t.Fatalf("selectedAgentID = %q, want ready agent", app.selectedAgentID)
	}
	if !app.conversation.IsAwake() {
		t.Fatal("ready agent with no assistant history should accept chat")
	}
	if app.conversation.IsThinking() {
		t.Fatal("ready agent with no messages should not be thinking")
	}
}

func TestStartupLockedChatDoesNotAcceptInput(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	agent.Status = "active"

	model, _ := app.Update(shared.AgentCreatedMsg{Agent: *agent})
	app = model.(App)
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	app = model.(App)
	if cmd != nil {
		t.Fatalf("locked startup input returned command: %#v", cmd())
	}
	if got := app.conversation.TextInput().Value(); got != "" {
		t.Fatalf("locked startup input value = %q, want empty", got)
	}

	model, cmd = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if cmd != nil {
		t.Fatalf("locked startup enter returned command: %#v", cmd())
	}
	if app.conversation.IsAwake() {
		t.Fatal("conversation should remain locked before first reply")
	}
}

func TestStartupLockedChatEnterFromListDoesNotWakeInput(t *testing.T) {
	db := openTestVault(t)
	app := NewApp(nil, nil, nil, db, &config.Config{}, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	agent.Status = "active"

	model, _ := app.Update(shared.AgentCreatedMsg{Agent: *agent})
	app = model.(App)
	app.focus = FocusAgentList
	app.inputMode = ""

	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)

	if app.focus != FocusConversation {
		t.Fatalf("focus = %d, want conversation", app.focus)
	}
	if app.inputMode != "chat" {
		t.Fatalf("inputMode = %q, want chat", app.inputMode)
	}
	if app.conversation.IsAwake() {
		t.Fatal("conversation should stay locked when focus changes before first reply")
	}
}

func TestStartupLockedChatAllowsQuitShortcut(t *testing.T) {
	db := openTestVault(t)
	app := NewApp(nil, nil, nil, db, &config.Config{}, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	agent.Status = "active"

	model, _ := app.Update(shared.AgentCreatedMsg{Agent: *agent})
	app = model.(App)
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	app = model.(App)

	if cmd == nil {
		t.Fatal("locked startup quit returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("locked startup quit did not return tea.QuitMsg")
	}
	if app.conversation.TextInput().Value() != "" {
		t.Fatal("locked startup quit should not type into chat")
	}
}

func TestNewAgentPromptsAllowQuitShortcut(t *testing.T) {
	for _, mode := range []string{"new-agent-name", "new-agent-desc"} {
		app := NewApp(nil, nil, nil, nil, &config.Config{}, nil)
		app.inputMode = mode

		_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		if cmd == nil {
			t.Fatalf("%s quit returned no command", mode)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%s quit did not return tea.QuitMsg", mode)
		}
	}
}

func TestFocusedChatAllowsQuitShortcut(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, &config.Config{}, nil)
	app.selectedAgentID = "agent-1"
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID("agent-1")
	app.conversation.SetAwake(true)
	app.conversation.Focus()

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	app = model.(App)
	if cmd == nil {
		t.Fatal("focused chat quit returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("focused chat quit did not return tea.QuitMsg")
	}
	if app.conversation.TextInput().Value() != "" {
		t.Fatal("focused chat quit should not type into chat")
	}
}

func TestFocusedChatAllowsTabFocusCycle(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, &config.Config{}, nil)
	app.selectedAgentID = "agent-1"
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID("agent-1")
	app.conversation.SetAwake(true)
	app.conversation.Focus()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyTab})
	app = model.(App)
	if app.focus != FocusAgentList {
		t.Fatalf("focus = %d, want agent list after tab from focused chat (2-panel cycle)", app.focus)
	}
	if app.inputMode != "" {
		t.Fatalf("inputMode = %q, want empty after tab focus cycle", app.inputMode)
	}
	if app.conversation.Focused() {
		t.Fatal("conversation should blur after tab focus cycle")
	}
}

func TestSettingsAllowsGlobalQuitShortcut(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, &config.Config{}, nil)
	app.focus = FocusSettings
	app.settings.SetActive(true)

	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("settings quit returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("settings quit did not return tea.QuitMsg")
	}
}

func TestSettingsProxyImportAcceptsLifecycleKeyCharacters(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, &config.Config{}, nil)
	app.focus = FocusSettings
	app.settings.SetActive(true)

	var cmd tea.Cmd
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyTab})
	app = model.(App)
	if cmd != nil {
		t.Fatalf("unexpected command after tab: %#v", cmd())
	}
	model, cmd = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	app = model.(App)
	if cmd == nil {
		t.Fatal("proxy import did not start text input blink command")
	}

	url := "socks5://user:pass@proxy.example:1080"
	for _, r := range url {
		model, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		app = model.(App)
	}
	model, cmd = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if cmd == nil {
		t.Fatal("proxy import enter returned no command")
	}
	msg, ok := cmd().(shared.ProxyAddMsg)
	if !ok {
		t.Fatalf("command returned %T, want ProxyAddMsg", cmd())
	}
	if msg.URL != url {
		t.Fatalf("proxy URL = %q, want %q", msg.URL, url)
	}
}

func TestAgentCreatedSelectsNewAgentListRow(t *testing.T) {
	db := openTestVault(t)
	oldAgent, err := db.CreateAgent("Old", "old task", "{}")
	if err != nil {
		t.Fatalf("create old agent: %v", err)
	}
	app := NewApp(nil, nil, nil, db, nil, nil)
	app.agentList.SelectAgentID(oldAgent.ID)
	app.selectedAgentID = oldAgent.ID

	newAgent, err := db.CreateAgent("New", "new task", "{}")
	if err != nil {
		t.Fatalf("create new agent: %v", err)
	}

	model, _ := app.Update(shared.AgentCreatedMsg{Agent: *newAgent})
	app = model.(App)

	if app.selectedAgentID != newAgent.ID {
		t.Fatalf("selected agent = %q, want %q", app.selectedAgentID, newAgent.ID)
	}
	if got := app.agentList.SelectedAgentID(); got != newAgent.ID {
		t.Fatalf("highlighted agent = %q, want %q", got, newAgent.ID)
	}
}

func TestPoolStatsMsgUpdatesVisibleSystemPanel(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.systemInfo.SetHeight(20)

	model, _ := app.Update(shared.PoolStatsMsg{Available: 3, Active: 2, Total: 5})
	app = model.(App)

	view := app.systemInfo.View()
	if !strings.Contains(view, "Pool: 3/2/5") {
		t.Fatalf("system panel missing pool stats:\n%s", view)
	}
}

func TestArrowKeysNavigateAgentsWhenResizeModeDisabled(t *testing.T) {
	db := openTestVault(t)
	first, err := db.CreateAgent("first-agent", "first task", "{}")
	if err != nil {
		t.Fatalf("create first agent: %v", err)
	}
	second, err := db.CreateAgent("second-agent", "second task", "{}")
	if err != nil {
		t.Fatalf("create second agent: %v", err)
	}
	if _, err := db.Conn().Exec(`UPDATE agents SET last_active = ? WHERE id = ?`, time.Now().Unix()+10, first.ID); err != nil {
		t.Fatalf("set first last_active: %v", err)
	}
	if _, err := db.Conn().Exec(`UPDATE agents SET last_active = ? WHERE id = ?`, time.Now().Unix(), second.ID); err != nil {
		t.Fatalf("set second last_active: %v", err)
	}

	cfg := &config.Config{ResizePanelsWithArrows: false}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.focus = FocusAgentList

	originalSplit := app.leftSplit
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyDown})
	app = model.(App)

	if app.leftSplit != originalSplit {
		t.Fatalf("leftSplit changed from %d to %d with resize mode disabled", originalSplit, app.leftSplit)
	}
	if app.selectedAgentID != second.ID {
		t.Fatalf("selected agent = %q, want %q after down arrow", app.selectedAgentID, second.ID)
	}
	if !strings.Contains(app.renderStatusBar(), "q:quit") {
		t.Fatalf("status bar missing quit hint: %s", app.renderStatusBar())
	}
}

func TestStatusBarStartsNavigateWhenLegacyResizeDefaultSet(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{ResizePanelsWithArrows: true}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.width = 180

	if !strings.Contains(app.renderStatusBar(), "q:quit") {
		t.Fatalf("status bar missing quit hint: %s", app.renderStatusBar())
	}
}

func TestResizeModeKeepsVerticalSplitsUsableAfterTerminalShrink(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, &config.Config{}, nil)
	app.width = 80
	app.height = 16
	app.focus = FocusAgentList
	app.resizeMode = true
	app.leftSplit = 30
	app.rightSplit = 30

	app.updatePanelSizes()

	if app.leftSplit >= 30 {
		t.Fatalf("left split was not clamped after shrink: %d", app.leftSplit)
	}
	if app.rightSplit >= 30 {
		t.Fatalf("right split was not clamped after shrink: %d", app.rightSplit)
	}
	clampedLeft := app.leftSplit
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyUp})
	app = model.(App)

	if app.leftSplit != clampedLeft-1 {
		t.Fatalf("up arrow leftSplit = %d, want %d after clamp", app.leftSplit, clampedLeft-1)
	}
}

func TestUpdatePanelSizesUsesInnerPanelWidthForConversation(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.width = 120
	app.height = 24
	app.updatePanelSizes()

	widths := resolveWorkbenchWidths(app.width, app.leftWidth, app.rightWidth)
	wantInputWidth := widths.center - 6
	if got := app.conversation.TextInput().Width; got != wantInputWidth {
		t.Fatalf("conversation input width = %d, want %d from inner panel width", got, wantInputWidth)
	}
}

func TestCompactWorkbenchPersistsInnerPanelWidthForConversation(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.width = 40
	app.height = 10
	app.focus = FocusConversation
	app.updatePanelSizes()

	wantInputWidth := app.width - 10
	if got := app.conversation.TextInput().Width; got != wantInputWidth {
		t.Fatalf("compact conversation input width = %d, want %d from compact inner width", got, wantInputWidth)
	}
}

func TestReplayBrowserTargetsRequestsEnableAfterTUISubscriptions(t *testing.T) {
	transport := testutil.NewFakeJugglerTransport(t)
	transport.RespondJSON("Browser.enable", map[string]any{})
	client := juggler.NewClient(transport)
	t.Cleanup(func() { _ = client.Close() })

	app := NewApp(nil, client, nil, nil, nil, nil)
	cmd := app.replayBrowserTargets()
	if cmd == nil {
		t.Fatal("replayBrowserTargets returned nil command")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("replayBrowserTargets returned unexpected message: %#v", msg)
	}

	calls := transport.CallsByMethod("Browser.enable")
	if len(calls) != 1 {
		t.Fatalf("Browser.enable calls = %d, want 1", len(calls))
	}
	params := testutil.ParamsAs[struct {
		AttachToDefaultContext bool `json:"attachToDefaultContext"`
	}](t, calls[0].Params)
	if !params.AttachToDefaultContext {
		t.Fatalf("attachToDefaultContext = false, want true")
	}
}

func TestRemoteControlAppLoadsAgentsAndMessages(t *testing.T) {
	control := &fakeControlClient{responses: map[string]any{
		"agents.list": map[string]any{"agents": []map[string]any{{
			"id":          "agent-remote",
			"name":        "Remote Scout",
			"task":        "Inspect remote state",
			"status":      "paused",
			"totalTokens": 42,
		}}},
		"agents.getMessages": map[string]any{"messages": []map[string]any{{
			"role":    "assistant",
			"content": "remote persisted reply",
			"tokens":  7,
		}}},
	}}
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, control)

	msg := app.loadRemoteAgents()()
	model, cmd := app.Update(msg)
	app = model.(App)
	if cmd == nil {
		t.Fatal("remote agent load should request selected agent messages")
	}
	model, _ = app.Update(cmd())
	app = model.(App)

	if app.selectedAgentID != "agent-remote" {
		t.Fatalf("selected agent = %q, want remote agent", app.selectedAgentID)
	}
	if !strings.Contains(app.conversation.View(), "remote persisted reply") {
		t.Fatalf("conversation missing remote messages:\n%s", app.conversation.View())
	}
	if !app.agentDetail.HasAgent() {
		t.Fatal("agent detail was not populated from remote agent list")
	}
}

func TestRemoteControlCreateAgentUsesControlPath(t *testing.T) {
	control := &fakeControlClient{responses: map[string]any{
		"agents.create": map[string]any{"agentId": "agent-created"},
		"agents.list": map[string]any{"agents": []map[string]any{{
			"id":     "agent-created",
			"name":   "Remote Builder",
			"task":   "Build remotely",
			"status": "ready",
		}}},
	}}
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, control)

	msg := app.createAgent("Remote Builder", "Build remotely", "ctx-1")()
	model, _ := app.Update(msg)
	app = model.(App)

	if len(control.calls) == 0 || control.calls[0].method != "agents.create" {
		t.Fatalf("first control call = %+v, want agents.create", control.calls)
	}
	var params struct {
		Name      string `json:"name"`
		Task      string `json:"task"`
		ContextID string `json:"contextId"`
	}
	if err := json.Unmarshal(control.calls[0].params, &params); err != nil {
		t.Fatalf("unmarshal spawn params: %v", err)
	}
	if params.Name != "Remote Builder" || params.Task != "Build remotely" || params.ContextID != "ctx-1" {
		t.Fatalf("spawn params = %+v", params)
	}
	if app.selectedAgentID != "agent-created" {
		t.Fatalf("selected agent = %q, want created remote agent", app.selectedAgentID)
	}
}

func TestRemoteControlCreateAgentReloadsAgentsAfterSpawnError(t *testing.T) {
	control := &fakeControlClient{
		errors: map[string]error{
			"agents.create": errors.New("vault unavailable"),
		},
		responses: map[string]any{
			"agents.list": map[string]any{"agents": []map[string]any{{
				"id":     "agent-error",
				"name":   "Remote Error",
				"task":   "Build remotely",
				"status": "error",
			}}},
		},
	}
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, control)

	msg := app.createAgent("Remote Error", "Build remotely", "")()
	loaded, ok := msg.(remoteAgentsLoadedMsg)
	if !ok {
		t.Fatalf("createAgent returned %#v, want remoteAgentsLoadedMsg", msg)
	}
	if !strings.Contains(loaded.Notice, "vault unavailable") {
		t.Fatalf("notice = %q, want create error", loaded.Notice)
	}

	model, _ := app.Update(loaded)
	app = model.(App)
	if app.selectedAgentID != "agent-error" {
		t.Fatalf("selected agent = %q, want error agent", app.selectedAgentID)
	}
	if !strings.Contains(app.notice, "vault unavailable") {
		t.Fatalf("app notice = %q, want create error", app.notice)
	}
}

func TestRemoteAgentsLoadedMarksSelectedLiveAgentThinking(t *testing.T) {
	control := &fakeControlClient{responses: map[string]any{
		"agents.getMessages": map[string]any{"messages": []map[string]any{}},
	}}
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, control)

	model, _ := app.Update(remoteAgentsLoadedMsg{
		Agents: []vault.Agent{{
			ID:     "agent-live",
			Name:   "Remote Live",
			Status: "active",
		}},
	})
	app = model.(App)

	if app.selectedAgentID != "agent-live" {
		t.Fatalf("selected agent = %q, want agent-live", app.selectedAgentID)
	}
	if !app.conversation.IsThinking() {
		t.Fatal("selected live remote agent should mark conversation as thinking")
	}
}

func TestRemoteControlNewAgentShortcutStartsCreation(t *testing.T) {
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, &fakeControlClient{})

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	app = model.(App)

	if app.inputMode != "new-agent-name" {
		t.Fatalf("inputMode = %q, want new-agent-name", app.inputMode)
	}
	if cmd == nil {
		t.Fatal("new-agent shortcut should focus the name input")
	}
}

func TestRemoteControlStatusEventRefreshesSelectedDetail(t *testing.T) {
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, &fakeControlClient{})
	app.agentList.SetAgents([]vault.Agent{{
		ID:          "agent-1",
		Name:        "Remote",
		Task:        "Inspect remote state",
		Status:      "paused",
		TotalTokens: 1,
		Fingerprint: `{"navigator.platform":"MacIntel","navigator.userAgent":"Mozilla/5.0 rv:146.0","screen.width":1440,"screen.height":900}`,
		Metadata:    vault.MarshalAgentMetadata(vault.AgentMetadata{ContextID: "ctx-remote-123456"}),
	}})
	app.agentList.SelectAgentID("agent-1")
	app.selectedAgentID = "agent-1"
	agent := vault.Agent{ID: "agent-1", Name: "Remote", Status: "paused", TotalTokens: 1, Task: "Inspect remote state"}
	app.updateAgentDetail(&agent)

	model, _ := app.Update(shared.AgentStatusMsg{AgentID: "agent-1", Status: "active", Tokens: 99})
	app = model.(App)

	view := app.agentDetail.View()
	if !strings.Contains(view, "Tokens: 99") || !strings.Contains(view, "working") {
		t.Fatalf("detail did not refresh from remote status:\n%s", view)
	}
	if !strings.Contains(view, "Inspect remote state") {
		t.Fatalf("detail lost remote task after status update:\n%s", view)
	}
	if !strings.Contains(view, "pinned ctx-remote") {
		t.Fatalf("detail lost remote context after status update:\n%s", view)
	}
	if !strings.Contains(view, "macOS") {
		t.Fatalf("detail lost remote fingerprint after status update:\n%s", view)
	}
}

func TestAgentStatusMsgShowsLiveContextInSelectedDetail(t *testing.T) {
	db := openTestVault(t)
	app := NewApp(nil, nil, nil, db, &config.Config{}, nil)

	agent, err := db.CreateAgent("Local", "Use live browser context", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.agentList.SetAgents([]vault.Agent{*agent})
	app.agentList.SelectAgentID(agent.ID)
	app.selectedAgentID = agent.ID
	app.updateAgentDetail(agent)

	model, _ := app.Update(shared.AgentStatusMsg{
		AgentID:   agent.ID,
		ContextID: "ctx-live-123456",
		Status:    "active",
		Tokens:    7,
	})
	app = model.(App)

	view := app.agentDetail.View()
	if !strings.Contains(view, "live ctx-live-123") {
		t.Fatalf("detail did not show live context from status event:\n%s", view)
	}
}

func TestRemoteControlBulkStatusRefreshesSelectedDetail(t *testing.T) {
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, &fakeControlClient{})
	app.agentList.SetAgents([]vault.Agent{{ID: "agent-1", Name: "Remote", Status: "active", TotalTokens: 7}})
	app.agentList.SelectAgentID("agent-1")
	app.selectedAgentID = "agent-1"
	app.updateAgentDetail(&vault.Agent{ID: "agent-1", Name: "Remote", Status: "active", TotalTokens: 7})

	model, _ := app.Update(shared.BulkAgentStatusMsg{
		AgentIDs: []string{"agent-1"},
		Status:   "paused",
		Notice:   "Paused 1 remote agent",
	})
	app = model.(App)

	view := app.agentDetail.View()
	if !strings.Contains(view, "paused") {
		t.Fatalf("detail did not refresh from remote bulk status:\n%s", view)
	}
}

func TestRemoteControlBulkStatusExcludesFailures(t *testing.T) {
	control := &fakeControlClient{responses: map[string]any{
		"agents.pauseMany": map[string]any{
			"status":   "ok",
			"paused":   1,
			"failures": map[string]string{"agent-2": "not running"},
		},
	}}
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, control)

	msg := app.remoteBulkAgentStatusCommand("agents.pauseMany", []string{"agent-1", "agent-2"}, "paused", "Paused %d remote agents")()
	bulk, ok := msg.(shared.BulkAgentStatusMsg)
	if !ok {
		t.Fatalf("remoteBulkAgentStatusCommand returned %#v", msg)
	}
	if len(bulk.AgentIDs) != 1 || bulk.AgentIDs[0] != "agent-1" {
		t.Fatalf("bulk AgentIDs = %#v, want only successful agent", bulk.AgentIDs)
	}
	if !strings.Contains(bulk.Notice, "1 failed") {
		t.Fatalf("notice = %q, want failure count", bulk.Notice)
	}
}

func TestRemoteControlLabelsExposeRemoteSettingsAndLogs(t *testing.T) {
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, &fakeControlClient{})
	app.width = 160
	app.agentDetail.SetAgent("agent-1", "Remote", "task", "active", 0, "", "", time.Now())

	status := app.renderStatusBar()
	detail := app.agentDetail.View()
	if !strings.Contains(status, "o:log") || !strings.Contains(status, "S:settings") || !strings.Contains(status, "x:kill") {
		t.Fatalf("remote status bar missing remote controls: %s", status)
	}
	if strings.Contains(status, "x:del") {
		t.Fatalf("remote status bar advertises local delete: %s", status)
	}
	if !strings.Contains(detail, "[o] log") || strings.Contains(detail, "[x] delete") {
		t.Fatalf("remote detail controls are wrong: %s", detail)
	}
}

func TestRemoteControlOpensSettingsAndStartsReconfigure(t *testing.T) {
	control := &fakeControlClient{responses: map[string]any{
		"settings.get": map[string]any{
			"config": map[string]any{
				"provider":      "ollama",
				"model":         "ollama/llama3.1",
				"apiKeySet":     false,
				"setupComplete": true,
			},
			"proxies": []any{},
			"skills":  []any{map[string]any{"name": "vulpine-browser", "enabled": true}},
		},
	}}
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, control)
	app.width = 100
	app.height = 30

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	app = model.(App)
	if cmd != nil {
		model, cmd = app.Update(cmd())
		app = model.(App)
	}
	if app.focus != FocusSettings || !app.settings.IsActive() {
		t.Fatal("remote TUI should open host settings")
	}
	if app.cfg == nil || app.cfg.Provider != "ollama" || app.cfg.Model != "ollama/llama3.1" {
		t.Fatalf("remote config not loaded into app: %#v", app.cfg)
	}
	if len(control.calls) == 0 || control.calls[0].method != "settings.get" {
		t.Fatalf("control calls = %+v, want settings.get", control.calls)
	}

	model, cmd = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	app = model.(App)
	if cmd != nil {
		model, _ = app.Update(cmd())
		app = model.(App)
	}
	if !strings.Contains(app.View(), "First Time Setup") {
		t.Fatalf("remote reconfigure should show setup wizard:\n%s", app.View())
	}
}

func TestRemoteControlProxyAndSkillSettingsUseHostControl(t *testing.T) {
	control := &fakeControlClient{responses: map[string]any{
		"proxies.add": map[string]any{
			"proxies": []any{map[string]any{
				"id":      "proxy-1",
				"label":   "127.0.0.1:8080",
				"type":    "http",
				"host":    "127.0.0.1",
				"port":    8080,
				"config":  `{"type":"http","host":"127.0.0.1","port":8080}`,
				"latency": "untested",
			}},
		},
		"skills.set": map[string]any{
			"skills": []any{map[string]any{"name": "vulpine-browser", "enabled": false}},
		},
	}}
	app := NewAppWithControl(nil, nil, nil, nil, &config.Config{}, nil, control)
	app.settings.SetActive(true)
	app.focus = FocusSettings

	model, cmd := app.Update(shared.ProxyAddMsg{URL: "http://127.0.0.1:8080"})
	app = model.(App)
	if cmd == nil {
		t.Fatal("remote proxy add should call host control")
	}
	model, _ = app.Update(cmd())
	app = model.(App)
	if len(control.calls) == 0 || control.calls[0].method != "proxies.add" {
		t.Fatalf("control calls = %+v, want proxies.add", control.calls)
	}

	model, cmd = app.Update(shared.SkillToggleMsg{Name: "vulpine-browser", Enabled: false})
	app = model.(App)
	if cmd == nil {
		t.Fatal("remote skill toggle should call host control")
	}
	model, _ = app.Update(cmd())
	app = model.(App)
	if len(control.calls) < 2 || control.calls[1].method != "skills.set" {
		t.Fatalf("control calls = %+v, want skills.set", control.calls)
	}
}

func TestRemoteApplySetupPreservesHostAPIKeyPlaceholder(t *testing.T) {
	control := &fakeControlClient{responses: map[string]any{
		"config.set": map[string]any{
			"provider":      "opencode",
			"model":         "opencode/deepseek-v4-flash-free",
			"apiKeySet":     true,
			"setupComplete": true,
		},
	}}
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, control)
	err := app.applySetupConfig(&config.Config{
		Provider:      "opencode",
		Model:         "opencode/deepseek-v4-flash-free",
		APIKey:        remoteAPIKeyPlaceholder,
		SetupComplete: true,
	})
	if err != nil {
		t.Fatalf("applySetupConfig: %v", err)
	}
	if len(control.calls) != 1 || control.calls[0].method != "config.set" {
		t.Fatalf("control calls = %+v, want config.set", control.calls)
	}
	var params struct {
		APIKey     string `json:"apiKey"`
		KeepAPIKey bool   `json:"keepApiKey"`
	}
	if err := json.Unmarshal(control.calls[0].params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.APIKey != "" || !params.KeepAPIKey {
		t.Fatalf("remote config.set params = %+v, want blank apiKey with keepApiKey", params)
	}
}

func TestRemoteOpenSessionLogFetchesAndOpensTempFile(t *testing.T) {
	control := &fakeControlClient{responses: map[string]any{
		"agents.getSessionLog": map[string]any{"exists": true, "content": "{\"message\":\"safe\"}\n"},
	}}
	original := startExternalCommand
	defer func() { startExternalCommand = original }()
	var opened []string
	startExternalCommand = func(name string, args ...string) error {
		opened = append([]string{name}, args...)
		return nil
	}

	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, control)
	app.selectedAgentID = "agent-1"

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	app = model.(App)
	if cmd == nil {
		t.Fatal("remote open log should fetch through control")
	}
	msg := cmd()
	if notice, ok := msg.(statusNotice); !ok || notice.text != "Opened remote session log" {
		t.Fatalf("cmd returned %#v, want opened remote session log notice", msg)
	}
	if len(opened) != 2 || opened[0] != "open" {
		t.Fatalf("open command = %#v", opened)
	}
	data, err := os.ReadFile(opened[1])
	if err != nil {
		t.Fatalf("read opened temp log: %v", err)
	}
	if string(data) != "{\"message\":\"safe\"}\n" {
		t.Fatalf("temp log content = %q", data)
	}
}

func TestRemoteControlKillConfirmationUsesKillLanguage(t *testing.T) {
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, &fakeControlClient{})
	app.agentList.SetAgents([]vault.Agent{{ID: "agent-1", Name: "Remote", Status: "active"}})
	app.agentList.SelectAgentID("agent-1")
	app.selectedAgentID = "agent-1"

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	app = model.(App)

	if !app.confirmDelete {
		t.Fatal("expected remote kill confirmation to be armed")
	}
	if !strings.Contains(app.notice, "kill remote agent") || strings.Contains(app.notice, "delete") {
		t.Fatalf("notice = %q, want remote kill confirmation", app.notice)
	}
}

func TestRemoteControlKillIgnoresNonLiveAgents(t *testing.T) {
	control := &fakeControlClient{responses: map[string]any{
		"agents.kill": map[string]any{"status": "ok"},
	}}
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, control)
	app.agentList.SetAgents([]vault.Agent{{ID: "agent-1", Name: "Remote", Status: "paused"}})
	app.agentList.SelectAgentID("agent-1")
	app.selectedAgentID = "agent-1"

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	app = model.(App)

	if cmd != nil {
		t.Fatal("paused remote kill should not return command")
	}
	if app.confirmDelete {
		t.Fatal("paused remote kill should not arm confirmation")
	}
	if !strings.Contains(app.notice, "only available for live agents") {
		t.Fatalf("notice = %q, want non-live kill notice", app.notice)
	}
	if len(control.calls) != 0 {
		t.Fatalf("remote kill should not be called, got %+v", control.calls)
	}
}

func TestViewKeepsRenderedLinesWithinTerminalWidthAfterShrink(t *testing.T) {
	db := openTestVault(t)
	app := NewApp(nil, nil, nil, db, nil, nil)
	app.width = 70
	app.height = 24
	app.leftWidth = 30
	app.rightWidth = 30
	app.leftSplit = 13
	app.rightSplit = 10
	app.updatePanelSizes()

	view := app.View()
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > app.width {
			t.Fatalf("line %d width = %d, want <= %d:\n%s", i+1, width, app.width, view)
		}
	}
}

func TestSettingsViewKeepsRenderedLinesWithinTerminalAfterShrink(t *testing.T) {
	cfg := &config.Config{
		Provider:               "anthropic",
		Model:                  "anthropic/claude-sonnet-4-6-with-a-long-display-name",
		APIKey:                 "sk-test",
		ResizePanelsWithArrows: true,
	}
	app := NewApp(nil, nil, nil, nil, cfg, nil)
	app.width = 58
	app.height = 16
	app.leftWidth = 30
	app.rightWidth = 30
	app.leftSplit = 13
	app.rightSplit = 10
	app.updatePanelSizes()
	app.focus = FocusSettings
	app.settings.SetActive(true)
	app.settings.SetConfig(cfg)
	app.settings.SetProxies([]settings.ProxyItem{{
		ID:      "proxy-1",
		Label:   "customer-research-proxy-with-a-very-long-label",
		Type:    "socks5",
		Host:    "very-long-proxy-hostname-that-would-wrap.example.internal",
		Port:    1080,
		Country: "GB",
		Latency: "123456789ms",
	}})

	view := app.View()
	lines := strings.Split(view, "\n")
	if len(lines) > app.height {
		t.Fatalf("settings view height = %d, want <= %d:\n%s", len(lines), app.height, view)
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width > app.width {
			t.Fatalf("settings line %d width = %d, want <= %d:\n%s", i+1, width, app.width, view)
		}
	}
}

func TestSettingsWorkbenchViewRendersFullContainerFrame(t *testing.T) {
	cfg := &config.Config{Provider: "anthropic", Model: "anthropic/claude-sonnet-4-6"}
	app := NewApp(nil, nil, nil, nil, cfg, nil)
	app.width = 100
	app.height = 24
	app.leftWidth = 28
	app.rightWidth = 28
	app.leftSplit = 10
	app.rightSplit = 10
	app.focus = FocusSettings
	app.settings.SetActive(true)
	app.settings.SetConfig(cfg)
	app.updatePanelSizes()

	view := app.View()
	lines := strings.Split(view, "\n")
	if len(lines) < 2 {
		t.Fatalf("settings workbench rendered too few lines:\n%s", view)
	}

	border := lipgloss.RoundedBorder()
	topLine := lines[0]
	bodyBottomLine := lines[len(lines)-2]
	// Two-column workbench (left sidebar + center): each row shows 2 panels = 4 corners.
	if got := strings.Count(topLine, border.TopLeft) + strings.Count(topLine, border.TopRight); got < 4 {
		t.Fatalf("top workbench row has %d panel corners, want at least 4:\n%s", got, view)
	}
	if got := strings.Count(bodyBottomLine, border.BottomLeft) + strings.Count(bodyBottomLine, border.BottomRight); got < 4 {
		t.Fatalf("bottom workbench row has %d panel corners, want at least 4:\n%s", got, view)
	}
}

func TestWorkbenchBodyHeightReservesOneStatusRow(t *testing.T) {
	tests := []struct {
		terminalHeight int
		want           int
	}{
		{terminalHeight: 24, want: 23},
		{terminalHeight: 4, want: 3},
		{terminalHeight: 1, want: 1},
		{terminalHeight: 0, want: 1},
	}

	for _, tt := range tests {
		if got := workbenchBodyHeight(tt.terminalHeight); got != tt.want {
			t.Fatalf("workbenchBodyHeight(%d) = %d, want %d", tt.terminalHeight, got, tt.want)
		}
	}
}

func TestViewHandlesTinyTerminalWithoutPanic(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.width = 20
	app.height = 2
	app.updatePanelSizes()

	view := app.View()
	lines := strings.Split(view, "\n")
	if len(lines) > app.height {
		t.Fatalf("tiny view height = %d, want <= %d:\n%s", len(lines), app.height, view)
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width > app.width {
			t.Fatalf("tiny line %d width = %d, want <= %d:\n%s", i+1, width, app.width, view)
		}
	}
}

func TestViewHandlesNarrowWorkbenchWithoutOverflow(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.width = 20
	app.height = 12
	app.leftWidth = 18
	app.rightWidth = 18
	app.leftSplit = 13
	app.rightSplit = 10
	app.updatePanelSizes()

	view := app.View()
	lines := strings.Split(view, "\n")
	if len(lines) > app.height {
		t.Fatalf("narrow view height = %d, want <= %d:\n%s", len(lines), app.height, view)
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width > app.width {
			t.Fatalf("narrow line %d width = %d, want <= %d:\n%s", i+1, width, app.width, view)
		}
	}
}

func TestCompactWorkbenchShowsNewAgentInput(t *testing.T) {
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, &fakeControlClient{})
	app.width = 40
	app.height = 10
	app.focus = FocusAgentList
	app.updatePanelSizes()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	app = model.(App)

	view := app.View()
	if !strings.Contains(view, "NEW AGENT") {
		t.Fatalf("compact new-agent prompt hidden:\n%s", view)
	}
	if strings.Contains(view, "No agents yet") {
		t.Fatalf("compact view rendered agent list instead of active input:\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > app.width {
			t.Fatalf("compact input line %d width = %d, want <= %d:\n%s", i+1, width, app.width, view)
		}
	}
}

func TestNoticeStatusLineIsWidthConstrained(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.width = 40
	app.height = 12
	app.notice = strings.Repeat("notice-", 20)
	app.updatePanelSizes()

	view := app.View()
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > app.width {
			t.Fatalf("notice line %d width = %d, want <= %d:\n%s", i+1, width, app.width, view)
		}
	}
}

func TestStatusBarPreservesModeAndQuitHints(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.width = 80

	bar := app.renderStatusBar()
	for _, want := range []string{"q:quit"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("status bar missing %q:\n%s", want, bar)
		}
	}
	if width := lipgloss.Width(bar); width > app.width {
		t.Fatalf("status bar width = %d, want <= %d:\n%s", width, app.width, bar)
	}
}

func TestModeHotkeyTogglesResizeMode(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{ResizePanelsWithArrows: false}
	app := NewApp(nil, nil, nil, db, cfg, nil)

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	app = model.(App)
	if !app.resizeModeEnabled() {
		t.Fatal("resize mode should be enabled after pressing m")
	}
	if app.notice == "" || !strings.Contains(app.notice, "Resize mode enabled") {
		t.Fatalf("unexpected notice after enabling resize mode: %q", app.notice)
	}

	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	app = model.(App)
	if app.resizeModeEnabled() {
		t.Fatal("resize mode should be disabled after pressing m again")
	}
	if app.notice == "" || !strings.Contains(app.notice, "Resize mode disabled") {
		t.Fatalf("unexpected notice after disabling resize mode: %q", app.notice)
	}
}

func TestModeHotkeyDoesNotPersistResizePreference(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{ResizePanelsWithArrows: false}
	app := NewApp(nil, nil, nil, db, cfg, nil)

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	app = model.(App)

	if !app.resizeModeEnabled() {
		t.Fatal("resize mode should be enabled after pressing m")
	}
	if cfg.ResizePanelsWithArrows {
		t.Fatal("mode hotkey should not persist the resize preference")
	}
}

func TestSelectedAssistantReplyRefocusesChatInput(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.conversation.Blur()

	model, cmd := app.Update(shared.ConversationEntryMsg{
		AgentID: agent.ID,
		Role:    "assistant",
		Content: "Ready",
	})
	app = model.(App)

	if cmd == nil {
		t.Fatal("expected selected assistant reply to return a focus command")
	}
	if !app.conversation.IsAwake() {
		t.Fatal("conversation should stay awake after assistant reply")
	}
}

func TestPendingStartupReplyRefocusesChatInput(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.focus = FocusAgentDetail
	app.inputMode = ""
	app.pendingChatFocusAgentID = agent.ID
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.Blur()

	model, cmd := app.Update(shared.ConversationEntryMsg{
		AgentID: agent.ID,
		Role:    "assistant",
		Content: "Ready to work.",
	})
	app = model.(App)

	if cmd == nil {
		t.Fatal("expected first assistant reply to return a focus command")
	}
	if app.focus != FocusConversation {
		t.Fatalf("focus = %d, want conversation", app.focus)
	}
	if app.inputMode != "chat" {
		t.Fatalf("inputMode = %q, want chat", app.inputMode)
	}
	if app.pendingChatFocusAgentID != "" {
		t.Fatalf("pendingChatFocusAgentID = %q, want cleared", app.pendingChatFocusAgentID)
	}
	if !app.conversation.IsAwake() {
		t.Fatal("conversation should be awake after first assistant reply")
	}
}

func TestBackgroundPendingStartupReplyClearsMarkerWithoutStealingFocus(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	selected, err := db.CreateAgent("Selected", "current task", "{}")
	if err != nil {
		t.Fatalf("create selected agent: %v", err)
	}
	background, err := db.CreateAgent("Background", "background task", "{}")
	if err != nil {
		t.Fatalf("create background agent: %v", err)
	}
	app.selectedAgentID = selected.ID
	app.focus = FocusAgentList
	app.inputMode = ""
	app.pendingChatFocusAgentID = background.ID
	app.conversation.SetAgentID(selected.ID)

	model, cmd := app.Update(shared.ConversationEntryMsg{
		AgentID: background.ID,
		Role:    "assistant",
		Content: "Ready in the background.",
	})
	app = model.(App)

	if cmd == nil {
		t.Fatal("expected wait-for-event command to continue event processing")
	}
	if app.pendingChatFocusAgentID != "" {
		t.Fatalf("pendingChatFocusAgentID = %q, want cleared", app.pendingChatFocusAgentID)
	}
	if app.focus != FocusAgentList {
		t.Fatalf("focus = %d, want agent list", app.focus)
	}
	if app.inputMode != "" {
		t.Fatalf("inputMode = %q, want unchanged empty mode", app.inputMode)
	}
}

func TestPendingStartupTerminalStatusRefocusesChatInput(t *testing.T) {
	for _, status := range []string{"completed", "error", "failed", "interrupted"} {
		t.Run(status, func(t *testing.T) {
			db := openTestVault(t)
			cfg := &config.Config{}
			app := NewApp(nil, nil, nil, db, cfg, nil)
			app.conversation.SetSize(80, 20)

			agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
			if err != nil {
				t.Fatalf("create agent: %v", err)
			}
			app.selectedAgentID = agent.ID
			app.focus = FocusAgentDetail
			app.inputMode = ""
			app.pendingChatFocusAgentID = agent.ID
			app.conversation.SetAgentID(agent.ID)
			app.conversation.SetAgentName(agent.Name)
			app.conversation.SetThinking(true)
			app.conversation.Blur()

			model, cmd := app.Update(shared.AgentStatusMsg{
				AgentID: agent.ID,
				Status:  status,
			})
			app = model.(App)

			if cmd == nil {
				t.Fatal("expected terminal status to return a focus command")
			}
			if app.focus != FocusConversation {
				t.Fatalf("focus = %d, want conversation", app.focus)
			}
			if app.inputMode != "chat" {
				t.Fatalf("inputMode = %q, want chat", app.inputMode)
			}
			if app.pendingChatFocusAgentID != "" {
				t.Fatalf("pendingChatFocusAgentID = %q, want cleared", app.pendingChatFocusAgentID)
			}
			if app.conversation.IsThinking() {
				t.Fatal("conversation should stop thinking on terminal status")
			}
		})
	}
}

func TestBackgroundPendingStartupTerminalStatusClearsMarkerWithoutStealingFocus(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	selected, err := db.CreateAgent("Selected", "current task", "{}")
	if err != nil {
		t.Fatalf("create selected agent: %v", err)
	}
	background, err := db.CreateAgent("Background", "background task", "{}")
	if err != nil {
		t.Fatalf("create background agent: %v", err)
	}
	app.selectedAgentID = selected.ID
	app.focus = FocusAgentList
	app.inputMode = ""
	app.pendingChatFocusAgentID = background.ID
	app.conversation.SetAgentID(selected.ID)

	model, _ := app.Update(shared.AgentStatusMsg{
		AgentID: background.ID,
		Status:  "completed",
	})
	app = model.(App)

	if app.pendingChatFocusAgentID != "" {
		t.Fatalf("pendingChatFocusAgentID = %q, want cleared", app.pendingChatFocusAgentID)
	}
	if app.focus != FocusAgentList {
		t.Fatalf("focus = %d, want agent list", app.focus)
	}
	if app.inputMode != "" {
		t.Fatalf("inputMode = %q, want unchanged empty mode", app.inputMode)
	}
}

func TestPausedStatusClearsSelectedConversationThinking(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetThinking(true)

	model, _ := app.Update(shared.AgentStatusMsg{
		AgentID: agent.ID,
		Status:  "paused",
	})
	app = model.(App)

	if app.conversation.IsThinking() {
		t.Fatal("conversation should stop thinking on paused status")
	}
}

func TestChatInputKeystrokeRefocusesAndCapturesFirstRune(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.conversation.Blur()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	app = model.(App)

	if !app.conversation.Focused() {
		t.Fatal("conversation input should refocus on first chat keystroke")
	}
	if got := app.conversation.TextInput().Value(); got != "h" {
		t.Fatalf("conversation input = %q, want %q", got, "h")
	}
}

func TestUnfocusedChatAllowsViewShortcut(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.conversation.Blur()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	app = model.(App)

	if got := app.conversation.TextInput().Value(); got != "" {
		t.Fatalf("conversation input = %q, want empty when view shortcut is used", got)
	}
}

func TestFocusedChatAllowsCtrlViewShortcut(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.conversation.Focus()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	app = model.(App)

	if got := app.conversation.TextInput().Value(); got != "" {
		t.Fatalf("conversation input = %q, want empty when ctrl+v shortcut is used", got)
	}
}

func TestChatPasteDisplaysMarkerAndPersistsRawContent(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.conversation.Focus()

	raw := "raw pasted transcript content"
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(raw), Paste: true})
	app = model.(App)

	if got := app.conversation.TextInput().Value(); got != "[Pasted Content 29 Chars]" {
		t.Fatalf("visible input = %q, want paste marker", got)
	}

	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)

	msgs, err := db.GetMessages(agent.ID)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1 user message", len(msgs))
	}
	if msgs[0].Content != raw {
		t.Fatalf("persisted content = %q, want raw paste", msgs[0].Content)
	}
	if msgs[0].DisplayContent != "[Pasted Content 29 Chars]" {
		t.Fatalf("display content = %q, want paste marker", msgs[0].DisplayContent)
	}
	view := app.conversation.View()
	if strings.Contains(view, raw) {
		t.Fatalf("conversation leaked raw paste:\n%s", view)
	}
	if !strings.Contains(view, "[Pasted Content 29 Chars]") {
		t.Fatalf("conversation missing paste marker:\n%s", view)
	}
}

func TestAgentTurnPromptIncludesPersistedVisibleHistory(t *testing.T) {
	db := openTestVault(t)
	app := NewApp(nil, nil, nil, db, &config.Config{}, nil)

	agent, err := db.CreateAgent("Researcher", "Diagnose browser", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := db.AppendMessage(agent.ID, "system", "Error: disk I/O error", 0); err != nil {
		t.Fatalf("append system message: %v", err)
	}
	if err := db.AppendMessage(agent.ID, "assistant", "Let me start probing.", 0); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	if err := db.AppendMessage(agent.ID, "user", "what error did you see?", 0); err != nil {
		t.Fatalf("append current message: %v", err)
	}

	prompt := app.agentTurnPrompt(agent.ID, "what error did you see?")
	if !strings.Contains(prompt, "[system] Error: disk I/O error") {
		t.Fatalf("prompt missing system error history:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[assistant] Let me start probing.") {
		t.Fatalf("prompt missing assistant history:\n%s", prompt)
	}
	if strings.Count(prompt, "what error did you see?") != 1 {
		t.Fatalf("prompt should include current message once:\n%s", prompt)
	}
}

func TestFocusedEmptyChatAllowsViewShortcut(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.conversation.Focus()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	app = model.(App)

	if got := app.conversation.TextInput().Value(); got != "" {
		t.Fatalf("conversation input = %q, want empty when v shortcut is used from empty focused chat", got)
	}
}

func TestFocusedEmptyChatAllowsModeShortcut(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.conversation.Focus()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	app = model.(App)

	if !app.resizeModeEnabled() {
		t.Fatal("mode shortcut should enable resize mode from empty focused chat")
	}
	if got := app.conversation.TextInput().Value(); got != "" {
		t.Fatalf("conversation input = %q, want empty when mode shortcut is used from empty focused chat", got)
	}
}

func TestFocusedEmptyChatAllowsSettingsShortcut(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.conversation.Focus()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	app = model.(App)

	if app.focus != FocusSettings || !app.settings.IsActive() {
		t.Fatal("settings shortcut should open settings from empty focused chat")
	}
	if got := app.conversation.TextInput().Value(); got != "" {
		t.Fatalf("conversation input = %q, want empty when settings shortcut is used from empty focused chat", got)
	}
}

func TestFocusedEmptyChatAllowsReconfigureShortcut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	db := openTestVault(t)
	cfg := &config.Config{SetupComplete: true}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.width = 100
	app.height = 30
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.conversation.Focus()

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	app = model.(App)

	if cmd != nil {
		if msg := cmd(); msg != nil {
			if _, ok := msg.(tea.QuitMsg); ok {
				t.Fatal("reconfigure shortcut should stay in the TUI instead of quitting")
			}
		}
	}
	if got := app.conversation.TextInput().Value(); got != "" {
		t.Fatalf("conversation input = %q, want empty when reconfigure shortcut is used from empty focused chat", got)
	}
	if !cfg.SetupComplete {
		t.Fatal("reconfigure shortcut should not clear setupComplete on the active config")
	}
	if config.ReconfigureRequested() {
		t.Fatal("reconfigure shortcut should not queue a next-launch setup marker")
	}
	if !strings.Contains(app.View(), "First Time Setup") {
		t.Fatalf("reconfigure shortcut should show setup in the current TUI:\n%s", app.View())
	}
}

func TestFocusedDraftChatTreatsShortcutLettersAsText(t *testing.T) {
	for _, key := range []rune{'v', 't', 'o', 'm', 'c', 'S'} {
		t.Run(string(key), func(t *testing.T) {
			db := openTestVault(t)
			cfg := &config.Config{}
			app := NewApp(nil, nil, nil, db, cfg, nil)
			app.conversation.SetSize(80, 20)

			agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
			if err != nil {
				t.Fatalf("create agent: %v", err)
			}
			app.selectedAgentID = agent.ID
			app.focus = FocusConversation
			app.inputMode = "chat"
			app.conversation.SetAgentID(agent.ID)
			app.conversation.SetAgentName(agent.Name)
			app.conversation.SetAwake(true)
			app.conversation.Focus()
			app.conversation.TextInput().SetValue("draft")

			model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
			app = model.(App)

			want := "draft" + string(key)
			if got := app.conversation.TextInput().Value(); got != want {
				t.Fatalf("conversation input = %q, want %q", got, want)
			}
		})
	}
}

func TestThinkingChatDoesNotStartSecondTurn(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Researcher", "Research", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.conversation.SetThinking(true)
	app.conversation.Focus()
	app.conversation.TextInput().SetValue("second turn")

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)

	if cmd != nil {
		t.Fatal("thinking chat enter returned a command")
	}
	if got := app.conversation.TextInput().Value(); got != "second turn" {
		t.Fatalf("conversation input = %q, want draft preserved", got)
	}
	if app.notice == "" {
		t.Fatal("expected a notice explaining why chat is locked")
	}
}

func TestUnfocusedChatAllowsTraceShortcut(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.conversation.Blur()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	app = model.(App)

	if !app.conversation.TraceOnly() {
		t.Fatal("trace shortcut should enable trace mode from unfocused chat state")
	}
	if got := app.conversation.TextInput().Value(); got != "" {
		t.Fatalf("conversation input = %q, want empty when trace shortcut is used", got)
	}
}

func TestFocusedEmptyChatAllowsTraceShortcut(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)

	agent, err := db.CreateAgent("Scraper", "Scrape prices", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.selectedAgentID = agent.ID
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.conversation.Focus()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	app = model.(App)

	if !app.conversation.TraceOnly() {
		t.Fatal("trace shortcut should enable trace mode from empty focused chat state")
	}
	if got := app.conversation.TextInput().Value(); got != "" {
		t.Fatalf("conversation input = %q, want empty when trace shortcut is used from empty focused chat", got)
	}
}

func TestReconfigureShortcutStartsSetupInPlaceWithoutClearingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &config.Config{
		Provider:      "anthropic",
		APIKey:        "sk-test",
		Model:         "anthropic/claude-sonnet-4-6",
		SetupComplete: true,
	}
	app := NewApp(nil, nil, nil, nil, cfg, nil)
	app.width = 100
	app.height = 30

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	updated := model.(App)

	if cmd != nil {
		if msg := cmd(); msg != nil {
			if _, ok := msg.(tea.QuitMsg); ok {
				t.Fatal("reconfigure shortcut should stay in the TUI instead of quitting")
			}
		}
	}
	if !cfg.SetupComplete {
		t.Fatal("reconfigure shortcut should not clear setupComplete on the active config")
	}
	if config.ReconfigureRequested() {
		t.Fatal("reconfigure shortcut should not queue the setup wizard for next launch")
	}
	if !strings.Contains(updated.View(), "First Time Setup") {
		t.Fatalf("reconfigure shortcut should show setup in the current TUI:\n%s", updated.View())
	}
}

func TestSettingsReconfigureShortcutStartsSetupInPlace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &config.Config{
		Provider:      "anthropic",
		APIKey:        "sk-test",
		Model:         "anthropic/claude-sonnet-4-6",
		SetupComplete: true,
	}
	app := NewApp(nil, nil, nil, nil, cfg, nil)
	app.width = 100
	app.height = 30

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	app = model.(App)
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	app = model.(App)
	if cmd == nil {
		t.Fatal("settings reconfigure shortcut should emit a reconfigure message")
	}
	model, cmd = app.Update(cmd())
	updated := model.(App)

	if cmd != nil {
		if msg := cmd(); msg != nil {
			if _, ok := msg.(tea.QuitMsg); ok {
				t.Fatal("settings reconfigure shortcut should stay in the TUI instead of quitting")
			}
		}
	}
	if config.ReconfigureRequested() {
		t.Fatal("settings reconfigure shortcut should not queue the setup wizard")
	}
	if !cfg.SetupComplete {
		t.Fatal("settings reconfigure shortcut should not clear setupComplete")
	}
	if !strings.Contains(updated.View(), "First Time Setup") {
		t.Fatalf("settings reconfigure shortcut should show setup in the current TUI:\n%s", updated.View())
	}
}

func TestEmbeddedReconfigureCompletionSavesConfigWithoutQuitting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &config.Config{
		Provider:        "anthropic",
		APIKey:          "sk-test",
		Model:           "anthropic/claude-sonnet-4-6",
		SetupComplete:   true,
		FoxbridgeCDPURL: "ws://127.0.0.1:9222",
	}
	app := NewApp(nil, nil, nil, nil, cfg, nil)
	app.width = 100
	app.height = 30

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	app = model.(App)
	if cmd != nil {
		_ = cmd()
	}

	for i := 0; i < 4; i++ {
		model, cmd = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
		app = model.(App)
		if cmd == nil {
			continue
		}
		if msg := cmd(); msg != nil {
			if _, ok := msg.(tea.QuitMsg); ok {
				t.Fatal("embedded setup completion should not quit the workbench")
			}
		}
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}
	if !loaded.SetupComplete {
		t.Fatal("saved config should remain setup complete")
	}
	if loaded.Provider != "anthropic" || loaded.Model != "anthropic/claude-sonnet-4-6" || loaded.APIKey != "sk-test" {
		t.Fatalf("saved config changed unexpectedly: provider=%q model=%q key=%q", loaded.Provider, loaded.Model, loaded.APIKey)
	}
	if app.cfg == nil || app.cfg.FoxbridgeCDPURL != "ws://127.0.0.1:9222" {
		t.Fatalf("runtime foxbridge route was not preserved: %#v", app.cfg)
	}
	if app.focus != FocusSettings || !app.settings.IsActive() {
		t.Fatalf("focus/settings = %d/%t, want settings active after setup", app.focus, app.settings.IsActive())
	}
	if !strings.Contains(app.notice, "Configuration updated") {
		t.Fatalf("notice = %q, want configuration updated", app.notice)
	}
}

func TestUnfocusedChatAllowsOpenLogShortcut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &config.Config{}
	db := openTestVault(t)
	agent, err := db.CreateAgent("agent", "task", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	if err := db.AppendMessage(agent.ID, "assistant", "session line", 0); err != nil {
		t.Fatalf("append message: %v", err)
	}
	logPath := filepath.Join(os.TempDir(), "vulpineos-session-"+safeTempAgentID(agent.ID)+".jsonl")

	original := startExternalCommand
	defer func() { startExternalCommand = original }()
	var opened []string
	startExternalCommand = func(name string, args ...string) error {
		opened = append([]string{name}, args...)
		return nil
	}

	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)
	app.selectedAgentID = agent.ID
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.conversation.Blur()
	app.focus = FocusConversation
	app.inputMode = "chat"

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	updated := model.(App)

	if got := updated.conversation.TextInput().Value(); got != "" {
		t.Fatalf("conversation input = %q, want empty when open-log shortcut is used", got)
	}
	if len(opened) != 2 || opened[0] != "open" || opened[1] != logPath {
		t.Fatalf("unexpected open command: %#v", opened)
	}
	if updated.notice != "Opened session log" {
		t.Fatalf("notice = %q, want %q", updated.notice, "Opened session log")
	}
}

func TestFocusedChatAllowsCtrlOpenLogShortcut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &config.Config{}
	db := openTestVault(t)
	agent, err := db.CreateAgent("agent", "task", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	if err := db.AppendMessage(agent.ID, "assistant", "session line", 0); err != nil {
		t.Fatalf("append message: %v", err)
	}
	logPath := filepath.Join(os.TempDir(), "vulpineos-session-"+safeTempAgentID(agent.ID)+".jsonl")

	original := startExternalCommand
	defer func() { startExternalCommand = original }()
	var opened []string
	startExternalCommand = func(name string, args ...string) error {
		opened = append([]string{name}, args...)
		return nil
	}

	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)
	app.selectedAgentID = agent.ID
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.Focus()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	updated := model.(App)

	if got := updated.conversation.TextInput().Value(); got != "" {
		t.Fatalf("conversation input = %q, want empty when ctrl+o shortcut is used", got)
	}
	if len(opened) != 2 || opened[0] != "open" || opened[1] != logPath {
		t.Fatalf("unexpected open command: %#v", opened)
	}
}

func TestFocusedEmptyChatAllowsOpenLogShortcut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &config.Config{}
	db := openTestVault(t)
	agent, err := db.CreateAgent("agent", "task", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	if err := db.AppendMessage(agent.ID, "assistant", "session line", 0); err != nil {
		t.Fatalf("append message: %v", err)
	}
	logPath := filepath.Join(os.TempDir(), "vulpineos-session-"+safeTempAgentID(agent.ID)+".jsonl")

	original := startExternalCommand
	defer func() { startExternalCommand = original }()
	var opened []string
	startExternalCommand = func(name string, args ...string) error {
		opened = append([]string{name}, args...)
		return nil
	}

	app := NewApp(nil, nil, nil, db, cfg, nil)
	app.conversation.SetSize(80, 20)
	app.selectedAgentID = agent.ID
	app.conversation.SetAgentID(agent.ID)
	app.conversation.SetAgentName(agent.Name)
	app.conversation.SetAwake(true)
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.Focus()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	updated := model.(App)

	if got := updated.conversation.TextInput().Value(); got != "" {
		t.Fatalf("conversation input = %q, want empty when open-log shortcut is used from empty focused chat", got)
	}
	if len(opened) != 2 || opened[0] != "open" || opened[1] != logPath {
		t.Fatalf("unexpected open command: %#v", opened)
	}
}

func TestOpenExternalTargetFallsBackToAvailableLauncher(t *testing.T) {
	originalStart := startExternalCommand
	originalLook := lookExternalCommand
	defer func() {
		startExternalCommand = originalStart
		lookExternalCommand = originalLook
	}()

	lookExternalCommand = func(name string) (string, error) {
		if name == "xdg-open" {
			return "/usr/bin/xdg-open", nil
		}
		return "", errors.New("missing")
	}
	var opened []string
	startExternalCommand = func(name string, args ...string) error {
		opened = append([]string{name}, args...)
		return nil
	}

	if err := openExternalTarget("https://example.test"); err != nil {
		t.Fatalf("openExternalTarget: %v", err)
	}
	if len(opened) != 2 || opened[0] != "xdg-open" || opened[1] != "https://example.test" {
		t.Fatalf("opened = %#v, want xdg-open fallback", opened)
	}
}

func TestTraceModeHotkeyTogglesConversationTrace(t *testing.T) {
	db := openTestVault(t)
	cfg := &config.Config{}
	app := NewApp(nil, nil, nil, db, cfg, nil)

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	app = model.(App)
	if !app.conversation.TraceOnly() {
		t.Fatal("trace mode should be enabled after pressing t")
	}
	if !strings.Contains(app.notice, "Trace mode enabled") {
		t.Fatalf("unexpected trace enable notice: %q", app.notice)
	}

	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	app = model.(App)
	if app.conversation.TraceOnly() {
		t.Fatal("trace mode should be disabled after pressing t again")
	}
	if !strings.Contains(app.notice, "Trace mode disabled") {
		t.Fatalf("unexpected trace disable notice: %q", app.notice)
	}
}

func TestBulkKillKeybindingWithoutOrchestrator(t *testing.T) {
	db := openTestVault(t)

	app := NewApp(nil, nil, nil, db, nil, nil)

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	if cmd != nil {
		t.Fatal("did not expect first X press to return a command")
	}
	app = model.(App)
	if !app.confirmKillAll {
		t.Fatal("expected bulk kill confirmation to be armed")
	}

	model, cmd = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	if cmd == nil {
		t.Fatal("expected second X press to return a command")
	}
	app = model.(App)
	if app.confirmKillAll {
		t.Fatal("expected bulk kill confirmation to clear after execution")
	}
	msg := cmd()
	notice, ok := msg.(statusNotice)
	if !ok {
		t.Fatalf("bulk kill command returned %T, want statusNotice", msg)
	}
	if notice.text != "Kill all unavailable" {
		t.Fatalf("bulk kill notice = %q, want %q", notice.text, "Kill all unavailable")
	}
}

func TestLocalKillPathsUseOrchestratorCleanup(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "app.go", nil, 0)
	if err != nil {
		t.Fatalf("parse app.go: %v", err)
	}
	data, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("read app.go: %v", err)
	}
	source := string(data)
	for _, decl := range file.Decls {
		start := fset.Position(decl.Pos()).Offset
		end := fset.Position(decl.End()).Offset
		body := source[start:end]
		switch {
		case strings.Contains(body, "func (a *App) deleteAgent"):
			if !strings.Contains(body, "a.orch.KillAgent(agentID)") {
				t.Fatal("deleteAgent should use Orchestrator.KillAgent for local live agents")
			}
			if strings.Contains(body, "a.orch.Agents.Kill(agentID)") {
				t.Fatal("deleteAgent bypasses orchestrator cleanup via Agents.Kill")
			}
		case strings.Contains(body, "func (a App) killAllAgents"):
			if !strings.Contains(body, "a.orch.KillAgent(status.AgentID)") {
				t.Fatal("killAllAgents should use Orchestrator.KillAgent for each live agent")
			}
			if strings.Contains(body, "a.orch.Agents.KillAll()") {
				t.Fatal("killAllAgents bypasses orchestrator cleanup via Agents.KillAll")
			}
		}
	}
}

func TestBrowserWindowLabelUsesCachedStatus(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "app.go", nil, 0)
	if err != nil {
		t.Fatalf("parse app.go: %v", err)
	}
	data, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("read app.go: %v", err)
	}
	source := string(data)
	for _, decl := range file.Decls {
		start := fset.Position(decl.Pos()).Offset
		end := fset.Position(decl.End()).Offset
		body := source[start:end]
		if !strings.Contains(body, "func (a *App) browserWindowLabel") {
			continue
		}
		if strings.Contains(body, ".Status()") {
			t.Fatal("browserWindowLabel should not synchronously refresh browser window status")
		}
		if !strings.Contains(body, ".CachedStatus()") {
			t.Fatal("browserWindowLabel should read cached browser window status")
		}
		return
	}
	t.Fatal("browserWindowLabel function not found")
}

type fakeBrowserContextWindow struct {
	visible bool
	calls   []string
}

func (f *fakeBrowserContextWindow) IsContextVisible(contextID string) bool {
	f.calls = append(f.calls, "visible:"+contextID)
	return f.visible
}

func (f *fakeBrowserContextWindow) HideContext(contextID string) error {
	f.calls = append(f.calls, "hide:"+contextID)
	return nil
}

func (f *fakeBrowserContextWindow) ShowContext(contextID string) error {
	f.calls = append(f.calls, "show:"+contextID)
	return nil
}

func TestBrowserToggleCmdDefersWindowWork(t *testing.T) {
	window := &fakeBrowserContextWindow{visible: true}

	cmd := browserToggleCmd(window, "ctx-1")
	if len(window.calls) != 0 {
		t.Fatalf("browserToggleCmd ran window work before command execution: %#v", window.calls)
	}

	msg := cmd()
	notice, ok := msg.(statusNotice)
	if !ok {
		t.Fatalf("browserToggleCmd returned %T, want statusNotice", msg)
	}
	if notice.text != "Context hidden" {
		t.Fatalf("notice = %q, want context hidden", notice.text)
	}
	if strings.Join(window.calls, ",") != "visible:ctx-1,hide:ctx-1" {
		t.Fatalf("window calls = %#v", window.calls)
	}
}

func TestBrowserToggleContextIDUsesSelectedAgentLiveContext(t *testing.T) {
	db := openTestVault(t)
	app := NewApp(nil, nil, nil, db, &config.Config{}, nil)

	agent, err := db.CreateAgent("Browser Agent", "Use a live context", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.agentList.SetAgents([]vault.Agent{*agent})
	app.agentList.SelectAgentID(agent.ID)
	app.selectedAgentID = agent.ID
	app.liveAgentContexts[agent.ID] = "ctx-agent-live"
	app.focus = FocusAgentList
	app.contextList, _ = app.contextList.Update(shared.TargetAttachedMsg{
		SessionID: "session-other",
		TargetID:  "target-other",
		ContextID: "ctx-other",
		URL:       "https://other.example",
	})

	contextID, notice := app.browserToggleContextID()
	if contextID != "ctx-agent-live" || notice != "" {
		t.Fatalf("toggle target = (%q, %q), want selected agent live context", contextID, notice)
	}
}

func TestBrowserToggleContextIDUsesSelectedAgentPinnedContext(t *testing.T) {
	db := openTestVault(t)
	app := NewApp(nil, nil, nil, db, &config.Config{}, nil)

	agent, err := db.CreateAgent("Pinned Agent", "Use a pinned context", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := db.UpdateAgentMetadata(agent.ID, vault.MarshalAgentMetadata(vault.AgentMetadata{ContextID: "ctx-agent-pinned"})); err != nil {
		t.Fatalf("update metadata: %v", err)
	}
	agent, err = db.GetAgent(agent.ID)
	if err != nil {
		t.Fatalf("reload agent: %v", err)
	}
	app.agentList.SetAgents([]vault.Agent{*agent})
	app.agentList.SelectAgentID(agent.ID)
	app.selectedAgentID = agent.ID
	app.focus = FocusConversation

	contextID, notice := app.browserToggleContextID()
	if contextID != "ctx-agent-pinned" || notice != "" {
		t.Fatalf("toggle target = (%q, %q), want selected agent pinned context", contextID, notice)
	}
}

func TestBrowserToggleContextIDUsesHighlightedContextWhenFocused(t *testing.T) {
	db := openTestVault(t)
	app := NewApp(nil, nil, nil, db, &config.Config{}, nil)

	agent, err := db.CreateAgent("Browser Agent", "Use a live context", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.agentList.SetAgents([]vault.Agent{*agent})
	app.agentList.SelectAgentID(agent.ID)
	app.selectedAgentID = agent.ID
	app.liveAgentContexts[agent.ID] = "ctx-agent-live"
	app.focus = FocusContextList
	app.contextList, _ = app.contextList.Update(shared.TargetAttachedMsg{
		SessionID: "session-selected",
		TargetID:  "target-selected",
		ContextID: "ctx-highlighted",
		URL:       "https://selected.example",
	})

	contextID, notice := app.browserToggleContextID()
	if contextID != "ctx-highlighted" || notice != "" {
		t.Fatalf("toggle target = (%q, %q), want highlighted context", contextID, notice)
	}
}

func TestBrowserToggleContextIDFallsBackToAgentContextFromContextPanel(t *testing.T) {
	db := openTestVault(t)
	app := NewApp(nil, nil, nil, db, &config.Config{}, nil)

	agent, err := db.CreateAgent("Browser Agent", "Use a live context", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.agentList.SetAgents([]vault.Agent{*agent})
	app.agentList.SelectAgentID(agent.ID)
	app.selectedAgentID = agent.ID
	app.liveAgentContexts[agent.ID] = "ctx-agent-live"
	app.focus = FocusContextList
	app.contextList, _ = app.contextList.Update(shared.TargetAttachedMsg{
		SessionID: "session-without-context-id",
		TargetID:  "target-without-context-id",
		URL:       "about:blank",
	})

	contextID, notice := app.browserToggleContextID()
	if contextID != "ctx-agent-live" || notice != "" {
		t.Fatalf("toggle target = (%q, %q), want selected agent live context fallback", contextID, notice)
	}
}

func TestBrowserToggleContextIDReportsMissingAgentContext(t *testing.T) {
	db := openTestVault(t)
	app := NewApp(nil, nil, nil, db, &config.Config{}, nil)

	agent, err := db.CreateAgent("Browser Agent", "No context yet", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app.agentList.SetAgents([]vault.Agent{*agent})
	app.agentList.SelectAgentID(agent.ID)
	app.selectedAgentID = agent.ID
	app.focus = FocusAgentList

	contextID, notice := app.browserToggleContextID()
	if contextID != "" || notice != "Selected agent has no active browser context" {
		t.Fatalf("toggle target = (%q, %q), want no active context notice", contextID, notice)
	}
}

func TestBulkAgentStatusMsgMarksAgentsInterrupted(t *testing.T) {
	db := openTestVault(t)

	first, err := db.CreateAgent("first-agent", "task", "{}")
	if err != nil {
		t.Fatalf("create first agent: %v", err)
	}
	second, err := db.CreateAgent("second-agent", "task", "{}")
	if err != nil {
		t.Fatalf("create second agent: %v", err)
	}
	if err := db.UpdateAgentStatus(first.ID, "active"); err != nil {
		t.Fatalf("set first status: %v", err)
	}
	if err := db.UpdateAgentStatus(second.ID, "active"); err != nil {
		t.Fatalf("set second status: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.selectedAgentID = first.ID
	app.conversation.SetAgentID(first.ID)
	app.conversation.SetThinking(true)

	model, _ := app.Update(shared.BulkAgentStatusMsg{
		AgentIDs: []string{first.ID, second.ID},
		Status:   "interrupted",
		Notice:   "Killed 2 agents",
	})
	app = model.(App)

	if got := app.agentList.Status(first.ID); got != "interrupted" {
		t.Fatalf("first status = %q, want interrupted", got)
	}
	if got := app.agentList.Status(second.ID); got != "interrupted" {
		t.Fatalf("second status = %q, want interrupted", got)
	}
	if app.notice != "Killed 2 agents" {
		t.Fatalf("notice = %q, want %q", app.notice, "Killed 2 agents")
	}
	if app.conversation.IsThinking() {
		t.Fatal("expected selected conversation thinking state to clear")
	}
	stored, err := db.GetAgent(second.ID)
	if err != nil {
		t.Fatalf("get second agent: %v", err)
	}
	if stored.Status != "interrupted" {
		t.Fatalf("stored second status = %q, want interrupted", stored.Status)
	}
}

func TestBulkAgentStatusMsgKeepsSelectedLiveAgentThinking(t *testing.T) {
	db := openTestVault(t)

	agent, err := db.CreateAgent("live-agent", "task", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.selectedAgentID = agent.ID
	app.conversation.SetAgentID(agent.ID)

	model, cmd := app.Update(shared.BulkAgentStatusMsg{
		AgentIDs: []string{agent.ID},
		Status:   "active",
		Notice:   "Agent running",
	})
	app = model.(App)

	if !app.conversation.IsThinking() {
		t.Fatal("selected live agent should keep conversation in thinking state")
	}
	if cmd == nil {
		t.Fatal("expected live bulk status to start thinking animation")
	}
}

func TestBulkAgentStatusMsgMarksAgentsPaused(t *testing.T) {
	db := openTestVault(t)

	first, err := db.CreateAgent("first-agent", "task", "{}")
	if err != nil {
		t.Fatalf("create first agent: %v", err)
	}
	second, err := db.CreateAgent("second-agent", "task", "{}")
	if err != nil {
		t.Fatalf("create second agent: %v", err)
	}
	if err := db.UpdateAgentStatus(first.ID, "active"); err != nil {
		t.Fatalf("set first status: %v", err)
	}
	if err := db.UpdateAgentStatus(second.ID, "active"); err != nil {
		t.Fatalf("set second status: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)

	model, _ := app.Update(shared.BulkAgentStatusMsg{
		AgentIDs: []string{first.ID, second.ID},
		Status:   "paused",
		Notice:   "Paused 2 agents",
	})
	app = model.(App)

	if got := app.agentList.Status(first.ID); got != "paused" {
		t.Fatalf("first status = %q, want paused", got)
	}
	if got := app.agentList.Status(second.ID); got != "paused" {
		t.Fatalf("second status = %q, want paused", got)
	}
	if app.notice != "Paused 2 agents" {
		t.Fatalf("notice = %q, want %q", app.notice, "Paused 2 agents")
	}
	stored, err := db.GetAgent(first.ID)
	if err != nil {
		t.Fatalf("get first agent: %v", err)
	}
	if stored.Status != "paused" {
		t.Fatalf("stored first status = %q, want paused", stored.Status)
	}
}

func TestGracefulShutdownWithoutOrchestratorLeavesVaultState(t *testing.T) {
	db := openTestVault(t)

	agent, err := db.CreateAgent("active-agent", "task", "{}")
	if err != nil {
		t.Fatalf("create active agent: %v", err)
	}
	if err := db.UpdateAgentStatus(agent.ID, "active"); err != nil {
		t.Fatalf("set active status: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	if err := db.UpdateAgentStatus(agent.ID, "active"); err != nil {
		t.Fatalf("restore active status after startup reconciliation: %v", err)
	}

	app.gracefulShutdown()

	stored, err := db.GetAgent(agent.ID)
	if err != nil {
		t.Fatalf("get agent after shutdown: %v", err)
	}
	if stored.Status != "active" {
		t.Fatalf("agent status = %q, want active without orchestrator", stored.Status)
	}
	if !shouldPauseOnShutdown("active") {
		t.Fatal("expected active agents to be paused during shutdown")
	}
	if shouldPauseOnShutdown("paused") {
		t.Fatal("did not expect paused agents to be re-paused during shutdown")
	}
}

func TestRemoteGracefulShutdownPausesActiveAgents(t *testing.T) {
	control := &fakeControlClient{responses: map[string]any{
		"agents.pauseMany": map[string]any{"failures": map[string]string{}},
	}}
	app := NewAppWithControl(nil, nil, nil, nil, nil, nil, control)
	app.agentList.SetAgents([]vault.Agent{
		{ID: "agent-active", Name: "Active", Status: "active"},
		{ID: "agent-paused", Name: "Paused", Status: "paused"},
	})

	cmd := app.shutdown()
	if cmd == nil {
		t.Fatal("remote shutdown returned no quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("remote shutdown did not return tea.QuitMsg")
	}
	if len(control.calls) != 1 {
		t.Fatalf("control calls = %+v, want one pauseMany call", control.calls)
	}
	if control.calls[0].method != "agents.pauseMany" {
		t.Fatalf("method = %q, want agents.pauseMany", control.calls[0].method)
	}
	var params struct {
		AgentIDs []string `json:"agentIds"`
	}
	if err := json.Unmarshal(control.calls[0].params, &params); err != nil {
		t.Fatalf("unmarshal pauseMany params: %v", err)
	}
	if len(params.AgentIDs) != 1 || params.AgentIDs[0] != "agent-active" {
		t.Fatalf("agentIds = %+v, want only active agent", params.AgentIDs)
	}
}

// shutdownFakeRuntime is a controllable AgentRuntime for testing gracefulShutdown
// without a live browser/runtime. It reports a fixed live-agent list and records
// PauseAgent calls.
type shutdownFakeRuntime struct {
	statuses []agentmsg.AgentStatus
	paused   map[string]bool
}

func (f *shutdownFakeRuntime) SetRuntimeAudit(*runtimeaudit.Manager)             {}
func (f *shutdownFakeRuntime) StatusChan() <-chan agentmsg.AgentStatus           { return nil }
func (f *shutdownFakeRuntime) ConversationChan() <-chan agentmsg.ConversationMsg { return nil }
func (f *shutdownFakeRuntime) SpawnIsolated(string, string, string, func(), ...string) (string, error) {
	return "", nil
}
func (f *shutdownFakeRuntime) SpawnWithSessionIsolated(string, string, string, string, func()) (string, error) {
	return "", nil
}
func (f *shutdownFakeRuntime) ResumeWithSessionIsolated(string, string, string, func()) (string, error) {
	return "", nil
}
func (f *shutdownFakeRuntime) Kill(string) error { return nil }
func (f *shutdownFakeRuntime) PauseAgent(id string) error {
	if f.paused == nil {
		f.paused = map[string]bool{}
	}
	f.paused[id] = true
	for i := range f.statuses {
		if f.statuses[i].AgentID == id {
			f.statuses = append(f.statuses[:i], f.statuses[i+1:]...)
			break
		}
	}
	return nil
}
func (f *shutdownFakeRuntime) KillAll()                     {}
func (f *shutdownFakeRuntime) Dispose()                     {}
func (f *shutdownFakeRuntime) Count() int                   { return len(f.statuses) }
func (f *shutdownFakeRuntime) List() []agentmsg.AgentStatus { return f.statuses }

func TestGracefulShutdownPausesActiveAgents(t *testing.T) {
	db := openTestVault(t)

	agent, err := db.CreateAgent("active-agent", "task", "{}")
	if err != nil {
		t.Fatalf("create active agent: %v", err)
	}

	rt := &shutdownFakeRuntime{statuses: []agentmsg.AgentStatus{{AgentID: agent.ID, Status: "active"}}}
	orch := orchestrator.New(nil, nil, db, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 1}, orchestrator.Opts{AgentRuntime: rt})
	defer orch.Agents.Dispose()
	defer orch.Pool.Close()

	app := NewApp(nil, nil, orch, db, nil, nil)
	if err := db.UpdateAgentStatus(agent.ID, "active"); err != nil {
		t.Fatalf("set active status: %v", err)
	}

	app.gracefulShutdown()

	stored, err := db.GetAgent(agent.ID)
	if err != nil {
		t.Fatalf("get agent after shutdown: %v", err)
	}
	if stored.Status != "paused" {
		t.Fatalf("agent status = %q, want paused", stored.Status)
	}
	if !rt.paused[agent.ID] {
		t.Fatalf("PauseAgent was not called for the active agent")
	}
	if len(orch.Agents.List()) != 0 {
		t.Fatalf("runtime List = %d, want 0 live agents after pause", len(orch.Agents.List()))
	}
}

func TestBrowserEventsDoNotPolluteSelectedConversation(t *testing.T) {
	db := openTestVault(t)

	agent, err := db.CreateAgent("agent", "task", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := db.AppendMessage(agent.ID, "assistant", "persisted reply", 0); err != nil {
		t.Fatalf("append message: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.conversation.SetSize(80, 20)
	app.selectedAgentID = agent.ID
	app.conversation.SetAgentID(agent.ID)
	app.conversation.LoadMessages([]vault.AgentMessage{
		{Role: "assistant", Content: "persisted reply"},
	})
	before := app.conversation.View()

	model, _ := app.Update(shared.NavigationMsg{
		SessionID: "sess-1",
		FrameID:   "frame-1",
		URL:       "https://example.com/other-agent",
	})
	app = model.(App)
	model, _ = app.Update(shared.PageLoadMsg{
		SessionID: "sess-1",
		FrameID:   "frame-1",
		Name:      "load",
	})
	app = model.(App)

	after := app.conversation.View()
	if after != before {
		t.Fatalf("conversation changed after unrelated browser events\nbefore:\n%s\n\nafter:\n%s", before, after)
	}
}

func TestEventNoticeRearmsEventPump(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	defer app.stopForwarders()
	app.eventCh <- shared.TickMsg{}

	model, cmd := app.Update(eventNotice{text: "monitor warning"})
	app = model.(App)

	if app.notice != "monitor warning" {
		t.Fatalf("notice = %q, want monitor warning", app.notice)
	}
	if cmd == nil {
		t.Fatal("event notice did not return a wait command")
	}
	if _, ok := cmd().(shared.TickMsg); !ok {
		t.Fatal("event notice wait command did not read the next event")
	}
}

func TestTrustWarmMsgShowsNoticeAndRearmsEventPump(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	defer app.stopForwarders()
	app.eventCh <- shared.TickMsg{}

	model, cmd := app.Update(shared.TrustWarmMsg{State: "warming", CurrentSite: "example.com"})
	app = model.(App)

	if app.notice != "Trust warming WARMING: example.com" {
		t.Fatalf("notice = %q, want trust warming status", app.notice)
	}
	if cmd == nil {
		t.Fatal("trust warm message did not return a wait command")
	}
	if _, ok := cmd().(shared.TickMsg); !ok {
		t.Fatal("trust warm wait command did not read the next event")
	}
}

func TestAlertMsgShowsNoticeAndRearmsEventPump(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	defer app.stopForwarders()
	app.eventCh <- shared.TickMsg{}

	model, cmd := app.Update(shared.AlertMsg{Type: "hidden_text", Details: "filtered invisible prompt", Blocked: true})
	app = model.(App)

	if app.notice != "WARNING hidden_text blocked: filtered invisible prompt" {
		t.Fatalf("notice = %q, want injection warning", app.notice)
	}
	if cmd == nil {
		t.Fatal("alert message did not return a wait command")
	}
	if _, ok := cmd().(shared.TickMsg); !ok {
		t.Fatal("alert wait command did not read the next event")
	}
}

func TestRemoteStatusUpdatesSystemPanel(t *testing.T) {
	control := &fakeControlClient{responses: map[string]any{
		"status.get": map[string]any{
			"kernel_running":  true,
			"kernel_pid":      1234,
			"kernel_headless": true,
			"browser_route":   "camoufox",
			"browser_window":  "headless",
			"pool_available":  2,
			"pool_active":     3,
			"pool_total":      5,
		},
	}}
	app := NewAppWithControl(nil, nil, nil, nil, &config.Config{}, nil, control)
	defer app.stopForwarders()

	msg := app.loadRemoteStatus()()
	model, _ := app.Update(msg)
	app = model.(App)

	view := app.systemInfo.View()
	for _, want := range []string{"RUNNING", "PID 1234", "Mode HEADLESS", "Route camoufox", "Win headless", "Pool: 2/3/5"} {
		if !strings.Contains(view, want) {
			t.Fatalf("system view missing %q:\n%s", want, view)
		}
	}
}

func TestStopForwardersPreventsBlockedEventSends(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	for i := 0; i < cap(app.eventCh); i++ {
		app.eventCh <- statusNotice{text: "fill"}
	}

	app.stopForwarders()

	done := make(chan struct{})
	go func() {
		app.emitEvent(statusNotice{text: "late"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("emitEvent blocked after TUI stop")
	}

	_, ok := <-app.monitor.AlertChan()
	if ok {
		t.Fatal("monitor alert channel should close when TUI forwarders stop")
	}
}

func TestEmitEventQueuesWhenEventChannelIsFull(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	defer app.stopForwarders()

	for i := 0; i < cap(app.eventCh); i++ {
		app.eventCh <- statusNotice{text: "fill"}
	}

	app.emitEvent(statusNotice{text: "queued"})

	deadline := time.After(500 * time.Millisecond)
	for i := 0; i < cap(app.eventCh)+1; i++ {
		select {
		case msg := <-app.eventCh:
			if notice, ok := msg.(statusNotice); ok && notice.text == "queued" {
				return
			}
		case <-deadline:
			t.Fatal("queued event was not delivered after channel space opened")
		}
	}
	t.Fatal("queued event was dropped")
}

func TestWaitForEventReturnsAfterStop(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.stopForwarders()

	done := make(chan tea.Msg, 1)
	go func() {
		done <- app.waitForEvent()()
	}()

	select {
	case msg := <-done:
		if msg != nil {
			t.Fatalf("waitForEvent returned %T after stop, want nil", msg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("waitForEvent did not return after TUI stop")
	}
}
