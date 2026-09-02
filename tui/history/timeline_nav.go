package history

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

// timelineLines renders the timeline body at the overlay's current width.
// Every timeline geometry question below is answered from this one walk so
// scroll clamping and rendering can never disagree about the line count.
func (in DetailInput) timelineLines() []string {
	// Padding(0,1) in styleOverlayBox subtracts 2 cols from the render width.
	renderWidth := in.boxInnerWidth() - 2
	if renderWidth < 1 {
		renderWidth = 1
	}
	phases := groupIntoPhases(in.Events)
	return renderTimelineLines(phases, renderWidth, in.TimelineFold, in.ShowSuppressed, in.ShowTimestamps)
}

// TimelineVisibleHeight is the number of timeline rows that fit inside the
// overlay box once the shared detail chrome is charged. It is also the page
// size the root's pgup/pgdn handlers step by.
func (in DetailInput) TimelineVisibleHeight() int {
	visibleH := in.overlayInnerHeight() - detailInnerChrome
	if visibleH < 0 {
		visibleH = 0
	}
	return visibleH
}

// TimelineMaxScroll is the largest scroll offset that still fills the
// viewport, i.e. the upper bound the j/k handlers clamp against.
func (in DetailInput) TimelineMaxScroll() int {
	maxOffset := len(in.timelineLines()) - in.TimelineVisibleHeight()
	if maxOffset < 0 {
		maxOffset = 0
	}
	return maxOffset
}

// ClampTimelineScroll pins in.TimelineScroll to [0, TimelineMaxScroll].
// The root calls it after any change that affects the timeline line count
// (fold toggle, suppressed toggle).
func (in DetailInput) ClampTimelineScroll() int {
	maxOffset := in.TimelineMaxScroll()
	scroll := in.TimelineScroll
	if scroll > maxOffset {
		scroll = maxOffset
	}
	if scroll < 0 {
		scroll = 0
	}
	return scroll
}

// TimelinePrevPhaseScroll returns the scroll offset of the nearest phase
// header strictly above the current one, or the current offset when the
// cursor is already at or above the first header.
func (in DetailInput) TimelinePrevPhaseScroll() int {
	target := -1
	for _, pos := range in.phaseHeaderPositions() {
		if pos < in.TimelineScroll {
			target = pos
		}
	}
	if target < 0 {
		return in.TimelineScroll
	}
	return target
}

// TimelineNextPhaseScroll returns the scroll offset of the nearest phase
// header strictly below the current one, or the current offset when the
// cursor is already at the last header.
func (in DetailInput) TimelineNextPhaseScroll() int {
	for _, pos := range in.phaseHeaderPositions() {
		if pos > in.TimelineScroll {
			return pos
		}
	}
	return in.TimelineScroll
}

// TimelineToggleFold flips the fold state of the phase at the current scroll
// offset and returns the updated fold map. ok is false when no phase is
// foldable there — a gap, a compact separator, or an empty preamble — in which
// case the returned map is nil and the caller must leave its own state alone.
func (in DetailInput) TimelineToggleFold() (fold map[int]bool, ok bool) {
	phases := groupIntoPhases(in.Events)
	positions := phaseHeaderLinePositions(phases, in.TimelineFold, in.ShowSuppressed)
	idx := phaseAtScroll(positions, in.TimelineScroll)
	if idx < 0 {
		return nil, false
	}
	ph := phases[idx]
	if ph.kind == "gap" || ph.kind == "separator" || (ph.kind == "preamble" && len(ph.events) == 0) {
		return nil, false
	}
	fold = make(map[int]bool, len(in.TimelineFold)+1)
	for k, v := range in.TimelineFold {
		fold[k] = v
	}
	fold[idx] = !isPhaseExpanded(ph, idx, in.TimelineFold)
	return fold, true
}

// TimelineFoldAll returns a fold map that sets every content phase to the
// supplied expanded state. Gaps and separators are omitted because they carry
// no fold state of their own.
func (in DetailInput) TimelineFoldAll(expanded bool) map[int]bool {
	phases := groupIntoPhases(in.Events)
	fold := make(map[int]bool, len(phases))
	for i, p := range phases {
		if p.kind != "gap" && p.kind != "separator" {
			fold[i] = expanded
		}
	}
	return fold
}

// phaseHeaderPositions is the line offset of each phase header at the input's
// current fold and suppression state.
func (in DetailInput) phaseHeaderPositions() []int {
	return phaseHeaderLinePositions(groupIntoPhases(in.Events), in.TimelineFold, in.ShowSuppressed)
}
