package tui

// renderSearch renders the filter input bar (shown when in ModeSearch) and
// the sigil-aware completion popup when it is open. The DSL is documented in
// tui/filter.go.
func renderSearch(m Model) string {
	prompt := styleSearch.Render("/") + m.filterText + "█"
	out := prompt + "\n"
	if popup := renderCompletion(m); popup != "" {
		out += popup
	}
	return out
}
