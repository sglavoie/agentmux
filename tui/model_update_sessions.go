// This file is how the root model reacts to work it started: the fetch and
// action results that land back as messages, the commands it issues to run
// conversation and status work off the UI goroutine, and the injectable
// background-command seam the scenario harness substitutes.

package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/history"
	tuiplans "github.com/sglavoie/agentmux/tui/plans"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// The live-topology refresh controller. Every refresh the dashboard performs —
// the startup load, each interval tick, and the follow-up after an action
// changed Kitty — enters through requestTopologyRefresh, which is what makes
// "at most one refresh in flight" a property of the model rather than a habit
// of its callers.

// requestTopologyRefresh asks for a refresh of the live window tree. If one is
// already running the request collapses into the pending bit and no command is
// returned: a burst of ticks arriving while a slow refresh is out costs exactly
// one catch-up, not one fetch each.
func (m *Model) requestTopologyRefresh() tea.Cmd {
	if m.topologyRefreshInFlight {
		m.topologyRefreshPending = true
		return nil
	}
	return m.startTopologyRefresh()
}

// startTopologyRefresh draws the next sequence and dispatches the fetch. It
// assumes the caller has established that nothing is in flight.
func (m *Model) startTopologyRefresh() tea.Cmd {
	m.topologyRefreshNextSeq++
	m.topologyRefreshActiveSeq = m.topologyRefreshNextSeq
	m.topologyRefreshInFlight = true
	m.topologyRefreshPending = false
	return m.runTopologyRefreshCmd(m.topologyRefreshActiveSeq, m.initialSourceWindowID)
}

// settleTopologyRefresh reports whether a completion with this sequence belongs
// to the refresh currently in flight, marking it finished when it does. A
// completion that arrives when nothing is outstanding was already settled, and
// one carrying a sequence other than the active one was superseded before it
// landed; both are obsolete and neither is allowed to touch the list.
func (m *Model) settleTopologyRefresh(seq int) bool {
	if !m.topologyRefreshInFlight || seq != m.topologyRefreshActiveSeq {
		return false
	}
	m.topologyRefreshInFlight = false
	return true
}

// catchUpTopologyRefresh starts the single refresh that requests coalesced
// while the one just finished was out. It is called only after a success: a
// failed refresh clears the pending bit instead, so a run of ticks that piled
// up during an outage cannot turn into an immediate retry storm against a
// daemon or a Kitty that is still down. The next scheduled tick asks again.
func (m *Model) catchUpTopologyRefresh() tea.Cmd {
	if m.topologyRefreshInFlight || !m.topologyRefreshPending {
		return nil
	}
	return m.startTopologyRefresh()
}

// handleSessionsMsg processes a commands.SessionsMsg: rebuilds the session list,
// restores the cursor position, and clamps scroll offsets.
func (m Model) handleSessionsMsg(msg commands.SessionsMsg) (tea.Model, tea.Cmd) {
	// A completion that is not the refresh we are waiting on is an obsolete
	// one: it was superseded before it landed, and applying it would move the
	// list backwards.
	if !m.settleTopologyRefresh(msg.Seq) {
		return m, nil
	}
	// Freshness is stamped with the tree it describes. Age is rendered later as
	// this reading plus everything elapsed since, so a newer tree — even one the
	// daemon already marked stale — resets the displayed age and, with it, any
	// staleness warning the previous one had raised.
	m.topology = msg.Topology
	m.topologyObservedAt = modelNow(m)
	// The source window has now been resolved against a real window tree, so
	// the startup selection is spent. Retaining it until here is what lets a
	// failed first refresh still select the caller's tab on the retry.
	m.initialSourceWindowID = 0
	m.err = nil
	m.portableErr = msg.PortableErr
	// Capture the prior cursor's item identity before the list is rebuilt so we
	// can restore it post-refresh. An empty list, or a cursor that is not on an
	// item, yields the zero identity — there is no prior selection to preserve.
	prior := dashboard.CaptureItemIdentity(m.items, m.cursor)
	m.lastActivity = msg.Activity
	m.watchdog = msg.Watchdog
	m.sessions = msg.Sessions
	m.recordTransitions()
	m.filtered = dashboard.ApplyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.sortFiltered()
	m.cursor = dashboard.RestoreItemCursor(m.items, prior, m.lastActivity, m.idleThreshold)
	selectionIsNew := msg.SelectionToken == "" || msg.SelectionToken != m.lastSelectionRequestToken
	if msg.SelectionTabID > 0 && selectionIsNew {
		// The requested tab is named by a session, but the cursor addresses a
		// line: resolve the session through the projection rather than
		// assuming the two are indexed alike.
		for i := range m.items.Items {
			running, ok := m.items.SessionAt(i)
			if ok && running.TabID == msg.SelectionTabID {
				m.cursor = i
				break
			}
		}
	}
	if msg.SelectionToken != "" {
		m.lastSelectionRequestToken = msg.SelectionToken
	}
	// The hosting tab is republished by every refresh rather than resolved once,
	// because a window can be moved between tabs while the dashboard is running.
	// A refresh that could not resolve it at all reports zero, which is "unknown"
	// and must not overwrite an answer an earlier refresh did have.
	if msg.HostWindowID > 0 {
		m.actionHost = sessionactions.Host{WindowID: msg.HostWindowID, TabID: msg.HostTabID}
	}
	clampListOffset(&m)
	scrollListToCursor(&m)
	// The preview is retargeted last, once the projection, the restored cursor,
	// and the selection token have all had their say. A refresh can move the
	// cursor onto a different session — or, in the Projects view, off a folded
	// family and onto its summary — and the pane must follow the selection the
	// refresh ended on rather than the one it started from.
	cmds := []tea.Cmd{m.catchUpTopologyRefresh()}
	if cmd := m.refreshPreviewForCursor(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// handleRefreshErr processes a commands.ErrMsg: a refresh that could reach
// neither the daemon nor Kitty. The session list is deliberately left as it
// was — a dashboard showing a slightly old list beats one that empties itself
// because a subprocess timed out — and the pending bit is dropped so the retry
// waits for the next tick rather than firing immediately.
//
// The error is recorded but the topology and its stamp are not touched, so the
// rows on screen keep aging from their own last successful capture. How loudly
// this failure is rendered is decided at render time by whether there is a
// usable tree to keep showing at all: see topologyStatus.
func (m Model) handleRefreshErr(msg commands.ErrMsg) (tea.Model, tea.Cmd) {
	if !m.settleTopologyRefresh(msg.Seq) {
		return m, nil
	}
	m.topologyRefreshPending = false
	m.err = msg.Err
	return m, nil
}

// handleActiveLoopsMsg stores the latest active-loop discovery result. Older
// in-flight refreshes are ignored so a slow discovery cannot restore stale
// candidates after a newer refresh has already completed. Tab loop markers
// are rendered by the daemon; these candidates remain for detail views only.
func (m Model) handleActiveLoopsMsg(msg commands.ActiveLoopsMsg) (tea.Model, tea.Cmd) {
	if msg.Seq != m.activeLoopRefreshSeq {
		return m, nil
	}
	m.activeLoopRefreshErr = msg.Err
	if msg.Err != nil {
		return m, nil
	}
	m.activeLoopCandidates = append([]agentloop.Candidate(nil), msg.Candidates...)
	return m, nil
}

// handleActionDone processes an commands.ActionDoneMsg: records any action error and
// triggers an immediate session refresh so the list reflects the new state.
//
// A completion carrying a request is one the shared controller dispatched, and
// it is retired from the correlation record before anything else happens. One
// that carries none was started outside the controller — a worktree mutation, a
// direct key that predates the controller — and is handled exactly as it always
// was. Either way the error goes to the header and a refresh follows, because
// the list is stale in both cases.
//
// The correlated Result is what the operator's feedback is built from. It names
// the action, the row as captured at dispatch, and an outcome that keeps "the
// tab was seen to go" apart from "nobody could say" — which is the difference
// between a recovery worth offering and one that would launch a second copy of a
// session still running.
func (m Model) handleActionDone(msg commands.ActionDoneMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	completion := sessionactions.Completion{Err: msg.Err, Unconfirmed: msg.Unconfirmed}
	if updated, result, correlated := sessionActionController(m).Complete(m, sessionactions.RequestID(msg.Request), completion); correlated {
		m = updated
		if c := m.applySessionActionResult(result); c != nil {
			cmds = append(cmds, c)
		}
	}
	m.actionErr = msg.Err
	// Trigger a refresh so the list reflects the new state.
	cmds = append(cmds, m.requestTopologyRefresh())
	return m, tea.Batch(cmds...)
}

// applySessionActionResult is what one completed dispatch does to this surface:
// the close's recovery bookkeeping, and the sentence the operator reads.
func (m *Model) applySessionActionResult(result sessionactions.Result) tea.Cmd {
	if result.Action == commands.CloseSession {
		return m.applyCloseOutcome(result)
	}
	return m.reportActionOutcome(result)
}

// handleCopyDone processes a commands.CopyDoneMsg without refreshing live sessions.
// Both outcomes are rendered as toasts so they remain visible inside full
// transcript and plan viewer layouts: a clipboard write is otherwise invisible,
// and a key that seems to do nothing gets pressed again.
func (m Model) handleCopyDone(msg commands.CopyDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Err == nil {
		return m, m.pushToast(Toast{Severity: toastSuccess, Text: "copied: " + copyExcerpt(msg.Text)})
	}
	if c := m.pushToast(Toast{Severity: toastError, Text: "copy: " + msg.Err.Error()}); c != nil {
		return m, c
	}
	return m, nil
}

// handleResortDone processes a commands.ResortDoneMsg: records any error, pushes a
// success or failure toast, and triggers a session refresh.
func (m Model) handleResortDone(msg commands.ResortDoneMsg) (tea.Model, tea.Cmd) {
	m.actionErr = msg.Err
	var cmds []tea.Cmd
	var t Toast
	if msg.Err != nil {
		t = Toast{Severity: toastError, Text: "Resort failed: " + msg.Err.Error()}
	} else {
		t = Toast{Severity: toastSuccess, Text: "Tabs resorted"}
	}
	if c := m.pushToast(t); c != nil {
		cmds = append(cmds, c)
	}
	if c := m.requestTopologyRefresh(); c != nil {
		cmds = append(cmds, c)
	}
	return m, tea.Batch(cmds...)
}

// handlePickerOpenDone processes a single or aggregate commands.PickerOpenDoneMsg:
// quits on success or surfaces the error in the header and triggers a refresh
// on failure.
func (m Model) handlePickerOpenDone(msg commands.PickerOpenDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.actionErr = msg.Err
		return m, m.requestTopologyRefresh()
	}
	// A view that ran in this terminal took the operator nowhere, so there is
	// no other tab to leave them in.
	if msg.Stay {
		return m, m.requestTopologyRefresh()
	}
	return m, tea.Quit
}

// handleAttachDone processes a commands.AttachDoneMsg: the operator came back
// from an attached session, so the dashboard carries on and re-reads a list
// that may have changed while it was not looking.
func (m Model) handleAttachDone(msg commands.AttachDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.actionErr = msg.Err
	}
	return m, m.requestTopologyRefresh()
}

// handleFocusDone processes a commands.FocusDoneMsg: quits on success or records the
// error.
func (m Model) handleFocusDone(msg commands.FocusDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.actionErr = msg.Err
		return m, nil
	}
	return m, tea.Quit
}

// handlePeekDone processes a commands.PeekDoneMsg: records any error from the overlay
// pager launch.
func (m Model) handlePeekDone(msg commands.PeekDoneMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.actionErr = msg.Err
	}
	return m, nil
}

// handleDetailEvents processes a commands.DetailEventsMsg: updates the detail pane
// event list, dropping stale responses from a previous mode or window.
func (m Model) handleDetailEvents(msg commands.DetailEventsMsg) (tea.Model, tea.Cmd) {
	// Stale responses (mode changed or different window than the one
	// currently shown in the pane) are dropped silently so a slow
	// daemon roundtrip cannot overwrite a fresh request's loading
	// state or repopulate the pane after the user closed it.
	if m.mode != ModeDetail || msg.WindowID != m.detailWindowID {
		return m, nil
	}
	m.detailEvents = msg.Events
	m.detailErr = msg.Err
	m.detailLoading = false
	// A refresh that shrinks the event ring (daemon GC, or an empty
	// reply) can leave detailScroll above the new maxOffset. The
	// renderer clamps for display, but the next j/k/pgup/pgdn press
	// would operate on the stale offset and chew through dead rows
	// before the viewport reacts — mirror the WindowSizeMsg clamp.
	clampDetailScroll(&m)
	return m, nil
}

func (m Model) handleSessionCacheLoadStarted(sessionCacheLoadStartedMsg) (tea.Model, tea.Cmd) {
	m.sessionCacheLoadInFlight = true
	m.sessionRefreshErr = nil
	return m, nil
}

// handleSessionCacheLoadDone publishes a usable filesystem snapshot even when
// loading returned a recovery warning, then starts the slower refresh/prune.
func (m Model) handleSessionCacheLoadDone(msg sessionCacheLoadDoneMsg) (tea.Model, tea.Cmd) {
	m.sessionCacheLoadInFlight = false
	m.sessionRefreshErr = msg.loadErr
	if msg.idx == nil {
		m.sessionSearchErr = nil
		m.sessionLoading = false
		return m, nil
	}

	m.transcriptIndex = msg.idx
	m.transcriptReaders = msg.readers

	var cmds []tea.Cmd
	publishCachedResults := msg.seq == m.sessionSearchSeq && sessionQueryIsEmpty(m.currentSessionQuery())
	if publishCachedResults {
		m.sessionSearchErr = msg.searchErr
		if msg.searchErr == nil {
			m.publishSessionResults(msg.results)
		}
		m.sessionLoading = false
	} else if m.activeRootMode() == ModeSessionSearch {
		m.sessionLoading = true
		cmds = append(cmds, m.runSessionSearchCmd(m.sessionSearchSeq, m.transcriptIndex, m.transcriptReaders, m.currentSessionQuery()))
	} else {
		m.sessionLoading = false
	}

	if !m.sessionRefreshInFlight {
		m.sessionRefreshInFlight = true
		warmSeq := msg.seq
		warmQuery := transcript.Query{MainOnly: true}
		if !publishCachedResults {
			if m.activeRootMode() == ModeSessionSearch {
				warmSeq = m.sessionSearchSeq
				warmQuery = m.currentSessionQuery()
			} else {
				warmSeq = -1
			}
		}
		cmds = append(cmds, m.runSessionWarmRefreshCmd(m.transcriptIndex, m.cfg, m.transcriptReaders, warmSeq, warmQuery))
	}
	return m, tea.Batch(cmds...)
}

// handleSessionWarmRefreshDone publishes the background refresh result if no
// newer user search has superseded the query captured at refresh start. When
// the query did change, the refresh still updated the shared index, so issue a
// new search against that index instead of leaving the visible rows stale.
func (m Model) handleSessionWarmRefreshDone(msg sessionWarmRefreshDoneMsg) (tea.Model, tea.Cmd) {
	m.sessionRefreshInFlight = false
	m.sessionRefreshErr = msg.refreshErr
	if msg.seq != m.sessionSearchSeq {
		return m, m.searchCurrentSessionsAfterRefresh()
	}
	m.sessionSearchErr = msg.searchErr
	if msg.searchErr == nil {
		m.publishSessionResults(msg.results)
	}
	m.sessionLoading = false
	return m, nil
}

// handleSessionsRefreshDone processes a sessionsRefreshDoneMsg: records any
// refresh error and follows up with a search dispatch.
func (m Model) handleSessionsRefreshDone(msg sessionsRefreshDoneMsg) (tea.Model, tea.Cmd) {
	m.sessionRefreshInFlight = false
	m.sessionRefreshErr = msg.err
	// Always follow a refresh with a search dispatch — even on error,
	// because Refresh updates whatever profiles succeeded and the old
	// index entries are still usable. Start a new generation so any search
	// that raced with the refresh cannot publish its pre-refresh snapshot
	// after this follow-up completes.
	return m, m.searchCurrentSessionsAfterRefresh()
}

// searchCurrentSessionsAfterRefresh invalidates searches that may have read
// the pre-refresh index and immediately runs the latest text/chip query against
// the refreshed snapshot. It intentionally skips the normal debounce: the
// user has already waited for the filesystem scan to finish.
func (m *Model) searchCurrentSessionsAfterRefresh() tea.Cmd {
	if m.transcriptIndex == nil {
		m.sessionLoading = false
		return nil
	}
	if m.transcriptReaders == nil {
		m.transcriptReaders = buildTranscriptReaders()
	}
	m.sessionSearchSeq++
	seq := m.sessionSearchSeq
	m.sessionLoading = true
	m.sessionSearchErr = nil
	return m.runSessionSearchCmd(seq, m.transcriptIndex, m.transcriptReaders, m.currentSessionQuery())
}

// handleSessionSearchDebounce processes a sessionSearchDebounceMsg: drops
// stale debounce ticks and fires the search for the current seq.
func (m Model) handleSessionSearchDebounce(msg sessionSearchDebounceMsg) (tea.Model, tea.Cmd) {
	// Stale debounce ticks (the user kept typing) are silently dropped.
	if msg.seq != m.sessionSearchSeq {
		return m, nil
	}
	if m.transcriptIndex == nil {
		if m.sessionCacheLoadInFlight {
			return m, nil
		}
		m.sessionCacheLoadInFlight = true
		return m, startupSessionWarmupCmd(m.sessionSearchSeq)
	}
	if m.transcriptReaders == nil {
		m.transcriptReaders = buildTranscriptReaders()
	}
	return m, m.runSessionSearchCmd(msg.seq, m.transcriptIndex, m.transcriptReaders, m.currentSessionQuery())
}

// handleSessionSearchDone processes a sessionSearchDoneMsg: updates the
// session results list and clamps the cursor.
func (m Model) handleSessionSearchDone(msg sessionSearchDoneMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.sessionSearchSeq {
		return m, nil
	}
	m.sessionLoading = false
	m.sessionSearchErr = msg.err
	if msg.err == nil {
		m.publishSessionResults(msg.results)
	}
	return m, nil
}

// publishSessionResults keeps the currently selected transcript selected when
// a same-query refresh changes row ordering, then clamps if it disappeared. A
// conversation the view was opened on is preferred until it has been found.
func (m *Model) publishSessionResults(results []transcript.Result) {
	selectedKey := ""
	if m.sessionCursor >= 0 && m.sessionCursor < len(m.sessionResults) {
		meta := m.sessionResults[m.sessionCursor].Meta
		selectedKey = transcript.IndexKey(meta.Harness, meta.ID)
	}
	m.sessionResults = results
	for _, key := range []string{m.sessionWantedKey, selectedKey} {
		if key == "" {
			continue
		}
		for index, result := range m.sessionResults {
			if transcript.IndexKey(result.Meta.Harness, result.Meta.ID) == key {
				if key == m.sessionWantedKey {
					m.sessionWantedKey = ""
				}
				m.sessionCursor = index
				scrollSessionSearchToCursor(m)
				return
			}
		}
	}
	if m.sessionCursor >= len(m.sessionResults) {
		m.sessionCursor = len(m.sessionResults) - 1
		if m.sessionCursor < 0 {
			m.sessionCursor = 0
		}
	}
	scrollSessionSearchToCursor(m)
}

// handlePlansRefreshDone processes a plansRefreshDoneMsg: records any refresh
// error and follows up with a plan search dispatch.
func (m Model) handlePlansRefreshDone(msg plansRefreshDoneMsg) (tea.Model, tea.Cmd) {
	m.planRefreshErr = msg.err
	if msg.seq < m.planSearchSeq {
		return m, nil
	}
	return m, m.runPlanSearchCmd(msg.seq, m.plansIndex, m.currentPlanQuery())
}

// handlePlanCacheLoadDone publishes usable warm rows from the recovering
// filesystem cache, then starts the slower source refresh. A stale load still
// installs the index, but its rows never overwrite a newer query.
func (m Model) handlePlanCacheLoadDone(msg planCacheLoadDoneMsg) (tea.Model, tea.Cmd) {
	m.planCacheLoadInFlight = false
	m.planRefreshErr = msg.loadErr
	if msg.idx == nil {
		m.planLoading = false
		m.planSearchErr = msg.searchErr
		return m, nil
	}
	m.plansIndex = msg.idx
	if msg.seq == m.planSearchSeq {
		m.planSearchErr = msg.searchErr
		if msg.searchErr == nil {
			m.publishPlanResults(msg.results)
		}
	}
	if m.activeRootMode() != ModePlanSearch {
		m.planLoading = false
		return m, nil
	}
	return m.startPlanSearchRefresh()
}

// handlePlanSearchDebounce processes a planSearchDebounceMsg: drops stale
// debounce ticks and fires the plan search for the current seq.
func (m Model) handlePlanSearchDebounce(msg planSearchDebounceMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.planSearchSeq {
		return m, nil
	}
	if m.plansIndex == nil {
		return m, nil
	}
	return m, m.runPlanSearchCmd(msg.seq, m.plansIndex, m.currentPlanQuery())
}

// handlePlanSearchDone processes a planSearchDoneMsg: updates the plan
// results list and clamps the cursor.
func (m Model) handlePlanSearchDone(msg planSearchDoneMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.planSearchSeq {
		return m, nil
	}
	m.planLoading = false
	m.planSearchErr = msg.err
	if msg.err == nil {
		m.publishPlanResults(msg.results)
	}
	return m, nil
}

// publishPlanResults retains the selected plan across refresh reordering and
// leaves the last-good rows untouched when callers report a search failure.
func (m *Model) publishPlanResults(results []plans.Result) {
	selectedPath := ""
	if m.planCursor >= 0 && m.planCursor < len(m.planResults) {
		selectedPath = m.planResults[m.planCursor].Meta.DirPath
	}
	m.planResults = results
	// Belt-and-suspenders: Search already honours Query.Sort, but the cycle
	// keystroke mutates planSortMode without issuing a new search.
	plans.SortResults(m.planResults, m.planSortMode)
	if selectedPath != "" {
		for index, result := range m.planResults {
			if result.Meta.DirPath == selectedPath {
				m.planCursor = index
				scrollPlanSearchToCursor(m)
				return
			}
		}
	}
	if m.planCursor >= len(m.planResults) {
		m.planCursor = len(m.planResults) - 1
		if m.planCursor < 0 {
			m.planCursor = 0
		}
	}
	scrollPlanSearchToCursor(m)
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
	// pressed Enter on a different plan before this one finished. An overlay
	// raised over the viewer is a detour, not an exit, so its return mode
	// still owns the result.
	if m.activeRootMode() != ModePlanView || msg.seq != m.planViewerLoadSeq {
		return m, nil
	}
	m.planViewerLoading = false
	m.planViewerLoadErr = msg.err
	m.planViewerLines = msg.lines
	m.planViewerMatchLines = tuiplans.ViewerMatches(m.planViewerLines, m.planQuery)
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
	// pressed Enter on a different row before this one finished. An overlay
	// raised over the viewer is not an exit, so its return mode owns the
	// result — otherwise a load finishing behind help or the handoff picker
	// would leave the viewer stuck on "loading" after the detour.
	if m.activeRootMode() != ModeSessionView || msg.seq != m.viewerLoadSeq {
		return m, nil
	}
	m.viewerLoading = false
	m.viewerLoadErr = msg.err
	m.viewerMessages = msg.messages
	m.viewerMatchLines = history.ComputeMatchesForSource(m.viewerMessages, m.sessionQuery, m.filterMessageSource)
	m.viewerMatchIdx = 0
	m.viewerScroll = 0
	m.viewerCursor = 0
	if len(m.viewerMatchLines) > 0 {
		m.viewerCursor = m.viewerMatchLines[0]
	}
	scrollViewerToCursor(&m)
	return m, nil
}

// This file is the model-bound half of the effect layer. The tea.Cmd builders
// themselves live in tui/commands, which knows nothing about the root model;
// what remains here is the wiring that charges the model's injected seams — the
// daemon transport, the Kitty client, the transcript readers, the handoff and
// worktree runners — against those builders.

func (m Model) clearStatusCmd(s session.RunningSession) tea.Cmd {
	return commands.ClearStatusWithDaemon(m.actionDaemon(), s)
}

func (m Model) markActiveCmd(s session.RunningSession) tea.Cmd {
	return commands.MarkActiveWithDaemon(m.actionDaemon(), s)
}

// conversationDependencies collects the injected seams every
// indexed-conversation action needs. A nil handoff or worktree runner tells the
// registry to use its real workflow.
func (m Model) conversationDependencies(readers map[string]transcript.Reader) actions.Dependencies {
	deps := commands.TerminalDependencies(m.client, m.cfg)
	deps.Readers = readers
	deps.Daemon = m.actionDaemon()
	deps.Handoff = m.handoffRunner
	deps.Worktree = m.worktreeRunner
	return deps
}

func (m Model) conversationActionCmd(readers map[string]transcript.Reader, meta transcript.SessionMeta, actionID actions.ID) tea.Cmd {
	return commands.ConversationAction(m.conversationDependencies(readers), meta, actionID)
}

func (m Model) conversationWorktreeForkCmd(meta transcript.SessionMeta) tea.Cmd {
	return commands.ConversationWorktreeFork(m.conversationDependencies(m.transcriptReaders), meta)
}

func (m Model) conversationHandoffCmd(meta transcript.SessionMeta, profile string, edit bool) tea.Cmd {
	return commands.ConversationHandoff(m.conversationDependencies(m.transcriptReaders), meta, profile, edit)
}

func (m Model) actionDaemon() func(protocol.Request) (protocol.Response, error) {
	if m.daemonCommand != nil {
		return m.daemonCommand
	}
	return commands.DaemonRequest
}

// modelBackgroundCommands contains the long-running environment-facing
// effects started by the model. Production uses the normal Kitty, daemon,
// hook, and active-loop implementations; the in-process UI harness replaces
// every entry with a deterministic fixture command.
type modelBackgroundCommands struct {
	// topologyRefresh takes the refresh sequence and the startup source window
	// ID; the message it produces carries that sequence back so the controller
	// can tell its own completion from an obsolete one.
	topologyRefresh func(Model, int, int) tea.Cmd
	activeLoops     func(int) tea.Cmd
	health          func(Model, bool, bool) tea.Cmd
	detail          func(int) tea.Cmd
	worktrees       func(Model, int) tea.Cmd
	worktreeAction  func(Model, actions.ID, actions.Request) tea.Cmd
	aslSnapshot     func(Model, int) tea.Cmd
	// aslWorkbench takes the row, plan directory, and focus rather than a link,
	// because the tokenized URL is minted and consumed inside the command.
	aslWorkbench func(Model, string, string, string) tea.Cmd
	// aslQuestion fetches exactly one question body.
	aslQuestion func(Model, int, string, string) tea.Cmd
	// aslAction submits one explicit mutation. The request carries the operator's
	// answer, so no layer between here and the protocol writer may format it.
	aslAction func(Model, int, bool, string, aslbridge.ActionRequest) tea.Cmd
	// scrollback reads one pane's scrollback for the inspector. Nil is
	// production: the read goes to the backend hosting the pane. The UI harness
	// sets it for a fixture whose sessions no fake Kitty client can answer for.
	scrollback func(windowID int) (string, error)
	// pageText suspends the surface for a pager over text it already holds.
	// Nil is production; the UI harness has no terminal to hand a pager.
	pageText func(text string, done func(error) tea.Msg) tea.Cmd
}

func defaultModelBackgroundCommands() modelBackgroundCommands {
	return modelBackgroundCommands{
		topologyRefresh: func(m Model, seq, sourceWindowID int) tea.Cmd {
			return commands.Refresh(m.client, m.cfg, m.sortMode, seq, sourceWindowID)
		},
		activeLoops: commands.ActiveLoopRefresh,
		health: func(m Model, checkDaemon, checkHooks bool) tea.Cmd {
			return commands.Health(m.cfg, m.amxBin, checkDaemon, checkHooks)
		},
		detail: sessionDetailCmd,
		worktrees: func(m Model, seq int) tea.Cmd {
			return worktreeListCmd(m, seq)
		},
		worktreeAction: func(m Model, id actions.ID, request actions.Request) tea.Cmd {
			return worktreeLifecycleActionCmd(m, id, request)
		},
		aslSnapshot: func(_ Model, seq int) tea.Cmd {
			return aslSnapshotCmd(seq)
		},
		aslWorkbench: func(_ Model, rowID, planDir, focus string) tea.Cmd {
			return aslWorkbenchCmd(rowID, planDir, focus)
		},
		aslQuestion: func(_ Model, seq int, planDir, questionID string) tea.Cmd {
			return aslQuestionCmd(seq, planDir, questionID)
		},
		aslAction: func(_ Model, seq int, control bool, rowID string, req aslbridge.ActionRequest) tea.Cmd {
			return aslActionCmd(seq, control, rowID, req)
		},
	}
}

func (m Model) runWorktreeListCmd(seq int) tea.Cmd {
	if m.backgroundCommands.worktrees != nil {
		return m.backgroundCommands.worktrees(m, seq)
	}
	return defaultModelBackgroundCommands().worktrees(m, seq)
}

func (m Model) runWorktreeActionCmd(id actions.ID, request actions.Request) tea.Cmd {
	if m.backgroundCommands.worktreeAction != nil {
		return m.backgroundCommands.worktreeAction(m, id, request)
	}
	return defaultModelBackgroundCommands().worktreeAction(m, id, request)
}

func (m Model) runPageTextCmd(text string, done func(error) tea.Msg) tea.Cmd {
	if m.backgroundCommands.pageText != nil {
		return m.backgroundCommands.pageText(text, done)
	}
	return commands.PageText(text, done)
}

func (m Model) runTopologyRefreshCmd(seq, sourceWindowID int) tea.Cmd {
	if m.backgroundCommands.topologyRefresh != nil {
		return m.backgroundCommands.topologyRefresh(m, seq, sourceWindowID)
	}
	return defaultModelBackgroundCommands().topologyRefresh(m, seq, sourceWindowID)
}

func (m Model) runActiveLoopRefreshCmd(seq int) tea.Cmd {
	if m.backgroundCommands.activeLoops != nil {
		return m.backgroundCommands.activeLoops(seq)
	}
	return defaultModelBackgroundCommands().activeLoops(seq)
}

func (m Model) runHealthCmd(checkDaemon, checkHooks bool) tea.Cmd {
	if m.backgroundCommands.health != nil {
		return m.backgroundCommands.health(m, checkDaemon, checkHooks)
	}
	return defaultModelBackgroundCommands().health(m, checkDaemon, checkHooks)
}

func (m Model) runSessionDetailCmd(windowID int) tea.Cmd {
	if m.backgroundCommands.detail != nil {
		return m.backgroundCommands.detail(windowID)
	}
	return defaultModelBackgroundCommands().detail(windowID)
}
