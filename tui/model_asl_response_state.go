package tui

// This file owns the response form's data: the identity it is bound to, the
// buttons it offers, the draft it holds, and the predicates that decide which
// form a row opens and whether a fetched body still answers it. Nothing here
// talks to the daemon; the flow that does lives in model_asl_response.go.

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/aslbridge"
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
