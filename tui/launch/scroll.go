package launch

// minVisibleRows floors the launcher's row-line budget so a degenerate
// terminal never yields a zero or negative window. It duplicates the root
// list's floor rather than importing it: both are the same number for the same
// reason, but the launcher owns its own windowing.
const minVisibleRows = 3

// internalChrome is the number of vertical lines the launcher emits before the
// first row inside RenderList. In the empty-filter nav state the filter hint
// lives in the header, so the chrome is header + blank spacer. Once search
// input is visible, the prompt gets its own line before that blank.
func (in PickerInput) internalChrome() int {
	if in.SearchMode || in.Filter != "" {
		return 3
	}
	return 2
}

// VisibleHeight returns the row-line budget for the scrollable launcher list at
// the current dimensions. The result is floored at minVisibleRows.
// Pre-WindowSizeMsg (Height==0) the budget equals the filtered length so
// scroll-affecting operations are no-ops until the first resize arrives.
func (in PickerInput) VisibleHeight() int {
	if in.Height == 0 {
		return len(in.FilteredIdx)
	}
	h := in.PaneHeight - in.internalChrome()
	if h < minVisibleRows {
		h = minVisibleRows
	}
	return h
}

// ClampOffset pins Offset to [0, max(0, total - visible)] at the current
// dimensions.
func (in *PickerInput) ClampOffset() {
	total := len(in.FilteredIdx)
	visible := in.VisibleHeight()
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if in.Offset > maxOffset {
		in.Offset = maxOffset
	}
	if in.Offset < 0 {
		in.Offset = 0
	}
}

// ScrollToCursor adjusts Offset so the launcher cursor sits within the
// currently visible window, clamping Cursor into range along the way. Launcher
// rows have no group labels so each row costs exactly one line, but RenderList
// still spends one line of the visible budget on each of the ▲ and ▼
// indicators when they appear — the row-by-row walk via cursorFitsWindow
// reserves those lines so the renderer never drops the cursor row.
func (in *PickerInput) ScrollToCursor() {
	if in.Height == 0 {
		return
	}
	if len(in.FilteredIdx) == 0 {
		in.Offset = 0
		return
	}
	if in.Cursor < 0 {
		in.Cursor = 0
	}
	if in.Cursor >= len(in.FilteredIdx) {
		in.Cursor = len(in.FilteredIdx) - 1
	}
	if in.Cursor < in.Offset {
		in.Offset = in.Cursor
	}
	visible := in.VisibleHeight()
	for in.Offset < in.Cursor {
		if in.cursorFitsWindow(in.Offset, visible) {
			break
		}
		in.Offset++
	}
	if in.Offset < 0 {
		in.Offset = 0
	}
}

// cursorFitsWindow reports whether RenderList can emit the cursor row from the
// given offset inside the visible height. The renderer spends one line on ▲
// when offset > 0 and one line on ▼ when rows remain below the cursor; those
// indicator lines must be reserved during scroll calculations, otherwise the
// renderer drops the cursor row while making space for the scroll markers.
func (in *PickerInput) cursorFitsWindow(offset, height int) bool {
	if offset < 0 || offset > in.Cursor || in.Cursor >= len(in.FilteredIdx) {
		return false
	}
	budget := height
	if offset > 0 {
		budget--
	}
	if in.Cursor < len(in.FilteredIdx)-1 {
		budget--
	}
	if budget < 0 {
		return false
	}
	return (in.Cursor - offset + 1) <= budget
}
