package loading

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	purple    = lipgloss.Color("#7C3AED")
	muted     = lipgloss.Color("#6B7280")
	textStyle = lipgloss.NewStyle().Foreground(purple).Bold(true)
	hintStyle = lipgloss.NewStyle().Foreground(muted)
)

// DoneMsg signals that background work is complete.
type DoneMsg struct{}

// Model is a loading spinner shown while the kernel starts.
type Model struct {
	spinner spinner.Model
	message string
	width   int
	height  int
	done    bool
}

// New creates a loading screen with the given message.
func New(message string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(purple)
	return Model{spinner: s, message: message}
}

func (m Model) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case DoneMsg:
		m.done = true
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) Done() bool {
	return m.done
}

func (m Model) View() string {
	content := m.spinner.View() + " " + textStyle.Render(m.message) + "\n" +
		hintStyle.Render("  Starting VulpineOS kernel...")

	pad := m.height / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat("\n", pad) + lipgloss.PlaceHorizontal(m.width, lipgloss.Center, content)
}

// WaitForStartup returns a tea.Cmd that waits for the given function to complete,
// then sends DoneMsg to dismiss the loading screen.
func WaitForStartup(fn func() error) tea.Cmd {
	return func() tea.Msg {
		// Small delay so the spinner actually renders before we block
		time.Sleep(100 * time.Millisecond)
		fn()
		return DoneMsg{}
	}
}
