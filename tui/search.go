package tui

// renderSearch renders the filter input bar (shown when in ModeSearch).
// The DSL is documented in tui/filter.go.
func renderSearch(m Model) string {
	prompt := styleSearch.Render("/") + m.filterText + "█"
	return prompt + "\n"
}
