package conversation

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"vulpineos/internal/tui/shared"
	"vulpineos/internal/vault"
)

// Entry is a single message displayed in the conversation.
type Entry struct {
	Role    string
	Content string
}

// Model holds the conversation panel state.
type Model struct {
	entries   []Entry
	agentID   string
	textInput textinput.Model
	width     int
	height    int
	scroll    int
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
	return m, nil
}

// SetAgentID sets the current agent and clears entries.
func (m *Model) SetAgentID(id string) {
	m.agentID = id
	m.entries = nil
	m.scroll = 0
}

// AgentID returns the current agent ID.
func (m Model) AgentID() string {
	return m.agentID
}

// LoadMessages loads conversation history from vault messages.
func (m *Model) LoadMessages(msgs []vault.AgentMessage) {
	m.entries = make([]Entry, len(msgs))
	for i, msg := range msgs {
		m.entries[i] = Entry{Role: msg.Role, Content: msg.Content}
	}
	m.scrollToBottom()
}

// AddEntry adds a new conversation entry.
func (m *Model) AddEntry(role, content string) {
	m.entries = append(m.entries, Entry{Role: role, Content: content})
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
func rolePrefix(role string) string {
	switch role {
	case "user":
		return shared.KeyStyle.Render("YOU ")
	case "assistant":
		return shared.RunningStyle.Render("AI  ")
	case "system":
		return shared.WarmingStyle.Render("SYS ")
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

	if len(m.entries) == 0 {
		b.WriteString(shared.MutedStyle.Render("  Agent created. Waiting for response..."))
		b.WriteString("\n")
		b.WriteString(shared.MutedStyle.Render("  Press "))
		b.WriteString(shared.KeyStyle.Render("Enter"))
		b.WriteString(shared.MutedStyle.Render(" to chat with this agent"))
		b.WriteString("\n")
	} else {
		visible := m.height - 6 // title + input + padding
		if visible < 1 {
			visible = 1
		}
		start := m.scroll
		end := start + visible
		if end > len(m.entries) {
			end = len(m.entries)
		}
		if start < 0 {
			start = 0
		}

		maxContent := m.width - 6
		if maxContent < 10 {
			maxContent = 10
		}

		for _, e := range m.entries[start:end] {
			content := e.Content
			if len(content) > maxContent {
				content = content[:maxContent-1] + "…"
			}
			b.WriteString(rolePrefix(e.Role))
			b.WriteString(content)
			b.WriteString("\n")
		}
	}

	// Always show input area at bottom when agent is selected
	b.WriteString("\n")
	if m.textInput.Focused() {
		b.WriteString(m.textInput.View())
	} else {
		b.WriteString(shared.MutedStyle.Render("  > Press Enter to chat..."))
	}

	return b.String()
}
