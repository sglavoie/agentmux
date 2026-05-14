package tui

import (
	"fmt"
	"strings"
)

// renderSearch renders the search bar header (shown when in ModeSearch).
func renderSearch(m Model) string {
	prompt := styleSearch.Render("/") + m.searchText + "█"
	return prompt + "\n"
}

// renderAccountFilter renders the account picker header (shown when in ModeAccountFilter).
func renderAccountFilter(m Model) string {
	var sb strings.Builder
	sb.WriteString(styleHeader.Render("Filter by account:") + "\n")

	// "All" option at index 0.
	allLabel := "  All"
	if m.acctCursor == 0 {
		allLabel = styleSelected.Render("> All")
	}
	sb.WriteString(allLabel + "\n")

	for i, name := range m.accounts {
		label := fmt.Sprintf("  %s", name)
		if m.acctCursor == i+1 {
			label = styleSelected.Render(fmt.Sprintf("> %s", name))
		}
		sb.WriteString(label + "\n")
	}

	return sb.String()
}
