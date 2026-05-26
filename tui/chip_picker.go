package tui

import "strings"

// chipPickerVisibleHeight returns the number of option rows that fit in the
// overlay. Capped at 12 so the ModeSessionSearch backdrop rendered beneath
// the overlay stays partially visible on tall terminals.
func chipPickerVisibleHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	// separator (1) + header (1) + trailing (1) + footer (2) = 5
	avail := m.height - 5
	if avail < 1 {
		return 1
	}
	if avail > 12 {
		avail = 12
	}
	return avail
}

// renderChipPicker renders the chip picker overlay for ModeSessionSearch.
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
	}
	sb.WriteString(styleHeader.Render(header))
	sb.WriteByte('\n')

	total := len(m.chipPickerOptions)
	if total == 0 {
		sb.WriteString(styleDim.Render("  (no options)"))
		sb.WriteByte('\n')
		return sb.String()
	}

	current := currentChipValue(m)
	height := chipPickerVisibleHeight(m)

	startIdx := 0
	endIdx := total
	showTop := false
	showBottom := false

	if height > 0 {
		offset := m.chipPickerOffset
		if offset < 0 {
			offset = 0
		}
		if offset > total {
			offset = total
		}
		startIdx = offset

		budget := height
		if startIdx > 0 {
			showTop = true
			budget--
		}
		if budget < 0 {
			budget = 0
		}
		endIdx = startIdx + budget
		if endIdx > total {
			endIdx = total
		}
		if endIdx < total {
			showBottom = true
			if budget > 0 {
				budget--
			}
			endIdx = startIdx + budget
			if endIdx > total {
				endIdx = total
			}
		}
	}

	if showTop {
		sb.WriteString(styleDim.Render("  ▲"))
		sb.WriteByte('\n')
	}

	for i := startIdx; i < endIdx; i++ {
		opt := m.chipPickerOptions[i]
		label := opt
		if opt == "" {
			label = "(any)"
		}
		line := "  " + label
		if opt == current {
			line += styleDim.Render(" (current)")
		}
		if i == m.chipPickerCursor {
			sb.WriteString(styleSelected.Render(line))
		} else {
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}

	if showBottom {
		sb.WriteString(styleDim.Render("  ▼"))
		sb.WriteByte('\n')
	}

	return sb.String()
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
		return singleChipValue(m.filterProject)
	}
	return ""
}
