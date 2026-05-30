package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sglavoie/agentmux/internal/session"
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
		// Modal overlays on top of ModeSessionSearch. The base view is
		// rendered then dimmed; the bordered popup is composited via
		// line-splicing so the underlying list stays visible around it.
		// See tui/overlay.go for the splicing algorithm.
		base := renderSessionSearch(m)
		var popup string
		if m.mode == ModeChipPicker {
			popup = renderChipPickerBox(m)
		} else {
			popup = renderDeleteChoiceBox(m)
		}
		overlayH := m.height - previewFooterLines(m) - 1 // trailing \n appended below
		sb.WriteString(centerOverlay(base, popup, m.width, overlayH, true))
	case ModeActions, ModeConfirm, ModeConfigPicker, ModeHelp, ModeDetail:
		var base string
		if m.mode == ModeConfigPicker && m.configPickerReturnMode == ModePicker {
			base = renderPicker(m)
		} else {
			base = renderDashboard(m)
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
		}
		overlayH := m.height - previewFooterLines(m) - 1
		if m.mode == ModeConfigPicker {
			// Top-anchored so the modal doesn't jump when the harness-switch
			// warning is appended/removed.
			sb.WriteString(topCenterOverlay(base, popup, m.width, overlayH, true))
		} else {
			sb.WriteString(centerOverlay(base, popup, m.width, overlayH, true))
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

	sb.WriteByte('\n')

	sb.WriteString(renderToasts(m))
	sb.WriteString(renderFooter(m))

	return sb.String()
}

// renderDashboard emits the canonical ModeList layout — header, deprecation
// banner, legend, chips, inline filter, then list (or list+preview split).
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
	footerLines := previewFooterLines(m)
	// Vertical space available for the list+preview area: total height minus
	// all chrome rendered above the list (header + banner + legend + chips +
	// inline filter), minus the trailing newline render() writes after the
	// list block, minus the footer. Must stay in lock-step with
	// listVisibleHeight's preview branch so scrollListToCursor and renderListAt
	// agree on the budget — otherwise the cursor can land at a row the
	// renderer emits past the visible terminal area.
	paneHeight := m.height - chromeAboveListLines(m) - 1 /* trailing newline */ - footerLines
	if paneHeight < 4 || m.width < 20 {
		return renderList(m)
	}

	var sess session.RunningSession
	if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
		sess = m.filtered[m.cursor]
	}
	acctColor := buildProfileColorMap(m)

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
		previewView := renderPreview(m.width, previewHeight, sess, m.previewText, m.previewError, acctColor, m.cfg)
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
	listView := renderListAt(m, listWidth, paneHeight)
	previewView := renderPreview(previewWidth, paneHeight, sess, m.previewText, m.previewError, acctColor, m.cfg)
	divider := strings.Repeat(styleDim.Render("│")+"\n", paneHeight)
	divider = strings.TrimRight(divider, "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top, listView, divider, previewView) + "\n"
}

// previewFooterLines returns the number of lines the footer will occupy in
// the current mode (used for layout math and resize fits-check).
func previewFooterLines(m Model) int {
	if m.mode == ModePicker {
		// Picker footer is always two lines regardless of preview state.
		return 2
	}
	if m.mode != ModeList {
		return 2
	}
	noticeActive := m.previewNotice != "" &&
		(m.previewNoticeExpires.IsZero() || time.Now().Before(m.previewNoticeExpires))
	if m.previewEnabled {
		// Footer keybindings are suppressed; only the notice (if any) takes a line.
		if noticeActive {
			return 1
		}
		return 0
	}
	if noticeActive {
		return 3
	}
	return 2
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
	if !previewFits(m.width, m.height, previewFooterLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
		m.previewNotice = "preview unavailable: terminal too small"
		m.previewNoticeExpires = time.Now().Add(2 * time.Second)
		return m, nil
	}
	m.previewEnabled = true
	m.previewText = ""
	m.previewError = ""
	m.previewWindowID = m.currentPreviewWindowID()
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
// the picker's highlighted config row, or 0 when nothing matches.
func (m Model) pickerCurrentWindowID() int {
	if len(m.pickerFilteredIdx) == 0 || m.pickerCursor >= len(m.pickerFilteredIdx) {
		return 0
	}
	origIdx := m.pickerFilteredIdx[m.pickerCursor]
	sess := m.pickerSessionWithResolvedProfile(origIdx)
	key := session.SessionKey(sess.EffectiveProfile(), sess.Title)
	for _, rs := range m.sessions {
		if runningSessionKey(rs) == key {
			return rs.WindowID
		}
	}
	return 0
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
	if !previewFits(m.width, m.height, previewFooterLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
		m.previewNotice = "preview unavailable: terminal too small"
		m.previewNoticeExpires = time.Now().Add(2 * time.Second)
		return m, nil
	}
	m.pickerPreviewEnabled = true
	m.previewText = ""
	m.previewError = ""
	m.previewWindowID = m.pickerCurrentWindowID()
	scrollPickerToCursor(&m)
	d := time.Duration(cfg.RefreshMs) * time.Millisecond
	return m, tea.Batch(fetchPreviewCmd(m.client, m.previewWindowID), previewTickCmd(d))
}

// disablePreviewWithNotice turns the preview off and shows a transient footer
// notice. Used both by the resize guard and any other auto-disable path.
func (m *Model) disablePreviewWithNotice(msg string) {
	m.previewEnabled = false
	m.previewText = ""
	m.previewError = ""
	m.previewWindowID = 0
	m.previewNotice = msg
	m.previewNoticeExpires = time.Now().Add(2 * time.Second)
}
