package tui

import (
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// dashboardGeom is the dashboard body's rectangle, resolved once. renderSplit
// draws from it and listVisibleHeight clamps the cursor against it, so the
// renderer and the scroll arithmetic cannot disagree about how many rows exist
// — including in the cases where the split silently falls back to the plain
// list, which is exactly where the two used to drift.
type dashboardGeom struct {
	// split reports whether the inspector pane is drawn at all. When it is false
	// the list takes the whole body and the preview fields are zero.
	split bool
	// fullScreen reports that the inspector has the whole body and no queue
	// column is drawn. listWidth and listHeight are still populated in that
	// state: the cursor and the scroll offset outlive the queue being hidden,
	// and clamping them against a zero-row window would lose the selection the
	// operator returns to.
	fullScreen bool

	listWidth, listHeight       int
	previewWidth, previewHeight int
}

// dashboardSplitWidths resolves the side-by-side split's column allocation
// against the approved pane floors: a queue column of at least
// inspectorMinQueueCols, an inspector column of at least inspectorMinCols, one
// divider between them, and a terminal at least as wide as the configured
// minimum. It reports false when the operator has hidden the inspector, when
// full screen has taken the row outright, or when any of those floors is
// unmet — in which case the queue takes the whole width.
//
// The configured ratio is a PREFERENCE, not an allocation: it is clamped up to
// the inspector's floor and down to whatever leaves the queue its own, so the
// two floors are guaranteed by construction and the ratio decides only what
// happens in the room above them.
//
// The dashboard has no vertical form. A stacked inspector would put the queue's
// rows and the output's rows in competition on the axis both are scarcest on,
// and the metadata block alone is six rows. cfg.Defaults.Preview.Direction still
// governs the launcher's pane, which is why the key is read there and not here.
//
// Nothing in here reads m.height, and that is load-bearing: the footer's row
// count is derived from the list geometry, so a width that depended on the
// height would close a loop through the footer measurement. The body-row floor
// is applied by dashboardGeometry and inspectorSplitFits instead, neither of
// which is reachable from the footer.
func dashboardSplitWidths(m Model) (listWidth, inspectorWidth int, ok bool) {
	if !m.inspectorSplitRequested() || m.width <= 0 {
		return 0, 0, false
	}
	floor := inspectorMinQueueCols + inspectorDividerCols + inspectorMinCols
	if configured := m.cfg.Defaults.Preview.MinCols; configured > floor {
		floor = configured
	}
	if m.width < floor {
		return 0, 0, false
	}
	inspectorWidth = int(float64(m.width) * m.cfg.Defaults.Preview.Ratio)
	if inspectorWidth < inspectorMinCols {
		inspectorWidth = inspectorMinCols
	}
	if room := m.width - inspectorDividerCols - inspectorMinQueueCols; inspectorWidth > room {
		inspectorWidth = room
	}
	return m.width - inspectorWidth - inspectorDividerCols, inspectorWidth, true
}

// dashboardGeometry lays the body out: the queue column, and the inspector pane
// in whichever of its two forms the terminal admits. Every height term the
// renderer will spend is subtracted here once — the chrome above the list, the
// padding row, the footer, and the sticky status bar — so no caller has to
// re-derive the same subtraction and get it subtly wrong.
//
// The three outcomes are decided in the order their preconditions are
// independent. Full screen answers first and asks nothing of the width, because
// a single pane has no floors to clear beyond existing. The split answers next
// and must clear both: the column floors dashboardSplitWidths enforces and the
// body-row floor, which is applied here rather than there so the width
// resolution stays free of the footer loop. Everything else is the queue alone.
func dashboardGeometry(m Model) dashboardGeom {
	width := m.width
	if width < 0 {
		width = 0
	}
	paneHeight := dashboardChromeFor(m).paneRows(m.height)

	if m.inspectorFullScreenActive() {
		return dashboardGeom{
			split: true, fullScreen: true,
			// The queue keeps its row budget even though it is not drawn, so the
			// cursor and the offset survive the round trip through full screen.
			listWidth: width, listHeight: clampListHeight(paneHeight),
			previewWidth: width, previewHeight: paneHeight,
		}
	}

	listWidth, inspectorWidth, widthsFit := dashboardSplitWidths(m)
	if !widthsFit || paneHeight < inspectorMinBodyRows {
		return dashboardGeom{listWidth: width, listHeight: clampListHeight(paneHeight)}
	}
	return dashboardGeom{
		split:         true,
		listWidth:     listWidth,
		listHeight:    clampListHeight(paneHeight),
		previewWidth:  inspectorWidth,
		previewHeight: paneHeight,
	}
}

// clampListHeight floors a computed row budget so a degenerate terminal yields
// a usable window rather than a zero or negative one.
func clampListHeight(h int) int {
	if h < listMinVisible {
		return listMinVisible
	}
	return h
}

// renderPreviewPane paints whatever the current selection makes the pane mean.
//
// A family summary gets a project card rather than scrollback. That is not a
// presentation preference: a summary owns no window, so there is no scrollback
// to show, and the only way to produce some would be to pick one of its members
// — the exact "resolve a summary to an arbitrary session" the item layer refuses
// everywhere else. The card is the honest answer to the selection that was made.
//
// Any other non-session selection resolves to InspectorNoSelection, which is the
// blank metadata block the pane shows over an empty list.
func renderPreviewPane(m Model, width, height int) string {
	if it, ok := m.selectedItem(); ok && it.Kind == dashboard.ItemFamilySummary {
		return dashboard.RenderProjectCard(dashboard.ProjectCardInput{
			Width:         width,
			Height:        height,
			Items:         m.items,
			Item:          it,
			LastActivity:  m.lastActivity,
			IdleThreshold: m.idleThreshold,
			Cfg:           m.cfg,
			LoopMarker: func(s session.RunningSession) (string, bool) {
				return aslMarkerFor(m, s)
			},
		})
	}
	return dashboard.RenderInspector(inspectorInputFor(m, width, height))
}

func inspectorInputFor(m Model, width, height int) dashboard.InspectorInput {
	sess, ok := m.selectedSession()
	marker, _ := aslMarkerFor(m, sess)
	return dashboard.InspectorInput{
		Width:         width,
		Height:        height,
		Content:       inspectorContentFor(m, sess, ok),
		Session:       sess,
		Cfg:           m.cfg,
		LastActivity:  m.lastActivity,
		IdleThreshold: m.idleThreshold,
		Watchdog:      m.watchdog[sess.WindowID],
		Now:           modelNow(m),
		Supervision:   marker,
		Output:        dashboard.SplitOutput(m.previewText),
		Error:         m.previewError,
		Viewport:      m.inspectorViewport,
	}
}

// inspectorContentFor decides which of the inspector's five situations the
// current selection is in. The order is the order the evidence rules things out:
// a selection that is not a session has no metadata to draw at all; a session
// with no live window has metadata but can have no output; a read that failed
// says so in preference to the empty text it left behind; and only a target that
// has actually heard back can claim its empty buffer is the session's own.
func inspectorContentFor(m Model, sess session.RunningSession, selected bool) dashboard.InspectorContent {
	switch {
	case !selected:
		return dashboard.InspectorNoSelection
	case sess.WindowID == 0:
		return dashboard.InspectorVanished
	case m.previewError != "":
		return dashboard.InspectorFailed
	case !m.previewLoaded:
		return dashboard.InspectorLoading
	default:
		return dashboard.InspectorReady
	}
}

// footerBudgetLines returns the number of footer rows reserved by layout math
// and preview resize checks. ModeList measures its reflowed content; other
// modes retain their established fixed budgets. The sticky status row is
// accounted for separately by callers.
//
// The dashboard footer is no longer suppressed when the inspector is on screen.
// It could be when the inspector was an opt-in the operator had just pressed a
// key for; now that the pane is the default, suppressing it would leave a
// first-run dashboard with no key hints at all. It is also what keeps the
// inspector's body-row floor out of a loop: the fit test reads the footer's row
// count, so a footer that read the fit could not be measured.
func footerBudgetLines(m Model) int {
	if m.mode == ModePicker {
		return pickerFooterLineCount(m)
	}
	if m.mode != ModeList {
		return 2
	}
	return listFooterBudgetLineCount(m)
}
