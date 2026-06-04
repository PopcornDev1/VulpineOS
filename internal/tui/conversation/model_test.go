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

func TestRenderMarkdownFormatsHeadings(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	lines := renderMarkdown("## Subheading", 80)
	if len(lines) != 1 {
		t.Fatalf("lines = %#v, want one heading line", lines)
	}
	if strings.Contains(lines[0], "##") {
		t.Fatalf("heading marker leaked into rendered text: %q", lines[0])
	}
	if !strings.Contains(lines[0], "Subheading") {
		t.Fatalf("heading text missing from rendered line: %q", lines[0])
	}
	if !strings.Contains(lines[0], "\x1b[1") {
		t.Fatalf("heading should render bold ANSI style: %q", lines[0])
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
