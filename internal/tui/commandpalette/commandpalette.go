package commandpalette

import (
	"os"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/tui/shared"
)

const (
	// maxVisible is the scroll window used for command selection.
	maxVisible = 10
	// maxInlineVisible keeps the inline palette compact above the message input.
	maxInlineVisible = 8
)

type Command struct {
	Name        string
	Display     string
	Description string
	Section     string
	RawInput    string
}

type Agent struct {
	ID     string
	Name   string
	Status string
}

type ExecuteCommandMsg struct {
	Name     string
	RawInput string
}

type Model struct {
	active       bool
	query        string
	commands     []Command
	agents       []Command
	filtered     []Command
	selected     int
	scrollOffset int
}

type paletteRow struct {
	section      string
	command      Command
	commandIndex int
	isSection    bool
}

var (
	paletteBg            = shared.ColorBg
	selectedBg           = lipgloss.Color("#312E81")
	paletteThinRailStyle = lipgloss.NewStyle().
				Foreground(shared.ColorMuted).
				Background(paletteBg)
	paletteSolidRailStyle = lipgloss.NewStyle().
				Foreground(shared.ColorMuted).
				Background(shared.ColorMuted)
	paletteBlockStyle = lipgloss.NewStyle().Background(paletteBg)
	selectedStyle     = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F472B6")).
				Background(selectedBg).
				Bold(true)
	selectedDescStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#9CA3AF")).
				Background(selectedBg)
	selectedPadStyle = lipgloss.NewStyle().Background(selectedBg)
	commandStyle     = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(paletteBg)
	descStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280")).
			Background(paletteBg)
	sectionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A78BFA")).
			Background(paletteBg).
			Bold(true)
	titleStyle = lipgloss.NewStyle().
			Foreground(shared.ColorPrimary).
			Background(paletteBg).
			Bold(true)
)

func New() Model {
	m := Model{
		active:       false,
		selected:     0,
		scrollOffset: 0,
	}
	m.rebuildCommands()
	m.applyFilter()
	return m
}

func (m Model) Active() bool { return m.active }

func (m *Model) SetQuery(value string) {
	m.query = normalizedInput(value)
	m.applyFilter()
}

func (m *Model) SetAgents(agents []Agent) {
	m.agents = nil
	for _, agent := range agents {
		name := strings.TrimSpace(agent.Name)
		if name == "" {
			continue
		}
		status := strings.TrimSpace(agent.Status)
		if status == "" {
			status = "agent"
		}
		m.agents = append(m.agents, Command{
			Name:        "switch",
			Display:     name,
			Description: status,
			Section:     "Agents",
			RawInput:    agentSelectValue(agent),
		})
	}
	m.rebuildCommands()
}

// Agents returns the agent entries currently loaded into the palette.
// Useful for tests and for callers that want to know which agents are
// available as quick-switch shortcuts.
func (m *Model) Agents() []Agent {
	seen := make(map[string]bool, len(m.agents))
	out := make([]Agent, 0, len(m.agents))
	for _, c := range m.agents {
		if seen[c.RawInput] {
			continue
		}
		seen[c.RawInput] = true
		out = append(out, Agent{ID: c.RawInput, Name: c.Display, Status: c.Description})
	}
	return out
}

func agentSelectValue(agent Agent) string {
	id := strings.TrimSpace(agent.ID)
	if id != "" {
		return id
	}
	return strings.TrimSpace(agent.Name)
}

func (m *Model) Activate() {
	m.active = true
	m.selected = 0
	m.scrollOffset = 0
	m.applyFilter()
}

func (m *Model) Deactivate() {
	m.active = false
	m.query = ""
	m.applyFilter()
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.active {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.Deactivate()
			return m, nil
		case "enter":
			if len(m.filtered) > 0 && m.selected >= 0 && m.selected < len(m.filtered) {
				selected := m.filtered[m.selected]
				cmdName := selected.Name
				rawInput := selected.RawInput
				if rawInput == "" {
					rawInput = m.query
				}
				m.Deactivate()
				return m, func() tea.Msg {
					return ExecuteCommandMsg{Name: cmdName, RawInput: rawInput}
				}
			}
			return m, nil
		case "up":
			if m.selected > 0 {
				m.selected--
			}
			m.clampScroll()
			return m, nil
		case "down":
			if m.selected < len(m.filtered)-1 {
				m.selected++
			}
			m.clampScroll()
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) rebuildCommands() {
	m.commands = append([]Command{}, m.agents...)
	m.commands = append(m.commands, defaultCommands()...)
}

func (m *Model) applyFilter() {
	value := strings.ToLower(m.query)
	words := strings.Fields(value)
	type scored struct {
		cmd   Command
		score int
	}
	var matches []scored
	for _, c := range m.commands {
		if score := matchScore(c, value, words); score > 0 {
			matches = append(matches, scored{cmd: c, score: score})
		}
	}
	slices.SortStableFunc(matches, func(a, b scored) int {
		return b.score - a.score
	})
	m.filtered = m.filtered[:0]
	for _, s := range matches {
		m.filtered = append(m.filtered, s.cmd)
	}
	if preferred := preferredCommandIndex(m.filtered, value, words); preferred >= 0 {
		m.selected = preferred
		m.clampScroll()
		return
	}
	if m.selected >= len(m.filtered) {
		m.selected = len(m.filtered) - 1
	}
	if m.selected < 0 && len(m.filtered) > 0 {
		m.selected = 0
	}
	m.clampScroll()
}

func preferredCommandIndex(commands []Command, value string, words []string) int {
	if value == "" || len(words) == 0 {
		return -1
	}
	firstWord := words[0]
	for i, c := range commands {
		if c.RawInput != "" {
			continue
		}
		name := strings.ToLower(c.Name)
		if firstWord == name || commandAliasExact(firstWord, commandAliases(name)) {
			return i
		}
	}
	return -1
}

func commandMatches(c Command, value string, words []string) bool {
	return matchScore(c, value, words) > 0
}

// matchScore ranks how well a command matches the query. Higher is better:
//   - 0: no match
//   - 1: only the description, display, or raw input contains the query
//   - 2: the command's name or one of its aliases starts with the first word
//   - 3: the first word equals the command's name or one of its aliases
//
// Name/alias prefix matches outrank description substring matches so that
// commands like "model" float to the top when the user types "mod", even
// though "trace" and "resize" also contain "mod" inside their "Toggle ... mode"
// descriptions.
func matchScore(c Command, value string, words []string) int {
	if value == "" {
		return 1
	}
	firstWord := ""
	if len(words) > 0 {
		firstWord = words[0]
	}
	name := strings.ToLower(c.Name)
	display := strings.ToLower(c.Display)
	desc := strings.ToLower(c.Description)
	rawInput := strings.ToLower(c.RawInput)
	aliases := commandAliases(name)

	if firstWord == name || commandAliasExact(firstWord, aliases) {
		return 3
	}
	if commandAliasMatches(firstWord, aliases) {
		return 2
	}
	if strings.HasPrefix(name, firstWord) && firstWord != "" {
		return 2
	}
	if strings.Contains(display, value) || strings.Contains(desc, value) || strings.Contains(rawInput, value) {
		return 1
	}
	return 0
}

func commandAliases(name string) []string {
	switch name {
	case "model":
		return []string{"models"}
	case "quit":
		return []string{"exit", "close"}
	default:
		return nil
	}
}

func commandAliasMatches(word string, aliases []string) bool {
	if word == "" {
		return false
	}
	for _, alias := range aliases {
		if strings.HasPrefix(alias, word) {
			return true
		}
	}
	return false
}

func commandAliasExact(word string, aliases []string) bool {
	if word == "" {
		return false
	}
	for _, alias := range aliases {
		if alias == word {
			return true
		}
	}
	return false
}

func defaultCommands() []Command {
	return []Command{
		{Name: "new", Description: "Create a new agent", Section: "Agent"},
		{Name: "rename", Description: "Rename selected agent", Section: "Agent"},
		{Name: "delete", Description: "Delete selected agent", Section: "Agent"},
		{Name: "copy", Description: "Copy latest response", Section: "Agent"},
		{Name: "view", Description: "Toggle browser visibility", Section: "View"},
		{Name: "hide", Description: "Hide all browser windows", Section: "View"},
		{Name: "log", Description: "Open selected session log", Section: "View"},
		{Name: "trace", Description: "Toggle trace mode", Section: "View"},
		{Name: "settings", Description: "Open settings panel", Section: "System"},
		{Name: "config", Description: "Reconfigure provider/model", Section: "System"},
		{Name: "model", Description: "Change provider/model", Section: "System"},
		{Name: "agents", Description: "Browse all agents", Section: "System"},
		{Name: "help", Description: "Show all commands", Section: "System"},
		{Name: "quit", Description: "Quit VulpineOS", Section: "System"},
	}
}

func normalizedInput(value string) string {
	value = strings.TrimSpace(value)
	return strings.TrimSpace(strings.TrimPrefix(value, "/"))
}

func (m *Model) clampScroll() {
	total := len(m.filtered)
	if total == 0 {
		m.scrollOffset = 0
		return
	}
	if m.selected < m.scrollOffset {
		m.scrollOffset = m.selected
	}
	if m.selected >= m.scrollOffset+maxVisible {
		m.scrollOffset = m.selected - maxVisible + 1
	}
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
}

func (m Model) InlineView(width, maxHeight int) string {
	if !m.active || maxHeight < 4 {
		return ""
	}

	boxWidth := width
	if boxWidth < 6 {
		return ""
	}
	innerWidth := boxWidth - 1
	if innerWidth < 1 {
		innerWidth = 1
	}

	rows := buildRows(m.filtered)
	totalRows := len(rows)
	visibleRows := maxHeight - 1 // title + command rows
	if visibleRows > maxInlineVisible {
		visibleRows = maxInlineVisible
	}
	if visibleRows < 0 {
		visibleRows = 0
	}
	if totalRows == 0 && visibleRows > 0 {
		visibleRows = 1
	} else if visibleRows > totalRows {
		visibleRows = totalRows
	}

	selectedRow := selectedRowIndex(rows, m.selected)
	start := m.scrollOffset
	if visibleRows > 0 && totalRows > 0 {
		if selectedRow < start {
			start = selectedRow
		}
		if selectedRow >= start+visibleRows {
			start = selectedRow - visibleRows + 1
		}
		if start < 0 {
			start = 0
		}
		if start+visibleRows > totalRows {
			start = totalRows - visibleRows
		}
		if start < 0 {
			start = 0
		}
	}
	end := start + visibleRows
	if end > totalRows {
		end = totalRows
	}

	listWidth := innerWidth - 1
	if listWidth < 1 {
		listWidth = innerWidth
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(fitPlain("Commands", innerWidth)))
	b.WriteString("\n")
	if totalRows == 0 && visibleRows > 0 {
		line := padPlain("No matching commands", listWidth)
		b.WriteString(descStyle.Render(line))
		if listWidth < innerWidth {
			b.WriteString(descStyle.Render(" "))
		}
		b.WriteString("\n")
	} else {
		for i := start; i < end; i++ {
			row := rows[i]
			scrollbar := scrollbarChar(i-start, start, visibleRows, totalRows)
			if row.isSection {
				b.WriteString(sectionStyle.Render(padPlain(row.section, listWidth)))
			} else {
				cmd := row.command
				prefix := "  "
				if row.commandIndex == m.selected {
					prefix = "> "
				}
				display := commandDisplay(cmd)
				b.WriteString(inlineCommandLine(prefix, display, cmd.Description, listWidth, row.commandIndex == m.selected))
			}
			if listWidth < innerWidth {
				b.WriteString(descStyle.Render(scrollbar))
			}
			b.WriteString("\n")
		}
	}

	return paletteShell(strings.TrimRight(b.String(), "\n"), boxWidth)
}

func paletteShell(content string, width int) string {
	if width < 2 {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = paletteShellLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func paletteShellLine(content string, width int) string {
	contentWidth := width - 1
	if contentWidth < 1 {
		return paletteRailCell()
	}
	content = fitStyled(content, contentWidth)
	padding := ""
	if contentWidth > lipgloss.Width(content) {
		padding = strings.Repeat(" ", contentWidth-lipgloss.Width(content))
	}
	return paletteRailCell() + content + paletteBlockStyle.Render(padding)
}

func paletteRailCell() string {
	if os.Getenv("TERM_PROGRAM") == "Apple_Terminal" {
		return paletteSolidRailStyle.Render(" ")
	}
	return paletteThinRailStyle.Render("▌")
}

func buildRows(commands []Command) []paletteRow {
	var rows []paletteRow
	lastSection := ""
	for i, command := range commands {
		section := command.Section
		if section == "" {
			section = "Commands"
		}
		if section != lastSection {
			rows = append(rows, paletteRow{section: section, isSection: true})
			lastSection = section
		}
		rows = append(rows, paletteRow{command: command, commandIndex: i})
	}
	return rows
}

func selectedRowIndex(rows []paletteRow, selected int) int {
	for i, row := range rows {
		if !row.isSection && row.commandIndex == selected {
			return i
		}
	}
	return 0
}

func scrollbarChar(row, start, visible, total int) string {
	if total <= visible || visible <= 0 {
		return " "
	}
	thumbHeight := visible * visible / total
	if thumbHeight < 1 {
		thumbHeight = 1
	}
	track := visible - thumbHeight
	thumbStart := 0
	if track > 0 {
		thumbStart = start * track / (total - visible)
	}
	if row >= thumbStart && row < thumbStart+thumbHeight {
		return "█"
	}
	return "│"
}

func commandDisplay(command Command) string {
	if command.Display != "" {
		return command.Display
	}
	return "/" + command.Name
}

func padPlain(text string, width int) string {
	text = fitPlain(text, width)
	for lipgloss.Width(text) < width {
		text += " "
	}
	return text
}

func inlineCommandLine(prefix, name, desc string, width int, selected bool) string {
	if width <= 0 {
		return ""
	}
	nameStyle := commandStyle
	descStyleForRow := descStyle
	padStyle := paletteBlockStyle
	if selected {
		nameStyle = selectedStyle
		descStyleForRow = selectedDescStyle
		padStyle = selectedPadStyle
	}

	nameWidth := lipgloss.Width(prefix) + lipgloss.Width(name)
	if nameWidth >= width {
		return nameStyle.Render(fitPlain(prefix+name, width))
	}
	availableDesc := width - nameWidth - 1
	if availableDesc < 0 {
		availableDesc = 0
	}
	desc = fitPlain(desc, availableDesc)
	pad := width - nameWidth - lipgloss.Width(desc)
	if pad < 1 {
		pad = 1
	}
	return nameStyle.Render(prefix+name) + padStyle.Render(strings.Repeat(" ", pad)) + descStyleForRow.Render(desc)
}

func fitPlain(text string, width int) string {
	if width <= 0 {
		return ""
	}
	for lipgloss.Width(text) > width {
		runes := []rune(text)
		if len(runes) == 0 {
			return ""
		}
		text = string(runes[:len(runes)-1])
	}
	return text
}

func fitStyled(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	fitted := lipgloss.NewStyle().MaxWidth(width).Render(text)
	lines := strings.Split(fitted, "\n")
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}
