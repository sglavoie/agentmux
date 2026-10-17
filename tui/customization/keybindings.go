package customization

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/kittykeys"
	"github.com/sglavoie/agentmux/tui/layout"
	"github.com/sglavoie/agentmux/tui/theme"
)

// keybindingsModel is a minimal Bubble Tea model for the keybindings overlay.
type keybindingsModel struct {
	width  int
	height int
	offset int
	// actions is what the reference lists; nil is Kitty's own bindings.
	actions []kittykeys.Action
}

func (m keybindingsModel) listed() []kittykeys.Action {
	if m.actions != nil {
		return m.actions
	}
	return kittykeys.Actions
}

func (m keybindingsModel) Init() tea.Cmd {
	theme.ConfigureFromEnvironment()
	return tea.RequestBackgroundColor
}

func (m keybindingsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		theme.ObserveBackground(msg.IsDark())
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			m.offset++
		case "k", "up":
			m.offset--
		case "g":
			m.offset = 0
		case "G":
			m.offset = m.maxOffset()
		}
	}
	m.clampOffset()
	return m, nil
}

const (
	keybindingsDefaultWidth = 42
	// Title, top divider, two scroll indicators, bottom divider, and footer.
	keybindingsChrome = 6
)

func (m keybindingsModel) contentWidth() int {
	if m.width > 0 {
		return m.width
	}
	return keybindingsDefaultWidth
}

func (m keybindingsModel) visibleBodyRows(total int) int {
	if m.height <= 0 {
		return total
	}
	visible := m.height - keybindingsChrome
	if visible < 1 {
		visible = 1
	}
	if visible > total {
		visible = total
	}
	return visible
}

func (m keybindingsModel) maxOffset() int {
	rows := m.bodyRows()
	maxOffset := len(rows) - m.visibleBodyRows(len(rows))
	if maxOffset < 0 {
		return 0
	}
	return maxOffset
}

func (m *keybindingsModel) clampOffset() {
	if m.offset < 0 {
		m.offset = 0
	}
	if maxOffset := m.maxOffset(); m.offset > maxOffset {
		m.offset = maxOffset
	}
}

func (m keybindingsModel) bodyRows() []string {
	width := m.contentWidth()
	var logical []string

	// Group actions by section, preserving declaration order.
	seen := make(map[string]bool)
	var sections []string
	for _, a := range m.listed() {
		if !seen[a.Section] {
			seen[a.Section] = true
			sections = append(sections, a.Section)
		}
	}

	for _, sec := range sections {
		if len(logical) > 0 {
			logical = append(logical, "")
		}
		logical = append(logical, styleHeader.Render("  "+sec))
		for _, a := range m.listed() {
			if a.Section != sec {
				continue
			}
			key := a.Key
			padding := 16 - layout.Width(key)
			if padding < 1 {
				padding = 1
			}
			line := styleFooterKey.Render("    "+key) + styleDim.Render(strings.Repeat(" ", padding)+a.Description)
			logical = append(logical, line)
		}
	}

	rows := make([]string, 0, len(logical))
	for _, line := range logical {
		rows = append(rows, layout.StyledWrapHard(line, width)...)
	}
	return rows
}

func (m keybindingsModel) View() tea.View {
	var sb strings.Builder
	width := m.contentWidth()
	dividerWidth := width
	if dividerWidth > keybindingsDefaultWidth {
		dividerWidth = keybindingsDefaultWidth
	}
	if dividerWidth < 1 {
		dividerWidth = 1
	}
	divider := strings.Repeat("─", dividerWidth)

	title := layout.Truncate("Keybinding Reference", width, "…")
	sb.WriteString(styleHeader.Render(title) + "\n")
	sb.WriteString(styleDim.Render(divider) + "\n")

	rows := m.bodyRows()
	visible := m.visibleBodyRows(len(rows))
	end := m.offset + visible
	if end > len(rows) {
		end = len(rows)
	}

	if m.offset > 0 {
		sb.WriteString(styleDim.Render("▲") + "\n")
	} else {
		sb.WriteByte('\n')
	}
	for _, row := range rows[m.offset:end] {
		sb.WriteString(row + "\n")
	}
	if end < len(rows) {
		sb.WriteString(styleDim.Render("▼") + "\n")
	} else {
		sb.WriteByte('\n')
	}

	sb.WriteString(styleDim.Render(divider) + "\n")
	footer := joinFooterBindings(" · ", renderFooterBinding("j/k", "scroll"), renderFooterBinding("g/G", "top/bottom"), renderFooterBinding("q/esc", "close"))
	sb.WriteString(layout.StyledTruncate(footer, width, "…"))

	view := tea.NewView(sb.String())
	view.AltScreen = true // Keep the title anchored to the overlay's first row.
	return view
}

// RunKeybindingsOverlay renders the keybinding reference and blocks until the
// user dismisses it. Designed to run inside a Kitty overlay window.
func RunKeybindingsOverlay() error { return RunKeybindingsReference(nil) }

// RunKeybindingsReference renders a reference of actions, or of Kitty's own
// bindings when actions is nil, and blocks until the user dismisses it.
func RunKeybindingsReference(actions []kittykeys.Action) error {
	p := tea.NewProgram(keybindingsModel{actions: actions}, tea.WithoutSignalHandler())
	_, err := p.Run()
	return err
}
