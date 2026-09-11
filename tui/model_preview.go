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
	if m.mode == ModeList {
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
	case ModeActions, ModeConfirm, ModeConfigPicker, ModeHelp, ModeDetail, ModeCommandPalette, ModeASLResponse:
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
		sb.WriteString(renderDetailStripBlock(m))
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
	// height is the terminal height the trim was resolved against. A
	// non-positive height means "not yet resized": nothing is clipped and the
	// body keeps its natural length.
	height int
}

func newRootChrome(m Model) rootChrome {
	c := rootChrome{
		footer: layout.SectionLines(renderFooter(m)),
		status: renderStatusBar(m),
		height: m.height,
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
	lines = append(lines, c.footer...)
	lines = append(lines, c.status)
	return strings.Join(lines, "\n")
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

const (
	// previewMinPaneWidth is the narrowest column either pane is worth drawing
	// in. It bounds the preview from below and, through dashboardSplitWidths,
	// the terminal width a horizontal split is attempted at all.
	previewMinPaneWidth = 20
	// previewMinPaneHeight is the same judgement on the vertical axis: below it
	// the list and the preview cannot both hold anything readable.
	previewMinPaneHeight = 4
	// previewMinVerticalHeight is the floor on the stacked preview's own rows.
	previewMinVerticalHeight = 3
)

// dashboardGeom is the dashboard body's rectangle, resolved once. renderSplit
// draws from it and listVisibleHeight clamps the cursor against it, so the
// renderer and the scroll arithmetic cannot disagree about how many rows exist
// — including in the cases where the split silently falls back to the plain
// list, which is exactly where the two used to drift.
type dashboardGeom struct {
	// split reports whether the preview pane is drawn at all. When it is false
	// the list takes the whole body and the preview fields are zero.
	split bool
	// vertical reports a stacked split rather than a side-by-side one.
	vertical bool
	// rail reports that the project rail fits beside the list after the preview
	// contract has claimed its space. railWidth is zero otherwise.
	rail bool

	railWidth, railHeight       int
	listWidth, listHeight       int
	previewWidth, previewHeight int
}

// dashboardPreviewRequested reports whether the model is asking for a preview
// pane, before any question of whether one fits.
func dashboardPreviewRequested(m Model) bool {
	return m.previewEnabled && m.cfg != nil
}

// dashboardSplitWidths resolves a side-by-side split's column allocation. It
// reports false when there is no preview, when the direction is vertical (both
// panes are then full width), or when the terminal cannot hold a readable
// preview beside a list at the compact row floor.
//
// The floor is the point of the ok result: MinRowWidth is the width the compact
// band's fixed prefix and its smallest useful title need, so a list column below
// it can only be an elided one. Pairing that with a preview is worse than
// dropping the preview, which is what the caller does.
//
// Nothing here reads m.height. That is load-bearing: the detail strip's height
// charge is derived from the list column's width, so a width that depended on
// the height would close a loop between the two and let a resize oscillate
// between the split and the plain list.
func dashboardSplitWidths(m Model) (listWidth, previewWidth int, ok bool) {
	if !dashboardPreviewRequested(m) || m.width < previewMinPaneWidth {
		return 0, 0, false
	}
	if m.cfg.Defaults.Preview.Direction == "vertical" {
		return 0, 0, false
	}
	maxPreview := m.width - dashboard.MinRowWidth() - 1
	previewWidth = int(float64(m.width) * m.cfg.Defaults.Preview.Ratio)
	if previewWidth > maxPreview {
		previewWidth = maxPreview
	}
	if previewWidth < previewMinPaneWidth {
		return 0, 0, false
	}
	return m.width - previewWidth - 1, previewWidth, true
}

// dashboardListPaneWidth returns the width the list column is laid out at, and
// therefore the width its band is judged from. Width-only, for the reason
// dashboardSplitWidths documents: a height-dependent answer here would feed the
// detail strip's charge back into the geometry that decides the strip.
//
// A short terminal can still drop the split after this returns, which leaves
// the list wider than the band it was charged for. That direction is safe — the
// strip is reserved and drawn consistently, it is merely more detail than a
// standard-band list strictly needs — and it is the price of the loop staying
// open.
func dashboardBaseListPaneWidth(m Model) int {
	if listWidth, _, ok := dashboardSplitWidths(m); ok {
		return listWidth
	}
	if m.width < 0 {
		return 0
	}
	return m.width
}

// dashboardRailWidth applies the rail's responsive rule to the one shared
// main-list base left by preview resolution. Every caller goes through this
// helper so a short terminal that later drops its preview cannot make body
// geometry disagree with the header and footer about whether the rail exists.
// The divider is charged before checking the 80-cell list guarantee.
func dashboardRailWidth(m Model) int {
	if !m.railRequested || m.listView != dashboard.ViewUrgency {
		return 0
	}
	baseListWidth := dashboardBaseListPaneWidth(m)
	railWidth := dashboard.RailWidth(dashboardRailRows(m))
	if baseListWidth-railWidth-1 < 80 {
		return 0
	}
	return railWidth
}

func dashboardListPaneWidth(m Model) int {
	base := dashboardBaseListPaneWidth(m)
	if railWidth := dashboardRailWidth(m); railWidth > 0 {
		return base - railWidth - 1
	}
	return base
}

// dashboardGeometry lays the body out: the list column, and the preview pane
// when one fits. Every height term the renderer will spend is subtracted here
// once — the chrome above the list, the compact detail strip below it, the
// padding row, the footer, and the sticky status bar — so no caller has to
// re-derive the same subtraction and get it subtly wrong.
func dashboardGeometry(m Model) dashboardGeom {
	width := m.width
	if width < 0 {
		width = 0
	}
	paneHeight := dashboardChromeFor(m).paneRows(m.height)

	listWidth, previewWidth, horizontal := dashboardSplitWidths(m)
	vertical := dashboardPreviewRequested(m) && width >= previewMinPaneWidth &&
		m.cfg.Defaults.Preview.Direction == "vertical"

	if paneHeight < previewMinPaneHeight || (!horizontal && !vertical) {
		listHeight := clampListHeight(paneHeight)
		railWidth := dashboardRailWidth(m)
		return dashboardGeom{
			rail: railWidth > 0, railWidth: railWidth, railHeight: listHeight,
			listWidth: width - railWidth - boolWidth(railWidth > 0), listHeight: listHeight,
		}
	}
	if vertical {
		previewHeight := int(float64(paneHeight) * m.cfg.Defaults.Preview.Ratio)
		if previewHeight < previewMinVerticalHeight {
			previewHeight = previewMinVerticalHeight
		}
		// The list's floor is taken out of the preview rather than out of the
		// pane, so the two heights plus the divider always sum to exactly
		// paneHeight. Clamping the list up afterwards instead would make the
		// body taller than the space it was given, and the root would clip the
		// bottom of the preview to fit it back.
		if room := paneHeight - listMinVisible - 1; previewHeight > room {
			previewHeight = room
		}
		if previewHeight < 1 {
			listHeight := clampListHeight(paneHeight)
			railWidth := dashboardRailWidth(m)
			return dashboardGeom{
				rail: railWidth > 0, railWidth: railWidth, railHeight: listHeight,
				listWidth: width - railWidth - boolWidth(railWidth > 0), listHeight: listHeight,
			}
		}
		railWidth := dashboardRailWidth(m)
		return dashboardGeom{
			split:         true,
			vertical:      true,
			rail:          railWidth > 0,
			railWidth:     railWidth,
			railHeight:    paneHeight - previewHeight - 1,
			listWidth:     width - railWidth - boolWidth(railWidth > 0),
			listHeight:    paneHeight - previewHeight - 1,
			previewWidth:  width,
			previewHeight: previewHeight,
		}
	}
	railWidth := dashboardRailWidth(m)
	return dashboardGeom{
		split:         true,
		rail:          railWidth > 0,
		railWidth:     railWidth,
		railHeight:    clampListHeight(paneHeight),
		listWidth:     listWidth - railWidth - boolWidth(railWidth > 0),
		listHeight:    clampListHeight(paneHeight),
		previewWidth:  previewWidth,
		previewHeight: paneHeight,
	}
}

func boolWidth(on bool) int {
	if on {
		return 1
	}
	return 0
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
func footerBudgetLines(m Model) int {
	if m.mode == ModePicker {
		return pickerFooterLineCount(m)
	}
	if m.mode != ModeList {
		return 2
	}
	if m.previewEnabled {
		// Footer keybindings are suppressed while preview owns the split pane.
		return 0
	}
	return listFooterBudgetLineCount(m)
}

// dashboardPreviewEnableFooterLines returns the footer budget used to decide
// whether enabling ModeList preview will fit. A successful enable suppresses
// the regular dashboard footer, so the preview-off footer must not make the
// initial fit check stricter than the enabled layout.
func dashboardPreviewEnableFooterLines(m Model) int {
	if m.mode != ModeList {
		return footerBudgetLines(m)
	}
	return 0
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

// togglePreview flips the preview pane on/off. Returns the model and any cmd
// (immediate fetch + ticker) to dispatch on enable.
func (m Model) togglePreview() (tea.Model, tea.Cmd) {
	if m.previewEnabled {
		m.previewEnabled = false
		m.retargetPreview(0)
		scrollListToCursor(&m)
		return m, nil
	}
	cfg := m.cfg.Defaults.Preview
	if !previewFits(m.width, m.height, dashboardPreviewEnableFooterLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
		return m, m.pushPreviewNotice("preview unavailable: terminal too small")
	}
	m.previewEnabled = true
	m.retargetPreview(m.currentPreviewWindowID())
	m.clearPreviewNotice()
	scrollListToCursor(&m)
	// Both halves go through the controller: the read may coalesce behind one
	// still in flight from before the pane was hidden, and the chain bump retires
	// any tick the previous chain has left over, so a hide/show cycle faster than
	// the refresh interval cannot end with two chains polling.
	read := m.requestPreviewRead()
	return m, tea.Batch(read, m.startPreviewPoll())
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
	return m.previewEnabled
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
	if !m.previewEnabled {
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
	if !m.previewEnabled {
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

// disablePreviewWithNotice turns the dashboard preview off and shows a
// transient status-bar notice. Used by the resize guard.
func (m *Model) disablePreviewWithNotice(msg string) tea.Cmd {
	m.previewEnabled = false
	m.retargetPreview(0)
	return m.pushPreviewNotice(msg)
}

func (m *Model) disablePickerPreviewWithNotice(msg string) tea.Cmd {
	m.pickerPreviewEnabled = false
	m.retargetPreview(0)
	return m.pushPreviewNotice(msg)
}
