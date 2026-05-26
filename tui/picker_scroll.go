package tui

// pickerInternalChrome is the number of vertical lines the picker emits
// before the first row inside renderPickerList: header (1) + the sticky
// filter / "press / to filter" prompt followed by a blank line (2) = 3.
const pickerInternalChrome = 3

// pickerOuterChrome is the number of vertical lines outside the picker
// block in ModePicker: the trailing newline written by render() (1) plus
// previewFooterLines for ModePicker which is fixed at 2 = 3. Preview state
// does not affect this — the picker pane (with or without right-side
// preview) always occupies m.height - pickerOuterChrome lines.
const pickerOuterChrome = 3

// pickerVisibleHeight returns the row-line budget for the scrollable picker
// list at the model's current dimensions. The result is floored at
// listMinVisible so a degenerate terminal does not yield a zero or negative
// window. Pre-WindowSizeMsg (height==0) the budget equals the filtered
// length so scroll-affecting operations are no-ops until the first resize
// arrives, mirroring listVisibleHeight.
func pickerVisibleHeight(m Model) int {
	if m.height == 0 {
		return len(m.pickerFilteredIdx)
	}
	h := m.height - pickerOuterChrome - pickerInternalChrome
	if h < listMinVisible {
		h = listMinVisible
	}
	return h
}

// clampPickerOffset pins m.pickerOffset to [0, max(0, total - visible)] at
// the model's current dimensions. Mirrors clampListOffset.
func clampPickerOffset(m *Model) {
	total := len(m.pickerFilteredIdx)
	visible := pickerVisibleHeight(*m)
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.pickerOffset > maxOffset {
		m.pickerOffset = maxOffset
	}
	if m.pickerOffset < 0 {
		m.pickerOffset = 0
	}
}

// scrollPickerToCursor adjusts m.pickerOffset so the picker cursor sits
// within the currently visible window. Picker rows have no group labels so
// each row costs exactly one line, but renderPickerList still spends one
// line of the visible budget on each of the ▲ and ▼ indicators when they
// appear — the row-by-row walk via pickerCursorFitsWindow reserves those
// lines so the renderer never drops the cursor row. Mirrors
// scrollListToCursor without the group-label cost.
func scrollPickerToCursor(m *Model) {
	if m.height == 0 {
		return
	}
	if len(m.pickerFilteredIdx) == 0 {
		m.pickerOffset = 0
		return
	}
	if m.pickerCursor < 0 {
		m.pickerCursor = 0
	}
	if m.pickerCursor >= len(m.pickerFilteredIdx) {
		m.pickerCursor = len(m.pickerFilteredIdx) - 1
	}
	if m.pickerCursor < m.pickerOffset {
		m.pickerOffset = m.pickerCursor
	}
	visible := pickerVisibleHeight(*m)
	for m.pickerOffset < m.pickerCursor {
		if pickerCursorFitsWindow(m, m.pickerOffset, visible) {
			break
		}
		m.pickerOffset++
	}
	if m.pickerOffset < 0 {
		m.pickerOffset = 0
	}
}

// pickerCursorFitsWindow reports whether renderPickerList can emit the
// cursor row from the given offset inside the visible height. The
// renderer spends one line on ▲ when offset > 0 and one line on ▼ when
// rows remain below the cursor; those indicator lines must be reserved
// during scroll calculations, otherwise the renderer drops the cursor
// row while making space for the scroll markers. Mirrors
// listCursorFitsWindow without the group-label cost.
func pickerCursorFitsWindow(m *Model, offset, height int) bool {
	if offset < 0 || offset > m.pickerCursor || m.pickerCursor >= len(m.pickerFilteredIdx) {
		return false
	}
	budget := height
	if offset > 0 {
		budget--
	}
	if m.pickerCursor < len(m.pickerFilteredIdx)-1 {
		budget--
	}
	if budget < 0 {
		return false
	}
	return (m.pickerCursor - offset + 1) <= budget
}
