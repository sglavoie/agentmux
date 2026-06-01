package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/plans"
)

// handleSessionsMsg processes a sessionsMsg: rebuilds the session list,
// restores the cursor position, and clamps scroll offsets.
func (m Model) handleSessionsMsg(msg sessionsMsg) (tea.Model, tea.Cmd) {
	m.err = nil
	// Capture the prior cursor's row identity before the list is
	// rebuilt so we can restore it post-refresh. Empty list → no
	// prior selection to preserve.
	var prior cursorIdentity
	if len(m.filtered) > 0 && m.cursor >= 0 && m.cursor < len(m.filtered) {
		prior = captureCursorIdentity(m.filtered[m.cursor])
	}
	m.lastActivity = msg.activity
	m.watchdog = msg.watchdog
	m.sessions = msg.sessions
	m.recordTransitions()
	m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.sortFiltered()
	m.cursor = restoreCursorByIdentity(m.filtered, prior, m.lastActivity, m.idleThreshold)
	clampListOffset(&m)
	scrollListToCursor(&m)
	return m, nil
}

// handleActionDone processes an actionDoneMsg: records any action error and
// triggers an immediate session refresh so the list reflects the new state.
func (m Model) handleActionDone(msg actionDoneMsg) (tea.Model, tea.Cmd) {
	m.actionErr = msg.err
	// Trigger a refresh so the list reflects the new state.
	return m, fetchCmd(m.client, m.cfg, m.sortMode)
}

// handleResortDone processes a resortDoneMsg: records any error, pushes a
// success or failure toast, and triggers a session refresh.
func (m Model) handleResortDone(msg resortDoneMsg) (tea.Model, tea.Cmd) {
	m.actionErr = msg.err
	var cmds []tea.Cmd
	var t Toast
	if msg.err != nil {
		t = Toast{Severity: toastError, Text: "Resort failed: " + msg.err.Error()}
	} else {
		t = Toast{Severity: toastSuccess, Text: "Tabs resorted"}
	}
	if c := m.pushToast(t); c != nil {
		cmds = append(cmds, c)
	}
	cmds = append(cmds, fetchCmd(m.client, m.cfg, m.sortMode))
	return m, tea.Batch(cmds...)
}

// handlePickerOpenDone processes a pickerOpenDoneMsg: quits on success or
// surfaces the error in the header and triggers a refresh on failure.
func (m Model) handlePickerOpenDone(msg pickerOpenDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.actionErr = msg.err
		return m, fetchCmd(m.client, m.cfg, m.sortMode)
	}
	return m, tea.Quit
}

// handleFocusDone processes a focusDoneMsg: quits on success or records the
// error.
func (m Model) handleFocusDone(msg focusDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.actionErr = msg.err
		return m, nil
	}
	return m, tea.Quit
}

// handlePeekDone processes a peekDoneMsg: records any error from the overlay
// pager launch.
func (m Model) handlePeekDone(msg peekDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.actionErr = msg.err
	}
	return m, nil
}

// handleDetailEvents processes a detailEventsMsg: updates the detail pane
// event list, dropping stale responses from a previous mode or window.
func (m Model) handleDetailEvents(msg detailEventsMsg) (tea.Model, tea.Cmd) {
	// Stale responses (mode changed or different window than the one
	// currently shown in the pane) are dropped silently so a slow
	// daemon roundtrip cannot overwrite a fresh request's loading
	// state or repopulate the pane after the user closed it.
	if m.mode != ModeDetail || msg.windowID != m.detailWindowID {
		return m, nil
	}
	m.detailEvents = msg.events
	m.detailErr = msg.err
	m.detailLoading = false
	// A refresh that shrinks the event ring (daemon GC, or an empty
	// reply) can leave detailScroll above the new maxOffset. The
	// renderer clamps for display, but the next j/k/pgup/pgdn press
	// would operate on the stale offset and chew through dead rows
	// before the viewport reacts — mirror the WindowSizeMsg clamp.
	clampDetailScroll(&m)
	return m, nil
}

// handleSessionsRefreshDone processes a sessionsRefreshDoneMsg: records any
// refresh error and follows up with a search dispatch.
func (m Model) handleSessionsRefreshDone(msg sessionsRefreshDoneMsg) (tea.Model, tea.Cmd) {
	m.sessionRefreshErr = msg.err
	// Always follow a refresh with a search dispatch — even on error,
	// because Refresh updates whatever profiles succeeded and the old
	// index entries are still usable. Reuse the seq the refresh was
	// dispatched with so a chip change made mid-refresh wins.
	if msg.seq < m.sessionSearchSeq {
		// A newer dispatch is already in flight; defer to it.
		return m, nil
	}
	return m, runSessionSearchCmd(msg.seq, m.transcriptIndex, m.transcriptReaders, m.currentSessionQuery())
}

// handleSessionSearchDebounce processes a sessionSearchDebounceMsg: drops
// stale debounce ticks and fires the search for the current seq.
func (m Model) handleSessionSearchDebounce(msg sessionSearchDebounceMsg) (tea.Model, tea.Cmd) {
	// Stale debounce ticks (the user kept typing) are silently dropped.
	if msg.seq != m.sessionSearchSeq {
		return m, nil
	}
	return m, runSessionSearchCmd(msg.seq, m.transcriptIndex, m.transcriptReaders, m.currentSessionQuery())
}

// handleSessionSearchDone processes a sessionSearchDoneMsg: updates the
// session results list and clamps the cursor.
func (m Model) handleSessionSearchDone(msg sessionSearchDoneMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.sessionSearchSeq {
		return m, nil
	}
	m.sessionLoading = false
	m.sessionSearchErr = msg.err
	m.sessionResults = msg.results
	if m.sessionCursor >= len(m.sessionResults) {
		m.sessionCursor = len(m.sessionResults) - 1
		if m.sessionCursor < 0 {
			m.sessionCursor = 0
		}
	}
	scrollSessionSearchToCursor(&m)
	return m, nil
}

// handlePlansRefreshDone processes a plansRefreshDoneMsg: records any refresh
// error and follows up with a plan search dispatch.
func (m Model) handlePlansRefreshDone(msg plansRefreshDoneMsg) (tea.Model, tea.Cmd) {
	m.planRefreshErr = msg.err
	if msg.seq < m.planSearchSeq {
		return m, nil
	}
	return m, runPlanSearchCmd(msg.seq, m.plansIndex, m.currentPlanQuery())
}

// handlePlanSearchDebounce processes a planSearchDebounceMsg: drops stale
// debounce ticks and fires the plan search for the current seq.
func (m Model) handlePlanSearchDebounce(msg planSearchDebounceMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.planSearchSeq {
		return m, nil
	}
	return m, runPlanSearchCmd(msg.seq, m.plansIndex, m.currentPlanQuery())
}

// handlePlanSearchDone processes a planSearchDoneMsg: updates the plan
// results list and clamps the cursor.
func (m Model) handlePlanSearchDone(msg planSearchDoneMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.planSearchSeq {
		return m, nil
	}
	m.planLoading = false
	m.planSearchErr = msg.err
	m.planResults = msg.results
	// Belt-and-suspenders: the in-search sort already honours
	// Query.Sort, but the cycle keystroke path mutates m.planSortMode
	// without re-issuing a search, so re-sort here on every receive.
	plans.SortResults(m.planResults, m.planSortMode)
	if m.planCursor >= len(m.planResults) {
		m.planCursor = len(m.planResults) - 1
		if m.planCursor < 0 {
			m.planCursor = 0
		}
	}
	scrollPlanSearchToCursor(&m)
	return m, nil
}

// handlePlanEditorErr processes a planEditorErrMsg: pushes an error toast.
func (m Model) handlePlanEditorErr(msg planEditorErrMsg) (tea.Model, tea.Cmd) {
	if c := m.pushToast(Toast{Severity: toastError, Text: "open editor: " + msg.err.Error()}); c != nil {
		return m, c
	}
	return m, nil
}

// handlePlanFilesLoaded processes a planFilesLoadedMsg: populates the plan
// viewer, dropping stale loads from a previous viewer session.
func (m Model) handlePlanFilesLoaded(msg planFilesLoadedMsg) (tea.Model, tea.Cmd) {
	// Drop stale loads: either the user already left the viewer, or
	// pressed Enter on a different plan before this one finished.
	if m.mode != ModePlanView || msg.seq != m.planViewerLoadSeq {
		return m, nil
	}
	m.planViewerLoading = false
	m.planViewerLoadErr = msg.err
	m.planViewerLines = msg.lines
	m.planViewerMatchLines = computePlanViewerMatches(m.planViewerLines, m.planQuery)
	m.planViewerMatchIdx = 0
	m.planViewerScroll = 0
	m.planViewerCursor = 0
	if len(m.planViewerMatchLines) > 0 {
		m.planViewerCursor = m.planViewerMatchLines[0]
	}
	scrollPlanViewerToCursor(&m)
	return m, nil
}

// handleViewerMessagesLoaded processes a viewerMessagesLoadedMsg: populates
// the session transcript viewer, dropping stale loads.
func (m Model) handleViewerMessagesLoaded(msg viewerMessagesLoadedMsg) (tea.Model, tea.Cmd) {
	// Drop stale loads: either the user already left the viewer, or
	// pressed Enter on a different row before this one finished.
	if m.mode != ModeSessionView || msg.seq != m.viewerLoadSeq {
		return m, nil
	}
	m.viewerLoading = false
	m.viewerLoadErr = msg.err
	m.viewerMessages = msg.messages
	m.viewerMatchLines = computeViewerMatches(m.viewerMessages, m.sessionQuery)
	m.viewerMatchIdx = 0
	m.viewerScroll = 0
	m.viewerCursor = 0
	if len(m.viewerMatchLines) > 0 {
		m.viewerCursor = m.viewerMatchLines[0]
	}
	scrollViewerToCursor(&m)
	return m, nil
}
