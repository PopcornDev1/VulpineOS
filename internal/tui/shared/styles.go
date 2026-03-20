package shared

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	ColorPrimary   = lipgloss.Color("#7C3AED") // violet
	ColorSecondary = lipgloss.Color("#06B6D4") // cyan
	ColorSuccess   = lipgloss.Color("#10B981") // green
	ColorWarning   = lipgloss.Color("#F59E0B") // amber
	ColorDanger    = lipgloss.Color("#EF4444") // red
	ColorMuted     = lipgloss.Color("#6B7280") // gray
	ColorBg        = lipgloss.Color("#1F2937") // dark gray
	ColorBorder    = lipgloss.Color("#374151") // border gray

	// Panel styles
	PanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	ActivePanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary).
				Padding(0, 1)

	// Title
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)

	// Status indicators
	RunningStyle = lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true)
	StoppedStyle = lipgloss.NewStyle().Foreground(ColorDanger).Bold(true)
	WarmingStyle = lipgloss.NewStyle().Foreground(ColorWarning)
	MutedStyle   = lipgloss.NewStyle().Foreground(ColorMuted)

	// Keybind help
	KeyStyle  = lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true)
	HelpStyle = lipgloss.NewStyle().Foreground(ColorMuted)

	// Alert
	AlertStyle = lipgloss.NewStyle().Foreground(ColorDanger)

	// Table header
	HeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(ColorSecondary)

	// Selected row
	SelectedStyle = lipgloss.NewStyle().Background(lipgloss.Color("#374151")).Bold(true)
)
