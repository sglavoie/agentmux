package tui

// timelinePhaseExpanded reports whether phase i should render expanded.
// Explicit fold entries take precedence; the default expands phases with ≤5
// events or the last content phase.
func timelinePhaseExpanded(idx int, phase timelinePhase, fold map[int]bool, lastContentIdx int) bool {
	if fold != nil {
		if v, ok := fold[idx]; ok {
			return v
		}
	}
	return len(phase.events) <= 5 || idx == lastContentIdx
}

// phaseHeaderLinePositions returns the starting line index (0-based) of each
// phase in the pre-rendered output from renderTimelineLines. The slice has
// one entry per phase in phases. Used by [ / ] navigation to jump between
// phase headers and by enter to identify which phase is at the scroll position.
func phaseHeaderLinePositions(phases []timelinePhase, fold map[int]bool, showSuppressed bool) []int {
	lastContentIdx := -1
	for i, p := range phases {
		if p.kind != "gap" && p.kind != "separator" {
			lastContentIdx = i
		}
	}

	positions := make([]int, len(phases))
	line := 0
	for i, phase := range phases {
		positions[i] = line
		switch phase.kind {
		case "preamble":
			if len(phase.events) > 0 {
				line++
			}
		case "gap", "separator":
			line++
		default:
			expanded := timelinePhaseExpanded(i, phase, fold, lastContentIdx)
			line += len(renderPhaseBlock(phase, expanded, showSuppressed, false))
		}
	}
	return positions
}

// isPhaseExpanded reports the current expanded state for a phase, consulting
// the fold map before falling back to the default (≤5 events → expanded).
func isPhaseExpanded(phase timelinePhase, idx int, fold map[int]bool) bool {
	if fold != nil {
		if v, ok := fold[idx]; ok {
			return v
		}
	}
	return len(phase.events) <= 5
}

// phaseAtScroll returns the index of the phase whose header is at or just
// above the supplied scroll offset. Returns -1 when positions is empty.
func phaseAtScroll(positions []int, scroll int) int {
	result := -1
	for i, pos := range positions {
		if pos <= scroll {
			result = i
		}
	}
	return result
}

// clampTimelineScroll pins m.detailTimelineScroll to [0, max(0, totalLines-visibleH)].
// Must be called after any change that affects the timeline line count
// (fold toggle, suppressed toggle).
func clampTimelineScroll(m *Model, phases []timelinePhase) {
	innerWidth := detailBoxInnerWidth(*m)
	renderWidth := innerWidth - 2
	if renderWidth < 1 {
		renderWidth = 1
	}
	lines := renderTimelineLines(phases, renderWidth, m.detailTimelineFold, m.detailShowSuppressed, m.detailShowTimestamps)
	innerH := detailOverlayInnerHeight(*m)
	visibleH := innerH - detailInnerChrome
	if visibleH < 0 {
		visibleH = 0
	}
	maxOffset := len(lines) - visibleH
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.detailTimelineScroll > maxOffset {
		m.detailTimelineScroll = maxOffset
	}
	if m.detailTimelineScroll < 0 {
		m.detailTimelineScroll = 0
	}
}
