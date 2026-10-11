package customization

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/tui/layout"
)

// ColorContext holds the inputs needed by the color picker overlay.
type ColorContext struct {
	Client       kitty.KittyClient
	TabID        int
	WindowID     int
	DefaultColor string
	// Apply, when set, records the pick somewhere other than a Kitty tab: the
	// picker then never touches Client. colorName is empty for "reset to
	// default", which is the absence of a pick.
	Apply func(colorName string) error
	// Subject names what is being colored in the header; empty means a tab.
	Subject string
}

type colorPickerItem struct {
	name    string
	emoji   string
	isReset bool
}

type colorPickerModel struct {
	ctx     ColorContext
	cursor  int
	offset  int
	items   []colorPickerItem
	height  int
	err     error
	applied bool
}

var colorPickerOrder = []string{
	"green", "blue", "red", "orange", "purple",
	"teal", "pink", "yellow", "gray", "cyan",
}

func newColorPickerModel(ctx ColorContext) colorPickerModel {
	items := make([]colorPickerItem, 0, len(colorPickerOrder)+1)
	initialCursor := 0
	for i, name := range colorPickerOrder {
		c, err := palette.Lookup(name)
		emoji := "?"
		if err == nil {
			emoji = c.Emoji
		}
		items = append(items, colorPickerItem{name: name, emoji: emoji})
		if name == ctx.DefaultColor {
			initialCursor = i
		}
	}
	resetLabel := fmt.Sprintf("reset to default (%s)", ctx.DefaultColor)
	items = append(items, colorPickerItem{name: resetLabel, emoji: "↩", isReset: true})
	return colorPickerModel{ctx: ctx, cursor: initialCursor, items: items}
}

func (m colorPickerModel) Init() tea.Cmd { return nil }

func (m colorPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "esc":
			return m, tea.Quit
		case "j", "down":
			m.cursor = (m.cursor + 1) % len(m.items)
		case "k", "up":
			m.cursor = (m.cursor - 1 + len(m.items)) % len(m.items)
		case "enter":
			m.apply()
			return m, tea.Quit
		}
	}
	m.scrollToCursor()
	return m, nil
}

// header + top divider + scroll-up indicator + scroll-down indicator +
// reset divider + reset item + bottom divider + footer = 8.
const colorPickerChrome = 8

func (m *colorPickerModel) visibleColorCount() int {
	avail := m.height - colorPickerChrome
	if m.err != nil {
		avail--
	}
	if avail < 1 {
		avail = 1
	}
	if avail > len(colorPickerOrder) {
		avail = len(colorPickerOrder)
	}
	return avail
}

func (m *colorPickerModel) scrollToCursor() {
	if m.height == 0 {
		return
	}
	vis := m.visibleColorCount()
	if m.cursor < len(colorPickerOrder) {
		if m.cursor < m.offset {
			m.offset = m.cursor
		}
		if m.cursor >= m.offset+vis {
			m.offset = m.cursor - vis + 1
		}
	} else {
		if m.offset+vis > len(colorPickerOrder) {
			m.offset = len(colorPickerOrder) - vis
		}
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *colorPickerModel) apply() {
	colorName := m.ctx.DefaultColor
	if !m.items[m.cursor].isReset {
		colorName = m.items[m.cursor].name
	}

	c, err := palette.Lookup(colorName)
	if err != nil {
		m.err = fmt.Errorf("lookup %q: %w", colorName, err)
		return
	}
	if m.ctx.Apply != nil {
		picked := colorName
		if m.items[m.cursor].isReset {
			picked = ""
		}
		if err := m.ctx.Apply(picked); err != nil {
			m.err = fmt.Errorf("set color: %w", err)
			return
		}
		m.applied = true
		return
	}

	err = m.ctx.Client.SetTabColor(
		kitty.MatchTabByID(m.ctx.TabID),
		kitty.TabColorDefault, c.InactiveBg,
		kitty.TabColorDefault, palette.InactiveFg,
	)
	if err != nil {
		m.err = fmt.Errorf("set tab color: %w", err)
		return
	}

	if m.ctx.WindowID != 0 {
		err = m.ctx.Client.SetUserVars(
			kitty.MatchWindowByID(m.ctx.WindowID),
			map[string]string{"cc_color": colorName},
		)
		if err != nil {
			m.err = fmt.Errorf("set user vars: %w", err)
			return
		}
	}

	m.applied = true
}

func (m colorPickerModel) View() tea.View {
	var sb strings.Builder

	divider := strings.Repeat("─", 30)

	header := "Set Tab Color"
	if m.ctx.Subject != "" {
		header = "Set Color: " + m.ctx.Subject
	}
	sb.WriteString(styleHeader.Render(header) + "\n")
	sb.WriteString(styleDim.Render(divider) + "\n")

	maxNameLen := 0
	for _, name := range colorPickerOrder {
		if w := layout.Width(name); w > maxNameLen {
			maxNameLen = w
		}
	}

	vis := m.visibleColorCount()
	end := m.offset + vis
	if end > len(colorPickerOrder) {
		end = len(colorPickerOrder)
	}

	if m.offset > 0 {
		sb.WriteString(styleDim.Render("      ▲") + "\n")
	} else {
		sb.WriteString("\n")
	}

	for i := m.offset; i < end; i++ {
		item := m.items[i]
		prefix := "  "
		suffix := ""
		c, err := palette.Lookup(item.name)
		if err == nil {
			padded := layout.PadRight(item.name, maxNameLen)
			colorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(c.ForegroundHex()))
			label := fmt.Sprintf("%s  %s  %s", prefix, item.emoji, colorStyle.Render(padded))
			if item.name == m.ctx.DefaultColor {
				suffix = " ←"
			}
			if i == m.cursor {
				sb.WriteString(renderSelectedRow(label+suffix) + "\n")
			} else {
				sb.WriteString(label + suffix + "\n")
			}
		}
	}

	if end < len(colorPickerOrder) {
		sb.WriteString(styleDim.Render("      ▼") + "\n")
	} else {
		sb.WriteString("\n")
	}

	resetItem := m.items[len(m.items)-1]
	sb.WriteString(styleDim.Render(divider) + "\n")
	resetLabel := fmt.Sprintf("  %s  %s", resetItem.emoji, resetItem.name)
	if m.cursor == len(m.items)-1 {
		sb.WriteString(renderSelectedRow(resetLabel) + "\n")
	} else {
		sb.WriteString(resetLabel + "\n")
	}

	sb.WriteString(styleDim.Render(divider) + "\n")

	if m.err != nil {
		sb.WriteString(styleError.Render(m.err.Error()) + "\n")
	}

	sb.WriteString(joinFooterBindings(" · ", renderFooterBinding("j/k", "navigate"), renderFooterBinding("enter", "apply"), renderFooterBinding("esc", "cancel")) + "\n")

	return tea.NewView(sb.String())
}

// RunColorOverlay renders the color picker and blocks until the user
// dismisses it. Designed to run inside a Kitty overlay window.
func RunColorOverlay(ctx ColorContext) error {
	p := tea.NewProgram(newColorPickerModel(ctx), tea.WithoutSignalHandler())
	_, err := p.Run()
	return err
}
