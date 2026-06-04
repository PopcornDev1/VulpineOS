package conversation

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"vulpineos/internal/tui/shared"
	"vulpineos/internal/vault"
)

// Thinking phrases — rotates like Claude Code
var thinkingPhrases = []string{
	"Thinking",
	"Reasoning",
	"Pondering",
	"Analyzing",
	"Processing",
	"Considering",
	"Evaluating",
	"Reflecting",
	"Working",
	"Crafting response",
	"Connecting dots",
	"Deep in thought",
}

var wakingPhrases = []string{
	"Waking up",
	"Initializing",
	"Coming online",
	"Booting up",
	"Spinning up",
	"Getting ready",
}

// Spinner frames for the animated icon
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Input block (OpenCode-style chat box) styling.
var (
	inputBlockBg     = shared.ColorBg
	inputStatusStyle = lipgloss.NewStyle().
				Foreground(shared.ColorPrimary).
				Background(inputBlockBg).
				Bold(true)
	inputModelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB")).Background(inputBlockBg)
	inputMutedStyle  = lipgloss.NewStyle().Foreground(shared.ColorMuted).Background(inputBlockBg)
	inputCursorStyle = lipgloss.NewStyle().
				Foreground(inputBlockBg).
				Background(lipgloss.Color("#E5E7EB"))
)

// inputBlockHeight is the fixed number of rendered lines the chat input block occupies.
const inputBlockHeight = 5

// ThinkingTickMsg triggers animation updates for the thinking indicator.
type ThinkingTickMsg struct{}

// InputPulseTickMsg advances the chat input caret pulse.
type InputPulseTickMsg struct{}

// ThinkingTick returns a command that ticks every 120ms for animation.
func ThinkingTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return ThinkingTickMsg{}
	})
}

// InputPulseTick keeps the chat input caret blinking while the TUI is open.
func InputPulseTick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return InputPulseTickMsg{}
	})
}

// SetModelLabel sets the provider/model string shown in the input block metadata.
func (m *Model) SetModelLabel(label string) {
	m.modelLabel = label
}

// SetNotice stores a transient message rendered just under the chat input.
func (m *Model) SetNotice(text string) {
	m.notice = text
}

// Entry is a single message displayed in the conversation.
type Entry struct {
	Role           string
	Content        string
	DisplayContent string
	renderedLines  []string // pre-rendered markdown lines (set on add)
	StreamActive   bool
}

type pasteSnippet struct {
	Marker string
	Raw    string
}

// Model holds the conversation panel state.
type Model struct {
	entries         []Entry
	agentID         string
	agentName       string
	agentStatus     string
	traceOnly       bool
	thinking        bool // true while waiting for agent response
	awake           bool // true after agent has sent its first message
	textInput       textinput.Model
	width           int
	height          int
	scroll          int  // scroll offset in rendered lines (not entries)
	autoScroll      bool // whether to auto-scroll to bottom on new messages
	spinnerFrame    int  // current spinner animation frame
	phraseIdx       int  // current thinking phrase index
	shimmerOffset   int  // shimmer position for the gradient effect
	phraseTicks     int  // ticks since last phrase change
	pasteSnippets   []pasteSnippet
	modelLabel      string // provider/model shown in the input block metadata
	notice          string // transient message rendered just under the chatbox
	inputPulseFrame int    // caret blink frame for the chat input
}

// SetAgentName sets the display name for the agent.
func (m *Model) SetAgentName(name string) {
	m.agentName = name
}

func (m *Model) SetAgentStatus(status string) {
	m.agentStatus = status
}

// SetTraceOnly switches the panel between mixed conversation and action-trace mode.
func (m *Model) SetTraceOnly(enabled bool) {
	m.traceOnly = enabled
	m.scrollToBottom()
}

// TraceOnly reports whether the panel is showing action-trace mode.
func (m Model) TraceOnly() bool {
	return m.traceOnly
}

// SetAwake marks the agent as having sent its first message.
func (m *Model) SetAwake(awake bool) {
	m.awake = awake
}

// IsAwake returns true if the agent has sent its first message.
func (m Model) IsAwake() bool {
	return m.awake
}

// SetThinking sets the thinking indicator and resets animation state.
func (m *Model) SetThinking(thinking bool) {
	m.thinking = thinking
	if thinking {
		m.spinnerFrame = 0
		m.shimmerOffset = 0
		m.phraseTicks = 0
		if m.awake {
			m.phraseIdx = rand.Intn(len(thinkingPhrases))
		} else {
			m.phraseIdx = rand.Intn(len(wakingPhrases))
		}
	}
}

// IsThinking returns whether the conversation is waiting on an agent response.
func (m Model) IsThinking() bool {
	return m.thinking
}

// New creates a new conversation panel.
func New() Model {
	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.Width = 60
	return Model{
		textInput:  ti,
		width:      40,
		height:     20,
		autoScroll: true,
	}
}

// SetSize sets the render dimensions.
func (m *Model) SetSize(w, h int) {
	oldContentWidth := m.contentWidth()
	m.width = w
	m.height = h
	m.textInput.Width = w - 4
	if m.textInput.Width < 1 {
		m.textInput.Width = 1
	}
	if newContentWidth := m.contentWidth(); newContentWidth != oldContentWidth {
		m.rewrapEntries(newContentWidth)
	}
	m.clampScroll()
	if m.autoScroll {
		m.scrollToBottom()
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ThinkingTickMsg:
		if m.thinking {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			m.shimmerOffset = (m.shimmerOffset + 1) % 20
			m.phraseTicks++
			// Change phrase every ~25 ticks (~3 seconds)
			if m.phraseTicks >= 25 {
				m.phraseTicks = 0
				if m.awake {
					m.phraseIdx = rand.Intn(len(thinkingPhrases))
				} else {
					m.phraseIdx = rand.Intn(len(wakingPhrases))
				}
			}
			return m, ThinkingTick()
		}
	case InputPulseTickMsg:
		m.inputPulseFrame++
		return m, InputPulseTick()
	case tea.KeyMsg:
		switch msg.String() {
		case "up":
			m.scroll--
			if m.scroll < 0 {
				m.scroll = 0
			}
			m.autoScroll = false
			return m, nil
		case "down":
			m.scroll++
			total := len(m.renderLines())
			maxScroll := total - m.visibleLines()
			if maxScroll < 0 {
				maxScroll = 0
			}
			if m.scroll >= maxScroll {
				m.scroll = maxScroll
				m.autoScroll = true
			}
			return m, nil
		case "pgup":
			m.scroll -= m.visibleLines() / 2
			if m.scroll < 0 {
				m.scroll = 0
			}
			m.autoScroll = false
			return m, nil
		case "pgdown":
			m.scroll += m.visibleLines() / 2
			total := len(m.renderLines())
			maxScroll := total - m.visibleLines()
			if maxScroll < 0 {
				maxScroll = 0
			}
			if m.scroll >= maxScroll {
				m.scroll = maxScroll
				m.autoScroll = true
			}
			return m, nil
		}
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scroll -= 3
			if m.scroll < 0 {
				m.scroll = 0
			}
			m.autoScroll = false
			return m, nil
		case tea.MouseButtonWheelDown:
			m.scroll += 3
			total := len(m.renderLines())
			maxScroll := total - m.visibleLines()
			if maxScroll < 0 {
				maxScroll = 0
			}
			if m.scroll >= maxScroll {
				m.scroll = maxScroll
				m.autoScroll = true
			}
			return m, nil
		}
	}
	return m, nil
}

// SetAgentID sets the current agent and clears entries.
func (m *Model) SetAgentID(id string) {
	m.agentID = id
	m.entries = nil
	m.scroll = 0
	m.autoScroll = true
	m.awake = false
	m.pasteSnippets = nil
	m.textInput.Reset()
}

// AgentID returns the current agent ID.
func (m Model) AgentID() string {
	return m.agentID
}

// LatestAssistantContent returns the Content of the most recent
// entry with Role == "assistant", or "" if none exists.
func (m Model) LatestAssistantContent() string {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Role == "assistant" {
			return m.entries[i].Content
		}
	}
	return ""
}

// IsLastEntryStreaming returns true if the last conversation entry
// is currently receiving streamed content.
func (m Model) IsLastEntryStreaming() bool {
	if len(m.entries) == 0 {
		return false
	}
	return m.entries[len(m.entries)-1].StreamActive
}

// LoadMessages loads conversation history from vault messages.
func (m *Model) LoadMessages(msgs []vault.AgentMessage) {
	maxWidth := m.contentWidth()
	m.entries = make([]Entry, 0, len(msgs))
	m.awake = false
	for _, msg := range msgs {
		m.entries = append(m.entries, Entry{
			Role:           msg.Role,
			Content:        msg.Content,
			DisplayContent: msg.DisplayContent,
			renderedLines:  renderMarkdown(messageDisplayText(msg.Content, msg.DisplayContent), maxWidth),
		})
		if msg.Role == "assistant" {
			m.awake = true
		}
	}
	m.scrollToBottom()
}

// AddEntry adds a new conversation entry.
func (m *Model) AddEntry(role, content string) {
	m.AddEntryWithDisplay(role, content, "")
}

// AddEntryWithDisplay adds a new conversation entry with optional operator-facing display text.
func (m *Model) AddEntryWithDisplay(role, content, displayContent string) {
	maxWidth := m.contentWidth()
	m.entries = append(m.entries, Entry{
		Role:           role,
		Content:        content,
		DisplayContent: displayContent,
		renderedLines:  renderMarkdown(messageDisplayText(content, displayContent), maxWidth),
	})
	if role == "assistant" {
		m.awake = true
	}
	m.scrollToBottom()
}

func (m *Model) UpdateLastAssistant(content string, streamActive bool) {
	if len(m.entries) == 0 {
		m.AddEntryWithDisplay("assistant", content, "")
		return
	}
	last := &m.entries[len(m.entries)-1]
	if last.Role != "assistant" && last.Role != "stream" {
		m.AddEntryWithDisplay("assistant", content, "")
		return
	}
	last.Role = "assistant"
	if streamActive {
		last.Role = "stream"
	}
	last.Content = content
	last.StreamActive = streamActive
	maxWidth := m.contentWidth()
	last.renderedLines = renderMarkdown(messageDisplayText(content, last.DisplayContent), maxWidth)

	if streamActive && m.autoScroll {
		m.scrollToBottom()
	}
}

// ForceScrollToBottom re-enables auto-scroll and moves to the latest entry.
func (m *Model) ForceScrollToBottom() {
	m.autoScroll = true
	m.scrollToBottom()
}

func (m Model) contentWidth() int {
	maxWidth := m.width - 8
	if maxWidth < 1 {
		return 1
	}
	return maxWidth
}

func (m *Model) rewrapEntries(maxWidth int) {
	for i := range m.entries {
		m.entries[i].renderedLines = renderMarkdown(messageDisplayText(m.entries[i].Content, m.entries[i].DisplayContent), maxWidth)
	}
}

// TextInput returns a pointer to the text input for external update.
func (m *Model) TextInput() *textinput.Model {
	return &m.textInput
}

// IsInputFocused reports whether the text input is focused.
func (m *Model) IsInputFocused() bool {
	return m.textInput.Focused()
}

// InputValue returns and clears the current input value.
func (m *Model) InputValue() string {
	v, _ := m.InputPayloadAndDisplay()
	return v
}

// InsertPastedContent inserts a compact paste marker while retaining the raw payload.
func (m *Model) InsertPastedContent(raw string) {
	if raw == "" {
		return
	}
	marker := pasteMarker(raw)
	m.pasteSnippets = append(m.pasteSnippets, pasteSnippet{Marker: marker, Raw: raw})

	value := []rune(m.textInput.Value())
	pos := m.textInput.Position()
	if pos < 0 {
		pos = 0
	}
	if pos > len(value) {
		pos = len(value)
	}
	next := make([]rune, 0, len(value)+utf8.RuneCountInString(marker))
	next = append(next, value[:pos]...)
	next = append(next, []rune(marker)...)
	next = append(next, value[pos:]...)
	m.textInput.SetValue(string(next))
	m.textInput.SetCursor(pos + utf8.RuneCountInString(marker))
}

// InputPayloadAndDisplay returns the agent payload and operator-facing display text, then clears the input.
func (m *Model) InputPayloadAndDisplay() (string, string) {
	display := strings.TrimSpace(m.textInput.Value())
	payload := m.expandPasteMarkers(display)
	m.textInput.Reset()
	m.pasteSnippets = nil
	return payload, display
}

func (m Model) expandPasteMarkers(display string) string {
	if display == "" || len(m.pasteSnippets) == 0 {
		return display
	}
	var b strings.Builder
	remaining := display
	for _, snippet := range m.pasteSnippets {
		idx := strings.Index(remaining, snippet.Marker)
		if idx < 0 {
			continue
		}
		b.WriteString(remaining[:idx])
		b.WriteString(snippet.Raw)
		remaining = remaining[idx+len(snippet.Marker):]
	}
	b.WriteString(remaining)
	return b.String()
}

func pasteMarker(raw string) string {
	count := utf8.RuneCountInString(raw)
	label := "Chars"
	if count == 1 {
		label = "Char"
	}
	return fmt.Sprintf("[Pasted Content %d %s]", count, label)
}

func messageDisplayText(content, displayContent string) string {
	if strings.TrimSpace(displayContent) != "" {
		return displayContent
	}
	return content
}

// Focus focuses the text input.
func (m *Model) Focus() tea.Cmd {
	return m.textInput.Focus()
}

// Blur blurs the text input.
func (m *Model) Blur() {
	m.textInput.Blur()
}

// Focused returns whether the text input is focused.
func (m Model) Focused() bool {
	return m.textInput.Focused()
}

// visibleLines returns how many rendered lines fit in the message area.
func (m Model) visibleLines() int {
	// Layout: title(1) + messages + thinking(0-1) + input block(inputBlockHeight) + notice(0-1)
	reserved := 1 + inputBlockHeight // title + input block
	if m.thinking {
		reserved++ // thinking indicator between messages and input block
	}
	if m.notice != "" {
		reserved++
	}
	visible := m.height - reserved
	if visible < 1 {
		visible = 1
	}
	return visible
}

// getDisplayLines builds display lines from pre-rendered entries.
func (m Model) getDisplayLines() []string {
	var rendered []string
	matched := 0
	for idx, e := range m.entries {
		if m.traceOnly && e.Role != "system" {
			continue
		}
		// Main (non-trace) view: collapse a run of consecutive tool-activity
		// (system) entries into a single status line — render only the last of
		// each run. While the agent works, that line is the most recent event,
		// so it reads as one status "cycling" through what it's doing; when an
		// assistant message breaks the run, the next run renders its own line,
		// giving a checkpoint per phase. The action-trace view keeps full detail.
		if !m.traceOnly && e.Role == "system" && idx+1 < len(m.entries) && m.entries[idx+1].Role == "system" {
			continue
		}
		lines := e.renderedLines
		if len(lines) == 0 {
			lines = []string{e.Content}
		}

		// Add spacing between messages (not before first)
		if matched > 0 {
			rendered = append(rendered, "")
		}
		matched++

		switch e.Role {
		case "user":
			// User messages: white arrow, subtle background box
			userBg := lipgloss.NewStyle().Background(lipgloss.Color("#374151")).Foreground(lipgloss.Color("#FFFFFF"))
			arrow := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true).Render("❯ ")
			for j, line := range lines {
				styledLine := userBg.Render(" " + line + " ")
				if j == 0 {
					rendered = append(rendered, arrow+styledLine)
				} else {
					rendered = append(rendered, "  "+styledLine)
				}
			}
		case "assistant":
			// Agent messages: colored marker based on content type
			marker := shared.RunningStyle.Render("◆ ") // green for normal
			if isBrowserAction(e.Content) {
				marker = shared.WarmingStyle.Render("◆ ") // amber for browser actions
			}
			for j, line := range lines {
				if j == 0 {
					rendered = append(rendered, marker+line)
				} else {
					rendered = append(rendered, "  "+line)
				}
			}
			if e.StreamActive && len(lines) > 0 {
				lastLine := rendered[len(rendered)-1]
				cursor := lipgloss.NewStyle().Faint(true).Render("▌")
				rendered[len(rendered)-1] = lastLine + cursor
			}
		case "stream":
			marker := shared.RunningStyle.Render("◆ ")
			for j, line := range lines {
				if j == 0 {
					rendered = append(rendered, marker+line)
				} else {
					rendered = append(rendered, "  "+line)
				}
			}
			cursor := lipgloss.NewStyle().Faint(true).Render("▌")
			if len(rendered) > 0 {
				rendered[len(rendered)-1] = rendered[len(rendered)-1] + cursor
			}
		case "system":
			// System messages: muted dot. The trailing status line while the
			// agent is working animates with the spinner to signal it's live.
			marker := shared.MutedStyle.Render("● ")
			if !m.traceOnly && idx == len(m.entries)-1 && m.thinking {
				marker = shared.WarmingStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)] + " ")
			}
			for j, line := range lines {
				if j == 0 {
					rendered = append(rendered, marker+line)
				} else {
					rendered = append(rendered, "  "+line)
				}
			}
		default:
			for _, line := range lines {
				rendered = append(rendered, "  "+line)
			}
		}
	}
	if m.traceOnly && len(rendered) == 0 {
		rendered = append(rendered, shared.MutedStyle.Render("No action trace yet."))
	}
	return rendered
}

// isBrowserAction checks if a message describes a browser action.
func isBrowserAction(content string) bool {
	lower := strings.ToLower(content)
	actions := []string{
		"navigat", "click", "screenshot", "scroll",
		"typing", "type ", "typed", "browser",
		"page.goto", "page.click", "vulpine_",
		"opening", "loading", "visited",
	}
	for _, a := range actions {
		if strings.Contains(lower, a) {
			return true
		}
	}
	return false
}

// renderLines returns display lines (for scroll calculation).
func (m Model) renderLines() []string {
	return m.getDisplayLines()
}

func (m *Model) scrollToBottom() {
	if !m.autoScroll {
		return
	}
	total := len(m.renderLines())
	visible := m.visibleLines()
	if total > visible {
		m.scroll = total - visible
	} else {
		m.scroll = 0
	}
}

func (m *Model) clampScroll() {
	total := len(m.renderLines())
	visible := m.visibleLines()
	maxScroll := total - visible
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// View renders the conversation panel.
// Messages are bottom-aligned: empty space at top, messages grow upward from the input box.
// inputBlock renders a fixed-height OpenCode-style chat input box with rounded
// border, prompt line, and agent/model metadata.
func (m Model) inputBlock(inputArea string) string {
	blockWidth := m.width - 2
	if blockWidth < 6 {
		return fitCellLine(inputArea, max(1, m.width))
	}
	contentWidth := blockWidth - 4 // border + horizontal padding
	if contentWidth < 1 {
		contentWidth = 1
	}
	body := strings.Join([]string{
		"",
		fitCellLine(inputArea, contentWidth),
		m.inputMetadata(contentWidth),
	}, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(shared.ColorPrimary).
		Background(inputBlockBg).
		Padding(0, 1).
		Width(contentWidth).
		Render(body)
}

func (m Model) inputArea() string {
	if m.textInput.Focused() && m.awake {
		return m.inputTextView()
	}
	if m.agentStatus == "error" {
		return inputMutedStyle.Render("Agent failed - press Enter to retry")
	}
	if !m.awake && m.thinking {
		return inputMutedStyle.Render("Chat available after agent responds")
	}
	if m.textInput.Focused() {
		return m.inputTextView()
	}
	if m.awake {
		return inputMutedStyle.Render("Press Enter to chat...")
	}
	return inputMutedStyle.Render("Agent not active")
}

func (m Model) inputTextView() string {
	value := m.textInput.Value()
	if value == "" {
		return m.inputCaret(" ") + inputMutedStyle.Render(m.textInput.Placeholder)
	}
	runes := []rune(value)
	pos := m.textInput.Position()
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes)
	}
	before := inputModelStyle.Render(string(runes[:pos]))
	if pos == len(runes) {
		return before + m.inputCaret(" ")
	}
	cursor := m.inputCaret(string(runes[pos]))
	after := inputModelStyle.Render(string(runes[pos+1:]))
	return before + cursor + after
}

func (m Model) inputCaret(text string) string {
	if m.inputPulseFrame%2 == 0 {
		return inputCursorStyle.Render(text)
	}
	return inputModelStyle.Render(text)
}

func (m Model) inputMetadata(maxWidth int) string {
	name := m.agentName
	if name == "" {
		name = "Agent"
	}
	model := strings.TrimSpace(m.modelLabel)
	if model == "" {
		model = "model"
	}
	fixedWidth := ansiVisualWidth(name) + ansiVisualWidth(" · ")
	modelWidth := maxWidth - fixedWidth
	if modelWidth < 1 {
		modelWidth = 1
	}
	model = clipCellText(model, modelWidth)
	return inputStatusStyle.Render(name) + inputMutedStyle.Render(" · ") + inputModelStyle.Render(model)
}

func clipCellText(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if ansiVisualWidth(text) <= maxWidth {
		return text
	}
	tail := "..."
	if maxWidth <= ansiVisualWidth(tail) {
		tail = ""
	}
	limit := maxWidth - ansiVisualWidth(tail)
	if limit <= 0 {
		return tail
	}
	var b strings.Builder
	width := 0
	for _, r := range text {
		runeWidth := runewidth.RuneWidth(r)
		if width+runeWidth > limit {
			break
		}
		b.WriteRune(r)
		width += runeWidth
	}
	return b.String() + tail
}

func fitCellLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansiVisualWidth(line) <= width {
		return line
	}
	fitted := lipgloss.NewStyle().MaxWidth(width).Render(line)
	lines := strings.Split(fitted, "\n")
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func (m Model) View() string {
	return m.view("")
}

// ViewWithCommandPalette renders the conversation with an inline command palette
// shown just above the chat input. Pass an empty string for no palette.
func (m Model) ViewWithCommandPalette(palette string) string {
	return m.view(palette)
}

func (m Model) view(palette string) string {
	var b strings.Builder

	var paletteLines []string
	if strings.TrimSpace(palette) != "" {
		paletteLines = strings.Split(palette, "\n")
	}

	// Build the input line (rendered inside the styled input block below).
	inputArea := m.inputArea()

	// Build thinking indicator
	var thinkingLine string
	if m.thinking {
		spinner := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
		var phrase string
		if !m.awake {
			phrase = wakingPhrases[m.phraseIdx%len(wakingPhrases)]
		} else {
			phrase = thinkingPhrases[m.phraseIdx%len(thinkingPhrases)]
		}
		shimmerText := renderShimmer(spinner+" "+phrase+"...", m.shimmerOffset)
		thinkingLine = "  " + shimmerText
	}

	// Calculate available lines for messages
	// Layout (bottom to top): notice(0-1) + input block(5) + palette(0-n) + thinking(0-1) + messages + title(1)
	bottomLines := inputBlockHeight + len(paletteLines)
	if m.thinking {
		bottomLines++
	}
	if m.notice != "" {
		bottomLines++
	}
	visibleMsgLines := m.height - 1 - bottomLines // 1 for title
	if visibleMsgLines < 1 {
		visibleMsgLines = 1
	}

	// Get display lines from pre-rendered entries (no markdown parsing in render path)
	rendered := m.getDisplayLines()

	// Clamp scroll
	maxScroll := len(rendered) - visibleMsgLines
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	if m.scroll < 0 {
		m.scroll = 0
	}

	start := m.scroll
	end := start + visibleMsgLines
	if end > len(rendered) {
		end = len(rendered)
	}
	visibleSlice := rendered[start:end]
	// Hard-truncate to visibleMsgLines (safety against markdown expanding lines)
	if len(visibleSlice) > visibleMsgLines {
		visibleSlice = visibleSlice[:visibleMsgLines]
	}
	linesWritten := len(visibleSlice)

	// === BUILD OUTPUT ===

	// 1. Title
	title := "CONVERSATION"
	if m.traceOnly {
		title = "ACTION TRACE"
	}
	b.WriteString(shared.TitleStyle.Render(title))
	b.WriteString("\n")

	// 2. Empty space (bottom-align messages)
	emptyLines := visibleMsgLines - linesWritten
	for i := 0; i < emptyLines; i++ {
		b.WriteString("\n")
	}

	// 3. Messages
	for _, line := range visibleSlice {
		b.WriteString(line)
		b.WriteString("\n")
	}

	// 4. Thinking indicator
	if m.thinking {
		b.WriteString(thinkingLine)
		b.WriteString("\n")
	}

	// 4b. Inline command palette (above the input)
	for _, line := range paletteLines {
		b.WriteString(line)
		b.WriteString("\n")
	}

	// 5. Styled input block (5 lines)
	b.WriteString(m.inputBlock(inputArea))

	// 6. Transient notice rendered just under the chatbox.
	if m.notice != "" {
		b.WriteString("\n")
		notice := m.notice
		if w := m.width - 2; w > 2 {
			notice = clipCellText(notice, w-2)
		}
		b.WriteString(inputMutedStyle.Render("  " + notice))
	}

	return b.String()
}

// renderShimmer creates a purple shimmer effect across text.
// The shimmer is a bright spot that moves through the characters.
func renderShimmer(text string, offset int) string {
	// Purple gradient: dark → bright → dark
	purples := []string{
		"#4C1D95", // very dark purple
		"#5B21B6", // dark purple
		"#7C3AED", // medium purple
		"#8B5CF6", // bright purple
		"#A78BFA", // light purple
		"#C4B5FD", // very light purple/white
		"#A78BFA", // light purple
		"#8B5CF6", // bright purple
		"#7C3AED", // medium purple
		"#5B21B6", // dark purple
	}

	runes := []rune(text)
	var result strings.Builder
	shimmerWidth := len(purples)

	for i, ch := range runes {
		// Calculate distance from shimmer center
		dist := (i - offset%len(runes) + len(runes)) % len(runes)
		if dist >= shimmerWidth {
			// Outside shimmer — use base purple
			result.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED")).Render(string(ch)))
		} else {
			// Inside shimmer — use gradient color
			result.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(purples[dist])).Bold(true).Render(string(ch)))
		}
	}
	return result.String()
}

var (
	reBold           = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic         = regexp.MustCompile(`\*(.+?)\*`)
	reCode           = regexp.MustCompile("`([^`]+)`")
	reTaskList       = regexp.MustCompile(`^\s*[-*]\s+\[([ xX])\]\s+(.+)$`)
	reBullet         = regexp.MustCompile(`^\s*[-*]\s+(.+)$`)
	reNumbered       = regexp.MustCompile(`^\s*(\d+)[.)]\s+(.+)$`)
	reTableDelimiter = regexp.MustCompile(`^:?-{3,}:?$`)
)

// renderMarkdown applies lightweight inline markdown styling and word wraps.
// Handles chat-oriented Markdown without pulling in a full parser; this keeps
// rendering deterministic and terminal-safe.
func renderMarkdown(text string, maxWidth int) []string {
	var allLines []string
	rawLines := strings.Split(text, "\n")
	for i := 0; i < len(rawLines); {
		if codeLines, next, ok := renderMarkdownCodeBlock(rawLines, i, maxWidth); ok {
			allLines = append(allLines, codeLines...)
			i = next
			continue
		}
		if tableLines, next, ok := renderMarkdownTable(rawLines, i, maxWidth); ok {
			allLines = append(allLines, tableLines...)
			i = next
			continue
		}
		rawLine := rawLines[i]
		line := strings.TrimRight(rawLine, " ")
		allLines = append(allLines, renderMarkdownLine(line, maxWidth)...)
		i++
	}

	if len(allLines) == 0 {
		return []string{""}
	}
	return allLines
}

func renderMarkdownLine(line string, maxWidth int) []string {
	if match := reTaskList.FindStringSubmatch(line); match != nil {
		checked := strings.EqualFold(match[1], "x")
		box := "☐"
		if checked {
			box = "☑"
		}
		return renderPrefixedMarkdown(strings.TrimSpace(match[2]), maxWidth, "  "+box+" ", "    ")
	}

	if match := reNumbered.FindStringSubmatch(line); match != nil {
		prefix := "  " + match[1] + ". "
		nextPrefix := strings.Repeat(" ", ansiVisualWidth(prefix))
		return renderPrefixedMarkdown(strings.TrimSpace(match[2]), maxWidth, prefix, nextPrefix)
	}

	if match := reBullet.FindStringSubmatch(line); match != nil {
		text := strings.TrimSpace(match[1])
		return renderPrefixedMarkdown(text, maxWidth, "  • ", "    ")
	}

	var out []string
	for _, wrapped := range wordWrap(line, maxWidth) {
		out = append(out, applyInlineMarkdown(wrapped))
	}
	return out
}

func renderPrefixedMarkdown(text string, maxWidth int, firstPrefix, nextPrefix string) []string {
	if text == "" {
		return []string{shared.MutedStyle.Render(strings.TrimRight(firstPrefix, " "))}
	}
	width := maxWidth - ansiVisualWidth(firstPrefix)
	if width < 1 {
		width = 1
	}
	wrapped := wordWrap(text, width)
	out := make([]string, 0, len(wrapped))
	for i, wrappedLine := range wrapped {
		prefix := nextPrefix
		if i == 0 {
			prefix = firstPrefix
		}
		out = append(out, shared.MutedStyle.Render(prefix)+applyInlineMarkdown(wrappedLine))
	}
	return out
}

func renderMarkdownCodeBlock(rawLines []string, start, maxWidth int) ([]string, int, bool) {
	open := strings.TrimSpace(rawLines[start])
	if !strings.HasPrefix(open, "```") {
		return nil, start, false
	}
	next := start + 1
	var out []string
	for next < len(rawLines) {
		line := strings.TrimRight(rawLines[next], " ")
		next++
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			return out, next, true
		}
		out = append(out, renderCodeBlockLine(line, maxWidth))
	}
	return out, next, true
}

func renderCodeBlockLine(line string, maxWidth int) string {
	const prefix = "  │ "
	width := maxWidth - ansiVisualWidth(prefix)
	if width < 1 {
		width = 1
	}
	line = clipCellText(line, width)
	return shared.MutedStyle.Render(prefix) + lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")).Render(line)
}

func renderMarkdownTable(rawLines []string, start, maxWidth int) ([]string, int, bool) {
	if start+1 >= len(rawLines) {
		return nil, start, false
	}
	header := strings.TrimRight(rawLines[start], " ")
	delimiter := strings.TrimRight(rawLines[start+1], " ")
	headerCells := parseMarkdownTableRow(header)
	delimiterCells := parseMarkdownTableRow(delimiter)
	if len(headerCells) < 2 || len(delimiterCells) != len(headerCells) || !isMarkdownTableDelimiter(delimiterCells) {
		return nil, start, false
	}

	rows := [][]string{headerCells}
	next := start + 2
	for next < len(rawLines) {
		line := strings.TrimRight(rawLines[next], " ")
		if strings.TrimSpace(line) == "" {
			break
		}
		cells := parseMarkdownTableRow(line)
		if len(cells) == 0 {
			break
		}
		rows = append(rows, normalizeTableRow(cells, len(headerCells)))
		next++
	}
	return renderTableRows(rows, maxWidth), next, true
}

func parseMarkdownTableRow(line string) []string {
	if !strings.Contains(line, "|") {
		return nil
	}
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, strings.TrimSpace(part))
	}
	return cells
}

func isMarkdownTableDelimiter(cells []string) bool {
	for _, cell := range cells {
		if !reTableDelimiter.MatchString(strings.ReplaceAll(cell, " ", "")) {
			return false
		}
	}
	return true
}

func normalizeTableRow(cells []string, columns int) []string {
	out := make([]string, columns)
	copy(out, cells)
	return out
}

func renderTableRows(rows [][]string, maxWidth int) []string {
	if len(rows) == 0 || len(rows[0]) == 0 {
		return nil
	}
	widths := tableColumnWidths(rows, maxWidth)
	out := make([]string, 0, len(rows)+1)
	out = append(out, renderTableRow(rows[0], widths, true))
	out = append(out, renderTableSeparator(widths))
	for _, row := range rows[1:] {
		out = append(out, renderTableRow(row, widths, false))
	}
	return out
}

func tableColumnWidths(rows [][]string, maxWidth int) []int {
	columns := len(rows[0])
	widths := make([]int, columns)
	for i := range widths {
		widths[i] = 1
	}
	for _, row := range rows {
		for i := 0; i < columns && i < len(row); i++ {
			if width := ansiVisualWidth(row[i]); width > widths[i] {
				widths[i] = width
			}
		}
	}

	available := maxWidth - 2 - ((columns - 1) * 3)
	if available < columns {
		available = columns
	}
	total := 0
	for _, width := range widths {
		total += width
	}
	if total <= available {
		return widths
	}

	scaled := make([]int, columns)
	remaining := available
	for i, width := range widths {
		next := width * available / total
		if next < 1 {
			next = 1
		}
		scaled[i] = next
		remaining -= next
	}
	for i := 0; remaining > 0; i = (i + 1) % columns {
		scaled[i]++
		remaining--
	}
	return scaled
}

func renderTableRow(row []string, widths []int, header bool) string {
	cells := make([]string, len(widths))
	for i, width := range widths {
		cell := ""
		if i < len(row) {
			cell = row[i]
		}
		cells[i] = renderTableCell(cell, width, header)
	}
	return "  " + strings.Join(cells, shared.MutedStyle.Render(" │ "))
}

func renderTableCell(text string, width int, header bool) string {
	clipped := clipCellText(text, width)
	rendered := applyInlineMarkdown(clipped)
	if header {
		rendered = lipgloss.NewStyle().Bold(true).Render(rendered)
	}
	if padding := width - ansiVisualWidth(rendered); padding > 0 {
		rendered += strings.Repeat(" ", padding)
	}
	return rendered
}

func renderTableSeparator(widths []int) string {
	parts := make([]string, len(widths))
	for i, width := range widths {
		parts[i] = strings.Repeat("─", width)
	}
	return shared.MutedStyle.Render("  " + strings.Join(parts, "─┼─"))
}

func applyInlineMarkdown(line string) string {
	boldStyle := lipgloss.NewStyle().Bold(true)
	italicStyle := lipgloss.NewStyle().Italic(true)
	codeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA"))

	styled := reBold.ReplaceAllStringFunc(line, func(m string) string {
		inner := m[2 : len(m)-2]
		return boldStyle.Render(inner)
	})
	styled = reCode.ReplaceAllStringFunc(styled, func(m string) string {
		inner := m[1 : len(m)-1]
		return codeStyle.Render(inner)
	})
	styled = reItalic.ReplaceAllStringFunc(styled, func(m string) string {
		inner := m[1 : len(m)-1]
		return italicStyle.Render(inner)
	})
	return styled
}

// ansiVisualWidth returns the visual width of a string, ignoring ANSI escape codes.
func ansiVisualWidth(s string) int {
	width := 0
	inEscape := false
	for _, r := range s {
		if inEscape {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		width += runewidth.RuneWidth(r)
	}
	return width
}

// wordWrap breaks text into lines of at most maxWidth visual characters,
// breaking at word boundaries when possible. ANSI escape codes are not
// counted toward the width since they add bytes but no visual width.
func wordWrap(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{text}
	}
	if ansiVisualWidth(text) <= maxWidth {
		return []string{text}
	}

	var lines []string
	for len(text) > 0 {
		if ansiVisualWidth(text) <= maxWidth {
			lines = append(lines, text)
			break
		}

		// Walk through runes counting only visible characters to find the byte
		// position corresponding to maxWidth visual chars.
		visualW := 0
		bytePos := 0
		inEscape := false
		for i, r := range text {
			if inEscape {
				if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
					inEscape = false
				}
				bytePos = i + len(string(r))
				continue
			}
			if r == '\x1b' {
				inEscape = true
				bytePos = i + len(string(r))
				continue
			}
			runeWidth := runewidth.RuneWidth(r)
			if visualW+runeWidth > maxWidth && bytePos > 0 {
				break
			}
			visualW += runeWidth
			bytePos = i + utf8.RuneLen(r)
			if visualW >= maxWidth {
				break
			}
		}

		// Find the last space within the visual-width boundary
		cut := bytePos
		trycut := cut
		for trycut > 0 && text[trycut-1] != ' ' {
			trycut--
		}
		if trycut > 0 {
			cut = trycut
		}
		// else: no space found, hard break at bytePos

		lines = append(lines, text[:cut])
		text = strings.TrimLeft(text[cut:], " ")
	}
	return lines
}
