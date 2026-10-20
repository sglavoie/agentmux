package tui

import (
	"strings"

	"github.com/sglavoie/agentmux/tui/layout"
)

// chipPickerVisibleHeight returns the number of option rows that fit in the
// overlay. Capped at 12 so the owning search backdrop rendered beneath the
// overlay stays partially visible on tall terminals.
func chipPickerVisibleHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	// Box chrome: top border (1) + bottom border (1) + header (1) + safety
	// margin (2) = 5. A picker also spends one row on the transient search
	// prompt once search has started. The outer footer/status rows are
	// already excluded by rootBodyHeight.
	chrome := 5
	if chipPickerSearchVisible(m) {
		chrome++
	}
	avail := rootBodyHeight(m) - chrome
	if avail < 1 {
		return 1
	}
	if avail > 12 {
		avail = 12
	}
	return avail
}

// renderChipPicker renders the chip picker overlay for a search view.
// The two-pass ▲/▼ budget logic mirrors renderSessionSearchList so the
// cursor stays inside the visible window on small terminals.
func renderChipPicker(m Model) string {
	var sb strings.Builder

	header := "Choose harness"
	switch m.chipPickerKind {
	case chipPickerProfile:
		header = "Choose profile"
	case chipPickerProject:
		header = "Choose project"
	case chipPickerMessageSource:
		header = "Choose message source"
	}
	sb.WriteString(styleHeader.Render(header))
	sb.WriteString(styleDim.Render("  / to filter"))
	sb.WriteByte('\n')

	if chipPickerSearchVisible(m) {
		sb.WriteString(renderChipPickerSearch(m))
		sb.WriteByte('\n')
	}

	total := len(m.chipPickerOptions)
	if total == 0 {
		if chipPickerSearchVisible(m) {
			// The overlay box wraps a line wider than itself, so the hint is
			// never cut off mid-key.
			sb.WriteString(styleDim.Render("  " + layout.Placeholder(layout.NoMatchText(chipPickerPluralLabel(m.chipPickerKind), layout.SearchEditHint(m.chipPickerSearchMode)))))
		} else {
			sb.WriteString(styleDim.Render("  (no options)"))
		}
		sb.WriteByte('\n')
		return sb.String()
	}

	current := currentChipValue(m)
	height := chipPickerVisibleHeight(m)

	window := layout.ListWindowFor(total, m.chipPickerOffset, height)

	if window.ShowTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	for i := window.Start; i < window.End; i++ {
		opt := m.chipPickerOptions[i]
		label := chipPickerOptionLabel(opt)
		if opt == "" {
			label = "(any)"
		}
		line := "  " + label
		if opt == current {
			line += styleDim.Render(" (current)")
		}
		if i == m.chipPickerCursor {
			sb.WriteString(renderSelectedRow(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}

	if window.ShowBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}

	return sb.String()
}

func chipPickerSearchVisible(m Model) bool {
	return m.chipPickerSearchMode || m.chipPickerQuery != ""
}

func renderChipPickerSearch(m Model) string {
	line := styleSearch.Render("/") + m.chipPickerQuery
	if m.chipPickerSearchMode {
		line += "█"
	}
	return line
}

func chipPickerPluralLabel(kind chipPickerKind) string {
	switch kind {
	case chipPickerHarness:
		return "harnesses"
	case chipPickerProfile:
		return "profiles"
	case chipPickerProject:
		return "projects"
	case chipPickerMessageSource:
		return "message sources"
	}
	return "options"
}

// renderChipPickerBox wraps renderChipPicker in styleOverlayBox so the chip
// picker can be composited as a centered overlay on top of the session
// search backdrop. The inner width is capped at 50 columns for visual
// readability on wide terminals and clamped to m.width-4 on narrow ones.
func renderChipPickerBox(m Model) string {
	innerWidth := 50
	if avail := m.width - 4; avail < innerWidth {
		innerWidth = avail
	}
	if innerWidth < 1 {
		innerWidth = 1
	}
	return styleOverlayBox.Width(innerWidth).Render(strings.TrimRight(renderChipPicker(m), "\n"))
}

// currentChipValue returns the active filter value for the kind currently
// being edited, or "" when no filter is set.
func currentChipValue(m Model) string {
	switch m.chipPickerKind {
	case chipPickerHarness:
		return singleChipValue(m.filterHarness)
	case chipPickerProfile:
		return singleChipValue(m.filterProfile)
	case chipPickerProject:
		if chipPickerSourceMode(m) == ModePlanSearch {
			return planProjectFilterOption(m.planFilterProject)
		}
		return singleChipValue(m.filterProject)
	case chipPickerMessageSource:
		return m.filterMessageSource
	}
	return ""
}

// singleChipValue returns the one active value of a multi-select filter, or
// the empty string when the filter is unset. Chip filters are single-valued in
// practice; the slice shape is what the model stores.
func singleChipValue(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
