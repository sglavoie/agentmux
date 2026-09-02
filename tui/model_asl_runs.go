package tui

import (
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/tui/asl"
	"github.com/sglavoie/agentmux/tui/commands"
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
		snapshot, meta, receipts, status, err := ipc.NewClient().ASLSnapshot()
		return aslSnapshotLoadedMsg{seq: seq, snapshot: snapshot, meta: meta, receipts: receipts, status: status, err: err}
	}
}

func aslWorkbenchCmd(rowID, planDir, focus string) tea.Cmd {
	return func() tea.Msg {
		link, err := ipc.NewClient().ASLWorkbench(aslbridge.WorkbenchRequest{PlanDir: planDir, Focus: focus})
		if err != nil {
			return aslWorkbenchDoneMsg{rowID: rowID, focus: focus, err: errASLWorkbenchOpen}
		}
		if commands.OpenBrowserURL(link.URL) != nil {
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
	m.aslRows = asl.SortRows(msg.snapshot.Rows)
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

func (m Model) selectedASLRow() (aslbridge.Row, bool) {
	return asl.RunsInput{Rows: m.aslRows, Cursor: m.aslCursor}.SelectedRow()
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
			return m, commands.CopyText(row.WorkspaceRoot)
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
	if bound, found := asl.ExactBinding(row, m.sessions); found {
		m.aslErr = nil
		return m, commands.Focus(m.client, bound.TabID)
	}
	candidates := asl.FallbackCandidates(row, m.sessions)
	switch len(candidates) {
	case 0:
		m.aslErr = errors.New("focus unavailable: no live session matches this run's workspace and harness")
		return m, nil
	case 1:
		m.aslErr = nil
		return m, commands.Focus(m.client, candidates[0].TabID)
	default:
		m.aslErr = nil
		m.aslFocusTarget = row.RowID
		m.openLiveSessionPicker(LivePickerASLFocus, ModeASLRuns, asl.RowLabel(row), candidates)
		return m, nil
	}
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
	if !commands.BrowserSupported() {
		m.aslErr = errors.New("workbench unavailable: no supported URL opener on this platform")
		return m, nil
	}
	m.aslErr = nil
	return m, m.runASLWorkbenchCmd(row.RowID, row.PlanDir, asl.WorkbenchFocus(row.PrimaryState))
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
