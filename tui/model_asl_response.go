package tui

// This file is the response form's flow: opening it against a captured target,
// fetching one question body at a time, editing a draft, submitting it, and
// advancing to whatever is still open. The state it operates on lives in
// model_asl_response_state.go and the refusal vocabulary in
// model_asl_refusal.go.

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
)

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
