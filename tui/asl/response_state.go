package asl

// This file owns the response modal's data: the identity it is bound to, the
// buttons it offers, the draft it holds, and the predicates that decide which
// form a row opens and whether a fetched body still answers it. Nothing here
// talks to the daemon; the flow that does stays in the root package, which owns
// the tea.Cmds and the mode stack.

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// ActionTarget is the identity a response form or confirmation overlay is bound
// to. It is copied out of the selected row when the modal opens and is never
// rewritten while the modal is open, so a snapshot landing underneath cannot
// retarget an action the operator already reviewed.
type ActionTarget struct {
	RowID       string
	PlanDir     string
	RunID       string
	QuestionID  string
	ActionToken string
	Label       string
}

// ResponseKind selects the form's shape. A live wait can be answered or closed
// answerlessly; a paused run can only be answered, and answering it resumes the
// run.
type ResponseKind int

const (
	ResponseLive ResponseKind = iota
	ResponsePaused
)

// ResponseButton is one submission the form offers. NeedsText marks the buttons
// that carry an operator answer and therefore refuse a blank editor.
type ResponseButton struct {
	Label      string
	Method     string
	Resolution string
	NeedsText  bool
}

// ResponseButtons is the closure set a form of this kind offers.
func ResponseButtons(kind ResponseKind) []ResponseButton {
	if kind == ResponsePaused {
		return []ResponseButton{{Label: "Reply and resume", Method: aslbridge.MethodRunResume, NeedsText: true}}
	}
	return []ResponseButton{
		{Label: "Reply", Method: aslbridge.MethodQuestionReply, NeedsText: true},
		{Label: "Continue", Method: aslbridge.MethodQuestionResolve, Resolution: aslbridge.ResolutionContinue},
		{Label: "Pause", Method: aslbridge.MethodQuestionResolve, Resolution: aslbridge.ResolutionPause},
	}
}

// PendingSubmission is the operation ID minted for one submission plus the
// exact call it was minted for. A manual retry after an ambiguous outcome
// reuses the ID so the bridge replays its own receipt instead of applying the
// mutation twice; changing the button mints a new ID, because replaying under a
// different method would return the wrong receipt.
type PendingSubmission struct {
	OperationID string
	Method      string
	Resolution  string
}

// ResponseState is every field the response form owns. It is replaced wholesale
// on cancel, on success, and on mode exit, so clearing the fetched question body
// and the operator's draft is one assignment rather than a checklist a later
// field can fall off.
//
// The fields are exported because the root package holds this value on its
// Bubble Tea model and drives the whole submission flow — fetching a body,
// editing a draft, submitting, advancing — from there. The redaction contract
// below is what keeps that exposure safe.
type ResponseState struct {
	Active       bool
	Kind         ResponseKind
	Target       ActionTarget
	ContextKind  string
	AskedAt      string
	QuestionIDs  []string
	QuestionIdx  int
	Body         aslbridge.SecretText
	BodyBytes    int
	BodyScroll   int
	Drafts       map[string]aslbridge.SecretText
	Fetching     bool
	FetchSeq     int
	Buttons      []ResponseButton
	FocusButtons bool
	ButtonIdx    int
	Pending      PendingSubmission
	Reauthorize  bool
	InFlight     bool
	ActionSeq    int
	Status       string
	Notice       string
}

// String renders the form body-free. SecretText protects a value formatted on
// its own, but fmt reaches an exported field directly, so the state that holds
// the body and the drafts has to redact itself.
func (s ResponseState) String() string {
	return fmt.Sprintf(
		"aslResponseState{active:%t kind:%d row:%s question:%s bodyBytes:%d drafts:%d status:%s}",
		s.Active, s.Kind, s.Target.RowID, s.Target.QuestionID, s.BodyBytes, len(s.Drafts), s.Status)
}

func (s ResponseState) GoString() string { return s.String() }

// Draft is the answer typed for the question the form is currently bound to.
func (s *ResponseState) Draft() aslbridge.SecretText {
	return s.Drafts[s.Target.QuestionID]
}

// SetDraft stores the answer under the current question's own ID, so navigating
// away never discards a half-written answer.
func (s *ResponseState) SetDraft(text aslbridge.SecretText) {
	if s.Drafts == nil {
		s.Drafts = map[string]aslbridge.SecretText{}
	}
	s.Drafts[s.Target.QuestionID] = text
}

// Button is the closure the cursor currently sits on.
func (s *ResponseState) Button() (ResponseButton, bool) {
	if s.ButtonIdx < 0 || s.ButtonIdx >= len(s.Buttons) {
		return ResponseButton{}, false
	}
	return s.Buttons[s.ButtonIdx], true
}

// ResponseKindFor decides which form a row opens. Permitted actions decide it;
// the projected context kind only breaks the tie for a row that permits neither,
// so the refusal names the action the operator expected.
func ResponseKindFor(row aslbridge.Row) ResponseKind {
	switch {
	case rowPermits(row, aslbridge.MethodQuestionReply):
		return ResponseLive
	case rowPermits(row, aslbridge.MethodRunResume):
		return ResponsePaused
	case row.Question != nil && row.Question.ContextKind == "pause":
		return ResponsePaused
	default:
		return ResponseLive
	}
}

// ResponseGateMethod is the method a form of this kind must be permitted to
// invoke before it may be opened at all.
func ResponseGateMethod(kind ResponseKind) string {
	if kind == ResponsePaused {
		return aslbridge.MethodRunResume
	}
	return aslbridge.MethodQuestionReply
}

// QuestionIDs returns the open question IDs a row projects, body-free. The list
// is the navigation set the form is opened against and does not grow while the
// form is open.
func QuestionIDs(row aslbridge.Row) []string {
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

// BodyMatchesTarget rejects a body that answers a different plan or question
// than the one the form is bound to. The captured target is the authority; a
// body that disagrees means the run moved under the form.
func BodyMatchesTarget(body aslbridge.QuestionBody, target ActionTarget) bool {
	if body.PlanDir != "" && target.PlanDir != "" &&
		canonicalDir(body.PlanDir) != canonicalDir(target.PlanDir) {
		return false
	}
	if target.QuestionID != "" && body.QuestionID != "" && body.QuestionID != target.QuestionID {
		return false
	}
	return true
}
