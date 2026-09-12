// Package theme holds the fixed dark-terminal palette shared by Agent Mux views.
// It does not color the terminal canvas or captured agent output.
package theme

import "charm.land/lipgloss/v2"

const (
	Primary   = "#4EC9B0"
	Secondary = "#7FAEDB"
	Text      = "#F2F4F7"
	Muted     = "#A5B1BE"
	Disabled  = "#8694A2"
	Selection = "#1D3F3C"
	Surface   = "#1B222B"
	Divider   = "#52677C"
	Footer    = "#252D36"
	Amber     = "#FFAA00"
	Red       = "#FF5555"
)

// Selected applies a single, opaque highlight to a normalized row. Callers
// strip embedded ANSI before rendering so inner resets cannot cut the span.
func Selected() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(Text)).Background(lipgloss.Color(Selection)).Bold(true)
}

// Blurred keeps the cursor visible when another pane owns keyboard focus.
func Blurred() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(Muted)).Background(lipgloss.Color(Surface)).Bold(true)
}

func Popup() lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(Secondary)).
		Background(lipgloss.Color(Surface)).Padding(0, 1)
}
