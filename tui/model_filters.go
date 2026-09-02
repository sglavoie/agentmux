package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/dashboard"
)

// applyFilterChange swaps in a new active filter, re-applies it to the
// session list, and resets cursor + chip focus. Used by chip removal and
// the Esc "clear all" path so they share the bookkeeping.
func (m *Model) applyFilterChange(f dashboard.Filter) {
	m.activeFilter = f
	m.filtered = dashboard.ApplyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.sortFiltered()
	m.cursor = 0
	m.focusedChipIdx = -1
	scrollListToCursor(m)
}

// cycleDashboardProfile updates the dashboard's existing @profile filter
// component while preserving all other DSL components.
func (m *Model) cycleDashboardProfile() tea.Cmd {
	next := dashboard.NextProfileFilter(m.sessions, m.activeFilter.Account)
	filter := m.activeFilter
	filter.Account = next
	filter.Raw = dashboard.SerializeFilter(filter)
	m.applyFilterChange(filter)
	m.listOffset = 0
	return m.refreshPreviewForCursor()
}

// enterFilter switches into ModeSearch with the given pre-filled buffer and
// snapshots the current filter so Esc can restore it. The pre-fill is parsed
// and live-applied immediately so the list reflects the buffer on entry.
func (m *Model) enterFilter(prefill string) {
	m.clearJumpChord()
	m.savedFilter = m.activeFilter
	m.filterText = prefill
	m.activeFilter = dashboard.Parse(prefill)
	m.filtered = dashboard.ApplyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.sortFiltered()
	m.cursor = 0
	m.focusedChipIdx = -1
	m.mode = ModeSearch
	m.recomputeCompletion()
	scrollListToCursor(m)
}

// reapplyFilterLive parses filterText and refreshes the list. Used by every
// keystroke handler in ModeSearch to provide live filtering.
func (m *Model) reapplyFilterLive() {
	m.activeFilter = dashboard.Parse(m.filterText)
	m.filtered = dashboard.ApplyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.sortFiltered()
	m.cursor = clamp(m.cursor, 0, len(m.filtered)-1)
	m.focusedChipIdx = -1
	scrollListToCursor(m)
}

// sortFiltered orders m.filtered so attention items come first, then active,
// then idle, using the model's activity map and idle threshold to bucket
// sessions with no state icon.
func (m Model) sortFiltered() {
	dashboard.SortRows(m.filtered, m.lastActivity, m.idleThreshold)
}

// clamp constrains v to [lo, hi]. Returns lo when hi < lo.
func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
