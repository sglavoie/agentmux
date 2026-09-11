package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
	"github.com/sglavoie/agentmux/tui/layout"
)

// View renders the full dashboard.
func (m Model) View() tea.View {
	frame := renderRootFrame(m)
	v := tea.NewView(frame.text)
	v.AltScreen = true
	// The two surfaces that publish hit regions are the two that take the mouse.
	// Asking the frame rather than the mode would be the same answer today, but
	// this way a mode that grows targets has to say so here as well, and a mode
	// that has none cannot leave cell-motion reporting on for nothing.
	if m.mode == ModeList || m.mode == ModeProjectSwitcher {
		v.MouseMode = tea.MouseModeCellMotion
		v.OnMouse = frame.hitMap.handler()
	}
	return v
}

// render builds the string content for View. The scenario harness captures the
// screen through here, so it deliberately shares renderRootFrame with View
// rather than composing its own: a committed scenario that asserted a different
// composition from the one an operator sees would assert nothing.
func (m Model) render() string {
	return renderRootFrame(m).text
}

// rootFrame is one composed screen: the text the terminal will show and, when
// the live dashboard owns it, the mouse regions the body produced beside that
// text and clipped to the body it was composed into.
type rootFrame struct {
	text   string
	hitMap mouseHitMap
}

// renderRootFrame composes the whole screen once. The bottom-of-screen chrome
// is measured a single time and both halves of the composition read that one
// measurement — the rows the body is clipped to and the rows an overlay is
// centred in are then the same number by construction rather than two
// independent answers to the same question.
func renderRootFrame(m Model) rootFrame {
	chrome := newRootChrome(m)
	if m.mode == ModeList {
		body := renderDashboardFrame(m)
		return rootFrame{
			text:   chrome.compose(body.text),
			hitMap: body.hitMap.clipped(m.width, chrome.bodyHeight()),
		}
	}
	// The project switcher is the one overlay whose popup carries click targets,
	// so it is composed here rather than in renderRootBody's switch: the regions
	// are produced by the same call that places the popup, and the placement is
	// what they are measured from.
	if m.mode == ModeProjectSwitcher {
		body, hitMap := renderProjectSwitcherFrame(m, chrome)
		return rootFrame{
			text:   chrome.compose(body),
			hitMap: hitMap.clipped(m.width, chrome.bodyHeight()),
		}
	}
	return rootFrame{text: chrome.compose(renderRootBody(m, chrome))}
}

// renderRootBody draws the body of whichever non-ModeList screen owns the
// terminal, including the overlay modes, which composite their popup over the
// backdrop the mode they were raised from owns.
func renderRootBody(m Model, chrome rootChrome) string {
	var sb strings.Builder

	switch m.mode {
	case ModePicker:
		// Picker replaces both header and list.
		sb.WriteString(renderPicker(m))
	case ModeSessionSearch:
		// Session search owns its full layout (header + chips + query +
		// results) so the dashboard list is not rendered below.
		sb.WriteString(renderSessionSearch(m))
	case ModeWorktrees:
		sb.WriteString(renderWorktrees(m))
	case ModeASLRuns:
		sb.WriteString(renderASLRuns(m))
	case ModeLiveSessionPicker:
		base := rootBaseView(m)
		popup := renderLiveSessionPickerBox(m)
		sb.WriteString(layout.CenterOverlay(base, popup, m.width, chrome.bodyHeight(), true))
	case ModePlanSearch:
		// Plan search owns its full layout — like ModeSessionSearch, the
		// dashboard list must not render underneath.
		sb.WriteString(renderPlanSearch(m))
	case ModePlanView:
		// Plan viewer owns its full layout — like ModeSessionView, the
		// dashboard list must not render underneath.
		sb.WriteString(renderPlanView(m))
	case ModeSessionView:
		// Viewer owns its full layout — like ModeSessionSearch, the
		// dashboard list must not render underneath.
		sb.WriteString(renderSessionView(m))
	case ModeMessageDetail:
		sb.WriteString(renderMessageDetail(m))
	case ModeDeleteChoice, ModeChipPicker:
		// Modal overlays on top of its owning search view. The base view is
		// rendered then dimmed; the bordered popup is composited via
		// line-splicing so the underlying list stays visible around it.
		// See tui/overlay.go for the splicing algorithm.
		//
		// These two resolve their owning view themselves rather than through
		// activeRootMode — the chip picker's is recorded on the picker, not on
		// the mode — but the view is drawn through the same dispatch as every
		// other backdrop.
		source := ModeSessionSearch
		if m.mode == ModeChipPicker {
			source = chipPickerSourceMode(m)
		}
		base := rootBaseViewFor(m, source)
		var popup string
		if m.mode == ModeChipPicker {
			popup = renderChipPickerBox(m)
		} else {
			popup = renderDeleteChoiceBox(m)
		}
		sb.WriteString(layout.CenterOverlay(base, popup, m.width, chrome.bodyHeight(), true))
	case ModeActions, ModeConfirm, ModeConfigPicker, ModeHelp, ModeDetail, ModeCommandPalette, ModeASLResponse, ModeHealth:
		// Every one of these raises a popup over the view esc returns to, and
		// activeRootMode is the one place that names that view — including for
		// help, whose backdrop is its own return mode, and for the config
		// picker raised from the launcher.
		base := rootBaseView(m)
		var popup string
		switch m.mode {
		case ModeActions:
			popup = renderActionsBox(m)
		case ModeConfirm:
			popup = renderConfirmBox(m)
		case ModeConfigPicker:
			popup = renderConfigPickerBox(m)
		case ModeHelp:
			popup = renderHelpBox(m)
		case ModeDetail:
			popup = renderDetailBox(m)
		case ModeCommandPalette:
			popup = renderCommandPaletteBox(m)
		case ModeASLResponse:
			popup = renderASLResponseBox(m)
		case ModeHealth:
			popup = renderHealthBox(m)
		}
		if m.mode == ModeConfigPicker {
			// Top-anchored so the modal doesn't jump when the harness-switch
			// warning is appended/removed.
			sb.WriteString(layout.TopCenterOverlay(base, popup, m.width, chrome.bodyHeight(), true))
		} else {
			sb.WriteString(layout.CenterOverlay(base, popup, m.width, chrome.bodyHeight(), true))
		}
	default:
		// Header area varies by mode; list is always shown below. ModeList never
		// arrives here — renderRootFrame routes it to the canonical dashboard
		// frame, which is where the chrome between the header and the list is
		// drawn.
		switch m.mode {
		case ModeSearch:
			sb.WriteString(renderSearch(m))
		default:
			sb.WriteString(renderHeader(m))
		}
		sb.WriteString(renderList(m))
	}

	return sb.String()
}

// rootBaseView renders the full-layout view that currently owns the screen, so
// an overlay raised from Worktrees or Runs dims that view rather than the
// dashboard the operator is not looking at.
func rootBaseView(m Model) string {
	return rootBaseViewFor(m, m.activeRootMode())
}

// rootBaseViewFor is the one place that answers "which view owns the screen
// behind this overlay". Both callers reach it: the overlay composition through
// activeRootMode, and the help overlay through its own recorded return mode.
// They used to be separate switches over overlapping sets of modes, so a
// backdrop could be correct under esc-from-help and wrong under esc-from-
// confirm for the very same screen.
func rootBaseViewFor(m Model, mode Mode) string {
	switch mode {
	case ModePicker:
		return renderPicker(m)
	case ModeSessionSearch:
		return renderSessionSearch(m)
	case ModeSessionView:
		return renderSessionView(m)
	case ModePlanSearch:
		return renderPlanSearch(m)
	case ModePlanView:
		return renderPlanView(m)
	case ModeWorktrees:
		return renderWorktrees(m)
	case ModeASLRuns:
		return renderASLRuns(m)
	default:
		return renderDashboard(m)
	}
}

// rootChrome is the bottom-of-screen budget a composed screen is laid out
// against: the reflowed footer rows and the sticky status row, measured once.
//
// Measuring once is not only a cost argument, though the ModeList footer is the
// expensive thing on the frame — its position indicator is derived from the
// list height, which is derived from the footer's own row count, so measuring
// it iterates to a fixpoint and each iteration re-renders the chrome above the
// list. It is also a correctness one: the composition clips the body to the
// rows the footer left it, and an overlay is centred in the rows the footer
// left it, and those were two separate calculations over two separate
// measurements of the same footer.
type rootChrome struct {
	// footer holds the footer rows already trimmed to what the terminal can
	// actually show, so len(footer) is what the composition will spend rather
	// than what the footer asked for.
	footer []string
	status string
	// statusAbove draws the status row above the footer rather than under it.
	//
	// That is the dashboard's chrome, and the difference is what the row is for.
	// Under the footer it is a sticky status line the eye reads as part of the
	// frame; above it, resting directly on the list, it is a feedback slot —
	// blank while nothing has happened, and the first thing the eye lands on
	// when something has. Every other root keeps the sticky line, so the two
	// readings never appear on one screen.
	statusAbove bool
	// height is the terminal height the trim was resolved against. A
	// non-positive height means "not yet resized": nothing is clipped and the
	// body keeps its natural length.
	height int
}

func newRootChrome(m Model) rootChrome {
	c := rootChrome{
		footer:      layout.SectionLines(renderFooter(m)),
		status:      renderStatusBar(m),
		statusAbove: dashboardOwnsChrome(m),
		height:      m.height,
	}
	if c.height > 0 {
		budget := c.height - statusBarLines
		if budget < 0 {
			budget = 0
		}
		if len(c.footer) > budget {
			c.footer = c.footer[len(c.footer)-budget:]
		}
	}
	return c
}

// bodyHeight is the rows left for the body above the footer and status row.
func (c rootChrome) bodyHeight() int {
	if c.height <= 0 {
		return 0
	}
	h := c.height - len(c.footer) - statusBarLines
	if h < 0 {
		return 0
	}
	return h
}

// compose stacks body, padding, footer, and status into the final screen,
// clipping the body to bodyHeight and padding it out to the same when it is
// short. The result is exactly the terminal's height whenever there is one.
func (c rootChrome) compose(body string) string {
	bodyLines := layout.SectionLines(body)
	paddingLines := 1
	if c.height > 0 {
		budget := c.bodyHeight()
		if len(bodyLines) > budget {
			bodyLines = bodyLines[:budget]
		}
		paddingLines = budget - len(bodyLines)
	}

	lines := make([]string, 0, len(bodyLines)+paddingLines+len(c.footer)+statusBarLines)
	lines = append(lines, bodyLines...)
	for i := 0; i < paddingLines; i++ {
		lines = append(lines, "")
	}
	if c.statusAbove {
		lines = append(lines, c.status)
		lines = append(lines, c.footer...)
	} else {
		lines = append(lines, c.footer...)
		lines = append(lines, c.status)
	}
	return strings.Join(lines, "\n")
}

// dashboardOwnsChrome reports that the screen belongs to the dashboard — the
// list itself, its search input, the project switcher, and every overlay raised
// over any of them, including the full-screen inspector.
//
// It asks activeRootMode rather than m.mode so an overlay inherits the chrome of
// the view it was raised from. An overlay does not redraw the bottom rows, so
// opening the command palette over the dashboard and over Past Sessions would
// otherwise shuffle the two rows under it in one case and not the other, which
// is the frame moving for a reason that has nothing to do with the frame.
func dashboardOwnsChrome(m Model) bool {
	switch m.mode {
	case ModeActions, ModeDetail:
		// Neither is reachable from anywhere else and both esc back to the list,
		// so they are dashboard overlays that happen not to record a return mode.
		// They are named here rather than taught to activeRootMode because that
		// answer is also what decides which root gets re-rendered underneath, and
		// these two already answer it correctly.
		return true
	}
	switch m.activeRootMode() {
	case ModeList, ModeSearch, ModeProjectSwitcher:
		return true
	default:
		return false
	}
}

// rootBodyHeight is the body-row budget for callers that are sizing one surface
// rather than composing a whole frame. renderRootFrame does not use it: it
// holds a rootChrome already and asking again would measure the footer twice.
func rootBodyHeight(m Model) int {
	return newRootChrome(m).bodyHeight()
}

// rootFooterLines is the footer rows the composition will spend, for the same
// one-surface callers. Trimmed, because a row the terminal cannot show is not a
// row anything downstream should budget for.
func rootFooterLines(m Model) []string {
	return newRootChrome(m).footer
}

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
	sess, ok := m.selectedSession()
	marker, _ := aslMarkerFor(m, sess)
	return dashboard.RenderInspector(dashboard.InspectorInput{
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
	})
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

const (
	previewNoticeToastID = -2
	previewNoticeTTL     = 2 * time.Second
)

func (m *Model) pushPreviewNotice(text string) tea.Cmd {
	return m.replaceToastByID(previewNoticeToastID, Toast{
		Severity: toastError,
		Text:     text,
		Expires:  time.Now().Add(previewNoticeTTL),
	})
}

func (m *Model) clearPreviewNotice() {
	m.clearToastByID(previewNoticeToastID)
}

// retargetPreview points the pane at a new target and opens a new generation for
// it. Every path that changes what the preview is showing goes through here, so
// no path can move the target without also invalidating the read that is still
// in flight for the previous one. The periodic tick is not such a path: it
// re-reads the current target under the current generation.
//
// It moves the target only. The read in flight keeps its request identity and
// its slot, because that read is still going to answer and the controller still
// has to hear it; what the retarget takes away is its permission to paint. The
// reset of the operator's scroll hold hangs off the target change for the same
// reason it is written here rather than in the controller: a coalesced
// re-request of the SAME target must not drop a hold, and only a genuine move to
// another buffer may.
func (m *Model) retargetPreview(windowID int) {
	m.previewWindowID = windowID
	m.previewTargetSeq++
	m.previewText = ""
	m.previewError = ""
	m.previewLoaded = false
	// A new target is a new buffer, so the hold is dropped with it. Keeping a
	// scroll position across a retarget would show one session's snapshot under
	// another session's metadata for as long as the read takes to land.
	m.inspectorViewport = m.inspectorViewport.Resume()
}

// activePreviewEnabled reports whether the preview pane is enabled for the
// currently active mode. ModePicker has its own independent flag so that
// toggling preview inside the picker does not disturb the list's preview state.
func (m Model) activePreviewEnabled() bool {
	if m.mode == ModePicker {
		return m.pickerPreviewEnabled
	}
	return m.inspectorActive()
}

// currentPreviewWindowID returns the window ID the preview should track for
// the current cursor in the active mode. ModePicker resolves through the
// picker cursor (matching the highlighted config session to a running one);
// every other mode uses the dashboard cursor.
func (m Model) currentPreviewWindowID() int {
	if m.mode == ModePicker {
		return m.pickerCurrentWindowID()
	}
	if s, ok := m.selectedSession(); ok {
		return s.WindowID
	}
	return 0
}

// pickerCurrentWindowID returns the WindowID of the running session matching
// the picker's highlighted config row, or 0 when nothing matches. When multiple
// instances share a key, use the last match in the current session ordering,
// which tracks the newest same-key tab after sorted insertion.
func (m Model) pickerCurrentWindowID() int {
	if len(m.pickerFilteredIdx) == 0 || m.pickerCursor >= len(m.pickerFilteredIdx) {
		return 0
	}
	origIdx := m.pickerFilteredIdx[m.pickerCursor]
	sess := m.pickerSessionWithResolvedProfile(origIdx)
	key := session.SessionKey(sess.EffectiveProfile(), sess.Title)
	winID := 0
	for _, rs := range m.sessions {
		if launch.RunningSessionKey(rs) == key {
			winID = rs.WindowID
		}
	}
	return winID
}

// refreshPreviewForPickerCursor returns an immediate fetch cmd when the
// picker cursor now points at a different running session (or none).
// Returns nil when preview is off or the window hasn't changed.
func (m *Model) refreshPreviewForPickerCursor() tea.Cmd {
	if !m.pickerPreviewEnabled {
		return nil
	}
	winID := m.pickerCurrentWindowID()
	if winID == m.previewWindowID {
		return nil
	}
	m.retargetPreview(winID)
	return m.requestPreviewRead()
}

// refreshPreviewForCursor returns an immediate fetch cmd when the cursor
// has moved to a session backed by a different window. Returns nil when
// preview is off or the selection still points at the same window.
func (m *Model) refreshPreviewForCursor() tea.Cmd {
	if !m.inspectorActive() {
		return nil
	}
	// A selection that is not a session tracks window 0, which fetchPreviewCmd
	// short-circuits into the "(session not running)" placeholder rather than a
	// Kitty read.
	var winID int
	if s, ok := m.selectedSession(); ok {
		winID = s.WindowID
	}
	if winID == m.previewWindowID {
		return nil
	}
	m.retargetPreview(winID)
	return m.requestPreviewRead()
}

// resumeListPreviewTickCmd reopens the dashboard's polling chain on the way back
// from a mode that owned the pane itself. It is a pointer method because the
// chain identity it draws has to survive on the model the caller returns:
// resuming without the bump is how the picker's exit used to leave the tick it
// had already scheduled beating beside the one it started.
func (m *Model) resumeListPreviewTickCmd() tea.Cmd {
	if !m.inspectorActive() {
		return nil
	}
	return m.startPreviewPoll()
}

// togglePickerPreview flips the picker's own preview pane on/off, independent
// of m.previewEnabled (the list's preview flag).
func (m Model) togglePickerPreview() (tea.Model, tea.Cmd) {
	if m.pickerPreviewEnabled {
		m.pickerPreviewEnabled = false
		m.retargetPreview(0)
		scrollPickerToCursor(&m)
		return m, nil
	}
	cfg := m.cfg.Defaults.Preview
	if !previewFits(m.width, m.height, footerBudgetLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
		return m, m.pushPreviewNotice("preview unavailable: terminal too small")
	}
	m.pickerPreviewEnabled = true
	m.retargetPreview(m.pickerCurrentWindowID())
	m.clearPreviewNotice()
	scrollPickerToCursor(&m)
	// The launcher's pane shares the dashboard's controller, target, and chain —
	// only the visibility flag is its own — so enabling it is the same two steps
	// and inherits the same one-read-at-a-time bound.
	read := m.requestPreviewRead()
	return m, tea.Batch(read, m.startPreviewPoll())
}

// The dashboard has no counterpart to disablePickerPreviewWithNotice on
// purpose. A resize that stops clearing the inspector's floors does not disable
// anything: the intent stays where the operator left it and the split simply
// stops being drawn until the terminal can hold one again. A notice would be
// announcing a decision nothing made.

func (m *Model) disablePickerPreviewWithNotice(msg string) tea.Cmd {
	m.pickerPreviewEnabled = false
	m.retargetPreview(0)
	return m.pushPreviewNotice(msg)
}
