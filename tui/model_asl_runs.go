package tui

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/session"
)

// aslSnapshotLoadedMsg carries one polled ASL projection. seq is compared
// against the model's counter so a slow response cannot overwrite newer data.
type aslSnapshotLoadedMsg struct {
	seq      int
	snapshot aslbridge.Snapshot
	meta     aslbridge.SnapshotMeta
	receipts []aslbridge.Operation
	status   aslbridge.Status
	err      error
}

// aslWorkbenchDoneMsg reports the outcome of a Workbench deep link. The link
// itself is deliberately absent: the command fetches the tokenized URL and
// hands it to the opener inside a single effect, so no bearer URL is ever
// carried by a message, stored on the model, or rendered.
type aslWorkbenchDoneMsg struct {
	rowID string
	focus string
	err   error
}

// errASLWorkbenchOpen is the only failure text shown for a Workbench open. The
// underlying error can embed the tokenized URL, so it is never surfaced.
var errASLWorkbenchOpen = errors.New("could not open the Workbench link")

func aslSnapshotCmd(seq int) tea.Cmd {
	return func() tea.Msg {
		snapshot, meta, receipts, status, err := daemon.NewClient().ASLSnapshot()
		return aslSnapshotLoadedMsg{seq: seq, snapshot: snapshot, meta: meta, receipts: receipts, status: status, err: err}
	}
}

func aslWorkbenchCmd(rowID, planDir, focus string) tea.Cmd {
	return func() tea.Msg {
		link, err := daemon.NewClient().ASLWorkbench(aslbridge.WorkbenchRequest{PlanDir: planDir, Focus: focus})
		if err != nil {
			return aslWorkbenchDoneMsg{rowID: rowID, focus: focus, err: errASLWorkbenchOpen}
		}
		if openBrowserURL(link.URL) != nil {
			return aslWorkbenchDoneMsg{rowID: rowID, focus: focus, err: errASLWorkbenchOpen}
		}
		return aslWorkbenchDoneMsg{rowID: rowID, focus: focus}
	}
}

func (m Model) runASLSnapshotCmd(seq int) tea.Cmd {
	if m.backgroundCommands.aslSnapshot != nil {
		return m.backgroundCommands.aslSnapshot(m, seq)
	}
	return defaultModelBackgroundCommands().aslSnapshot(m, seq)
}

func (m Model) runASLWorkbenchCmd(rowID, planDir, focus string) tea.Cmd {
	if m.backgroundCommands.aslWorkbench != nil {
		return m.backgroundCommands.aslWorkbench(m, rowID, planDir, focus)
	}
	return defaultModelBackgroundCommands().aslWorkbench(m, rowID, planDir, focus)
}

// enterASLRuns opens the Runs view. Dashboard filters, query, and selection are
// untouched so Esc restores exactly the list the operator left.
func (m Model) enterASLRuns() (tea.Model, tea.Cmd) {
	m.clearJumpChord()
	m.mode = ModeASLRuns
	m.aslErr = nil
	m.aslNotice = ""
	return m.startASLRefresh()
}

// exitASLRuns returns to the dashboard. It only changes the mode: no filter is
// cleared and no dashboard cursor is moved.
func (m Model) exitASLRuns() (tea.Model, tea.Cmd) {
	m.mode = ModeList
	return m, nil
}

func (m Model) startASLRefresh() (tea.Model, tea.Cmd) {
	return m, m.beginASLRefresh()
}

// beginASLRefresh arms the next snapshot poll. Callers that already own a
// pointer to the model use it directly so a refresh can be batched alongside
// other commands.
func (m *Model) beginASLRefresh() tea.Cmd {
	m.captureASLSelection()
	m.aslLoading = true
	m.aslRefreshSeq++
	return m.runASLSnapshotCmd(m.aslRefreshSeq)
}

func (m *Model) captureASLSelection() {
	if m.aslCursor >= 0 && m.aslCursor < len(m.aslRows) {
		m.aslSelectedID = m.aslRows[m.aslCursor].RowID
	}
}

func (m Model) handleASLSnapshotLoaded(msg aslSnapshotLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.aslRefreshSeq {
		return m, nil
	}
	m.aslLoading = false
	m.aslStatus = msg.status
	m.applyASLReceipts(msg.receipts)
	if msg.err != nil {
		m.aslErr = msg.err
		return m, nil
	}
	m.aslErr = nil
	m.aslLoaded = true
	m.aslMeta = msg.meta
	m.aslRows = sortASLRows(msg.snapshot.Rows)
	m.restoreASLSelection()
	return m, nil
}

// restoreASLSelection re-finds the previously selected row_id after a refresh.
// A poll must never move the cursor, so a row that disappeared falls back to
// the clamped previous index rather than to the top of the list.
func (m *Model) restoreASLSelection() {
	m.aslCursor = clamp(m.aslCursor, 0, len(m.aslRows)-1)
	if len(m.aslRows) == 0 {
		m.aslCursor = 0
		m.aslOffset = 0
		return
	}
	if m.aslSelectedID != "" {
		for i := range m.aslRows {
			if m.aslRows[i].RowID == m.aslSelectedID {
				m.aslCursor = i
				break
			}
		}
	}
	scrollASLToCursor(m)
}

// sortASLRows orders needs-action ahead of in-progress and everything else,
// then by ASL's own urgency rank, then by most recent activity. Label and plan
// directory break the final tie so an unchanged snapshot always renders in the
// same order.
func sortASLRows(rows []aslbridge.Row) []aslbridge.Row {
	ordered := append([]aslbridge.Row(nil), rows...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if ga, gb := aslGroupRank(a.UrgencyGroup), aslGroupRank(b.UrgencyGroup); ga != gb {
			return ga < gb
		}
		if a.UrgencyRank != b.UrgencyRank {
			return a.UrgencyRank < b.UrgencyRank
		}
		at, bt := aslActivityTime(a), aslActivityTime(b)
		if !at.Equal(bt) {
			return at.After(bt)
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.PlanDir < b.PlanDir
	})
	return ordered
}

// aslGroupRank mirrors ASL's urgency grouping. An unknown group sorts after
// every known one rather than silently joining needs-action.
func aslGroupRank(group string) int {
	switch group {
	case "needs-action":
		return 0
	case "in-progress":
		return 1
	case "ready":
		return 2
	case "complete":
		return 3
	default:
		return 4
	}
}

func aslActivityTime(row aslbridge.Row) time.Time {
	for _, stamp := range []string{row.LastActivityAt, row.RunStartedAt} {
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(stamp)); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func (m Model) selectedASLRow() (aslbridge.Row, bool) {
	if m.aslCursor < 0 || m.aslCursor >= len(m.aslRows) {
		return aslbridge.Row{}, false
	}
	return m.aslRows[m.aslCursor], true
}

func (m Model) handleASLRunsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc, keyQ, keyASLRuns:
		return m.exitASLRuns()
	case keyQuestion:
		m.openHelp(ModeASLRuns)
		return m, nil
	case keyF:
		return m.focusSelectedASLRun()
	case keyASLWorkbench:
		return m.openSelectedASLWorkbench()
	case keyASLRespond:
		return m.openASLResponse()
	case keyASLStop:
		return m.confirmASLControl(aslbridge.MethodRunStopAfterCurrent)
	case keyASLAbort:
		return m.confirmASLControl(aslbridge.MethodRunAbort)
	case keyCopyPath:
		if row, ok := m.selectedASLRow(); ok && row.WorkspaceRoot != "" {
			return m, copyTextCmd(row.WorkspaceRoot)
		}
		return m, nil
	case keyJ, keyDown:
		m.aslCursor = clamp(m.aslCursor+1, 0, len(m.aslRows)-1)
	case keyK, keyUp:
		m.aslCursor = clamp(m.aslCursor-1, 0, len(m.aslRows)-1)
	case keyG:
		m.aslCursor = 0
	case keyGG:
		m.aslCursor = clamp(len(m.aslRows)-1, 0, len(m.aslRows)-1)
	case keyCtrlD:
		m.aslCursor = clamp(m.aslCursor+halfPageStep(aslVisibleHeight(m)), 0, len(m.aslRows)-1)
	case keyCtrlU:
		m.aslCursor = clamp(m.aslCursor-halfPageStep(aslVisibleHeight(m)), 0, len(m.aslRows)-1)
	case keyPgDown, keyCtrlF:
		m.aslCursor = clamp(m.aslCursor+fullPageStep(aslVisibleHeight(m)), 0, len(m.aslRows)-1)
	case keyPgUp, keyCtrlB:
		m.aslCursor = clamp(m.aslCursor-fullPageStep(aslVisibleHeight(m)), 0, len(m.aslRows)-1)
	default:
		return m, nil
	}
	// Moving the cursor retires an action notice: it explained the row the
	// operator was on, not the one they moved to.
	m.aslNotice = ""
	m.captureASLSelection()
	scrollASLToCursor(&m)
	return m, nil
}

// focusSelectedASLRun focuses the Kitty window running the selected plan. An
// exact projected binding wins outright. Without one it falls back to live
// sessions whose canonical workspace and harness both match: exactly one
// focuses, several raise the shared ambiguity picker, and none refuses with a
// reason rather than focusing an unrelated window.
func (m Model) focusSelectedASLRun() (tea.Model, tea.Cmd) {
	row, ok := m.selectedASLRow()
	if !ok {
		return m, nil
	}
	if bound, found := aslExactBinding(row, m.sessions); found {
		m.aslErr = nil
		return m, focusCmd(m.client, bound.TabID)
	}
	candidates := aslFallbackCandidates(row, m.sessions)
	switch len(candidates) {
	case 0:
		m.aslErr = errors.New("focus unavailable: no live session matches this run's workspace and harness")
		return m, nil
	case 1:
		m.aslErr = nil
		return m, focusCmd(m.client, candidates[0].TabID)
	default:
		m.aslErr = nil
		m.aslFocusTarget = row.RowID
		m.openLiveSessionPicker(LivePickerASLFocus, ModeASLRuns, aslRowLabel(row), candidates)
		return m, nil
	}
}

// aslExactBinding reports the live session ASL has positively identified for a
// row: either the projected Kitty window, or a session carrying the same run ID
// in its managed user variables. Anything looser is a guess and is left to the
// fallback path.
func aslExactBinding(row aslbridge.Row, sessions []session.RunningSession) (session.RunningSession, bool) {
	for _, candidate := range sessions {
		if row.Window != nil && row.Window.KittyWindowID != 0 && candidate.WindowID == row.Window.KittyWindowID {
			return candidate, true
		}
		if row.RunID != "" && candidate.ASLRunID == row.RunID {
			return candidate, true
		}
	}
	return session.RunningSession{}, false
}

// aslFallbackCandidates returns live sessions in the run's canonical workspace
// running the run's harness. Both must match: a workspace alone can host
// several harnesses, and a harness alone says nothing about which run it serves.
func aslFallbackCandidates(row aslbridge.Row, sessions []session.RunningSession) []actions.WorktreeLiveSession {
	root := aslCanonicalDir(row.WorkspaceRoot)
	harness := strings.TrimSpace(strings.ToLower(row.Harness))
	if root == "" || harness == "" {
		return nil
	}
	var out []actions.WorktreeLiveSession
	for _, candidate := range sessions {
		if aslCanonicalDir(candidate.Directory) != root {
			continue
		}
		if strings.ToLower(strings.TrimSpace(candidate.Harness)) != harness {
			continue
		}
		out = append(out, actions.WorktreeLiveSession{
			ID:               candidate.HarnessSessionID,
			TabID:            candidate.TabID,
			WindowID:         candidate.WindowID,
			Project:          candidate.Project,
			Profile:          candidate.Profile,
			Harness:          candidate.Harness,
			HarnessSessionID: candidate.HarnessSessionID,
			Directory:        candidate.Directory,
		})
	}
	return out
}

// aslMarkerFor returns the dashboard marker for a session an ASL run is
// exactly bound to. It reports false for every session ASL has not positively
// identified, and for any snapshot the view already considers untrustworthy.
func aslMarkerFor(m Model, s session.RunningSession) (string, bool) {
	if len(m.aslRows) == 0 {
		return "", false
	}
	switch aslView(m) {
	case aslViewLive, aslViewStale:
	default:
		return "", false
	}
	for _, row := range m.aslRows {
		if row.Window != nil && row.Window.KittyWindowID != 0 && row.Window.KittyWindowID == s.WindowID {
			return aslStateMarker(row.PrimaryState), true
		}
		if row.RunID != "" && row.RunID == s.ASLRunID {
			return aslStateMarker(row.PrimaryState), true
		}
	}
	return "", false
}

func aslStateMarker(state string) string {
	switch state {
	case "paused-for-reply":
		return "❓"
	case "verifying":
		return "🔍"
	case "failed", "invalid":
		return "❌"
	case "stale":
		return "⏳"
	default:
		return "🔁"
	}
}

func aslCanonicalDir(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return filepath.Clean(trimmed)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// openSelectedASLWorkbench opens the run's Workbench detail view at the section
// that matches its state, so a run waiting on a reply lands on the reply form
// rather than on a generic overview.
func (m Model) openSelectedASLWorkbench() (tea.Model, tea.Cmd) {
	row, ok := m.selectedASLRow()
	if !ok {
		return m, nil
	}
	if row.PlanDir == "" {
		m.aslErr = errors.New("workbench unavailable: this run reports no plan directory")
		return m, nil
	}
	if !openBrowserSupported() {
		m.aslErr = errors.New("workbench unavailable: no supported URL opener on this platform")
		return m, nil
	}
	m.aslErr = nil
	return m, m.runASLWorkbenchCmd(row.RowID, row.PlanDir, aslWorkbenchFocus(row.PrimaryState))
}

// aslWorkbenchFocus maps a run state to the Workbench section an operator needs
// next for that state.
func aslWorkbenchFocus(state string) string {
	switch state {
	case "paused-for-reply":
		return aslbridge.FocusReply
	case "verifying":
		return aslbridge.FocusVerification
	case "failed", "stale", "invalid":
		return aslbridge.FocusEvents
	default:
		return aslbridge.FocusActive
	}
}

func (m Model) handleASLWorkbenchDone(msg aslWorkbenchDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.aslErr = msg.err
		return m, nil
	}
	m.aslErr = nil
	return m, nil
}

func scrollASLToCursor(m *Model) {
	total := len(m.aslRows)
	height := aslVisibleHeight(*m)
	if height <= 0 || total == 0 {
		m.aslOffset = 0
		return
	}
	if m.aslCursor < m.aslOffset {
		m.aslOffset = m.aslCursor
	}
	if m.aslCursor >= m.aslOffset+height {
		m.aslOffset = m.aslCursor - height + 1
	}
	maxOffset := total - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	m.aslOffset = clamp(m.aslOffset, 0, maxOffset)
}
