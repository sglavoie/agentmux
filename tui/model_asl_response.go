package tui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon"
)

// aslActionTarget is the identity a response form or confirmation overlay is
// bound to. It is copied out of the selected row when the modal opens and is
// never rewritten while the modal is open, so a snapshot landing underneath
// cannot retarget an action the operator already reviewed.
type aslActionTarget struct {
	RowID       string
	PlanDir     string
	RunID       string
	QuestionID  string
	ActionToken string
	Label       string
}

// aslResponseKind selects the form's shape. A live wait can be answered or
// closed answerlessly; a paused run can only be answered, and answering it
// resumes the run.
type aslResponseKind int

const (
	aslResponseLive aslResponseKind = iota
	aslResponsePaused
)

// aslResponseButton is one submission the form offers. needsText marks the
// buttons that carry an operator answer and therefore refuse a blank editor.
type aslResponseButton struct {
	label      string
	method     string
	resolution string
	needsText  bool
}

func aslResponseButtons(kind aslResponseKind) []aslResponseButton {
	if kind == aslResponsePaused {
		return []aslResponseButton{{label: "Reply and resume", method: aslbridge.MethodRunResume, needsText: true}}
	}
	return []aslResponseButton{
		{label: "Reply", method: aslbridge.MethodQuestionReply, needsText: true},
		{label: "Continue", method: aslbridge.MethodQuestionResolve, resolution: aslbridge.ResolutionContinue},
		{label: "Pause", method: aslbridge.MethodQuestionResolve, resolution: aslbridge.ResolutionPause},
	}
}

// Body-free operation states the UI reports. They are derived from the bridge
// receipt vocabulary rather than from anything the operator typed.
const (
	aslOpSubmitting = "submitting"
	aslOpAccepted   = "accepted"
	aslOpConsumed   = "consumed"
	aslOpFailed     = "failed"
	aslOpExpired    = "expired"
)

func aslOperationState(status string) string {
	switch status {
	case aslbridge.OperationAccepted:
		return aslOpAccepted
	case aslbridge.OperationConsumed:
		return aslOpConsumed
	case aslbridge.OperationExpired:
		return aslOpExpired
	case aslbridge.OperationRejected, aslbridge.OperationSuperseded:
		return aslOpFailed
	default:
		return aslOpSubmitting
	}
}

// aslOperationView is one tracked submission. It carries identity and state
// only, so the receipt history can outlive the form without holding an answer.
type aslOperationView struct {
	OperationID string
	Method      string
	RowID       string
	State       string
}

// aslOperationHistory bounds how many receipts the Runs view keeps. A receipt
// is a status line, not a journal, so the window only has to be long enough to
// watch a submission travel from accepted to consumed.
const aslOperationHistory = 5

// aslPendingSubmission is the operation ID minted for one submission plus the
// exact call it was minted for. A manual retry after an ambiguous outcome
// reuses the ID so the bridge replays its own receipt instead of applying the
// mutation twice; changing the button mints a new ID, because replaying under a
// different method would return the wrong receipt.
type aslPendingSubmission struct {
	operationID string
	method      string
	resolution  string
}

// aslResponseState is every field the response form owns. It is replaced
// wholesale on cancel, on success, and on mode exit, so clearing the fetched
// question body and the operator's draft is one assignment rather than a
// checklist a later field can fall off.
type aslResponseState struct {
	active       bool
	kind         aslResponseKind
	target       aslActionTarget
	contextKind  string
	askedAt      string
	questionIDs  []string
	questionIdx  int
	body         aslbridge.SecretText
	bodyBytes    int
	bodyScroll   int
	drafts       map[string]aslbridge.SecretText
	fetching     bool
	fetchSeq     int
	buttons      []aslResponseButton
	focusButtons bool
	buttonIdx    int
	pending      aslPendingSubmission
	reauthorize  bool
	inFlight     bool
	actionSeq    int
	status       string
	notice       string
}

// String renders the form body-free. SecretText protects a value formatted on
// its own, but fmt cannot reach a String method through an unexported field, so
// the state that holds the body and the drafts has to redact itself.
func (s aslResponseState) String() string {
	return fmt.Sprintf(
		"aslResponseState{active:%t kind:%d row:%s question:%s bodyBytes:%d drafts:%d status:%s}",
		s.active, s.kind, s.target.RowID, s.target.QuestionID, s.bodyBytes, len(s.drafts), s.status)
}

func (s aslResponseState) GoString() string { return s.String() }

func (s *aslResponseState) draft() aslbridge.SecretText {
	return s.drafts[s.target.QuestionID]
}

func (s *aslResponseState) setDraft(text aslbridge.SecretText) {
	if s.drafts == nil {
		s.drafts = map[string]aslbridge.SecretText{}
	}
	s.drafts[s.target.QuestionID] = text
}

func (s *aslResponseState) button() (aslResponseButton, bool) {
	if s.buttonIdx < 0 || s.buttonIdx >= len(s.buttons) {
		return aslResponseButton{}, false
	}
	return s.buttons[s.buttonIdx], true
}

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

// aslTargetChangedText is shown when the bridge refuses because the run moved
// under a modal. The action is not retried against a fresh token.
const aslTargetChangedText = "this run changed since it was selected: the form was closed and the list refreshed"

func aslQuestionCmd(seq int, planDir, questionID string) tea.Cmd {
	return func() tea.Msg {
		body, err := daemon.NewClient().ASLQuestionBody(aslbridge.QuestionRequest{
			PlanDir:    planDir,
			QuestionID: questionID,
		})
		return aslQuestionLoadedMsg{seq: seq, body: body, err: err}
	}
}

func aslActionCmd(seq int, control bool, rowID string, req aslbridge.ActionRequest) tea.Cmd {
	return func() tea.Msg {
		op, err := daemon.NewClient().ASLAct(req)
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

// newASLOperationID mints the client operation ID for one submission. It is
// opaque and unique per submission; the bridge uses it both to deduplicate a
// replayed call and, for control commands, as the durable command ID.
func newASLOperationID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("amx-%d", time.Now().UnixNano())
	}
	return "amx-" + hex.EncodeToString(buf[:])
}

// aslRowPermits reports whether ASL's own projection lists a method as
// permitted on this row. Permission is never inferred from the state string.
func aslRowPermits(row aslbridge.Row, method string) bool {
	for _, permitted := range row.Actions {
		if permitted == method {
			return true
		}
	}
	return false
}

// aslActionAllowed decides whether one bridge method may be invoked against a
// row right now, and explains the refusal when it may not. Availability comes
// only from the daemon capability block and the row's projected metadata.
func aslActionAllowed(m Model, row aslbridge.Row, method string) (bool, string) {
	switch aslView(m) {
	case aslViewLive:
	case aslViewStale:
		return false, "actions are unavailable: this snapshot is read-only"
	default:
		return false, "actions are unavailable: agent-session-loop is not reachable"
	}
	if !m.aslStatus.MutationsEnabled {
		return false, "actions are unavailable: the daemon reports agent-session-loop mutations as disabled"
	}
	if strings.TrimSpace(row.ActionToken) == "" {
		return false, "actions are unavailable: this run carries no action token"
	}
	if aslRowPermits(row, method) {
		return true, ""
	}
	label := aslMethodLabel(method)
	if reason := strings.TrimSpace(row.DisabledReasons[method]); reason != "" {
		return false, label + " is unavailable: " + reason
	}
	return false, label + " is unavailable for this run"
}

func aslMethodLabel(method string) string {
	switch method {
	case aslbridge.MethodQuestionReply:
		return "reply"
	case aslbridge.MethodQuestionResolve:
		return "closing the question"
	case aslbridge.MethodRunResume:
		return "resume"
	case aslbridge.MethodRunStopAfterCurrent:
		return "stop after current"
	case aslbridge.MethodRunAbort:
		return "abort"
	default:
		return method
	}
}

// aslResponseKindFor decides which form a row opens. Permitted actions decide
// it; the projected context kind only breaks the tie for a row that permits
// neither, so the refusal names the action the operator expected.
func aslResponseKindFor(row aslbridge.Row) aslResponseKind {
	switch {
	case aslRowPermits(row, aslbridge.MethodQuestionReply):
		return aslResponseLive
	case aslRowPermits(row, aslbridge.MethodRunResume):
		return aslResponsePaused
	case row.Question != nil && row.Question.ContextKind == "pause":
		return aslResponsePaused
	default:
		return aslResponseLive
	}
}

func aslResponseGateMethod(kind aslResponseKind) string {
	if kind == aslResponsePaused {
		return aslbridge.MethodRunResume
	}
	return aslbridge.MethodQuestionReply
}

// aslQuestionIDs returns the open question IDs a row projects, body-free. The
// list is the navigation set the form is opened against and does not grow
// while the form is open.
func aslQuestionIDs(row aslbridge.Row) []string {
	if row.Question == nil {
		return nil
	}
	if len(row.Question.QuestionIDs) > 0 {
		return append([]string(nil), row.Question.QuestionIDs...)
	}
	if row.Question.QuestionID != "" {
		return []string{row.Question.QuestionID}
	}
	return nil
}

// openASLResponse opens the response form for the selected run. Only the
// selected question's body is fetched; the remaining IDs travel as metadata and
// are fetched one at a time as the operator navigates.
func (m Model) openASLResponse() (tea.Model, tea.Cmd) {
	row, ok := m.selectedASLRow()
	if !ok {
		return m, nil
	}
	kind := aslResponseKindFor(row)
	if allowed, reason := aslActionAllowed(m, row, aslResponseGateMethod(kind)); !allowed {
		m.aslNotice = reason
		return m, nil
	}

	ids := aslQuestionIDs(row)
	state := aslResponseState{
		active: true,
		kind:   kind,
		target: aslActionTarget{
			RowID:       row.RowID,
			PlanDir:     row.PlanDir,
			RunID:       row.RunID,
			ActionToken: row.ActionToken,
			Label:       aslRowLabel(row),
		},
		questionIDs: ids,
		buttons:     aslResponseButtons(kind),
		drafts:      map[string]aslbridge.SecretText{},
	}
	if row.Question != nil {
		state.contextKind = row.Question.ContextKind
		state.askedAt = row.Question.AskedAt
		for i, id := range ids {
			if id == row.Question.QuestionID {
				state.questionIdx = i
			}
		}
	}
	if len(ids) > 0 {
		state.target.QuestionID = ids[state.questionIdx]
	}

	m.aslNotice = ""
	m.aslResponse = state
	m.mode = ModeASLResponse
	return m, m.startASLQuestionFetch()
}

func (m *Model) startASLQuestionFetch() tea.Cmd {
	m.aslResponse.fetchSeq++
	m.aslResponse.fetching = true
	m.aslResponse.body = ""
	m.aslResponse.bodyBytes = 0
	m.aslResponse.bodyScroll = 0
	m.aslResponse.notice = ""
	return m.runASLQuestionCmd(m.aslResponse.fetchSeq, m.aslResponse.target.PlanDir, m.aslResponse.target.QuestionID)
}

// closeASLResponse returns to the Runs list and drops the whole form state, so
// the fetched body and every draft go with it.
func (m *Model) closeASLResponse() {
	m.aslResponse = aslResponseState{}
	m.mode = ModeASLRuns
}

// invalidateASLResponse tears the form down when the bridge reports that the
// target no longer matches what the operator was answering. The submission is
// not retried under a fresh token: the list is refreshed and the operator
// re-selects deliberately.
func (m Model) invalidateASLResponse() (tea.Model, tea.Cmd) {
	m.closeASLResponse()
	m.aslNotice = aslTargetChangedText
	return m, m.beginASLRefresh()
}

func (m Model) handleASLQuestionLoaded(msg aslQuestionLoadedMsg) (tea.Model, tea.Cmd) {
	if !m.aslResponse.active || msg.seq != m.aslResponse.fetchSeq {
		return m, nil
	}
	m.aslResponse.fetching = false
	if msg.err != nil {
		if aslStaleTarget(msg.err) {
			return m.invalidateASLResponse()
		}
		m.aslResponse.notice = aslRefusalText(msg.err)
		return m, nil
	}
	if !aslBodyMatchesTarget(msg.body, m.aslResponse.target) {
		return m.invalidateASLResponse()
	}
	if m.aslResponse.reauthorize {
		// The previous closure changed what the run's token fingerprints, so
		// the captured authorization is spent. The fetch that proved the next
		// question is still open is also what re-mints it; without a fresh one
		// there is nothing legitimate left to submit under.
		if msg.body.ActionToken == "" {
			return m.invalidateASLResponse()
		}
		m.aslResponse.target.ActionToken = msg.body.ActionToken
		m.aslResponse.reauthorize = false
	}
	m.aslResponse.body = msg.body.Body
	m.aslResponse.bodyBytes = msg.body.BodyBytes
	if msg.body.ContextKind != "" {
		m.aslResponse.contextKind = msg.body.ContextKind
	}
	return m, nil
}

// aslBodyMatchesTarget rejects a body that answers a different plan or
// question than the one the form is bound to. The captured target is the
// authority; a body that disagrees means the run moved under the form.
func aslBodyMatchesTarget(body aslbridge.QuestionBody, target aslActionTarget) bool {
	if body.PlanDir != "" && target.PlanDir != "" &&
		aslCanonicalDir(body.PlanDir) != aslCanonicalDir(target.PlanDir) {
		return false
	}
	if target.QuestionID != "" && body.QuestionID != "" && body.QuestionID != target.QuestionID {
		return false
	}
	return true
}

func (m Model) handleASLResponseKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if !m.aslResponse.active {
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
		m.aslResponse.focusButtons = !m.aslResponse.focusButtons
		return m, nil
	case keyCtrlS:
		button, ok := m.aslResponse.button()
		if !ok {
			return m, nil
		}
		if !button.needsText {
			// Ctrl+S is the answer-submitting shortcut. When the cursor sits on
			// an answerless button it still submits the answer, so a typed reply
			// is never discarded by a stray focus position.
			button = m.aslResponse.buttons[0]
		}
		return m.submitASLResponse(button)
	case keyCtrlN:
		return m.moveASLQuestion(1)
	case keyCtrlP:
		return m.moveASLQuestion(-1)
	}

	if m.aslResponse.focusButtons {
		switch key {
		case keyEnter:
			button, ok := m.aslResponse.button()
			if !ok {
				return m, nil
			}
			return m.submitASLResponse(button)
		case keyLeft, keyUp:
			m.aslResponse.buttonIdx = clamp(m.aslResponse.buttonIdx-1, 0, len(m.aslResponse.buttons)-1)
		case keyRight, keyDown:
			m.aslResponse.buttonIdx = clamp(m.aslResponse.buttonIdx+1, 0, len(m.aslResponse.buttons)-1)
		}
		return m, nil
	}

	draft := string(m.aslResponse.draft())
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
	m.aslResponse.setDraft(aslbridge.SecretText(draft))
	return m, nil
}

// moveASLQuestion walks the captured question set. Each question's body is
// fetched separately, and the draft typed for the question being left is kept
// under its own ID so navigating away never discards a half-written answer.
func (m Model) moveASLQuestion(delta int) (tea.Model, tea.Cmd) {
	if len(m.aslResponse.questionIDs) < 2 || m.aslResponse.inFlight {
		return m, nil
	}
	next := clamp(m.aslResponse.questionIdx+delta, 0, len(m.aslResponse.questionIDs)-1)
	if next == m.aslResponse.questionIdx {
		return m, nil
	}
	m.aslResponse.questionIdx = next
	m.aslResponse.target.QuestionID = m.aslResponse.questionIDs[next]
	m.aslResponse.pending = aslPendingSubmission{}
	m.aslResponse.status = ""
	m.aslResponse.focusButtons = false
	m.aslResponse.buttonIdx = 0
	return m, m.startASLQuestionFetch()
}

func (m Model) submitASLResponse(button aslResponseButton) (tea.Model, tea.Cmd) {
	if m.aslResponse.inFlight {
		return m, nil
	}
	if m.aslResponse.reauthorize {
		m.aslResponse.notice = "the previous answer was accepted: this question is still being re-authorized"
		return m, nil
	}
	text := m.aslResponse.draft()
	if button.needsText && strings.TrimSpace(string(text)) == "" {
		m.aslResponse.notice = "the answer is empty: type a reply before submitting"
		return m, nil
	}

	pending := m.aslResponse.pending
	if pending.operationID == "" || pending.method != button.method || pending.resolution != button.resolution {
		pending = aslPendingSubmission{
			operationID: newASLOperationID(),
			method:      button.method,
			resolution:  button.resolution,
		}
	}
	target := m.aslResponse.target
	req := aslbridge.ActionRequest{
		Method:      button.method,
		PlanDir:     target.PlanDir,
		RunID:       target.RunID,
		QuestionID:  target.QuestionID,
		OperationID: pending.operationID,
		ActionToken: target.ActionToken,
		Resolution:  button.resolution,
	}
	if button.needsText {
		req.Text = text
	}

	m.aslResponse.pending = pending
	m.aslResponse.inFlight = true
	m.aslResponse.actionSeq++
	m.aslResponse.status = aslOpSubmitting
	m.aslResponse.notice = ""
	m.trackASLOperation(aslOperationView{
		OperationID: pending.operationID,
		Method:      button.method,
		RowID:       target.RowID,
		State:       aslOpSubmitting,
	})
	return m, m.runASLActionCmd(m.aslResponse.actionSeq, false, target.RowID, req)
}

func (m Model) handleASLActionDone(msg aslActionDoneMsg) (tea.Model, tea.Cmd) {
	state := aslOperationState(msg.status)
	if msg.err != nil {
		state = aslOpFailed
	}
	m.recordASLOperationState(msg.operationID, state)

	if msg.control {
		return m.handleASLControlDone(msg, state)
	}
	if !m.aslResponse.active || msg.seq != m.aslResponse.actionSeq {
		return m, nil
	}
	m.aslResponse.inFlight = false
	if msg.err != nil {
		if aslStaleTarget(msg.err) {
			return m.invalidateASLResponse()
		}
		m.aslResponse.status = aslOpFailed
		m.aslResponse.notice = aslRefusalText(msg.err)
		return m, nil
	}
	m.aslResponse.status = state
	return m.advanceASLResponse()
}

// advanceASLResponse closes out an accepted submission: the answered question's
// draft is dropped, the list is refreshed because ASL now knows more than the
// held snapshot, and any question still open in the captured set becomes the
// form's new target. A resume closes the form outright, because the run it
// resumed is no longer paused.
func (m Model) advanceASLResponse() (tea.Model, tea.Cmd) {
	answered := m.aslResponse.target.QuestionID
	delete(m.aslResponse.drafts, answered)
	m.aslResponse.pending = aslPendingSubmission{}

	remaining := make([]string, 0, len(m.aslResponse.questionIDs))
	for _, id := range m.aslResponse.questionIDs {
		if id != answered {
			remaining = append(remaining, id)
		}
	}
	if m.aslResponse.kind == aslResponsePaused || len(remaining) == 0 {
		m.closeASLResponse()
		return m, m.beginASLRefresh()
	}

	m.aslResponse.questionIDs = remaining
	m.aslResponse.questionIdx = clamp(m.aslResponse.questionIdx, 0, len(remaining)-1)
	m.aslResponse.target.QuestionID = remaining[m.aslResponse.questionIdx]
	m.aslResponse.reauthorize = true
	m.aslResponse.focusButtons = false
	m.aslResponse.buttonIdx = 0
	m.aslResponse.status = ""
	return m, tea.Batch(m.startASLQuestionFetch(), m.beginASLRefresh())
}

// Confirmation action names for the shared y/n overlay.
const (
	aslConfirmStop  = "asl stop"
	aslConfirmAbort = "asl abort"
)

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
	m.aslActionTarget = &aslActionTarget{
		RowID:       row.RowID,
		PlanDir:     row.PlanDir,
		RunID:       row.RunID,
		ActionToken: row.ActionToken,
		Label:       aslRowLabel(row),
	}
	if method == aslbridge.MethodRunAbort {
		m.confirmAction = aslConfirmAbort
	} else {
		m.confirmAction = aslConfirmStop
	}
	m.confirmConsequence = aslControlConsequence(method)
	m.confirmReturnMode = ModeASLRuns
	m.mode = ModeConfirm
	return m, nil
}

func aslControlConsequence(method string) string {
	if method == aslbridge.MethodRunAbort {
		return fmt.Sprintf(
			"The in-flight turn is asked to stop within %d seconds. A turn that cannot be interrupted in time degrades to stopping after the current session.",
			aslbridge.AbortGraceSeconds)
	}
	return "The current session runs to completion and the run then stops. The in-flight turn is not interrupted."
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
	operationID := newASLOperationID()
	m.aslControlInFlight = true
	m.aslControlSeq++
	m.trackASLOperation(aslOperationView{
		OperationID: operationID,
		Method:      method,
		RowID:       target.RowID,
		State:       aslOpSubmitting,
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
		if aslStaleTarget(msg.err) {
			m.aslNotice = aslTargetChangedText
			return m, m.beginASLRefresh()
		}
		m.aslNotice = aslRefusalText(msg.err)
		return m, nil
	}
	m.aslNotice = ""
	toast := m.pushToast(Toast{
		Severity: toastSuccess,
		Text:     fmt.Sprintf("%s: %s", sentenceCase(aslMethodLabel(msg.method)), state),
	})
	return m, tea.Batch(toast, m.beginASLRefresh())
}

func (m *Model) trackASLOperation(view aslOperationView) {
	for i := range m.aslOperations {
		if m.aslOperations[i].OperationID == view.OperationID {
			m.aslOperations[i] = view
			return
		}
	}
	m.aslOperations = append(m.aslOperations, view)
	if len(m.aslOperations) > aslOperationHistory {
		m.aslOperations = m.aslOperations[len(m.aslOperations)-aslOperationHistory:]
	}
}

func (m *Model) recordASLOperationState(operationID, state string) {
	if operationID == "" {
		return
	}
	for i := range m.aslOperations {
		if m.aslOperations[i].OperationID == operationID {
			m.aslOperations[i].State = state
			return
		}
	}
}

// applyASLReceipts folds the daemon's bounded receipt window into the tracked
// operations. This is how an accepted submission becomes consumed after its
// form has already closed: the status advances without the answer being held,
// replayed, or resubmitted.
func (m *Model) applyASLReceipts(receipts []aslbridge.Operation) {
	for _, receipt := range receipts {
		m.recordASLOperationState(receipt.OperationID, aslOperationState(receipt.Status))
	}
}

// aslStaleCodes are the refusals that mean the run moved: the token, the
// question, or the run itself no longer matches what the operator reviewed.
var aslStaleCodes = map[string]bool{
	"stale-action-token":   true,
	"action-not-permitted": true,
	"no-active-run":        true,
	"no-open-question":     true,
	"question-mismatch":    true,
	"run-mismatch":         true,
	"not-paused":           true,
}

var aslErrorCodes = map[string]bool{
	"invalid-plan":            true,
	"invalid-action":          true,
	"empty-text":              true,
	"question-body-too-large": true,
	"missing-question-body":   true,
	"malformed-json":          true,
	"invalid-request":         true,
	"unsupported-protocol":    true,
	"unknown-method":          true,
	"frame-too-large":         true,
	"internal-error":          true,
	"mutation-failed":         true,
	"workbench-unavailable":   true,
}

// aslErrorCode recovers the bridge refusal code from a daemon error. The daemon
// flattens a typed bridge error to "<code>: <message>", so the code is the only
// machine-readable part that survives the IPC boundary.
func aslErrorCode(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	code := text
	if idx := strings.Index(text, ": "); idx > 0 {
		code = text[:idx]
	}
	if aslStaleCodes[code] || aslErrorCodes[code] {
		return code
	}
	return ""
}

func aslStaleTarget(err error) bool {
	return aslStaleCodes[aslErrorCode(err)]
}

// aslRefusalText maps a refusal to a fixed operator-facing sentence. The
// bridge's own message is deliberately dropped: mapping by code is what makes
// it structurally impossible for question or answer text to reach an error
// string, a status line, or a toast.
func aslRefusalText(err error) string {
	switch aslErrorCode(err) {
	case "empty-text":
		return "the answer is empty: type a reply before submitting"
	case "question-body-too-large":
		return "this question is too large to show here: open the Workbench with w"
	case "missing-question-body":
		return "this question carries no readable text: open the Workbench with w"
	case "invalid-plan":
		return "agent-session-loop no longer recognizes this plan directory"
	case "mutation-failed":
		return "agent-session-loop could not record the action; nothing was applied"
	case "workbench-unavailable":
		return "the Workbench is unavailable in this bridge session"
	case "invalid-request", "invalid-action":
		return "the daemon refused the request as malformed"
	case "unknown-method", "unsupported-protocol":
		return "this agent-session-loop build does not support this action"
	case "malformed-json", "frame-too-large", "internal-error":
		return "agent-session-loop reported an internal error"
	default:
		return "the action did not complete; its outcome is unknown and it was not retried"
	}
}
