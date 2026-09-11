// This file is what the root model does about project scope: the overlay that
// lists the projects the current session set is spread across, and the one path
// by which choosing one of them reaches the dashboard filter.
//
// Two properties are the surface rather than incidental to it.
//
// The scope list is DERIVED, never captured. Every question the overlay asks —
// which rows exist, where the cursor is, how many sessions each scope holds — is
// answered against the model at the moment it is asked, so a background refresh
// that lands while the overlay is open updates the tallies in place. The
// selection survives that because it is stored as a project identity rather than
// as an index: a scope that moved is followed, and a scope that vanished falls
// back to All rather than to whatever row inherited its number.
//
// Applying a scope changes ONE filter component. It goes through the same
// serialization and the same selection-preserving reapplication every other
// filter edit goes through, which is what keeps a quoted label quoted, keeps the
// operator's other chips, and lands the dashboard cursor back on the session it
// was on when that session is still visible.

package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/dashboard"
)

// projectScopes is the contextual project aggregation the switcher lists. Only
// the applied project selector is removed before aggregating, so every other
// filter component still constrains both the scopes offered and their tallies —
// a switcher opened under `@work !attention` offers the projects that have work
// needing attention, not every project that exists.
func projectScopes(m Model) []dashboard.ProjectScope {
	contextual := dashboard.ApplyFilter(m.sessions, m.activeFilter.WithoutProject(), m.lastActivity, m.idleThreshold)
	return dashboard.BuildProjectScopes(contextual, m.lastActivity, m.idleThreshold)
}

// projectSwitcherScopes is the list the overlay actually draws: the contextual
// aggregation narrowed by the transient search query.
func projectSwitcherScopes(m Model) []dashboard.ProjectScope {
	return dashboard.FilterProjectScopes(projectScopes(m), m.projectSwitcherQuery)
}

// projectSwitcherCursor resolves the stored scope identity to a row in the list
// as it stands right now. An identity the current list no longer offers — a
// project whose last session closed, or one the search query excludes — lands on
// row zero, which the aggregation guarantees is All.
func projectSwitcherCursor(m Model) int {
	scopes := projectSwitcherScopes(m)
	if i := dashboard.FindProjectScope(scopes, m.projectSwitcherKey); i >= 0 {
		return i
	}
	return 0
}

// openProjectSwitcher raises the overlay over the dashboard, preselecting the
// scope the filter currently names so the first thing the operator sees is where
// they already are.
func (m Model) openProjectSwitcher() (tea.Model, tea.Cmd) {
	m.mode = ModeProjectSwitcher
	m.projectSwitcherQuery = ""
	m.projectSwitcherSearch = false
	m.projectSwitcherKey = dashboard.ProjectScope{Project: m.activeFilter.Project}.Key()
	m.projectSwitcherOffset = 0
	scrollProjectSwitcherToCursor(&m)
	return m, nil
}

// closeProjectSwitcher drops the overlay without touching the filter.
func (m *Model) closeProjectSwitcher() {
	m.mode = ModeList
	m.projectSwitcherQuery = ""
	m.projectSwitcherSearch = false
	m.projectSwitcherOffset = 0
}

// moveProjectSwitcherCursor walks the visible scope list by delta and records
// the identity it landed on.
func (m *Model) moveProjectSwitcherCursor(delta int) {
	scopes := projectSwitcherScopes(*m)
	if len(scopes) == 0 {
		return
	}
	next := clamp(projectSwitcherCursor(*m)+delta, 0, len(scopes)-1)
	m.projectSwitcherKey = scopes[next].Key()
	scrollProjectSwitcherToCursor(m)
}

// selectProjectScopeAt records the identity of the row at index, for the pointer
// and for the g/G jumps.
func (m *Model) selectProjectScopeAt(index int) {
	scopes := projectSwitcherScopes(*m)
	if index < 0 || index >= len(scopes) {
		return
	}
	m.projectSwitcherKey = scopes[index].Key()
	scrollProjectSwitcherToCursor(m)
}

// applyProjectScope swaps the filter's project component for the selected scope
// and closes the overlay.
//
// Only Filter.Project is touched. Re-serializing from the whole filter is what
// carries every other component across unchanged and what re-quotes a label with
// a space in it, and applyFilterChange is what carries the dashboard's selection
// across the narrowing. Neither is re-implemented here, because a second
// spelling of either is a second chance to drop a chip or lose a cursor.
func (m *Model) applyProjectScope() tea.Cmd {
	scopes := projectSwitcherScopes(*m)
	cursor := projectSwitcherCursor(*m)
	if cursor < 0 || cursor >= len(scopes) {
		m.closeProjectSwitcher()
		return nil
	}
	filter := m.activeFilter
	filter.Project = scopes[cursor].Project
	filter.Raw = dashboard.SerializeFilter(filter)
	cmd := m.applyFilterChange(filter)
	m.closeProjectSwitcher()
	return cmd
}

// handleProjectSwitcherKey dispatches keys for ModeProjectSwitcher. The search
// sub-mode owns the printable keys while it is active, which is why it is asked
// first rather than folded into the navigation switch.
func (m Model) handleProjectSwitcherKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.projectSwitcherSearch {
		return m.handleProjectSwitcherSearchKey(msg)
	}
	return m.handleProjectSwitcherNavKey(msg)
}

func (m Model) handleProjectSwitcherSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc, keyEnter:
		// Leaving the input keeps the query. Esc peels the sub-mode, not the
		// overlay: the narrowed list is what the operator typed it for, and a
		// second Esc still cancels the whole thing.
		m.projectSwitcherSearch = false
		return m, nil
	case keyBackspace:
		if r := []rune(m.projectSwitcherQuery); len(r) > 0 {
			m.projectSwitcherQuery = string(r[:len(r)-1])
			m.reselectProjectScopeAfterQuery()
		}
		return m, nil
	case keyDown, keyUp:
		delta := 1
		if msg.String() == keyUp {
			delta = -1
		}
		m.moveProjectSwitcherCursor(delta)
		return m, nil
	default:
		if t := msg.Key().Text; t != "" {
			m.projectSwitcherQuery += t
			m.reselectProjectScopeAfterQuery()
		}
		return m, nil
	}
}

// reselectProjectScopeAfterQuery keeps the selection on the scope it was on when
// that scope is still listed, and lands on the first row otherwise. A query is a
// narrowing of what is offered rather than a new choice, so typing a character
// that still leaves the selected project visible must not move off it.
func (m *Model) reselectProjectScopeAfterQuery() {
	scopes := projectSwitcherScopes(*m)
	if dashboard.FindProjectScope(scopes, m.projectSwitcherKey) < 0 && len(scopes) > 0 {
		m.projectSwitcherKey = scopes[0].Key()
	}
	m.projectSwitcherOffset = 0
	scrollProjectSwitcherToCursor(m)
}

func (m Model) handleProjectSwitcherNavKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keySlash:
		m.projectSwitcherSearch = true
		return m, nil
	case keyEsc, keyQ, keyProjectSwitcher:
		m.closeProjectSwitcher()
		return m, nil
	case keyJ, keyDown:
		m.moveProjectSwitcherCursor(1)
		return m, nil
	case keyK, keyUp:
		m.moveProjectSwitcherCursor(-1)
		return m, nil
	case keyG:
		m.selectProjectScopeAt(0)
		return m, nil
	case keyGG:
		m.selectProjectScopeAt(len(projectSwitcherScopes(m)) - 1)
		return m, nil
	case keyPgDown:
		m.moveProjectSwitcherCursor(fullPageStep(projectSwitcherVisibleHeight(m)))
		return m, nil
	case keyPgUp:
		m.moveProjectSwitcherCursor(-fullPageStep(projectSwitcherVisibleHeight(m)))
		return m, nil
	case keyCtrlD:
		m.moveProjectSwitcherCursor(halfPageStep(projectSwitcherVisibleHeight(m)))
		return m, nil
	case keyCtrlU:
		m.moveProjectSwitcherCursor(-halfPageStep(projectSwitcherVisibleHeight(m)))
		return m, nil
	case keyEnter:
		return m, m.applyProjectScope()
	}
	return m, nil
}

// scrollProjectSwitcherToCursor keeps the resolved cursor inside the visible
// window. The renderer spends a row on ▲ when the window is scrolled and a row
// on ▼ when scopes remain past it, so the budget subtracts them here too.
func scrollProjectSwitcherToCursor(m *Model) {
	total := len(projectSwitcherScopes(*m))
	height := projectSwitcherVisibleHeight(*m)
	if height <= 0 || total == 0 {
		m.projectSwitcherOffset = 0
		return
	}
	cursor := projectSwitcherCursor(*m)
	if cursor < m.projectSwitcherOffset {
		m.projectSwitcherOffset = cursor
	}
	for m.projectSwitcherOffset < cursor {
		budget := height
		if m.projectSwitcherOffset > 0 {
			budget--
		}
		if cursor < total-1 {
			budget--
		}
		if budget < 1 {
			budget = 1
		}
		if cursor-m.projectSwitcherOffset+1 <= budget {
			break
		}
		m.projectSwitcherOffset++
	}
	if m.projectSwitcherOffset < 0 {
		m.projectSwitcherOffset = 0
	}
}
