package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
	"github.com/sglavoie/agentmux/tui/layout"
)

// View renders the full dashboard.
func (m Model) View() tea.View {
	content := m.render()
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// render builds the string content for View.
func (m Model) render() string {
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
		sb.WriteString(layout.CenterOverlay(base, popup, m.width, rootBodyHeight(m), true))
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
		base := renderSessionSearch(m)
		if m.mode == ModeChipPicker && chipPickerSourceMode(m) == ModePlanSearch {
			base = renderPlanSearch(m)
		}
		var popup string
		if m.mode == ModeChipPicker {
			popup = renderChipPickerBox(m)
		} else {
			popup = renderDeleteChoiceBox(m)
		}
		overlayH := rootBodyHeight(m)
		sb.WriteString(layout.CenterOverlay(base, popup, m.width, overlayH, true))
	case ModeActions, ModeConfirm, ModeConfigPicker, ModeHelp, ModeDetail, ModeCommandPalette, ModeASLResponse:
		var base string
		switch {
		case m.mode == ModeConfigPicker && m.configPickerReturnMode == ModePicker:
			base = renderPicker(m)
		case m.mode == ModeHelp:
			base = helpBaseView(m)
		default:
			base = rootBaseView(m)
		}
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
		overlayH := rootBodyHeight(m)
		if m.mode == ModeConfigPicker {
			// Top-anchored so the modal doesn't jump when the harness-switch
			// warning is appended/removed.
			sb.WriteString(layout.TopCenterOverlay(base, popup, m.width, overlayH, true))
		} else {
			sb.WriteString(layout.CenterOverlay(base, popup, m.width, overlayH, true))
		}
	default:
		// Header area varies by mode; list is always shown below.
		switch m.mode {
		case ModeSearch:
			sb.WriteString(renderSearch(m))
		default:
			sb.WriteString(renderHeader(m))
		}
		if m.mode == ModeList {
			sb.WriteString(renderDeprecationBanner(m))
			sb.WriteString(renderLegend(m))
			sb.WriteString(renderChips(m, m.width))
			sb.WriteString(renderInlineFilter(m))
		}
		if m.mode == ModeList && m.previewEnabled {
			sb.WriteString(renderSplit(m))
		} else {
			sb.WriteString(renderList(m))
		}
		sb.WriteString(renderDetailStripBlock(m))
	}

	return composeRootView(sb.String(), m)
}

// rootBaseView renders the full-layout view that currently owns the screen, so
// an overlay raised from Worktrees or Runs dims that view rather than the
// dashboard the operator is not looking at.
func rootBaseView(m Model) string {
	switch m.activeRootMode() {
	case ModeWorktrees:
		return renderWorktrees(m)
	case ModeASLRuns:
		return renderASLRuns(m)
	default:
		return renderDashboard(m)
	}
}

func composeRootView(body string, m Model) string {
	bodyLines := layout.SectionLines(body)
	footerLines := rootFooterLines(m)
	status := renderStatusBar(m)

	paddingLines := 1
	if m.height > 0 {
		footerBudget := m.height - statusBarLines
		if footerBudget < 0 {
			footerBudget = 0
		}
		if len(footerLines) > footerBudget {
			footerLines = footerLines[len(footerLines)-footerBudget:]
		}

		bodyBudget := m.height - len(footerLines) - statusBarLines
		if bodyBudget < 0 {
			bodyBudget = 0
		}
		if len(bodyLines) > bodyBudget {
			bodyLines = bodyLines[:bodyBudget]
		}
		paddingLines = bodyBudget - len(bodyLines)
	}

	lines := make([]string, 0, len(bodyLines)+paddingLines+len(footerLines)+statusBarLines)
	lines = append(lines, bodyLines...)
	for i := 0; i < paddingLines; i++ {
		lines = append(lines, "")
	}
	lines = append(lines, footerLines...)
	lines = append(lines, status)
	return strings.Join(lines, "\n")
}

func rootBodyHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	h := m.height - len(rootFooterLines(m)) - statusBarLines
	if h < 0 {
		return 0
	}
	return h
}

func rootFooterLines(m Model) []string {
	return layout.SectionLines(renderFooter(m))
}

// renderDashboard emits the canonical ModeList layout — header, deprecation
// banner, legend, chips, spacer, then list (or list+preview split).
// Overlay-modal modes use this as the dimmed base view behind their popup.
func renderDashboard(m Model) string {
	var sb strings.Builder
	sb.WriteString(renderHeader(m))
	sb.WriteString(renderDeprecationBanner(m))
	sb.WriteString(renderLegend(m))
	sb.WriteString(renderChips(m, m.width))
	sb.WriteString(renderInlineFilter(m))
	if m.previewEnabled {
		sb.WriteString(renderSplit(m))
	} else {
		sb.WriteString(renderList(m))
	}
	sb.WriteString(renderDetailStripBlock(m))
	return sb.String()
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
func dashboardListPaneWidth(m Model) int {
	if listWidth, _, ok := dashboardSplitWidths(m); ok {
		return listWidth
	}
	if m.width < 0 {
		return 0
	}
	return m.width
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
	paneHeight := m.height - chromeAboveListLines(m) - chromeBelowListLines(m) -
		1 /* padding row */ - footerBudgetLines(m) - statusBarLines

	listWidth, previewWidth, horizontal := dashboardSplitWidths(m)
	vertical := dashboardPreviewRequested(m) && width >= previewMinPaneWidth &&
		m.cfg.Defaults.Preview.Direction == "vertical"

	if paneHeight < previewMinPaneHeight || (!horizontal && !vertical) {
		return dashboardGeom{listWidth: width, listHeight: clampListHeight(paneHeight)}
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
			return dashboardGeom{listWidth: width, listHeight: clampListHeight(paneHeight)}
		}
		return dashboardGeom{
			split:         true,
			vertical:      true,
			listWidth:     width,
			listHeight:    paneHeight - previewHeight - 1,
			previewWidth:  width,
			previewHeight: previewHeight,
		}
	}
	return dashboardGeom{
		split:         true,
		listWidth:     listWidth,
		listHeight:    clampListHeight(paneHeight),
		previewWidth:  previewWidth,
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

// renderSplit renders the list and preview pane side-by-side or stacked
// according to the resolved geometry. Falls back to the plain list when
// dimensions make the preview unusable.
func renderSplit(m Model) string {
	g := dashboardGeometry(m)
	if !g.split {
		return renderList(m)
	}
	if g.vertical {
		listView := renderListAt(m, g.listWidth, g.listHeight)
		divider := styleDim.Render(strings.Repeat("─", g.listWidth))
		return lipgloss.JoinVertical(lipgloss.Left, listView, divider,
			renderPreviewPane(m, g.previewWidth, g.previewHeight)) + "\n"
	}
	listView := renderListAt(m, g.listWidth, g.listHeight)
	divider := strings.Repeat(styleDim.Render("│")+"\n", g.listHeight)
	divider = strings.TrimRight(divider, "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top, listView, divider,
		renderPreviewPane(m, g.previewWidth, g.previewHeight)) + "\n"
}

// renderPreviewPane paints whatever the current selection makes the pane mean.
//
// A family summary gets a project card rather than scrollback. That is not a
// presentation preference: a summary owns no window, so there is no scrollback
// to show, and the only way to produce some would be to pick one of its members
// — the exact "resolve a summary to an arbitrary session" the item layer refuses
// everywhere else. The card is the honest answer to the selection that was made.
//
// Any other non-session selection leaves sess at its zero value, which
// RenderPreview already paints as the empty pane it uses for an empty list.
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
		})
	}
	sess, _ := m.selectedSession()
	return dashboard.RenderPreview(width, height, sess, m.previewText, m.previewError, dashboard.ProfileColorMap(m.cfg), m.cfg)
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
	d := time.Duration(cfg.RefreshMs) * time.Millisecond
	return m, tea.Batch(fetchPreviewCmd(m.client, m.previewWindowID, m.previewTargetSeq), previewTickCmd(d))
}

// retargetPreview points the pane at a new target and opens a new generation for
// it. Every path that changes what the preview is showing goes through here, so
// no path can move the target without also invalidating the read that is still
// in flight for the previous one. The periodic tick is not such a path: it
// re-reads the current target under the current generation.
func (m *Model) retargetPreview(windowID int) {
	m.previewWindowID = windowID
	m.previewTargetSeq++
	m.previewText = ""
	m.previewError = ""
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
	return fetchPreviewCmd(m.client, winID, m.previewTargetSeq)
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
	return fetchPreviewCmd(m.client, winID, m.previewTargetSeq)
}

func (m Model) resumeListPreviewTickCmd() tea.Cmd {
	if !m.previewEnabled {
		return nil
	}
	d := time.Duration(m.cfg.Defaults.Preview.RefreshMs) * time.Millisecond
	return previewTickCmd(d)
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
	d := time.Duration(cfg.RefreshMs) * time.Millisecond
	return m, tea.Batch(fetchPreviewCmd(m.client, m.previewWindowID, m.previewTargetSeq), previewTickCmd(d))
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
