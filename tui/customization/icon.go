package customization

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// IconContext holds the inputs needed by the harness icon picker overlay.
type IconContext struct {
	Client        kitty.KittyClient
	TabID         int
	CurrentTitle  string
	HarnessEmojis map[string]string
}

type iconPickerItem struct {
	name  string
	emoji string
}

type iconPickerModel struct {
	ctx     IconContext
	cursor  int
	items   []iconPickerItem
	err     error
	applied bool
}

var errNoHarnessIcons = errors.New("no harness icons configured in [harness_emojis]")

func newIconPickerModel(ctx IconContext) (iconPickerModel, error) {
	items := make([]iconPickerItem, 0, len(harness.Names())+1)
	for _, name := range harness.Names() {
		emoji := ctx.HarnessEmojis[name]
		if emoji == "" {
			continue
		}
		items = append(items, iconPickerItem{name: name, emoji: emoji})
	}
	if len(items) == 0 {
		return iconPickerModel{}, errNoHarnessIcons
	}
	items = append(items, iconPickerItem{name: "None (clear icon)"})

	base := palette.ParseTitleParts(ctx.CurrentTitle).Base
	cursor := 0
	for i, item := range items {
		if item.emoji != "" && strings.HasPrefix(base, item.emoji) {
			cursor = i
			break
		}
	}

	return iconPickerModel{ctx: ctx, cursor: cursor, items: items}, nil
}

func (m iconPickerModel) Init() tea.Cmd { return nil }

func (m iconPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "q", "esc":
		return m, tea.Quit
	case "j", "down":
		m.cursor = (m.cursor + 1) % len(m.items)
	case "k", "up":
		m.cursor = (m.cursor - 1 + len(m.items)) % len(m.items)
	case "enter":
		if m.apply() {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *iconPickerModel) apply() bool {
	if m.ctx.Client == nil {
		m.err = errors.New("set tab title: Kitty client is unavailable")
		return false
	}

	currentTitle := m.ctx.CurrentTitle
	if osWindows, err := m.ctx.Client.ListWindows(); err == nil {
		for i := range osWindows {
			for j := range osWindows[i].Tabs {
				tab := &osWindows[i].Tabs[j]
				if tab.ID == m.ctx.TabID {
					currentTitle = tab.Title
					m.ctx.CurrentTitle = currentTitle
				}
			}
		}
	}

	parts := palette.ParseTitleParts(currentTitle)
	parts.Base = removeConfiguredHarnessPrefix(parts.Base, m.ctx.HarnessEmojis)
	parts.Base = m.items[m.cursor].emoji + parts.Base
	title := palette.BuildTitleParts(parts)

	if err := m.ctx.Client.SetTabTitle(kitty.MatchTabByID(m.ctx.TabID), title); err != nil {
		m.err = fmt.Errorf("set tab title: %w", err)
		return false
	}

	m.ctx.CurrentTitle = title
	m.err = nil
	m.applied = true
	return true
}

func removeConfiguredHarnessPrefix(base string, emojis map[string]string) string {
	for _, prefix := range configuredHarnessPrefixes(emojis) {
		if strings.HasPrefix(base, prefix) {
			return base[len(prefix):]
		}
	}
	return base
}

func configuredHarnessPrefixes(emojis map[string]string) []string {
	prefixes := make([]string, 0, len(harness.Names()))
	seen := make(map[string]struct{}, len(harness.Names()))
	for _, name := range harness.Names() {
		prefix := emojis[name]
		if prefix == "" {
			continue
		}
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		if len(prefixes[i]) != len(prefixes[j]) {
			return len(prefixes[i]) > len(prefixes[j])
		}
		return prefixes[i] < prefixes[j]
	})
	return prefixes
}

func (m iconPickerModel) View() tea.View {
	var sb strings.Builder
	divider := strings.Repeat("─", 30)

	sb.WriteString(styleHeader.Render("Set Harness Icon") + "\n")
	sb.WriteString(styleDim.Render(divider) + "\n")
	for i, item := range m.items {
		label := fmt.Sprintf("  %s  %s", item.emoji, item.name)
		if i == m.cursor {
			sb.WriteString(renderSelectedRow(label) + "\n")
		} else {
			sb.WriteString(label + "\n")
		}
	}
	sb.WriteString(styleDim.Render(divider) + "\n")
	if m.err != nil {
		sb.WriteString(styleError.Render(m.err.Error()) + "\n")
	}
	sb.WriteString(joinFooterBindings(" · ", renderFooterBinding("j/k", "navigate"), renderFooterBinding("enter", "apply"), renderFooterBinding("esc", "cancel")) + "\n")

	return tea.NewView(sb.String())
}

// RunIconOverlay renders the harness icon picker and blocks until the
// user dismisses it. It is designed to run inside a Kitty overlay window.
func RunIconOverlay(ctx IconContext) error {
	model, err := newIconPickerModel(ctx)
	if err != nil {
		return err
	}
	p := tea.NewProgram(model, tea.WithoutSignalHandler())
	_, err = p.Run()
	return err
}
