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
	return sb.String()
}

// renderSplit renders the list and preview pane side-by-side or stacked
// according to the configured direction. Falls back to the plain list when
// dimensions or selection make the preview unusable.
func renderSplit(m Model) string {
	cfg := m.cfg.Defaults.Preview
	footerLines := footerBudgetLines(m)
	// Vertical space available for the list+preview area: total height minus
	// all chrome rendered above the list (header + banner + legend + chips +
	// spacer), minus the padding row between body and footer, minus the footer,
	// minus the sticky status bar. Must stay in lock-step with
	// listVisibleHeight's preview branch so scrollListToCursor and renderListAt
	// agree on the budget — otherwise the cursor can land at a row the
	// renderer emits past the visible terminal area.
	paneHeight := m.height - chromeAboveListLines(m) - 1 /* padding row */ - footerLines - statusBarLines
	if paneHeight < 4 || m.width < 20 {
		return renderList(m)
	}

	var sess session.RunningSession
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		sess = m.filtered[m.cursor]
	}
	acctColor := dashboard.ProfileColorMap(m.cfg)

	if cfg.Direction == "vertical" {
		previewHeight := int(float64(paneHeight) * cfg.Ratio)
		if previewHeight < 3 {
			previewHeight = 3
		}
		if previewHeight > paneHeight-2 {
			previewHeight = paneHeight - 2
		}
		listHeight := paneHeight - previewHeight - 1
		listView := renderListAt(m, m.width, listHeight)
		divider := styleDim.Render(strings.Repeat("─", m.width))
		previewView := dashboard.RenderPreview(m.width, previewHeight, sess, m.previewText, m.previewError, acctColor, m.cfg)
		return lipgloss.JoinVertical(lipgloss.Left, listView, divider, previewView) + "\n"
	}

	previewWidth := int(float64(m.width) * cfg.Ratio)
	if previewWidth < 20 {
		previewWidth = 20
	}
	if previewWidth > m.width-20 {
		previewWidth = m.width - 20
	}
	listWidth := m.width - previewWidth - 1
	// A horizontal split shrinks the list to listWidth, but formatRow cannot
	// lay out its fixed columns below minListRowWidth. Below the floor the list
	// column can only be truncated, which pairs a cramped, elided list with the
	// preview; fall back to the full-width plain list instead (mirrors the
	// paneHeight<4 || m.width<20 guard above). The plain list is itself clamped
	// to m.width by renderListAt's safety truncation.
	if listWidth < dashboard.MinRowWidth() {
		return renderList(m)
	}
	listView := renderListAt(m, listWidth, paneHeight)
	previewView := dashboard.RenderPreview(previewWidth, paneHeight, sess, m.previewText, m.previewError, acctColor, m.cfg)
	divider := strings.Repeat(styleDim.Render("│")+"\n", paneHeight)
	divider = strings.TrimRight(divider, "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top, listView, divider, previewView) + "\n"
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
		m.previewText = ""
		m.previewError = ""
		m.previewWindowID = 0
		scrollListToCursor(&m)
		return m, nil
	}
	cfg := m.cfg.Defaults.Preview
	if !previewFits(m.width, m.height, dashboardPreviewEnableFooterLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
		return m, m.pushPreviewNotice("preview unavailable: terminal too small")
	}
	m.previewEnabled = true
	m.previewText = ""
	m.previewError = ""
	m.previewWindowID = m.currentPreviewWindowID()
	m.clearPreviewNotice()
	scrollListToCursor(&m)
	d := time.Duration(cfg.RefreshMs) * time.Millisecond
	return m, tea.Batch(fetchPreviewCmd(m.client, m.previewWindowID), previewTickCmd(d))
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
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		return m.filtered[m.cursor].WindowID
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
	m.previewWindowID = winID
	m.previewText = ""
	m.previewError = ""
	return fetchPreviewCmd(m.client, winID)
}

// refreshPreviewForCursor returns an immediate fetch cmd when the cursor
// has moved to a session backed by a different window. Returns nil when
// preview is off or the selection still points at the same window.
func (m *Model) refreshPreviewForCursor() tea.Cmd {
	if !m.previewEnabled {
		return nil
	}
	var winID int
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		winID = m.filtered[m.cursor].WindowID
	}
	if winID == m.previewWindowID {
		return nil
	}
	m.previewWindowID = winID
	m.previewText = ""
	m.previewError = ""
	return fetchPreviewCmd(m.client, winID)
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
		m.previewText = ""
		m.previewError = ""
		m.previewWindowID = 0
		scrollPickerToCursor(&m)
		return m, nil
	}
	cfg := m.cfg.Defaults.Preview
	if !previewFits(m.width, m.height, footerBudgetLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
		return m, m.pushPreviewNotice("preview unavailable: terminal too small")
	}
	m.pickerPreviewEnabled = true
	m.previewText = ""
	m.previewError = ""
	m.previewWindowID = m.pickerCurrentWindowID()
	m.clearPreviewNotice()
	scrollPickerToCursor(&m)
	d := time.Duration(cfg.RefreshMs) * time.Millisecond
	return m, tea.Batch(fetchPreviewCmd(m.client, m.previewWindowID), previewTickCmd(d))
}

// disablePreviewWithNotice turns the dashboard preview off and shows a
// transient status-bar notice. Used by the resize guard.
func (m *Model) disablePreviewWithNotice(msg string) tea.Cmd {
	m.previewEnabled = false
	m.previewText = ""
	m.previewError = ""
	m.previewWindowID = 0
	return m.pushPreviewNotice(msg)
}

func (m *Model) disablePickerPreviewWithNotice(msg string) tea.Cmd {
	m.pickerPreviewEnabled = false
	m.previewText = ""
	m.previewError = ""
	m.previewWindowID = 0
	return m.pushPreviewNotice(msg)
}
