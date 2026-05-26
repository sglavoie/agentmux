package tui

import (
	tea "charm.land/bubbletea/v2"
)

// enterMessageDetail opens the full-message overlay for the message at
// viewerCursor. Callers should only invoke this when m.viewerMessages is
// non-empty and 0 <= m.viewerCursor < len(m.viewerMessages); the guard
// belongs at the call site so this helper stays a pure mode transition.
func (m Model) enterMessageDetail() (tea.Model, tea.Cmd) {
	m.mode = ModeMessageDetail
	m.messageDetailIdx = m.viewerCursor
	m.messageDetailScroll = 0
	return m, nil
}

// exitMessageDetail returns to ModeSessionView and zeroes the overlay
// state. viewerCursor is preserved so the user lands back on the same row
// they opened the overlay from.
func (m *Model) exitMessageDetail() {
	m.mode = ModeSessionView
	m.messageDetailIdx = -1
	m.messageDetailScroll = 0
}

// handleMessageDetailKey mirrors handleHelpKey: scroll-only navigation
// with esc/q/enter all closing the overlay. ctrl+c still quits the app.
func (m Model) handleMessageDetailKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	total, visible := messageDetailScrollBounds(m)
	switch msg.String() {
	case keyEsc, keyQ, keyEnter:
		m.exitMessageDetail()
		return m, nil
	case keyCtrlC:
		return m, tea.Quit
	case keyJ, keyDown:
		if m.messageDetailScroll+visible < total {
			m.messageDetailScroll++
		}
	case keyK, keyUp:
		if m.messageDetailScroll > 0 {
			m.messageDetailScroll--
		}
	case keyPgDown:
		m.messageDetailScroll += visible
		if m.messageDetailScroll+visible > total {
			m.messageDetailScroll = total - visible
		}
		if m.messageDetailScroll < 0 {
			m.messageDetailScroll = 0
		}
	case keyPgUp:
		m.messageDetailScroll -= visible
		if m.messageDetailScroll < 0 {
			m.messageDetailScroll = 0
		}
	case keyG:
		m.messageDetailScroll = 0
	case keyGG:
		m.messageDetailScroll = total - visible
		if m.messageDetailScroll < 0 {
			m.messageDetailScroll = 0
		}
	}
	return m, nil
}

// clampMessageDetailScroll pins m.messageDetailScroll to [0, max(0, total -
// visible)] at the model's current dimensions. Mirrors clampHelpOffset:
// called from the WindowSizeMsg handler so a viewport shrink doesn't leave
// a stale offset the user has to scroll through before the overlay reacts.
func clampMessageDetailScroll(m *Model) {
	total, visible := messageDetailScrollBounds(*m)
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.messageDetailScroll > maxOffset {
		m.messageDetailScroll = maxOffset
	}
	if m.messageDetailScroll < 0 {
		m.messageDetailScroll = 0
	}
}
