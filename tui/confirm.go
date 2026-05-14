package tui

import "fmt"

// renderConfirm renders the inline y/n confirmation prompt.
func renderConfirm(m Model) string {
	prompt := fmt.Sprintf("  %s %q? (y/n)", m.confirmAction, m.actionTarget.Base)
	return styleHeader.Render(prompt) + "\n"
}
