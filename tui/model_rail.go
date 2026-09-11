package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/dashboard"
)

func (m *Model) syncRailState() {
	rows := dashboardRailRows(*m)
	if len(rows) == 0 {
		m.railCursor = 0
		m.railOffset = 0
		m.railFocused = false
		return
	}
	m.railCursor = clamp(m.railCursor, 0, len(rows)-1)
	g := dashboardGeometry(*m)
	if !g.rail {
		m.railFocused = false
		m.railOffset = 0
		return
	}
	m.railOffset = dashboard.ScrollRailToCursor(len(rows), m.railCursor, m.railOffset, g.railHeight)
}

func (m *Model) toggleRail() {
	if m.listView != dashboard.ViewUrgency {
		return
	}
	m.railRequested = !m.railRequested
	if !m.railRequested {
		m.railFocused = false
	}
	m.syncRailState()
	scrollListToCursor(m)
}

func (m *Model) focusRail() {
	if m.listView != dashboard.ViewUrgency || !dashboardGeometry(*m).rail {
		return
	}
	m.railFocused = true
	m.clearJumpChord()
	m.syncRailState()
}

func (m *Model) moveRailCursor(delta int) {
	rows := dashboardRailRows(*m)
	if len(rows) == 0 {
		return
	}
	m.railCursor = clamp(m.railCursor+delta, 0, len(rows)-1)
	m.syncRailState()
}

func (m *Model) applyRailSelection() tea.Cmd {
	rows := dashboardRailRows(*m)
	if m.railCursor < 0 || m.railCursor >= len(rows) {
		return nil
	}
	filter := m.activeFilter
	filter.Project = rows[m.railCursor].Project
	filter.Raw = dashboard.SerializeFilter(filter)
	cmd := m.applyFilterChange(filter)
	// The contextual rail source ignores Project, so applying a scope leaves the
	// same row selected and keeps keyboard focus in the rail.
	m.railFocused = true
	m.syncRailState()
	return cmd
}

func (m Model) handleRailKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case keyCtrlC, keyQ:
		return m, tea.Quit
	case keyRight:
		m.railFocused = false
		return m, nil
	case keyJ, keyDown:
		m.moveRailCursor(1)
	case keyK, keyUp:
		m.moveRailCursor(-1)
	case keyG:
		m.railCursor = 0
		m.syncRailState()
	case keyGG:
		m.railCursor = len(dashboardRailRows(m)) - 1
		m.syncRailState()
	case keyCtrlD:
		m.moveRailCursor(halfPageStep(dashboardGeometry(m).railHeight - 1))
	case keyCtrlU:
		m.moveRailCursor(-halfPageStep(dashboardGeometry(m).railHeight - 1))
	case keyPgDown:
		m.moveRailCursor(fullPageStep(dashboardGeometry(m).railHeight - 1))
	case keyPgUp:
		m.moveRailCursor(-fullPageStep(dashboardGeometry(m).railHeight - 1))
	case keyEnter:
		return m, m.applyRailSelection()
	case keySlash:
		m.railFocused = false
		return m, m.enterFilter("")
	case keyQuestion:
		m.railFocused = false
		m.openHelp(ModeList)
	case keyToggleRail:
		m.toggleRail()
	case keyInspector:
		// toggleInspector already re-syncs the rail and drops the rail's keyboard
		// claim when it opens, so the rail hands the key straight on rather than
		// post-processing the result the way the preview toggle did.
		return m.toggleInspector()
	case keyToggleView:
		m.railFocused = false
		return m, m.toggleListView()
	}
	return m, nil
}
