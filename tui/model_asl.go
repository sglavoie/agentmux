// This file is what the root model does about agent-session-loop: navigating
// the Runs list, driving the response form from open to submitted, issuing the
// control commands a run permits, and rediscovering the active runs the
// dashboard marks its rows with.

package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/session"
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
	case keyEnter, keyF:
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
	case keyG, keyHome:
		m.aslCursor = 0
	case keyGG, keyEnd:
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

// This file is the response form's flow: opening it against a captured target,
// fetching one question body at a time, editing a draft, submitting it, and
// advancing to whatever is still open. The state it operates on and the refusal
// vocabulary it reports both live in tui/asl.

// aslQuestionLoadedMsg carries one deliberately fetched question body. The body
// is a SecretText, so it is redacted by every formatter it could reach on the
// way to the form.
type aslQuestionLoadedMsg struct {
	seq  int
	body aslbridge.QuestionBody
	err  error
}

// aslActionDoneMsg is the outcome of one submitted mutation. It reports the
// receipt's identity and status and never the text that was submitted.
type aslActionDoneMsg struct {
	seq         int
	control     bool
	rowID       string
	method      string
	operationID string
	status      string
	err         error
}

func aslQuestionCmd(seq int, planDir, questionID string) tea.Cmd {
	return func() tea.Msg {
		body, err := ipc.NewClient().ASLQuestionBody(aslbridge.QuestionRequest{
			PlanDir:    planDir,
			QuestionID: questionID,
		})
		return aslQuestionLoadedMsg{seq: seq, body: body, err: err}
	}
}

func aslActionCmd(seq int, control bool, rowID string, req aslbridge.ActionRequest) tea.Cmd {
	return func() tea.Msg {
		op, err := ipc.NewClient().ASLAct(req)
		return aslActionDoneMsg{
			seq: seq, control: control, rowID: rowID, method: req.Method,
			operationID: req.OperationID, status: op.Status, err: err,
		}
	}
}

func (m Model) runASLQuestionCmd(seq int, planDir, questionID string) tea.Cmd {
	if m.backgroundCommands.aslQuestion != nil {
		return m.backgroundCommands.aslQuestion(m, seq, planDir, questionID)
	}
	return defaultModelBackgroundCommands().aslQuestion(m, seq, planDir, questionID)
}

func (m Model) runASLActionCmd(seq int, control bool, rowID string, req aslbridge.ActionRequest) tea.Cmd {
	if m.backgroundCommands.aslAction != nil {
		return m.backgroundCommands.aslAction(m, seq, control, rowID, req)
	}
	return defaultModelBackgroundCommands().aslAction(m, seq, control, rowID, req)
}

// openASLResponse opens the response form for the selected run. Only the
// selected question's body is fetched; the remaining IDs travel as metadata and
// are fetched one at a time as the operator navigates.
func (m Model) openASLResponse() (tea.Model, tea.Cmd) {
	row, ok := m.selectedASLRow()
	if !ok {
		return m, nil
	}
	kind := asl.ResponseKindFor(row)
	if allowed, reason := aslActionAllowed(m, row, asl.ResponseGateMethod(kind)); !allowed {
		m.aslNotice = reason
		return m, nil
	}

	ids := asl.QuestionIDs(row)
	state := asl.ResponseState{
		Active: true,
		Kind:   kind,
		Target: asl.ActionTarget{
			RowID:       row.RowID,
			PlanDir:     row.PlanDir,
			RunID:       row.RunID,
			ActionToken: row.ActionToken,
			Label:       asl.RowLabel(row),
		},
		QuestionIDs: ids,
		Buttons:     asl.ResponseButtons(kind),
		Drafts:      map[string]aslbridge.SecretText{},
	}
	if row.Question != nil {
		state.ContextKind = row.Question.ContextKind
		state.AskedAt = row.Question.AskedAt
		for i, id := range ids {
			if id == row.Question.QuestionID {
				state.QuestionIdx = i
			}
		}
	}
	if len(ids) > 0 {
		state.Target.QuestionID = ids[state.QuestionIdx]
	}

	m.aslNotice = ""
	m.aslResponse = state
	m.mode = ModeASLResponse
	return m, m.startASLQuestionFetch()
}

func (m *Model) startASLQuestionFetch() tea.Cmd {
	m.aslResponse.FetchSeq++
	m.aslResponse.Fetching = true
	m.aslResponse.Body = ""
	m.aslResponse.BodyBytes = 0
	m.aslResponse.BodyScroll = 0
	m.aslResponse.Notice = ""
	return m.runASLQuestionCmd(m.aslResponse.FetchSeq, m.aslResponse.Target.PlanDir, m.aslResponse.Target.QuestionID)
}

// closeASLResponse returns to the Runs list and drops the whole form state, so
// the fetched body and every draft go with it.
func (m *Model) closeASLResponse() {
	m.aslResponse = asl.ResponseState{}
	m.mode = ModeASLRuns
}

// invalidateASLResponse tears the form down when the bridge reports that the
// target no longer matches what the operator was answering. The submission is
// not retried under a fresh token: the list is refreshed and the operator
// re-selects deliberately.
func (m Model) invalidateASLResponse() (tea.Model, tea.Cmd) {
	m.closeASLResponse()
	m.aslNotice = asl.TargetChangedText
	return m, m.beginASLRefresh()
}

func (m Model) handleASLQuestionLoaded(msg aslQuestionLoadedMsg) (tea.Model, tea.Cmd) {
	if !m.aslResponse.Active || msg.seq != m.aslResponse.FetchSeq {
		return m, nil
	}
	m.aslResponse.Fetching = false
	if msg.err != nil {
		if asl.StaleTarget(msg.err) {
			return m.invalidateASLResponse()
		}
		m.aslResponse.Notice = asl.RefusalText(msg.err)
		return m, nil
	}
	if !asl.BodyMatchesTarget(msg.body, m.aslResponse.Target) {
		return m.invalidateASLResponse()
	}
	if m.aslResponse.Reauthorize {
		// The previous closure changed what the run's token fingerprints, so
		// the captured authorization is spent. The fetch that proved the next
		// question is still open is also what re-mints it; without a fresh one
		// there is nothing legitimate left to submit under.
		if msg.body.ActionToken == "" {
			return m.invalidateASLResponse()
		}
		m.aslResponse.Target.ActionToken = msg.body.ActionToken
		m.aslResponse.Reauthorize = false
	}
	m.aslResponse.Body = msg.body.Body
	m.aslResponse.BodyBytes = msg.body.BodyBytes
	if msg.body.ContextKind != "" {
		m.aslResponse.ContextKind = msg.body.ContextKind
	}
	return m, nil
}

func (m Model) handleASLResponseKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if !m.aslResponse.Active {
		m.mode = ModeASLRuns
		return m, nil
	}
	key := msg.String()
	switch key {
	case keyCtrlC:
		return m, tea.Quit
	case keyEsc:
		m.closeASLResponse()
		return m, nil
	case keyTab, keyShiftTab:
		m.aslResponse.FocusButtons = !m.aslResponse.FocusButtons
		return m, nil
	case keyCtrlS:
		button, ok := m.aslResponse.Button()
		if !ok {
			return m, nil
		}
		if !button.NeedsText {
			// Ctrl+S is the answer-submitting shortcut. When the cursor sits on
			// an answerless button it still submits the answer, so a typed reply
			// is never discarded by a stray focus position.
			button = m.aslResponse.Buttons[0]
		}
		return m.submitASLResponse(button)
	case keyCtrlN:
		return m.moveASLQuestion(1)
	case keyCtrlP:
		return m.moveASLQuestion(-1)
	}

	if m.aslResponse.FocusButtons {
		switch key {
		case keyEnter:
			button, ok := m.aslResponse.Button()
			if !ok {
				return m, nil
			}
			return m.submitASLResponse(button)
		case keyLeft, keyUp:
			m.aslResponse.ButtonIdx = clamp(m.aslResponse.ButtonIdx-1, 0, len(m.aslResponse.Buttons)-1)
		case keyRight, keyDown:
			m.aslResponse.ButtonIdx = clamp(m.aslResponse.ButtonIdx+1, 0, len(m.aslResponse.Buttons)-1)
		}
		return m, nil
	}

	draft := string(m.aslResponse.Draft())
	switch key {
	case keyEnter:
		draft += "\n"
	case keyBackspace:
		if runes := []rune(draft); len(runes) > 0 {
			draft = string(runes[:len(runes)-1])
		}
	default:
		text := msg.Key().Text
		if text == "" {
			return m, nil
		}
		draft += text
	}
	m.aslResponse.SetDraft(aslbridge.SecretText(draft))
	return m, nil
}

// moveASLQuestion walks the captured question set. Each question's body is
// fetched separately, and the draft typed for the question being left is kept
// under its own ID so navigating away never discards a half-written answer.
func (m Model) moveASLQuestion(delta int) (tea.Model, tea.Cmd) {
	if len(m.aslResponse.QuestionIDs) < 2 || m.aslResponse.InFlight {
		return m, nil
	}
	next := clamp(m.aslResponse.QuestionIdx+delta, 0, len(m.aslResponse.QuestionIDs)-1)
	if next == m.aslResponse.QuestionIdx {
		return m, nil
	}
	m.aslResponse.QuestionIdx = next
	m.aslResponse.Target.QuestionID = m.aslResponse.QuestionIDs[next]
	m.aslResponse.Pending = asl.PendingSubmission{}
	m.aslResponse.Status = ""
	m.aslResponse.FocusButtons = false
	m.aslResponse.ButtonIdx = 0
	return m, m.startASLQuestionFetch()
}

func (m Model) submitASLResponse(button asl.ResponseButton) (tea.Model, tea.Cmd) {
	if m.aslResponse.InFlight {
		return m, nil
	}
	if m.aslResponse.Reauthorize {
		m.aslResponse.Notice = "the previous answer was accepted: this question is still being re-authorized"
		return m, nil
	}
	text := m.aslResponse.Draft()
	if button.NeedsText && strings.TrimSpace(string(text)) == "" {
		m.aslResponse.Notice = "the answer is empty: type a reply before submitting"
		return m, nil
	}

	pending := m.aslResponse.Pending
	if pending.OperationID == "" || pending.Method != button.Method || pending.Resolution != button.Resolution {
		pending = asl.PendingSubmission{
			OperationID: asl.NewOperationID(),
			Method:      button.Method,
			Resolution:  button.Resolution,
		}
	}
	target := m.aslResponse.Target
	req := aslbridge.ActionRequest{
		Method:      button.Method,
		PlanDir:     target.PlanDir,
		RunID:       target.RunID,
		QuestionID:  target.QuestionID,
		OperationID: pending.OperationID,
		ActionToken: target.ActionToken,
		Resolution:  button.Resolution,
	}
	if button.NeedsText {
		req.Text = text
	}

	m.aslResponse.Pending = pending
	m.aslResponse.InFlight = true
	m.aslResponse.ActionSeq++
	m.aslResponse.Status = asl.OpSubmitting
	m.aslResponse.Notice = ""
	m.trackASLOperation(asl.OperationView{
		OperationID: pending.OperationID,
		Method:      button.Method,
		RowID:       target.RowID,
		State:       asl.OpSubmitting,
	})
	return m, m.runASLActionCmd(m.aslResponse.ActionSeq, false, target.RowID, req)
}

func (m Model) handleASLActionDone(msg aslActionDoneMsg) (tea.Model, tea.Cmd) {
	state := asl.OperationState(msg.status)
	if msg.err != nil {
		state = asl.OpFailed
	}
	m.recordASLOperationState(msg.operationID, state)

	if msg.control {
		return m.handleASLControlDone(msg, state)
	}
	if !m.aslResponse.Active || msg.seq != m.aslResponse.ActionSeq {
		return m, nil
	}
	m.aslResponse.InFlight = false
	if msg.err != nil {
		if asl.StaleTarget(msg.err) {
			return m.invalidateASLResponse()
		}
		m.aslResponse.Status = asl.OpFailed
		m.aslResponse.Notice = asl.RefusalText(msg.err)
		return m, nil
	}
	m.aslResponse.Status = state
	return m.advanceASLResponse()
}

// advanceASLResponse closes out an accepted submission: the answered question's
// draft is dropped, the list is refreshed because ASL now knows more than the
// held snapshot, and any question still open in the captured set becomes the
// form's new target. A resume closes the form outright, because the run it
// resumed is no longer paused.
func (m Model) advanceASLResponse() (tea.Model, tea.Cmd) {
	answered := m.aslResponse.Target.QuestionID
	delete(m.aslResponse.Drafts, answered)
	m.aslResponse.Pending = asl.PendingSubmission{}

	remaining := make([]string, 0, len(m.aslResponse.QuestionIDs))
	for _, id := range m.aslResponse.QuestionIDs {
		if id != answered {
			remaining = append(remaining, id)
		}
	}
	if m.aslResponse.Kind == asl.ResponsePaused || len(remaining) == 0 {
		m.closeASLResponse()
		return m, m.beginASLRefresh()
	}

	m.aslResponse.QuestionIDs = remaining
	m.aslResponse.QuestionIdx = clamp(m.aslResponse.QuestionIdx, 0, len(remaining)-1)
	m.aslResponse.Target.QuestionID = remaining[m.aslResponse.QuestionIdx]
	m.aslResponse.Reauthorize = true
	m.aslResponse.FocusButtons = false
	m.aslResponse.ButtonIdx = 0
	m.aslResponse.Status = ""
	return m, tea.Batch(m.startASLQuestionFetch(), m.beginASLRefresh())
}

// This file owns the two run-level control commands (stop after current and
// abort) and the model's half of the receipt window. The window's own shape and
// bound live in tui/asl; what a confirmation DOES to the model stays here.

// confirmASLControl captures the selected run and raises the shared
// confirmation overlay. Nothing is queued until the operator answers y.
func (m Model) confirmASLControl(method string) (tea.Model, tea.Cmd) {
	row, ok := m.selectedASLRow()
	if !ok {
		return m, nil
	}
	if allowed, reason := aslActionAllowed(m, row, method); !allowed {
		m.aslNotice = reason
		return m, nil
	}
	if m.aslControlInFlight {
		m.aslNotice = "a control action for this view is still in flight"
		return m, nil
	}
	m.aslNotice = ""
	m.aslActionTarget = &asl.ActionTarget{
		RowID:       row.RowID,
		PlanDir:     row.PlanDir,
		RunID:       row.RunID,
		ActionToken: row.ActionToken,
		Label:       asl.RowLabel(row),
	}
	if method == aslbridge.MethodRunAbort {
		m.confirmAction = asl.ConfirmAbort
	} else {
		m.confirmAction = asl.ConfirmStop
	}
	m.confirmConsequence = asl.ControlConsequence(method)
	m.confirmReturnMode = ModeASLRuns
	m.mode = ModeConfirm
	return m, nil
}

// submitASLControl queues one control command against the captured target. The
// capture is consumed here, so a second confirmation cannot reuse it.
func (m Model) submitASLControl(method string) (tea.Model, tea.Cmd) {
	if m.aslActionTarget == nil {
		return m, nil
	}
	target := *m.aslActionTarget
	m.aslActionTarget = nil
	if m.aslControlInFlight {
		return m, nil
	}
	operationID := asl.NewOperationID()
	m.aslControlInFlight = true
	m.aslControlSeq++
	m.trackASLOperation(asl.OperationView{
		OperationID: operationID,
		Method:      method,
		RowID:       target.RowID,
		State:       asl.OpSubmitting,
	})
	return m, m.runASLActionCmd(m.aslControlSeq, true, target.RowID, aslbridge.ActionRequest{
		Method:      method,
		PlanDir:     target.PlanDir,
		RunID:       target.RunID,
		OperationID: operationID,
		ActionToken: target.ActionToken,
	})
}

func (m Model) handleASLControlDone(msg aslActionDoneMsg, state string) (tea.Model, tea.Cmd) {
	if msg.seq != m.aslControlSeq {
		return m, nil
	}
	m.aslControlInFlight = false
	if msg.err != nil {
		if asl.StaleTarget(msg.err) {
			m.aslNotice = asl.TargetChangedText
			return m, m.beginASLRefresh()
		}
		m.aslNotice = asl.RefusalText(msg.err)
		return m, nil
	}
	m.aslNotice = ""
	toast := m.pushToast(Toast{
		Severity: toastSuccess,
		Text:     fmt.Sprintf("%s: %s", sentenceCase(asl.MethodLabel(msg.method)), state),
	})
	return m, tea.Batch(toast, m.beginASLRefresh())
}

func (m *Model) trackASLOperation(view asl.OperationView) {
	m.aslOperations = asl.TrackOperation(m.aslOperations, view)
}

func (m *Model) recordASLOperationState(operationID, state string) {
	asl.RecordOperationState(m.aslOperations, operationID, state)
}

// applyASLReceipts folds the daemon's bounded receipt window into the tracked
// operations. This is how an accepted submission becomes consumed after its
// form has already closed: the status advances without the answer being held,
// replayed, or resubmitted.
func (m *Model) applyASLReceipts(receipts []aslbridge.Operation) {
	asl.ApplyReceipts(m.aslOperations, receipts)
}

func (m Model) activeLoopsForSession(s session.RunningSession) []agentloop.Candidate {
	matches, err := agentloop.MatchingWorkspace(
		m.activeLoopCandidates,
		agentloop.WorkspaceTarget{CWD: s.Directory, Harness: s.Harness, WindowID: s.WindowID},
		commands.ActiveLoopWorkspaceTargets(m.sessions),
	)
	if err != nil {
		return nil
	}
	return matches
}
