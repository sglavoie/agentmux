package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// ColorPickerContext holds the inputs needed by the color picker overlay.
type ColorPickerContext struct {
	Client       kitty.KittyClient
	TabID        int
	WindowID     int
	DefaultColor string
}

type colorPickerItem struct {
	name    string
	emoji   string
	isReset bool
}

type colorPickerModel struct {
	ctx    ColorPickerContext
	cursor int
	items  []colorPickerItem
	err    error
	applied bool
}

var colorPickerOrder = []string{
	"green", "blue", "red", "orange", "purple",
	"teal", "pink", "yellow", "gray", "cyan",
}

func newColorPickerModel(ctx ColorPickerContext) colorPickerModel {
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
	items = append(items, colorPickerItem{name: "reset to default", emoji: "↩", isReset: true})
	return colorPickerModel{ctx: ctx, cursor: initialCursor, items: items}
}

func (m colorPickerModel) Init() tea.Cmd { return nil }

func (m colorPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
	return m, nil
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

	err = m.ctx.Client.SetTabColor(
		kitty.MatchTabByID(m.ctx.TabID),
		c.ActiveBg, c.InactiveBg,
		palette.ActiveFg, palette.InactiveFg,
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

	sb.WriteString(styleHeader.Render("Set Tab Color") + "\n")
	sb.WriteString(styleDim.Render(divider) + "\n")

	for i, item := range m.items {
		if item.isReset {
			sb.WriteString(styleDim.Render(divider) + "\n")
		}

		prefix := "  "
		suffix := ""

		if !item.isReset {
			c, err := palette.Lookup(item.name)
			if err == nil {
				colorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(c.ForegroundHex()))
				label := fmt.Sprintf("%s  %s  %s", prefix, item.emoji, colorStyle.Render(item.name))
				if item.name == m.ctx.DefaultColor {
					suffix = "  ←"
				}
				if i == m.cursor {
					sb.WriteString(styleSelected.Render(label+suffix) + "\n")
				} else {
					sb.WriteString(label + suffix + "\n")
				}
				continue
			}
		}

		label := fmt.Sprintf("%s  %s  %s", prefix, item.emoji, item.name)
		if i == m.cursor {
			sb.WriteString(styleSelected.Render(label+suffix) + "\n")
		} else {
			sb.WriteString(label + suffix + "\n")
		}
	}

	sb.WriteString(styleDim.Render(divider) + "\n")

	if m.err != nil {
		sb.WriteString(styleError.Render(m.err.Error()) + "\n")
	}

	sb.WriteString(styleDim.Render("j/k navigate · enter apply · esc cancel") + "\n")

	return tea.NewView(sb.String())
}

// RunColorPickerOverlay renders the color picker and blocks until the user
// dismisses it. Designed to run inside a Kitty overlay window.
func RunColorPickerOverlay(ctx ColorPickerContext) error {
	p := tea.NewProgram(newColorPickerModel(ctx), tea.WithoutSignalHandler())
	_, err := p.Run()
	return err
}
