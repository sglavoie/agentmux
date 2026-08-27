package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/kittykeys"
)

// keybindingsModel is a minimal Bubble Tea model for the keybindings overlay.
type keybindingsModel struct{}

func (m keybindingsModel) Init() tea.Cmd { return nil }

func (m keybindingsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		return m, tea.Quit
	}
	return m, nil
}

func (m keybindingsModel) View() tea.View {
	var sb strings.Builder

	divider := strings.Repeat("─", 42)

	sb.WriteString(styleHeader.Render("Keybinding Reference") + "\n")
	sb.WriteString(styleDim.Render(divider) + "\n")

	// Group actions by section, preserving declaration order.
	seen := make(map[string]bool)
	var sections []string
	for _, a := range kittykeys.Actions {
		if !seen[a.Section] {
			seen[a.Section] = true
			sections = append(sections, a.Section)
		}
	}

	for _, sec := range sections {
		sb.WriteByte('\n')
		sb.WriteString(styleHeader.Render("  "+sec) + "\n")
		for _, a := range kittykeys.Actions {
			if a.Section != sec {
				continue
			}
			key := a.Key
			padding := 16 - displayWidth(key)
			if padding < 1 {
				padding = 1
			}
			line := "    " + key + strings.Repeat(" ", padding) + a.Description
			sb.WriteString(styleDim.Render(line) + "\n")
		}
	}

	sb.WriteByte('\n')
	sb.WriteString(styleDim.Render(divider) + "\n")
	sb.WriteString("  " + styleFooter.Render("press ") + renderFooterBindingWithLabel("any key", " ", "to close", styleFooter) + "\n")

	return tea.NewView(sb.String())
}

// RunKeybindingsOverlay renders the keybinding reference and blocks until the
// user dismisses it. Designed to run inside a Kitty overlay window.
func RunKeybindingsOverlay() error {
	p := tea.NewProgram(keybindingsModel{}, tea.WithoutSignalHandler())
	_, err := p.Run()
	return err
}
