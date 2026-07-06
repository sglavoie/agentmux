package tui

// listMinVisible is the floor on the visible row budget for the session list.
// Even on very short terminals we keep at least this many rows visible so the
// pane remains useful. Mirrors detailMinVisible.
const listMinVisible = 3

const statusBarLines = 1

// chromeAboveListLines returns the number of vertical lines render() writes
// between the very top of the screen and the start of the list block in
// ModeList. This is the "header + everything between header and list" budget;
// the trailing newline after the list and the footer area are accounted for
// separately by callers so the preview-ON path can subtract them in the same
// place as the preview's own height allocation.
//
// Elements (each contributes its row count when visible):
//   - header + health hints                                             (1..3)
//   - deprecation banner (when undismissed and at least one entry)        (2)
//   - legend (omitted when m.height > 0 && m.height < legendMinHeight)    (1..n)
//   - chips (when activeFilter has at least one component)                (1)
//   - spacer where the inline filter hint used to sit in ModeList          (1)
//
// The renderHeader hint and chip-wrap-to-two-lines edge cases can drift the
// real chrome by one line; the listMinVisible floor in listVisibleHeight
// absorbs that drift on short terminals.
func chromeAboveListLines(m Model) int {
	n := 1 // header
	n += healthHintLines(m)
	if !m.deprecationDismissed && len(m.deprecations) > 0 {
		n += 2
	}
	n += legendLineCount(m)
	if len(m.activeFilter.Components()) > 0 {
		n++
	}
	if m.mode == ModeList {
		n++ // spacer is always one line in ModeList
	}
	return n
}

func legendLineCount(m Model) int {
	return len(rootSectionLines(renderLegend(m)))
}

// healthHintLines mirrors renderHealthHints' line count without rendering
// styled text. renderHeader appends one line per degraded health signal below
// the title row, so list/preview height math must reserve those rows.
func healthHintLines(m Model) int {
	if m.daemonHealthCheckedAt.IsZero() && m.hookHealthCheckedAt.IsZero() {
		return 0
	}
	n := 0
	if !m.daemonHealthCheckedAt.IsZero() && !m.daemonAlive {
		n++
	}
	if m.hookHealthErr != nil {
		n++
	} else if !m.hookHealthCheckedAt.IsZero() && !m.hookHealth.Healthy() {
		n++
	}
	return n
}

// listChromeLines returns the total number of vertical lines the dashboard
// chrome occupies around the session list in ModeList, mirroring the
// composition in render(). Sum of chromeAboveListLines (above) + the padding
// row between body and footer + footerBudgetLines + the sticky status bar.
// Used by listVisibleHeight to derive the row budget when the preview pane
// is off.
func listChromeLines(m Model) int {
	return chromeAboveListLines(m) + 1 /* padding row */ + footerBudgetLines(m) + statusBarLines
}

// listVisibleHeight returns the row-line budget for the scrollable session
// list at the model's current dimensions. The result is floored at
// listMinVisible so a degenerate terminal does not yield a zero or negative
// window. Pre-WindowSizeMsg (height==0) the budget equals the filtered length
// so scroll-affecting operations are no-ops until the first resize arrives,
// mirroring detailScrollBounds.
func listVisibleHeight(m Model) int {
	if m.height == 0 {
		return len(m.filtered)
	}
	var h int
	if m.previewEnabled && m.cfg != nil {
		cfg := m.cfg.Defaults.Preview
		footerLines := footerBudgetLines(m)
		paneHeight := m.height - chromeAboveListLines(m) - 1 /* padding row */ - footerLines - statusBarLines
		if cfg.Direction == "vertical" {
			previewHeight := int(float64(paneHeight) * cfg.Ratio)
			if previewHeight < 3 {
				previewHeight = 3
			}
			if previewHeight > paneHeight-2 {
				previewHeight = paneHeight - 2
			}
			h = paneHeight - previewHeight - 1
		} else {
			h = paneHeight
		}
	} else {
		h = m.height - listChromeLines(m)
	}
	if h < listMinVisible {
		h = listMinVisible
	}
	return h
}

// clampListOffset pins m.listOffset to [0, max(0, len(m.filtered) - visible)]
// at the model's current dimensions. Mirrors clampDetailScroll: a resize or a
// shrink in m.filtered can leave the persisted offset above maxOffset and the
// next j/k keypress would chew through dead rows before the viewport reacts.
func clampListOffset(m *Model) {
	total := len(m.filtered)
	visible := listVisibleHeight(*m)
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.listOffset > maxOffset {
		m.listOffset = maxOffset
	}
	if m.listOffset < 0 {
		m.listOffset = 0
	}
}

// scrollListToCursor adjusts m.listOffset so the cursor sits within the
// currently visible window. Mirrors color_picker_app.scrollToCursor but
// accounts for the group chrome emitted by renderListAt when multiGroup is
// true: a row that starts a visible group costs one extra line for its label,
// plus a blank separator when a prior group is already visible.
//
// The renderer initializes prevGroup to -1, so the first row at the window
// origin always emits a group label when multiGroup is true. The cost walk
// here mirrors that initialization.
func scrollListToCursor(m *Model) {
	if m.height == 0 {
		return
	}
	if len(m.filtered) == 0 {
		m.listOffset = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}

	// Cursor moved above the window: pin offset to cursor.
	if m.cursor < m.listOffset {
		m.listOffset = m.cursor
	}

	visible := listVisibleHeight(*m)
	multiGroup := countGroups(m.filtered, m.lastActivity, m.idleThreshold) > 1

	// Advance offset row-by-row until the cumulative line cost from offset
	// through cursor fits within the visible budget. Stops when cursor and
	// offset coincide so we never overshoot. We deliberately do not call
	// clampListOffset here: its upper bound is computed in rows, but the
	// label-aware walk above can require an offset > (total - visible) when
	// group labels eat into the line budget. Mirrors
	// color_picker_app.scrollToCursor, which also only floors at zero.
	for m.listOffset < m.cursor {
		if listCursorFitsWindow(m, m.listOffset, visible, multiGroup) {
			break
		}
		m.listOffset++
	}

	if m.listOffset < 0 {
		m.listOffset = 0
	}
}

// listCursorFitsWindow reports whether renderListAt can emit the cursor row
// from the given offset inside the full list pane height. The pane height is
// not all row budget: renderListAt spends one line on ▲ when offset > 0 and
// one line on ▼ when rows remain below the cursor. Those indicator lines must
// be reserved during scroll calculations, otherwise the model can select a
// row that the renderer drops while making space for the scroll markers.
func listCursorFitsWindow(m *Model, offset, height int, multiGroup bool) bool {
	if offset < 0 || offset > m.cursor || m.cursor >= len(m.filtered) {
		return false
	}
	budget := height
	if offset > 0 {
		budget--
	}
	if m.cursor < len(m.filtered)-1 {
		budget--
	}
	if budget < 0 {
		return false
	}
	return listLineCost(m, offset, m.cursor, multiGroup) <= budget
}

// listLineCost returns the number of rendered lines from offset through
// cursor inclusive, matching the renderListAt walk. When multiGroup is true a
// group-label line precedes the first row and every subsequent row whose
// group differs from the previous one; subsequent visible group labels also
// get a blank separator line. The walk initializes prevGroup to -1 to match
// renderListAt, so the row at offset always emits a label when multiGroup is
// true.
func listLineCost(m *Model, offset, cursor int, multiGroup bool) int {
	if offset > cursor || offset < 0 || cursor >= len(m.filtered) {
		return 0
	}
	cost := 0
	prevGroup := -1
	for i := offset; i <= cursor; i++ {
		if multiGroup {
			g := stateGroup(m.filtered[i], m.lastActivity, m.idleThreshold)
			if g != prevGroup {
				if prevGroup != -1 {
					cost++ // blank separator before a subsequent group
				}
				cost++ // group label
				prevGroup = g
			}
		}
		cost++ // row line
	}
	return cost
}
