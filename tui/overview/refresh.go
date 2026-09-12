// The topology controller and what a completed read does to the selection.
//
// Two independent rules live here and they are worth separating before reading
// either.
//
// The CONTROLLER decides how many reads may be outstanding: exactly one, with at
// most one coalesced follow-up. Every refresh the overview performs — the startup
// load and each interval tick — enters through requestRefresh, which is what
// makes "one in flight" a property of the model rather than a habit of its
// callers. A burst of ticks arriving behind a slow read costs one catch-up, not
// one read each, and a completion carrying a superseded sequence is dropped
// rather than applied, so a slow read can never move the list backwards over a
// newer one.
//
// The RECONCILER decides what a fresh list does to the cursor. The selection is
// an index, but its identity is the Kitty TAB — the one fact about a row that is
// stable for the life of the pane — so reordering, a metadata change, an added
// session and a resize all leave the cursor on the same session by construction
// rather than by arithmetic. The anchor WINDOW is reconciled separately from it,
// because a tab whose harness restarted keeps its tab ID and changes its window
// ID, and it is the window ID that keys a preview.
//
// What a fresh list does to the CAPTURES is not decided here. A closed session
// and a re-anchored one both leave a stored buffer describing a window that no
// longer exists, but so does paging away from a tile that is still running, and
// expressing one of those against the live set and the other against the page
// would be two rules that have to agree. Both are therefore the visibility rule
// in capture.go, which this calls once the selection — and so the page — is
// settled.
//
// Failure is retention, never truncation. A read that reached neither the daemon
// nor Kitty leaves the rows, the freshness stamp and the selection exactly as
// they were and records the error; the surface then ages visibly out of its own
// last successful capture instead of emptying itself because a subprocess timed
// out.
package overview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// requestRefresh asks for a read of the live window tree. If one is already out
// the request collapses into the pending bit and no command is returned.
func (m *model) requestRefresh() tea.Cmd {
	if m.refreshInFlight {
		m.refreshPending = true
		return nil
	}
	return m.startRefresh()
}

// startRefresh draws the next sequence and dispatches the read. It assumes the
// caller has established that nothing is outstanding.
func (m *model) startRefresh() tea.Cmd {
	m.refreshNextSeq++
	m.refreshActiveSeq = m.refreshNextSeq
	m.refreshInFlight = true
	m.refreshPending = false
	return m.deps.topology(m.refreshActiveSeq, m.startupSourceWindowID)
}

// settleRefresh reports whether a completion belongs to the read currently
// outstanding, marking it finished when it does.
//
// A completion arriving when nothing is out was already settled, and one
// carrying a sequence other than the active one was superseded before it landed.
// Both are obsolete, and neither is allowed to touch the list, the freshness
// stamp, or the selection.
func (m *model) settleRefresh(seq int) bool {
	if !m.refreshInFlight || seq != m.refreshActiveSeq {
		return false
	}
	m.refreshInFlight = false
	return true
}

// catchUpRefresh starts the single read that coalesced while the one just
// finished was out.
//
// It is called only after a SUCCESS. A failure clears the pending bit instead,
// so a run of ticks that piled up during an outage cannot turn into an immediate
// retry storm against a daemon or a Kitty that is still down — the next scheduled
// tick asks again, one interval later.
func (m *model) catchUpRefresh() tea.Cmd {
	if m.refreshInFlight || !m.refreshPending {
		return nil
	}
	return m.startRefresh()
}

// handleSessions applies one successful read.
func (m model) handleSessions(msg commands.SessionsMsg) (tea.Model, tea.Cmd) {
	if !m.settleRefresh(msg.Seq) {
		return m, nil
	}
	// Freshness is stamped with the tree it describes rather than with the
	// moment it arrived, so the age rendered later is this reading plus
	// everything elapsed since. A newer tree — even one the daemon already
	// marked stale — therefore resets the displayed age and any delay warning
	// the previous one had raised.
	m.topology = msg.Topology
	m.topologyObservedAt = m.deps.now()
	m.err = nil
	wasLoaded := m.loaded
	m.loaded = true

	m.lastActivity = msg.Activity
	// msg.Watchdog and msg.States are deliberately dropped. The per-window state
	// the tile paints is already overlaid onto these rows by the shared reader,
	// and the watchdog is a dashboard status-bar input the grid has no slot for;
	// holding either here would be state nothing reads.
	if wasLoaded {
		m.sessions = RetainOrder(m.sessions, msg.Sessions)
	} else {
		m.sessions = OpeningOrder(msg.Sessions, msg.Activity, m.idleThreshold)
	}

	m.reconcileSelection(msg.SelectionTabID)
	// The selection has been placed, so the page is settled and the capture set
	// can be re-aimed at it. Doing it HERE rather than before the reconcile is
	// what lets a read that moved the cursor onto another page start that page's
	// captures in the same update.
	m.reconcileCaptures()
	// The source pane has now been resolved against a real window tree, so the
	// startup request is spent. Retaining it until HERE — rather than clearing it
	// when it was dispatched — is what lets a failed first read still select the
	// caller's tab on the retry.
	m.startupSourceWindowID = 0

	return m, tea.Batch(m.catchUpRefresh(), m.pumpCaptures())
}

// handleRefreshErr applies one failed read: the error is recorded, the rows and
// their freshness stamp are left alone, and the pending bit is dropped so the
// retry waits for the next tick.
//
// The startup source is deliberately NOT consumed. A first read that could not
// reach a window tree resolved nothing, so the operator's tab is still owed to
// them on whichever read succeeds first.
func (m model) handleRefreshErr(msg commands.ErrMsg) (tea.Model, tea.Cmd) {
	if !m.settleRefresh(msg.Seq) {
		return m, nil
	}
	m.refreshPending = false
	m.err = msg.Err
	return m, nil
}

// reconcileSelection places the cursor on the refreshed list.
//
// The three cases, in the order they are tried:
//
//  1. An unspent STARTUP request. The overview was opened from a pane, that
//     pane's tab is in the list, and the operator is looking at the session they
//     launched from. This outranks a prior selection because the only way a prior
//     selection exists while the request is still unspent is that the first read
//     failed and a navigation happened during the retry — and the request is
//     consumed by the same read that honors it, so it can win at most once.
//  2. The prior selection's TAB. Found means the cursor moves to wherever that
//     session now sits, which survives removal of earlier tabs, metadata
//     changes and resizes.
//  3. The prior POSITION, clamped. This is the tab that disappeared under the
//     cursor: the nearest surviving row is the one at the same place in the list,
//     or the last row when the list shrank past it. It is also the answer for a
//     list that was empty a moment ago and for a first read with no startup
//     request, both of which arrive here with a zero index and select the first
//     session — the same thing "nearest to position zero" means.
func (m *model) reconcileSelection(requestedTabID int) {
	if len(m.sessions) == 0 {
		// Everything closed. The cursor has nothing to be on and no identity to
		// remember; a later non-empty read starts from the first session rather
		// than from wherever the last surviving pane happened to be.
		m.selected, m.selectedTabID, m.selectedWindowID = 0, 0, 0
		return
	}
	if m.startupSourceWindowID != 0 && requestedTabID > 0 {
		if index, ok := indexOfTab(m.sessions, requestedTabID); ok {
			m.selectIndex(index)
			return
		}
	}
	if m.selectedTabID > 0 {
		if index, ok := indexOfTab(m.sessions, m.selectedTabID); ok {
			m.selectIndex(index)
			return
		}
	}
	m.selectIndex(clamp(m.selected, 0, len(m.sessions)-1))
}

// selectIndex moves the cursor and re-resolves the identity it addresses.
//
// Taking the tab AND the anchor window from the row is the "reconcile anchor
// changes separately" rule in two lines: the tab is the identity the next refresh
// will look the cursor up by, and the window is only a cache key, refreshed here
// so a restarted pane's tile stops being matched against the preview of a window
// that no longer exists.
func (m *model) selectIndex(index int) {
	m.selected = index
	m.selectedTabID = m.sessions[index].TabID
	m.selectedWindowID = m.sessions[index].WindowID
}

// indexOfTab finds the row for a Kitty tab.
func indexOfTab(sessions []session.RunningSession, tabID int) (int, bool) {
	for index := range sessions {
		if sessions[index].TabID == tabID {
			return index, true
		}
	}
	return 0, false
}
