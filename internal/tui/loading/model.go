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

// StatusMsg updates the secondary loading status line.
type StatusMsg string

// Model is a loading spinner shown while the kernel starts.
type Model struct {
	spinner spinner.Model
	message string
	status  string
	width   int
	height  int
	done    bool
}

// New creates a loading screen with the given message.
func New(message string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(purple)
	return Model{spinner: s, message: message, status: "Starting VulpineOS kernel..."}
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
	case StatusMsg:
		if status := strings.TrimSpace(string(msg)); status != "" {
			m.status = status
		}
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
	width := m.width
	if width <= 0 {
		width = 80
	}
	lines := []string{
		fitLoadingLine(m.spinner.View()+" "+textStyle.Render(m.message), width),
		fitLoadingLine(hintStyle.Render(m.status), width),
	}
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}
	content := strings.Join(lines, "\n")

	pad := 0
	if m.height > len(lines) {
		pad = (m.height - len(lines)) / 2
	}
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat("\n", pad) + lipgloss.PlaceHorizontal(width, lipgloss.Center, content)
}

func fitLoadingLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	fitted := lipgloss.NewStyle().MaxWidth(width).Render(line)
	lines := strings.Split(fitted, "\n")
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
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
