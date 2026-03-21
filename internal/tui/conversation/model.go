package conversation

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"regexp"

	"github.com/charmbracelet/lipgloss"

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

// ThinkingTickMsg triggers animation updates for the thinking indicator.
type ThinkingTickMsg struct{}

// ThinkingTick returns a command that ticks every 120ms for animation.
func ThinkingTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return ThinkingTickMsg{}
	})
}

// Entry is a single message displayed in the conversation.
type Entry struct {
	Role          string
	Content       string
	renderedLines []string // pre-rendered markdown lines (set on add)
}

// Model holds the conversation panel state.
type Model struct {
	entries       []Entry
	agentID       string
	agentName     string
	thinking      bool // true while waiting for agent response
	awake         bool // true after agent has sent its first message
	textInput     textinput.Model
	width         int
	height        int
	scroll        int  // scroll offset in rendered lines (not entries)
	autoScroll    bool // whether to auto-scroll to bottom on new messages
	spinnerFrame  int    // current spinner animation frame
	phraseIdx     int    // current thinking phrase index
	shimmerOffset int    // shimmer position for the gradient effect
	phraseTicks   int    // ticks since last phrase change

}

// SetAgentName sets the display name for the agent.
func (m *Model) SetAgentName(name string) {
	m.agentName = name
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

// New creates a new conversation panel.
func New() Model {
	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.CharLimit = 1000
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
	m.width = w
	m.height = h
	m.textInput.Width = w - 4
	if m.textInput.Width < 10 {
		m.textInput.Width = 10
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
}

// AgentID returns the current agent ID.
func (m Model) AgentID() string {
	return m.agentID
}

// LoadMessages loads conversation history from vault messages.
func (m *Model) LoadMessages(msgs []vault.AgentMessage) {
	maxWidth := m.width - 8
	if maxWidth < 10 {
		maxWidth = 10
	}
	m.entries = make([]Entry, 0, len(msgs))
	m.awake = false
	for _, msg := range msgs {
		m.entries = append(m.entries, Entry{
			Role:          msg.Role,
			Content:       msg.Content,
			renderedLines: renderMarkdown(msg.Content, maxWidth),
		})
		if msg.Role == "assistant" {
			m.awake = true
		}
	}
	m.scrollToBottom()
}

// AddEntry adds a new conversation entry.
func (m *Model) AddEntry(role, content string) {
	maxWidth := m.width - 8
	if maxWidth < 10 {
		maxWidth = 10
	}
	m.entries = append(m.entries, Entry{
		Role:          role,
		Content:       content,
		renderedLines: renderMarkdown(content, maxWidth),
	})
	if role == "assistant" {
		m.awake = true
	}
	m.scrollToBottom()
}

// TextInput returns a pointer to the text input for external update.
func (m *Model) TextInput() *textinput.Model {
	return &m.textInput
}

// InputValue returns and clears the current input value.
func (m *Model) InputValue() string {
	v := strings.TrimSpace(m.textInput.Value())
	m.textInput.Reset()
	return v
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
	// Layout: title(1) + messages + thinking(0-1) + divider(1) + input(1) + divider(1)
	reserved := 4 // title + divider above + input + divider below
	if m.thinking {
		reserved++ // thinking indicator between messages and top divider
	}
	visible := m.height - reserved
	if visible < 1 {
		visible = 1
	}
	return visible
}

// getDisplayLines builds display lines from pre-rendered entries. No markdown parsing here.
func (m Model) getDisplayLines() []string {
	var rendered []string
	for _, e := range m.entries {
		prefix := m.rolePrefix(e.Role)
		lines := e.renderedLines
		if len(lines) == 0 {
			lines = []string{e.Content}
		}
		for i, line := range lines {
			if i == 0 {
				rendered = append(rendered, prefix+line)
			} else {
				rendered = append(rendered, strings.Repeat(" ", 5)+shared.MutedStyle.Render("│ ")+line)
			}
		}
	}
	return rendered
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

// rolePrefix returns a styled role prefix.
func (m Model) rolePrefix(role string) string {
	switch role {
	case "user":
		return shared.KeyStyle.Render("you ")
	case "assistant":
		name := m.agentName
		if name == "" {
			name = "agent"
		}
		// Truncate name to 10 chars for alignment
		if len(name) > 10 {
			name = name[:10]
		}
		return shared.RunningStyle.Render(name + " ")
	case "system":
		return shared.MutedStyle.Render("sys ")
	default:
		return shared.MutedStyle.Render(fmt.Sprintf("%-4s", role))
	}
}

// View renders the conversation panel.
// Messages are bottom-aligned: empty space at top, messages grow upward from the input box.
// Input box is framed by dividers above and below.
func (m Model) View() string {
	var b strings.Builder

	dividerWidth := m.width - 2
	if dividerWidth < 1 {
		dividerWidth = 1
	}
	divider := shared.MutedStyle.Render(strings.Repeat("─", dividerWidth))

	// No agent selected — show centered prompt
	if m.agentID == "" {
		for i := 0; i < m.height/2-2; i++ {
			b.WriteString("\n")
		}
		b.WriteString(shared.MutedStyle.Render("  Press "))
		b.WriteString(shared.KeyStyle.Render("n"))
		b.WriteString(shared.MutedStyle.Render(" to create a new agent"))
		b.WriteString("\n\n")
		b.WriteString(shared.MutedStyle.Render("  Or select an agent from the left panel"))
		b.WriteString("\n")
		b.WriteString(shared.MutedStyle.Render("  with "))
		b.WriteString(shared.KeyStyle.Render("j/k"))
		b.WriteString(shared.MutedStyle.Render(" to view its conversation"))
		return b.String()
	}

	// Build the input line
	var inputArea string
	if !m.awake && m.thinking {
		inputArea = shared.MutedStyle.Render("  Chat available after agent responds")
	} else if m.textInput.Focused() {
		inputArea = m.textInput.View()
	} else if m.awake {
		inputArea = shared.MutedStyle.Render("  > Press Enter to chat...")
	} else {
		inputArea = shared.MutedStyle.Render("  > Agent not active")
	}

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
	// Layout (bottom to top): divider(1) + input(1) + divider(1) + thinking(0-1) + messages + title(1)
	bottomLines := 3 // divider + input + divider
	if m.thinking {
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
	b.WriteString(shared.TitleStyle.Render("CONVERSATION"))
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

	// 5. Divider above input
	b.WriteString(divider)
	b.WriteString("\n")

	// 6. Input area
	b.WriteString(inputArea)
	b.WriteString("\n")

	// 7. Divider below input
	b.WriteString(divider)

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
	reBold   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reItalic = regexp.MustCompile(`\*(.+?)\*`)
	reCode   = regexp.MustCompile("`([^`]+)`")
)

// renderMarkdown applies lightweight inline markdown styling and word wraps.
// Handles **bold**, *italic*, `code` — no heavy library needed.
func renderMarkdown(text string, maxWidth int) []string {
	boldStyle := lipgloss.NewStyle().Bold(true)
	italicStyle := lipgloss.NewStyle().Italic(true)
	codeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA"))

	// Process each paragraph (double newline separated)
	var allLines []string
	paragraphs := strings.Split(text, "\n")
	for _, para := range paragraphs {
		para = strings.TrimRight(para, " ")

		// Apply inline styles (order matters: bold before italic to handle ** vs *)
		styled := reBold.ReplaceAllStringFunc(para, func(m string) string {
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

		// Word wrap the styled text
		wrapped := wordWrap(styled, maxWidth)
		allLines = append(allLines, wrapped...)
	}

	if len(allLines) == 0 {
		return []string{""}
	}
	return allLines
}

// wordWrap breaks text into lines of at most maxWidth characters,
// breaking at word boundaries when possible.
func wordWrap(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{text}
	}
	if len(text) <= maxWidth {
		return []string{text}
	}

	var lines []string
	for len(text) > 0 {
		if len(text) <= maxWidth {
			lines = append(lines, text)
			break
		}

		// Find the last space within maxWidth
		cut := maxWidth
		for cut > 0 && text[cut] != ' ' {
			cut--
		}
		if cut == 0 {
			// No space found — hard break
			cut = maxWidth
		}

		lines = append(lines, text[:cut])
		text = strings.TrimLeft(text[cut:], " ")
	}
	return lines
}
