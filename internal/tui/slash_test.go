package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"vulpineos/internal/tui/commandpalette"
	"vulpineos/internal/tui/shared"
	"vulpineos/internal/vault"
)

func TestSlashKeyActivatesCommandPalette(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.width = 80
	app.height = 24

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	app = model.(App)

	if !app.commandPalette.Active() {
		t.Fatal("slash key should activate command palette")
	}
}

// sendKey forwards a key through the app's Update and drains any returned
// command. Useful for driving the agent picker, which dispatches its
// picked/cancelled messages via a tea.Cmd.
func sendKey(app App, key tea.KeyMsg) (App, tea.Cmd) {
	model, cmd := app.Update(key)
	return model.(App), cmd
}

func TestCommandPaletteEscCloses(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.width = 80
	app.height = 24

	app.commandPalette.Activate()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	app = model.(App)

	if app.commandPalette.Active() {
		t.Fatal("esc should close command palette")
	}
}

func TestCommandPaletteViewWhenActive(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.width = 100
	app.height = 28
	app.updatePanelSizes()
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.selectedAgentID = "agent-1"
	app.conversation.SetAgentID("agent-1")
	app.conversation.SetAwake(true)
	app.conversation.Focus()
	app.commandPalette.SetAgents([]commandpalette.Agent{{Name: "Alpha", Status: "ready"}})

	app.commandPalette.Activate()

	view := app.View()
	if view == "" {
		t.Fatal("view should not be empty when palette is active")
	}
	if !strings.Contains(view, "Commands") {
		t.Fatalf("view should contain 'Commands' header, got:\n%s", view)
	}
	if !strings.Contains(view, "Agents") || !strings.Contains(view, "Alpha") {
		t.Fatalf("view should contain agent section, got:\n%s", view)
	}
	if !strings.Contains(view, "█") {
		t.Fatalf("view should contain scrollbar, got:\n%s", view)
	}
	if strings.Contains(view, "\x1b[48;5;235m") {
		t.Fatalf("command palette should not render a full-screen curtain, got:\n%s", view)
	}
	paletteIndex := strings.Index(view, "Commands")
	inputIndex := strings.Index(view, "Type a message")
	if paletteIndex == -1 || inputIndex == -1 {
		t.Fatalf("view should contain inline palette and message input, got:\n%s", view)
	}
	if paletteIndex > inputIndex {
		t.Fatalf("command palette should render above message input, got:\n%s", view)
	}
}

func TestCommandPaletteTypingUsesChatInput(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.width = 100
	app.height = 28
	app.updatePanelSizes()
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.selectedAgentID = "agent-1"
	app.conversation.SetAgentID("agent-1")
	app.conversation.SetAwake(true)
	app.conversation.Focus()

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	app = model.(App)
	_ = cmd
	if got := app.conversation.TextInput().Value(); got != "/" {
		t.Fatalf("chat input after slash = %q, want /", got)
	}

	model, cmd = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("rename")})
	app = model.(App)
	_ = cmd
	if got := app.conversation.TextInput().Value(); got != "/rename" {
		t.Fatalf("chat input after command typing = %q, want /rename", got)
	}
	view := app.View()
	if strings.Contains(view, "type a command") {
		t.Fatalf("command box should not render its own input, got:\n%s", view)
	}
}

func TestBareLetterCommandTypesInChatInput(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.width = 100
	app.height = 28
	app.updatePanelSizes()
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.selectedAgentID = "agent-1"
	app.conversation.SetAgentID("agent-1")
	app.conversation.SetAwake(true)
	app.conversation.Focus()

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	app = model.(App)
	_ = cmd

	if got := app.conversation.TextInput().Value(); got != "p" {
		t.Fatalf("chat input after bare p = %q, want p", got)
	}
	if app.notice != "" {
		t.Fatalf("bare p should not trigger a command notice, got %q", app.notice)
	}
	if app.commandPalette.Active() {
		t.Fatal("bare p should not activate command palette")
	}
}

func TestCommandPaletteBackspaceOnSlashCloses(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.width = 100
	app.height = 28
	app.updatePanelSizes()
	app.focus = FocusConversation
	app.inputMode = "chat"
	app.conversation.Focus()

	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	app = model.(App)
	if !app.commandPalette.Active() {
		t.Fatal("slash key should activate command palette")
	}

	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	app = model.(App)
	if app.commandPalette.Active() {
		t.Fatal("backspace on slash should close command palette")
	}
	if got := app.conversation.TextInput().Value(); got != "" {
		t.Fatalf("chat input after backspace = %q, want empty", got)
	}
}

func TestCommandPaletteDefaultAgentSelectionSkipsCurrentAgent(t *testing.T) {
	first := vault.Agent{ID: "agent-current", Name: "Worker", Task: "first task", Status: "ready"}
	second := vault.Agent{ID: "agent-next", Name: "Worker", Task: "second task", Status: "ready"}

	app := NewApp(nil, nil, nil, nil, nil, nil)
	app.agentList.SetAgents([]vault.Agent{first, second})
	app.agentList.SelectAgentID(first.ID)
	app.selectedAgentID = first.ID
	app.conversation.SetAgentID(first.ID)
	app.syncCommandPaletteAgents()

	app, _ = runSlashCommand(t, app, "agents")
	if !app.agentPickerActive || app.agentPicker == nil {
		t.Fatalf("/agents should open the picker: active=%t picker=%v", app.agentPickerActive, app.agentPicker)
	}

	// The picker lists all agents; down+enter selects the second one.
	var cmd tea.Cmd
	app, _ = sendKey(app, tea.KeyMsg{Type: tea.KeyDown})
	app, cmd = sendKey(app, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		model, _ := app.Update(cmd())
		app = model.(App)
	}

	if app.selectedAgentID != second.ID {
		t.Fatalf("selected agent = %q, want %q", app.selectedAgentID, second.ID)
	}
}

func TestCommandPaletteSelectSwitchesAgentWithSlashPrefix(t *testing.T) {
	db := openTestVault(t)
	first, err := db.CreateAgent("Alpha", "first task", "{}")
	if err != nil {
		t.Fatalf("create first agent: %v", err)
	}
	second, err := db.CreateAgent("Beta", "second task", "{}")
	if err != nil {
		t.Fatalf("create second agent: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.width = 100
	app.height = 30
	app.updatePanelSizes()
	app.agentList.SetAgents([]vault.Agent{*first, *second})
	app.agentList.SelectAgentID(first.ID)
	app.selectedAgentID = first.ID
	app.conversation.SetAgentID(first.ID)
	app.syncCommandPaletteAgents()

	app, _ = runSlashCommand(t, app, "agents")
	if !app.agentPickerActive {
		t.Fatal("/agents should open the picker")
	}

	// Filter to "Beta" so the picker narrows and selects the right agent.
	var cmd tea.Cmd
	for _, r := range "Beta" {
		app, _ = sendKey(app, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	app, cmd = sendKey(app, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		model, _ := app.Update(cmd())
		app = model.(App)
	}

	if app.selectedAgentID != second.ID {
		t.Fatalf("selected agent = %q, want %q", app.selectedAgentID, second.ID)
	}
	if app.conversation.AgentID() != second.ID {
		t.Fatalf("conversation agent = %q, want %q", app.conversation.AgentID(), second.ID)
	}
}

func TestCommandPaletteRenameShowsRenameInput(t *testing.T) {
	db := openTestVault(t)
	agent, err := db.CreateAgent("Alpha", "task", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.width = 100
	app.height = 28
	app.updatePanelSizes()
	app.agentList.SetAgents([]vault.Agent{*agent})
	app.agentList.SelectAgentID(agent.ID)
	app.selectedAgentID = agent.ID
	app.conversation.SetAgentID(agent.ID)
	app.commandPalette.Activate()

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/rename")})
	app = model.(App)
	_ = cmd
	model, cmd = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if cmd == nil {
		t.Fatal("enter should dispatch rename command")
	}
	model, cmd = app.Update(cmd())
	app = model.(App)
	_ = cmd

	if app.inputMode != "rename" {
		t.Fatalf("inputMode = %q, want rename", app.inputMode)
	}
	view := app.View()
	if !strings.Contains(view, "RENAME AGENT") {
		t.Fatalf("rename prompt not visible after command palette rename, got:\n%s", view)
	}
	if !strings.Contains(view, "Alpha") {
		t.Fatalf("rename input should be prefilled with current name, got:\n%s", view)
	}

	app.renameInput.SetValue("Bravo")
	model, cmd = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if cmd != nil {
		t.Fatalf("rename submit returned unexpected command: %#v", cmd())
	}

	agentAfter, err := db.GetAgent(agent.ID)
	if err != nil {
		t.Fatalf("get renamed agent: %v", err)
	}
	if agentAfter.Name != "Bravo" {
		t.Fatalf("vault agent name = %q, want Bravo", agentAfter.Name)
	}
	item, ok := app.agentList.Agent(agent.ID)
	if !ok {
		t.Fatal("renamed agent missing from list")
	}
	if item.Name != "Bravo" {
		t.Fatalf("agent list name = %q, want Bravo", item.Name)
	}
	view = app.View()
	if !strings.Contains(view, "Bravo") {
		t.Fatalf("renamed agent name not visible, got:\n%s", view)
	}
}

func TestCommandPaletteDeleteConfirmsWithEnter(t *testing.T) {
	db := openTestVault(t)
	agent, err := db.CreateAgent("Alpha", "task", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	app := NewApp(nil, nil, nil, db, nil, nil)
	app.agentList.SetAgents([]vault.Agent{*agent})
	app.agentList.SelectAgentID(agent.ID)
	app.selectedAgentID = agent.ID

	cmd := app.dispatchCommand("delete", "delete")
	if cmd != nil {
		t.Fatalf("initial delete command returned unexpected command: %#v", cmd())
	}
	if !app.confirmDelete || !app.confirmWithEnter {
		t.Fatal("delete command should arm Enter confirmation")
	}
	if !strings.Contains(app.notice, "Press Enter to delete agent") {
		t.Fatalf("notice = %q, want Enter delete confirmation", app.notice)
	}

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if cmd == nil {
		t.Fatal("Enter should return delete command")
	}
	model, _ = app.Update(cmd())
	app = model.(App)
	if app.confirmDelete || app.confirmWithEnter {
		t.Fatal("delete confirmation should be cleared")
	}
	if _, err := db.GetAgent(agent.ID); err == nil {
		t.Fatal("agent should be deleted after Enter confirmation")
	}
}

func TestCommandPaletteKillAllConfirmsWithEnter(t *testing.T) {
	app := NewApp(nil, nil, nil, nil, nil, nil)

	cmd := app.dispatchCommand("killall", "killall")
	if cmd != nil {
		t.Fatalf("initial killall command returned unexpected command: %#v", cmd())
	}
	if !app.confirmKillAll || !app.confirmWithEnter {
		t.Fatal("killall command should arm Enter confirmation")
	}
	if !strings.Contains(app.notice, "Press Enter to kill all") {
		t.Fatalf("notice = %q, want Enter killall confirmation", app.notice)
	}

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if cmd == nil {
		t.Fatal("Enter should return killall command")
	}
	msg := cmd()
	if notice, ok := msg.(statusNotice); !ok || !strings.Contains(notice.text, "Kill all unavailable") {
		t.Fatalf("killall command returned %#v, want unavailable notice", msg)
	}
	if app.confirmKillAll || app.confirmWithEnter {
		t.Fatal("killall confirmation should be cleared")
	}
}

func TestConfirmDisarmsWhenNoticeExpires(t *testing.T) {
	db := openTestVault(t)
	agent, err := db.CreateAgent("Alpha", "task", "{}")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.width = 100
	app.height = 28
	app.agentList.SetAgents([]vault.Agent{*agent})
	app.agentList.SelectAgentID(agent.ID)
	app.selectedAgentID = agent.ID
	app.conversation.SetAgentID(agent.ID)

	cmd := app.dispatchCommand("delete", "delete")
	_ = cmd
	if !app.confirmDelete || !app.confirmWithEnter {
		t.Fatal("delete should arm Enter confirmation")
	}
	if app.noticeTTL == 0 {
		t.Fatal("notice TTL should be set while confirmation is armed")
	}

	ttl := app.noticeTTL
	for i := 0; i < ttl; i++ {
		model, _ := app.Update(shared.TickMsg{})
		app = model.(App)
	}

	if app.notice != "" {
		t.Errorf("notice = %q, want cleared after TTL", app.notice)
	}
	if app.confirmDelete || app.confirmWithEnter {
		t.Fatal("delete confirmation should be disarmed once the notice expires")
	}

	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if cmd != nil {
		t.Fatalf("Enter after disarmed confirmation returned command: %#v", cmd())
	}
	if _, err := db.GetAgent(agent.ID); err != nil {
		t.Fatalf("agent should still exist after Enter on disarmed confirmation: %v", err)
	}
}

func TestAllPaletteCommandsHaveSafeAppDispatch(t *testing.T) {
	commands := []string{
		"new",
		"rename",
		"delete",
		"pause",
		"resume",
		"copy",
		"pauseall",
		"resumeall",
		"killall",
		"view",
		"hide",
		"log",
		"trace",
		"resize",
		"settings",
		"config",
		"agents",
		"help",
		"quit",
	}

	for _, name := range commands {
		t.Run(name, func(t *testing.T) {
			db := openTestVault(t)
			agent, err := db.CreateAgent("Alpha", "task", "{}")
			if err != nil {
				t.Fatalf("create agent: %v", err)
			}

			app := NewApp(nil, nil, nil, db, nil, nil)
			app.width = 100
			app.height = 28
			app.agentList.SetAgents([]vault.Agent{*agent})
			app.agentList.SelectAgentID(agent.ID)
			app.selectedAgentID = agent.ID
			app.conversation.SetAgentID(agent.ID)

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("dispatchCommand(%q) panicked: %v", name, r)
				}
			}()
			_ = app.dispatchCommand(name, name+" "+agent.ID)
			// Dismiss the agent picker if we opened one.
			if app.agentPickerActive {
				app.agentPickerActive = false
				app.agentPicker = nil
			}
		})
	}
}

func TestInlinePaletteTruncatesToThreeRecentAgents(t *testing.T) {
	db := openTestVault(t)

	created := make([]vault.Agent, 0, 5)
	for i := 0; i < 5; i++ {
		agent, err := db.CreateAgent(fmt.Sprintf("Agent%d", i), "task", "{}")
		if err != nil {
			t.Fatalf("create agent %d: %v", i, err)
		}
		created = append(created, *agent)
	}

	now := time.Now().Truncate(time.Second)
	if err := db.MarkAgentSelected(created[2].ID, now.Add(-2*time.Minute)); err != nil {
		t.Fatalf("mark selected: %v", err)
	}
	if err := db.MarkAgentSelected(created[4].ID, now.Add(-1*time.Minute)); err != nil {
		t.Fatalf("mark selected: %v", err)
	}
	if err := db.MarkAgentSelected(created[0].ID, now); err != nil {
		t.Fatalf("mark selected: %v", err)
	}

	loaded, err := db.ListAgents()
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.agentList.SetAgents(loaded)
	app.syncCommandPaletteAgents()

	agents := app.commandPalette.Agents()
	if len(agents) != 3 {
		t.Fatalf("inline palette agents = %d, want 3: %+v", len(agents), agents)
	}
	wantOrder := []string{created[0].ID, created[4].ID, created[2].ID}
	for i, want := range wantOrder {
		if agents[i].ID != want {
			t.Errorf("palette agent[%d] = %q, want %q", i, agents[i].ID, want)
		}
	}
}

func TestAgentsSlashCommandOpensPicker(t *testing.T) {
	db := openTestVault(t)
	alpha, err := db.CreateAgent("Alpha", "first", "{}")
	if err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	beta, err := db.CreateAgent("Beta", "second", "{}")
	if err != nil {
		t.Fatalf("create beta: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.width = 100
	app.height = 30
	app.updatePanelSizes()
	app.agentList.SetAgents([]vault.Agent{*alpha, *beta})
	app.agentList.SelectAgentID(alpha.ID)
	app.selectedAgentID = alpha.ID
	app.conversation.SetAgentID(alpha.ID)
	app.syncCommandPaletteAgents()

	app, _ = runSlashCommand(t, app, "agents")

	if !app.agentPickerActive || app.agentPicker == nil {
		t.Fatalf("/agents should open the picker: active=%t picker=%v", app.agentPickerActive, app.agentPicker)
	}
	if app.focus != FocusConversation {
		t.Errorf("focus after /agents = %d, want FocusConversation", app.focus)
	}
	if app.focus == FocusSettings {
		t.Error("focus should not have moved to settings when picker opens")
	}
}

func TestAgentPickerSelectionPersistsLastSelected(t *testing.T) {
	db := openTestVault(t)
	first, err := db.CreateAgent("First", "first", "{}")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := db.CreateAgent("Second", "second", "{}")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.width = 100
	app.height = 30
	app.updatePanelSizes()
	app.agentList.SetAgents([]vault.Agent{*first, *second})
	app.agentList.SelectAgentID(first.ID)
	app.selectedAgentID = first.ID
	app.conversation.SetAgentID(first.ID)
	app.syncCommandPaletteAgents()

	app, _ = runSlashCommand(t, app, "agents")

	for _, r := range "Second" {
		app, _ = sendKey(app, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	var cmd tea.Cmd
	app, cmd = sendKey(app, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		model, _ := app.Update(cmd())
		app = model.(App)
	}

	if app.selectedAgentID != second.ID {
		t.Fatalf("selected agent = %q, want %q", app.selectedAgentID, second.ID)
	}

	loaded, err := db.GetAgent(second.ID)
	if err != nil {
		t.Fatalf("get second: %v", err)
	}
	if loaded.LastSelectedAt.IsZero() {
		t.Fatal("second agent last_selected_at should be set after picker pick")
	}
	if time.Since(loaded.LastSelectedAt) > time.Minute {
		t.Fatalf("last_selected_at too old: %s", loaded.LastSelectedAt)
	}
}

func TestMouseClickOnAgentListSelectsAgent(t *testing.T) {
	db := openTestVault(t)
	first, err := db.CreateAgent("Alpha", "first", "{}")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := db.CreateAgent("Beta", "second", "{}")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	app := NewApp(nil, nil, nil, db, nil, nil)
	app.width = 100
	app.height = 30
	app.updatePanelSizes()
	app.agentList.SetAgents([]vault.Agent{*first, *second})
	app.agentList.SelectAgentID(first.ID)
	app.selectedAgentID = first.ID
	app.conversation.SetAgentID(first.ID)
	app.syncCommandPaletteAgents()

	// Compute the agent list panel rect the way the MouseMsg handler does.
	x, y, w, h := app.agentListPanelRect()
	if w == 0 || h == 0 {
		t.Fatal("agent list rect should be non-zero in full workbench layout")
	}

	// Click on the second agent row (title is at y+1, first agent at +2,
	// second agent at +3).
	clickY := y + 3
	clickX := x + 1
	model, _ := app.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      clickX,
		Y:      clickY,
	})
	app = model.(App)

	if app.selectedAgentID != second.ID {
		t.Fatalf("selected agent = %q, want %q", app.selectedAgentID, second.ID)
	}
	if app.conversation.AgentID() != second.ID {
		t.Errorf("conversation agent = %q, want %q", app.conversation.AgentID(), second.ID)
	}
}
