package conversation

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
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
	Role    string
	Content string
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
	scroll        int
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
		textInput: ti,
		width:     40,
		height:    20,
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
	switch msg.(type) {
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
	}
	return m, nil
}

// SetAgentID sets the current agent and clears entries.
func (m *Model) SetAgentID(id string) {
	m.agentID = id
	m.entries = nil
	m.scroll = 0
	m.awake = false
}

// AgentID returns the current agent ID.
func (m Model) AgentID() string {
	return m.agentID
}

// LoadMessages loads conversation history from vault messages.
func (m *Model) LoadMessages(msgs []vault.AgentMessage) {
	m.entries = make([]Entry, 0, len(msgs))
	m.awake = false
	for _, msg := range msgs {
		m.entries = append(m.entries, Entry{Role: msg.Role, Content: msg.Content})
		if msg.Role == "assistant" {
			m.awake = true
		}
	}
	m.scrollToBottom()
}

// AddEntry adds a new conversation entry.
func (m *Model) AddEntry(role, content string) {
	m.entries = append(m.entries, Entry{Role: role, Content: content})
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

func (m *Model) scrollToBottom() {
	visible := m.height - 4 // title + input + padding
	if visible < 1 {
		visible = 1
	}
	if len(m.entries) > visible {
		m.scroll = len(m.entries) - visible
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
func (m Model) View() string {
	var b strings.Builder

	// No agent selected — show prompt
	if m.agentID == "" {
		b.WriteString(shared.TitleStyle.Render("CONVERSATION"))
		b.WriteString("\n\n")
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

	b.WriteString(shared.TitleStyle.Render("CONVERSATION"))
	b.WriteString("\n")

	if len(m.entries) == 0 && !m.thinking {
		b.WriteString(shared.MutedStyle.Render("  No messages yet"))
		b.WriteString("\n")
	} else {
		visible := m.height - 6
		if visible < 1 {
			visible = 1
		}

		// Render entries with word wrapping
		maxWidth := m.width - 8
		if maxWidth < 10 {
			maxWidth = 10
		}

		var rendered []string
		for _, e := range m.entries {
			prefix := m.rolePrefix(e.Role)
			// Word wrap long content
			lines := wordWrap(e.Content, maxWidth)
			for i, line := range lines {
				if i == 0 {
					rendered = append(rendered, prefix+line)
				} else {
					// Indent continuation lines
					rendered = append(rendered, strings.Repeat(" ", 5)+shared.MutedStyle.Render("│ ")+line)
				}
			}
		}

		// Scrolling
		start := m.scroll
		end := start + visible
		if end > len(rendered) {
			end = len(rendered)
		}
		if start < 0 {
			start = 0
		}

		for _, line := range rendered[start:end] {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	// Animated thinking/waking indicator
	if m.thinking {
		spinner := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
		var phrase string
		if !m.awake {
			phrase = wakingPhrases[m.phraseIdx%len(wakingPhrases)]
		} else {
			phrase = thinkingPhrases[m.phraseIdx%len(thinkingPhrases)]
		}
		// Shimmer effect: gradient across the text using purple shades
		shimmerText := renderShimmer(spinner+" "+phrase+"...", m.shimmerOffset)
		b.WriteString("  " + shimmerText)
		b.WriteString("\n")
	}

	// Input area
	b.WriteString("\n")
	if !m.awake && m.thinking {
		// Agent hasn't spoken yet — lock the chat
		b.WriteString(shared.MutedStyle.Render("  Chat available after agent responds"))
	} else if m.textInput.Focused() {
		b.WriteString(m.textInput.View())
	} else if m.awake {
		b.WriteString(shared.MutedStyle.Render("  > Press Enter to chat..."))
	} else {
		b.WriteString(shared.MutedStyle.Render("  > Agent not active"))
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
