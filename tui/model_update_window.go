package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Health-pip refresh cadences. Daemon checks are a cheap socket round-trip
// (5s), hook checks read each profile's settings.json so they run on a
// longer interval (15s).
const (
	daemonHealthInterval = 5 * time.Second
	hookHealthInterval   = 15 * time.Second
)

func (m Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	cfg := m.cfg.Defaults.Preview
	var cmds []tea.Cmd
	if m.previewEnabled {
		if !previewFits(m.width, m.height, footerBudgetLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
			if c := m.disablePreviewWithNotice("preview disabled: terminal too small"); c != nil {
				cmds = append(cmds, c)
			}
		}
	}
	if m.pickerPreviewEnabled {
		if !previewFits(m.width, m.height, footerBudgetLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
			if c := m.disablePickerPreviewWithNotice("preview disabled: terminal too small"); c != nil {
				cmds = append(cmds, c)
			}
		}
	}
	// Clamp scroll offsets to the new viewport. Without this, a resize
	// that shrinks the visible window leaves the persisted offset above
	// maxOffset; the renderer clamps for display but the next j/k press
	// operates on the stale value, so the user has to scroll through
	// dead rows before the viewport reacts.
	clampDetailScroll(&m)
	clampHelpOffset(&m)
	clampMessageDetailScroll(&m)
	clampListOffset(&m)
	scrollListToCursor(&m)
	clampPickerOffset(&m)
	scrollPickerToCursor(&m)
	scrollSessionSearchToCursor(&m)
	scrollPlanSearchToCursor(&m)
	clampViewerScroll(&m)
	scrollViewerToCursor(&m)
	clampPlanViewerScroll(&m)
	scrollPlanViewerToCursor(&m)
	scrollChipPickerToCursor(&m)
	scrollLoopTailPickerToCursor(&m)
	if len(cmds) > 0 {
		return m, tea.Batch(cmds...)
	}
	return m, nil
}
