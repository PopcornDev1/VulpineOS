package conversation

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func teaKey(key string) tea.KeyMsg {
	switch key {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

func TestUpdateLastAssistant(t *testing.T) {
	m := Model{}
	m.width = 80
	m.AddEntry("user", "hello")
	m.AddEntry("assistant", "")

	// First streaming update
	m.UpdateLastAssistant("Hel", true)
	if len(m.entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(m.entries))
	}
	if m.entries[1].Content != "Hel" {
		t.Fatalf("expected 'Hel', got %q", m.entries[1].Content)
	}
	if !m.entries[1].StreamActive {
		t.Fatal("expected StreamActive=true")
	}

	// Second streaming update
	m.UpdateLastAssistant("Hello", true)
	if m.entries[1].Content != "Hello" {
		t.Fatalf("expected 'Hello', got %q", m.entries[1].Content)
	}

	// Final flush
	m.UpdateLastAssistant("Hello world!", false)
	if m.entries[1].Content != "Hello world!" {
		t.Fatalf("expected 'Hello world!', got %q", m.entries[1].Content)
	}
	if m.entries[1].StreamActive {
		t.Fatal("expected StreamActive=false on final")
	}
	if m.entries[1].Role != "assistant" {
		t.Fatalf("expected role=assistant, got %q", m.entries[1].Role)
	}
}

func TestTraceOnlyFiltersToSystemMessages(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.SetAgentID("agent-1")
	m.SetAwake(true)
	m.AddEntry("user", "hello")
	m.AddEntry("assistant", "working on it")
	m.AddEntry("system", "Running browser action: open https://example.com")
	m.AddEntry("system", "Tool failed: browser — gateway token mismatch")
	m.SetTraceOnly(true)

	view := m.View()
	if !strings.Contains(view, "ACTION TRACE") {
		t.Fatalf("expected action trace title, got:\n%s", view)
	}
	if strings.Contains(view, "hello") {
		t.Fatalf("trace view should not include user messages, got:\n%s", view)
	}
	if strings.Contains(view, "working on it") {
		t.Fatalf("trace view should not include assistant messages, got:\n%s", view)
	}
	if !strings.Contains(view, "Running browser action: open https://example.com") {
		t.Fatalf("trace view missing tool start, got:\n%s", view)
	}
	if !strings.Contains(view, "Tool failed: browser") {
		t.Fatalf("trace view missing tool result, got:\n%s", view)
	}
}

func TestTraceOnlyShowsPlaceholderWhenEmpty(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.SetAgentID("agent-1")
	m.SetAwake(true)
	m.AddEntry("assistant", "ready")
	m.SetTraceOnly(true)

	view := m.View()
	if !strings.Contains(view, "No action trace yet.") {
		t.Fatalf("expected empty trace placeholder, got:\n%s", view)
	}
}

func TestNoAgentViewDoesNotShowCreateInstructions(t *testing.T) {
	m := New()
	m.SetSize(80, 20)

	view := m.View()
	if strings.Contains(view, "to create a new agent") || strings.Contains(view, "arrow keys") {
		t.Fatalf("no-agent view still shows legacy create instructions:\n%s", view)
	}
}

func TestInputBlockUsesOpenCodeRailBox(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")

	m := New()
	m.SetSize(60, 14)
	m.SetAgentID("agent-1")
	m.SetAgentName("Agent 1")
	m.SetModelLabel("opencode/claude-sonnet")
	m.SetAwake(true)

	view := m.View()
	for _, border := range []string{"╭", "╮", "╰", "╯"} {
		if strings.Contains(view, border) {
			t.Fatalf("chat input should not use rounded border %q, got:\n%s", border, view)
		}
	}
	if !strings.Contains(view, "▀") {
		t.Fatalf("chat input should use half-block bottom border, got:\n%s", view)
	}
	if strings.Contains(view, "▌") {
		t.Fatalf("chat input rail should be a solid styled cell, not a stacked block glyph:\n%s", view)
	}
	if !strings.Contains(view, "Agent 1") || !strings.Contains(view, "opencode/claude-sonnet") {
		t.Fatalf("chat input should keep agent/model metadata, got:\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 60 {
			t.Fatalf("line %d width = %d, want <= 60:\n%s", i+1, width, view)
		}
	}
}

func TestInputBlockUsesOriginalThinRailOutsideAppleTerminal(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")

	m := New()
	m.SetSize(60, 14)
	m.SetAgentID("agent-1")
	m.SetAgentName("Agent 1")
	m.SetModelLabel("opencode/claude-sonnet")
	m.SetAwake(true)

	view := m.View()
	if !strings.Contains(view, "▌") {
		t.Fatalf("non-Apple terminal should use Nick's original thin rail glyph, got:\n%s", view)
	}
	if !strings.Contains(view, "Agent 1") || !strings.Contains(view, "opencode/claude-sonnet") {
		t.Fatalf("chat input should keep agent/model metadata, got:\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 60 {
			t.Fatalf("line %d width = %d, want <= 60:\n%s", i+1, width, view)
		}
	}
}

func TestViewLeavesBlankLineAboveInputBlock(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")

	m := New()
	m.SetSize(60, 14)
	m.SetAgentID("agent-1")
	m.SetAgentName("Agent 1")
	m.SetAwake(true)
	m.AddEntry("assistant", "latest response")

	view := m.View()
	lines := strings.Split(view, "\n")
	if got := len(lines); got != 14 {
		t.Fatalf("view height = %d, want 14:\n%s", got, view)
	}

	placeholderLine := -1
	for i, line := range lines {
		if strings.Contains(line, "Type a message...") {
			placeholderLine = i
			break
		}
	}
	if placeholderLine < 2 {
		t.Fatalf("could not locate input placeholder with enough leading rows:\n%s", view)
	}
	if gap := lines[placeholderLine-2]; strings.TrimSpace(gap) != "" {
		t.Fatalf("line above input block = %q, want blank breathing row:\n%s", gap, view)
	}
	if topRail := lines[placeholderLine-1]; !strings.Contains(topRail, "▌") {
		t.Fatalf("expected input block top rail before placeholder, got %q:\n%s", topRail, view)
	}
}

func TestThinkingIndicatorLeavesBlankLineAfterMessage(t *testing.T) {
	m := New()
	m.SetSize(60, 14)
	m.SetAgentID("agent-1")
	m.SetAwake(true)
	m.AddEntry("user", "please check")
	m.SetThinking(true)

	lines := strings.Split(stripANSI(m.View()), "\n")
	messageLine := -1
	for i, line := range lines {
		if strings.Contains(line, "please check") {
			messageLine = i
			break
		}
	}
	if messageLine < 0 {
		t.Fatalf("view missing sent message:\n%s", strings.Join(lines, "\n"))
	}
	if messageLine+2 >= len(lines) {
		t.Fatalf("view missing rows after sent message:\n%s", strings.Join(lines, "\n"))
	}
	if strings.TrimSpace(lines[messageLine+1]) != "" {
		t.Fatalf("line after sent message = %q, want blank gap:\n%s", lines[messageLine+1], strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[messageLine+2], "...") {
		t.Fatalf("loading indicator should follow blank gap, got %q:\n%s", lines[messageLine+2], strings.Join(lines, "\n"))
	}
}

func TestInputBlockSoftWrapsLongDraft(t *testing.T) {
	m := New()
	m.SetSize(46, 18)
	m.SetAgentID("agent-1")
	m.SetAgentName("Agent 1")
	m.SetModelLabel("model")
	m.SetAwake(true)
	m.Focus()
	m.TextInput().SetValue("alpha beta gamma delta epsilon zeta eta theta iota kappa")

	block := m.inputBlock(m.inputArea())
	if got := lipgloss.Height(block); got < 6 {
		t.Fatalf("input block height = %d, want growth for wrapped draft:\n%s", got, block)
	}
	view := m.View()
	for _, want := range []string{"alpha beta", "theta iota"} {
		if !strings.Contains(view, want) {
			t.Fatalf("wrapped draft missing %q:\n%s", want, view)
		}
	}
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 46 {
			t.Fatalf("line %d width = %d, want <= 46:\n%s", i+1, width, view)
		}
	}
}

func TestInputPulseTickTogglesCustomCaretWithoutChangingWidth(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	m := New()
	m.SetSize(46, 18)
	m.SetAgentID("agent-1")
	m.SetAgentName("Agent 1")
	m.SetModelLabel("model")
	m.SetAwake(true)
	m.Focus()
	m.TextInput().SetValue("draft")

	before := m.inputTextView()
	updated, cmd := m.Update(InputPulseTickMsg{})
	after := updated.inputTextView()

	if cmd == nil {
		t.Fatal("input pulse should re-arm itself")
	}
	if before == after {
		t.Fatalf("input pulse should toggle the custom caret render, got unchanged view %q", before)
	}
	if got, want := lipgloss.Width(after), lipgloss.Width(before); got != want {
		t.Fatalf("input pulse changed visual width: got %d want %d\nbefore: %q\nafter:  %q", got, want, before, after)
	}
}

func TestInputBlockCapsWrappedDraftHeight(t *testing.T) {
	m := New()
	m.SetSize(50, 20)
	m.SetAgentID("agent-1")
	m.SetAwake(true)
	m.Focus()
	m.TextInput().SetValue(strings.Repeat("wrapped words ", 20))

	block := m.inputBlock(m.inputArea())
	wantHeight := inputBlockChromeLines + maxDraftInputLines
	if got := lipgloss.Height(block); got != wantHeight {
		t.Fatalf("input block height = %d, want capped height %d:\n%s", got, wantHeight, block)
	}
}

func TestInputBlockUsesCompactDraftHeightOnShortTerminals(t *testing.T) {
	m := New()
	m.SetSize(50, 10)
	m.SetAgentID("agent-1")
	m.SetAwake(true)
	m.Focus()
	m.TextInput().SetValue(strings.Repeat("wrapped words ", 20))

	block := m.inputBlock(m.inputArea())
	wantHeight := inputBlockChromeLines + compactDraftInputMax
	if got := lipgloss.Height(block); got != wantHeight {
		t.Fatalf("compact input block height = %d, want %d:\n%s", got, wantHeight, block)
	}
}

func TestSetSizeRewrapsRenderedEntries(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.SetAgentID("agent-1")
	m.AddEntry("assistant", strings.Repeat("wrapped words ", 12))

	wideLineCount := len(m.entries[0].renderedLines)
	m.SetSize(24, 20)

	narrowLineCount := len(m.entries[0].renderedLines)
	if narrowLineCount <= wideLineCount {
		t.Fatalf("narrow line count = %d, want more than wide count %d", narrowLineCount, wideLineCount)
	}
	for _, line := range m.entries[0].renderedLines {
		if got := ansiVisualWidth(line); got > 16 {
			t.Fatalf("line width = %d, want <= 16 after resize: %q", got, line)
		}
	}
}

func TestSetSizeAllowsVeryNarrowContentWidth(t *testing.T) {
	m := New()
	m.SetSize(6, 10)
	m.SetAgentID("agent-1")
	m.AddEntry("assistant", "abcdef")

	if m.textInput.Width > 2 {
		t.Fatalf("text input width = %d, want <= 2", m.textInput.Width)
	}
	for _, line := range m.entries[0].renderedLines {
		if got := ansiVisualWidth(line); got > 1 {
			t.Fatalf("line width = %d, want <= 1 in narrow view: %q", got, line)
		}
	}
}

func TestSetAgentIDClearsDraftInput(t *testing.T) {
	m := New()
	m.SetAgentID("agent-1")
	m.textInput.SetValue("draft for first agent")

	m.SetAgentID("agent-2")

	if got := m.textInput.Value(); got != "" {
		t.Fatalf("draft after agent switch = %q, want empty", got)
	}
}

func TestInsertPastedContentUsesMarkerButKeepsPayload(t *testing.T) {
	m := New()
	m.SetAgentID("agent-1")
	raw := "first line\nsecond line with enough content"

	m.InsertPastedContent(raw)

	if got := m.textInput.Value(); got != "[Pasted Content 42 Chars]" {
		t.Fatalf("visible input = %q, want paste marker", got)
	}
	payload, display := m.InputPayloadAndDisplay()
	if payload != raw {
		t.Fatalf("payload = %q, want raw paste", payload)
	}
	if display != "[Pasted Content 42 Chars]" {
		t.Fatalf("display = %q, want paste marker", display)
	}
}

func TestDeletedPasteMarkerDoesNotSendHiddenPayload(t *testing.T) {
	m := New()
	m.SetAgentID("agent-1")
	m.InsertPastedContent("secret pasted text")
	m.textInput.SetValue("manual replacement")

	payload, display := m.InputPayloadAndDisplay()
	if payload != "manual replacement" || display != "manual replacement" {
		t.Fatalf("payload/display = %q/%q, want manual replacement only", payload, display)
	}
}

func TestAddEntryWithDisplayRendersMarkerNotRawPaste(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.SetAgentID("agent-1")
	m.AddEntryWithDisplay("user", "raw pasted secret", "[Pasted Content 17 Chars]")

	view := m.View()
	if strings.Contains(view, "raw pasted secret") {
		t.Fatalf("view leaked raw paste:\n%s", view)
	}
	if !strings.Contains(view, "[Pasted Content 17 Chars]") {
		t.Fatalf("view missing paste marker:\n%s", view)
	}
}

func TestVisibleRowSelectionCopiesPlainRenderedText(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	m := New()
	m.SetSize(80, 16)
	m.SetAgentID("agent-1")
	m.SetAwake(true)
	m.AddEntry("assistant", "alpha **bold**")
	m.AddEntry("assistant", "beta line")

	view := m.View()
	lines := strings.Split(view, "\n")
	start := -1
	end := -1
	for i, line := range lines {
		switch {
		case strings.Contains(line, "alpha"):
			start = i
		case strings.Contains(line, "beta line"):
			end = i
		}
	}
	if start < 0 || end < 0 {
		t.Fatalf("could not locate selectable rows:\n%s", view)
	}

	startLine := stripANSI(lines[start])
	endLine := stripANSI(lines[end])
	startCol := cellIndexOf(startLine, "alpha")
	endCol := cellIndexOf(endLine, "line") + ansiVisualWidth("line")
	if startCol < 0 || endCol < ansiVisualWidth("line") {
		t.Fatalf("could not locate selection columns in %q / %q", startLine, endLine)
	}

	if !m.BeginSelectionAtViewCell(start, startCol) {
		t.Fatalf("begin selection row %d failed:\n%s", start, view)
	}
	if !m.ExtendSelectionAtViewCell(end, endCol) {
		t.Fatalf("extend selection row %d failed:\n%s", end, view)
	}

	copied := m.SelectedText()
	if !strings.Contains(copied, "alpha bold") || !strings.Contains(copied, "beta line") {
		t.Fatalf("copied text = %q, want selected rendered rows", copied)
	}
	if strings.Contains(copied, "\x1b[") {
		t.Fatalf("copied text should not contain ANSI escapes: %q", copied)
	}
	if !m.HasSelection() {
		t.Fatal("selection should remain active after selecting rows")
	}
	selectedView := m.View()
	if selectedView == view {
		t.Fatalf("view did not change after selection:\n%s", selectedView)
	}
}

func TestVisibleCellSelectionCopiesPartialLineOnly(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	m := New()
	m.SetSize(80, 16)
	m.SetAgentID("agent-1")
	m.SetAwake(true)
	m.AddEntry("assistant", "prefix target suffix")

	view := m.View()
	lines := strings.Split(view, "\n")
	row := -1
	for i, line := range lines {
		if strings.Contains(line, "prefix target suffix") {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatalf("could not locate selectable row:\n%s", view)
	}
	plain := stripANSI(lines[row])
	startCol := cellIndexOf(plain, "target")
	endCol := startCol + ansiVisualWidth("target")
	if startCol < 0 {
		t.Fatalf("could not locate target in %q", plain)
	}

	if !m.BeginSelectionAtViewCell(row, startCol) {
		t.Fatalf("begin selection row %d failed:\n%s", row, view)
	}
	if !m.ExtendSelectionAtViewCell(row, endCol) {
		t.Fatalf("extend selection row %d failed:\n%s", row, view)
	}

	if got := m.SelectedText(); got != "target" {
		t.Fatalf("selected text = %q, want target", got)
	}
	selectedView := m.View()
	if strings.Contains(selectedView, selectedLineStyle.Render(plain)) {
		t.Fatalf("selection highlighted the full line, want partial selection:\n%s", selectedView)
	}
}

func TestVisibleClickDoesNotCreateSelection(t *testing.T) {
	m := New()
	m.SetSize(80, 16)
	m.SetAgentID("agent-1")
	m.SetAwake(true)
	m.AddEntry("assistant", "click only")

	view := m.View()
	lines := strings.Split(view, "\n")
	row := -1
	for i, line := range lines {
		if strings.Contains(line, "click only") {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatalf("could not locate selectable row:\n%s", view)
	}

	if !m.BeginSelectionAtViewCell(row, 2) {
		t.Fatalf("begin selection row %d failed:\n%s", row, view)
	}
	m.EndSelection()

	if m.HasSelection() {
		t.Fatal("click without drag should not leave a selection")
	}
	if got := m.SelectedText(); got != "" {
		t.Fatalf("selected text = %q, want empty after click without drag", got)
	}
}

func TestForceScrollToBottomOverridesManualScroll(t *testing.T) {
	m := New()
	m.SetSize(80, 6)
	m.SetAgentID("agent-1")
	for i := 0; i < 12; i++ {
		m.AddEntry("assistant", "line")
	}

	updated, _ := m.Update(teaKey("up"))
	m = updated
	if m.autoScroll {
		t.Fatal("manual scroll should disable auto-scroll")
	}

	m.AddEntry("assistant", "latest")
	if m.autoScroll {
		t.Fatal("AddEntry should respect manual scroll state before force")
	}

	m.ForceScrollToBottom()
	if !m.autoScroll {
		t.Fatal("ForceScrollToBottom should re-enable auto-scroll")
	}
	if m.scroll == 0 {
		t.Fatal("ForceScrollToBottom should move to the latest entries")
	}
}

func TestWordWrapUsesTerminalCellWidth(t *testing.T) {
	lines := wordWrap("界界界界界", 4)
	if len(lines) < 2 {
		t.Fatalf("wide glyph text did not wrap: %#v", lines)
	}
	for _, line := range lines {
		if got := lipgloss.Width(line); got > 4 {
			t.Fatalf("line cell width = %d, want <= 4: %q in %#v", got, line, lines)
		}
	}
}

func TestRenderMarkdownDoesNotSplitStyledANSI(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	lines := renderMarkdown("**"+strings.Repeat("bold ", 8)+"**", 12)
	for _, line := range lines {
		if hasUnclosedSGR(line) {
			t.Fatalf("line has unclosed ANSI style: %q\nall lines: %#v", line, lines)
		}
	}
}

func TestRenderMarkdownLeavesHeadingsPlain(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	lines := renderMarkdown("## Subheading", 80)
	if len(lines) != 1 {
		t.Fatalf("lines = %#v, want one plain line", lines)
	}
	if !strings.Contains(lines[0], "## Subheading") {
		t.Fatalf("heading marker should remain plain text: %q", lines[0])
	}
	if strings.Contains(lines[0], "\x1b[1") {
		t.Fatalf("heading should not render as a styled subheading: %q", lines[0])
	}
}

func TestRenderMarkdownFormatsInlineEmphasis(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	lines := renderMarkdown("This is **bold** and *italic*.", 80)
	if got := strings.Join(lines, "\n"); strings.Contains(got, "**") || strings.Contains(got, "*italic*") {
		t.Fatalf("markdown emphasis markers leaked into rendered text: %q", got)
	}
	if got := strings.Join(lines, "\n"); !strings.Contains(got, "\x1b[1m") || !strings.Contains(got, "\x1b[3m") {
		t.Fatalf("emphasis should render bold and italic ANSI styles: %q", got)
	}
}

func TestRenderMarkdownFormatsBullets(t *testing.T) {
	lines := renderMarkdown("- first item\n- second item", 80)
	if len(lines) != 2 {
		t.Fatalf("lines = %#v, want two bullet lines", lines)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "  • ") {
			t.Fatalf("bullet line = %q, want indented bullet prefix", line)
		}
		if strings.Contains(line, "- ") {
			t.Fatalf("dash marker leaked into bullet line: %q", line)
		}
	}
}

func TestRenderMarkdownFormatsTaskLists(t *testing.T) {
	lines := renderMarkdown("- [x] done\n- [ ] todo", 80)
	if len(lines) != 2 {
		t.Fatalf("lines = %#v, want two task lines", lines)
	}
	if !strings.Contains(lines[0], "☑ done") || !strings.Contains(lines[1], "☐ todo") {
		t.Fatalf("task list did not render checkbox glyphs: %#v", lines)
	}
	if strings.Contains(strings.Join(lines, "\n"), "[x]") || strings.Contains(strings.Join(lines, "\n"), "[ ]") {
		t.Fatalf("task list markers leaked into rendered lines: %#v", lines)
	}
}

func TestRenderMarkdownFormatsNumberedLists(t *testing.T) {
	lines := renderMarkdown("1. first\n2. second", 80)
	if len(lines) != 2 {
		t.Fatalf("lines = %#v, want two numbered lines", lines)
	}
	if !strings.HasPrefix(lines[0], "  1. ") || !strings.HasPrefix(lines[1], "  2. ") {
		t.Fatalf("numbered lines should use aligned prefixes: %#v", lines)
	}
}

func TestRenderMarkdownFormatsTables(t *testing.T) {
	lines := renderMarkdown("| Name | Status |\n| --- | --- |\n| Build | Pass |\n| CI | Running |", 80)
	if len(lines) != 4 {
		t.Fatalf("lines = %#v, want four rendered table lines", lines)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Name", "Status", "Build", "Pass", "CI", "Running"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("table missing %q in rendered lines: %#v", want, lines)
		}
	}
	if strings.Contains(joined, "| --- | --- |") || strings.Contains(joined, "| Build | Pass |") {
		t.Fatalf("raw table syntax leaked into rendered lines: %#v", lines)
	}
	if !strings.Contains(joined, "─") {
		t.Fatalf("table should include a terminal separator line: %#v", lines)
	}
}

func TestRenderMarkdownFormatsFencedCodeBlocks(t *testing.T) {
	lines := renderMarkdown("```go\nfmt.Println(\"ok\")\n```", 80)
	if len(lines) != 1 {
		t.Fatalf("lines = %#v, want one rendered code line", lines)
	}
	if !strings.Contains(lines[0], "fmt.Println(\"ok\")") {
		t.Fatalf("code line missing content: %#v", lines)
	}
	if strings.Contains(strings.Join(lines, "\n"), "```") {
		t.Fatalf("code fence markers leaked into rendered lines: %#v", lines)
	}
	if !strings.HasPrefix(lines[0], "  │ ") {
		t.Fatalf("code line should use terminal code-block prefix: %#v", lines)
	}
}

func hasUnclosedSGR(line string) bool {
	active := false
	for i := 0; i < len(line); i++ {
		if line[i] != '\x1b' || i+1 >= len(line) || line[i+1] != '[' {
			continue
		}
		end := i + 2
		for end < len(line) && (line[end] < 0x40 || line[end] > 0x7e) {
			end++
		}
		if end >= len(line) {
			return true
		}
		seq := line[i : end+1]
		if strings.HasSuffix(seq, "m") {
			active = seq != "\x1b[0m" && seq != "\x1b[m"
		}
		i = end
	}
	return active
}

func cellIndexOf(s, sub string) int {
	idx := strings.Index(s, sub)
	if idx < 0 {
		return -1
	}
	return ansiVisualWidth(s[:idx])
}
