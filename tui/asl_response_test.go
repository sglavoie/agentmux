package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// openASLResponseDriver enters the Runs view, selects a run by ID, and opens its
// response form.
func openASLResponseDriver(t *testing.T, rowID string) *UITestDriver {
	t.Helper()
	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, rowID)
	if err := d.Key(keyASLRespond); err != nil {
		t.Fatalf("opening the response form: %v", err)
	}
	if got := d.Model().mode; got != ModeASLResponse {
		t.Fatalf("mode after %q = %v, want ModeASLResponse", keyASLRespond, got)
	}
	return d
}

func typeASLAnswer(t *testing.T, d *UITestDriver, text string) {
	t.Helper()
	if err := d.Text(text); err != nil {
		t.Fatalf("typing the answer: %v", err)
	}
}

func TestASLResponseLiveReplySubmitsCapturedTarget(t *testing.T) {
	d := openASLResponseDriver(t, "row-reply")
	m := d.Model()
	if m.aslResponse.kind != aslResponseLive {
		t.Fatalf("kind = %v, want aslResponseLive", m.aslResponse.kind)
	}
	if m.aslResponse.target.ActionToken != "token-reply" || m.aslResponse.target.QuestionID != "q-api-1" {
		t.Fatalf("capture = %+v, want the row's token and first question", m.aslResponse.target)
	}
	if m.aslResponse.body.Reveal() == "" {
		t.Fatal("the selected question's body was not fetched")
	}

	typeASLAnswer(t, d, "after the cutover")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting: %v", err)
	}

	actions := d.ASLActions()
	if len(actions) != 1 {
		t.Fatalf("submitted %d actions, want 1 (%+v)", len(actions), actions)
	}
	if actions[0].Method != aslbridge.MethodQuestionReply {
		t.Fatalf("method = %q, want %q", actions[0].Method, aslbridge.MethodQuestionReply)
	}
	if actions[0].QuestionID != "q-api-1" || actions[0].RowID != "row-reply" {
		t.Fatalf("action targeted %+v, want row-reply/q-api-1", actions[0])
	}
	if actions[0].OperationID == "" {
		t.Fatal("submission carried no operation ID")
	}
}

func TestASLResponseContinueAndPauseNeedNoAnswer(t *testing.T) {
	for _, tc := range []struct {
		name       string
		presses    int
		resolution string
	}{
		{"continue", 1, aslbridge.ResolutionContinue},
		{"pause", 2, aslbridge.ResolutionPause},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := openASLResponseDriver(t, "row-reply")
			if err := d.Key(keyTab); err != nil {
				t.Fatalf("moving focus to the buttons: %v", err)
			}
			for i := 0; i < tc.presses; i++ {
				if err := d.Key(keyRight); err != nil {
					t.Fatalf("selecting the button: %v", err)
				}
			}
			state := d.Model().aslResponse
			button, ok := state.button()
			if !ok || button.resolution != tc.resolution {
				t.Fatalf("selected button = %+v, want resolution %q", button, tc.resolution)
			}
			if err := d.Key(keyEnter); err != nil {
				t.Fatalf("activating the button: %v", err)
			}
			actions := d.ASLActions()
			if len(actions) != 1 || actions[0].Method != aslbridge.MethodQuestionResolve {
				t.Fatalf("actions = %+v, want one question.resolve", actions)
			}
		})
	}
}

func TestASLResponsePausedRunOffersOnlyReplyAndResume(t *testing.T) {
	d := openASLResponseDriver(t, "row-paused")
	m := d.Model()
	if m.aslResponse.kind != aslResponsePaused {
		t.Fatalf("kind = %v, want aslResponsePaused", m.aslResponse.kind)
	}
	if len(m.aslResponse.buttons) != 1 || m.aslResponse.buttons[0].method != aslbridge.MethodRunResume {
		t.Fatalf("buttons = %+v, want a single run.resume", m.aslResponse.buttons)
	}

	typeASLAnswer(t, d, "proceed with the migration")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	actions := d.ASLActions()
	if len(actions) != 1 || actions[0].Method != aslbridge.MethodRunResume {
		t.Fatalf("actions = %+v, want exactly one run.resume", actions)
	}
	if d.Model().mode != ModeASLRuns {
		t.Fatalf("mode after a resume = %v, want ModeASLRuns", d.Model().mode)
	}
	if d.Model().aslResponse.active {
		t.Fatal("the form survived a resume")
	}
}

func TestASLResponseNavigatesQuestionsAndFetchesEachBodySeparately(t *testing.T) {
	d := openASLResponseDriver(t, "row-reply")
	first := d.Model().aslResponse.body.Reveal()
	typeASLAnswer(t, d, "draft for one")

	if err := d.Key(keyCtrlN); err != nil {
		t.Fatalf("moving to the next question: %v", err)
	}
	m := d.Model()
	if m.aslResponse.target.QuestionID != "q-api-2" {
		t.Fatalf("question = %q, want q-api-2", m.aslResponse.target.QuestionID)
	}
	if body := m.aslResponse.body.Reveal(); body == first || body == "" {
		t.Fatalf("the second question reused the first body: %q", body)
	}
	if m.aslResponse.target.ActionToken != "token-reply" {
		t.Fatalf("navigation rewrote the captured token to %q", m.aslResponse.target.ActionToken)
	}
	if got := m.aslResponse.drafts["q-api-1"].Reveal(); got != "draft for one" {
		t.Fatalf("draft for the first question = %q, want it kept", got)
	}
	if got := m.aslResponse.drafts[m.aslResponse.target.QuestionID].Reveal(); got != "" {
		t.Fatalf("second question opened with draft %q, want empty", got)
	}
}

func TestASLResponseAdvancesToTheRemainingQuestionAfterClosure(t *testing.T) {
	d := openASLResponseDriver(t, "row-reply")
	typeASLAnswer(t, d, "answered")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	m := d.Model()
	if !m.aslResponse.active {
		t.Fatal("the form closed while a captured question was still open")
	}
	if m.aslResponse.target.QuestionID != "q-api-2" {
		t.Fatalf("advanced to %q, want q-api-2", m.aslResponse.target.QuestionID)
	}
	if _, kept := m.aslResponse.drafts["q-api-1"]; kept {
		t.Fatal("the answered question's draft survived its submission")
	}
}

func TestASLResponseSubmitsTwoQuestionsEndToEnd(t *testing.T) {
	d := openASLResponseDriver(t, "row-reply")
	typeASLAnswer(t, d, "answer for one")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting the first question: %v", err)
	}

	m := d.Model()
	if !m.aslResponse.active || m.aslResponse.target.QuestionID != "q-api-2" {
		t.Fatalf("after the first closure the form was %+v, want q-api-2 open", m.aslResponse)
	}
	if m.aslResponse.target.ActionToken != "token-reply-2" {
		t.Fatalf("token = %q, want the remaining question's refreshed token", m.aslResponse.target.ActionToken)
	}
	if m.aslResponse.reauthorize {
		t.Fatal("the form is still awaiting authorization after its body loaded")
	}

	typeASLAnswer(t, d, "answer for two")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting the second question: %v", err)
	}

	actions := d.ASLActions()
	if len(actions) != 2 {
		t.Fatalf("submitted %d actions, want 2 (%+v)", len(actions), actions)
	}
	for i, want := range []struct{ questionID, token string }{
		{"q-api-1", "token-reply"},
		{"q-api-2", "token-reply-2"},
	} {
		if actions[i].QuestionID != want.questionID || actions[i].ActionToken != want.token {
			t.Fatalf("action %d = %+v, want %s under %s", i, actions[i], want.questionID, want.token)
		}
		if actions[i].Method != aslbridge.MethodQuestionReply {
			t.Fatalf("action %d method = %q, want %q", i, actions[i].Method, aslbridge.MethodQuestionReply)
		}
	}
	if actions[0].OperationID == actions[1].OperationID {
		t.Fatalf("both closures reused operation ID %q", actions[0].OperationID)
	}

	m = d.Model()
	if m.aslNotice != "" || m.aslResponse.notice != "" {
		t.Fatalf("a closure was refused: list notice %q, form notice %q", m.aslNotice, m.aslResponse.notice)
	}
	if m.mode != ModeASLRuns || m.aslResponse.active {
		t.Fatalf("the form survived its last question: mode=%v active=%v", m.mode, m.aslResponse.active)
	}
}

func TestASLResponseRefusesASubmissionBeforeItIsReauthorized(t *testing.T) {
	fx := aslRunsFixture()
	body := fx.ASLQuestionBodies["q-api-2"]
	body.ActionToken = ""
	fx.ASLQuestionBodies["q-api-2"] = body
	d, _ := NewUITestFixtureDriver(fx, UITestSize{})
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Key(keyASLRuns); err != nil {
		t.Fatalf("entering Runs: %v", err)
	}
	selectASLRun(t, d, "row-reply")
	if err := d.Key(keyASLRespond); err != nil {
		t.Fatalf("opening the form: %v", err)
	}
	typeASLAnswer(t, d, "answer for one")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting the first question: %v", err)
	}

	m := d.Model()
	if m.aslResponse.active {
		t.Fatal("the form stayed open without a token to submit under")
	}
	if !strings.Contains(m.aslNotice, "changed") {
		t.Fatalf("notice = %q, want a changed-target explanation", m.aslNotice)
	}
	if got := len(d.ASLActions()); got != 1 {
		t.Fatalf("submitted %d actions, want exactly 1 under the spent token", got)
	}
}

func TestASLResponseRefusesAnEmptyAnswerWithoutSubmitting(t *testing.T) {
	d := openASLResponseDriver(t, "row-reply")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	if actions := d.ASLActions(); len(actions) != 0 {
		t.Fatalf("an empty answer was submitted: %+v", actions)
	}
	if notice := d.Model().aslResponse.notice; !strings.Contains(notice, "empty") {
		t.Fatalf("notice = %q, want an empty-answer explanation", notice)
	}

	// Whitespace is not an answer either.
	typeASLAnswer(t, d, "   ")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting whitespace: %v", err)
	}
	if actions := d.ASLActions(); len(actions) != 0 {
		t.Fatalf("a blank answer was submitted: %+v", actions)
	}
}

func TestASLResponseEnterInsertsANewlineAndTabMovesFocus(t *testing.T) {
	d := openASLResponseDriver(t, "row-reply")
	typeASLAnswer(t, d, "one")
	if err := d.Key(keyEnter); err != nil {
		t.Fatalf("pressing enter in the editor: %v", err)
	}
	typeASLAnswer(t, d, "two")
	editor := d.Model().aslResponse
	if got := editor.draft().Reveal(); got != "one\ntwo" {
		t.Fatalf("draft = %q, want a newline between the lines", got)
	}
	if actions := d.ASLActions(); len(actions) != 0 {
		t.Fatalf("enter submitted the form: %+v", actions)
	}

	if err := d.Key(keyTab); err != nil {
		t.Fatalf("pressing tab: %v", err)
	}
	if !d.Model().aslResponse.focusButtons {
		t.Fatal("tab did not move focus to the buttons")
	}
	if err := d.Key(keyShiftTab); err != nil {
		t.Fatalf("pressing shift+tab: %v", err)
	}
	if d.Model().aslResponse.focusButtons {
		t.Fatal("shift+tab did not return focus to the editor")
	}
}

func TestASLResponseEscapeClearsTheBodyAndEveryDraft(t *testing.T) {
	d := openASLResponseDriver(t, "row-reply")
	typeASLAnswer(t, d, "secret answer text")
	if err := d.Key(keyEsc); err != nil {
		t.Fatalf("cancelling: %v", err)
	}
	m := d.Model()
	if m.mode != ModeASLRuns {
		t.Fatalf("mode after cancel = %v, want ModeASLRuns", m.mode)
	}
	if m.aslResponse.active || len(m.aslResponse.drafts) != 0 || m.aslResponse.body != "" {
		t.Fatalf("cancel left form state behind: %+v", m.aslResponse)
	}
	if actions := d.ASLActions(); len(actions) != 0 {
		t.Fatalf("cancel submitted something: %+v", actions)
	}
	assertNoASLTextLeak(t, d, "secret answer text")
}

// assertNoASLTextLeak checks the surfaces an answer must never reach: the
// rendered screen, the model's own formatting, and the observation a scenario
// serializes.
func assertNoASLTextLeak(t *testing.T, d *UITestDriver, text string) {
	t.Helper()
	observation := d.Observe()
	for _, line := range observation.Screen.Lines {
		if strings.Contains(line, text) {
			t.Fatalf("answer text reached the screen: %q", line)
		}
	}
	if dump := fmt.Sprintf("%+v", d.Model()); strings.Contains(dump, text) {
		t.Fatal("answer text survived formatting the model")
	}
}

func TestASLResponseRedactsDraftsAndBodiesInFormattedOutput(t *testing.T) {
	d := openASLResponseDriver(t, "row-reply")
	typeASLAnswer(t, d, "confidential-answer")
	dump := fmt.Sprintf("%+v", d.Model().aslResponse)
	if strings.Contains(dump, "confidential-answer") {
		t.Fatal("the draft is not redacted by fmt")
	}
	if strings.Contains(dump, "Should the migration run") {
		t.Fatal("the question body is not redacted by fmt")
	}
	if !strings.Contains(dump, "drafts:1") {
		t.Fatalf("formatted state lost its body-free draft count: %s", dump)
	}
	if got := fmt.Sprintf("%+v", d.Model().aslResponse.body); got != "[redacted]" {
		t.Fatalf("formatted body = %q, want [redacted]", got)
	}
}

func TestASLResponseDoubleSubmitIsRefusedWhileInFlight(t *testing.T) {
	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-reply")
	m := d.Model()
	updated, _ := m.openASLResponse()
	m = updated.(Model)
	m.aslResponse.setDraft("an answer")
	m.aslResponse.inFlight = true

	updated, cmd := m.submitASLResponse(m.aslResponse.buttons[0])
	if cmd != nil {
		t.Fatal("a second submission was dispatched while one was in flight")
	}
	if updated.(Model).aslResponse.actionSeq != m.aslResponse.actionSeq {
		t.Fatal("the refused submission still advanced the action sequence")
	}
}

func TestASLResponseRetryReusesTheOperationIDForTheSameCall(t *testing.T) {
	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-reply")
	m := d.Model()
	updated, _ := m.openASLResponse()
	m = updated.(Model)
	m.aslResponse.setDraft("an answer")

	updated, _ = m.submitASLResponse(m.aslResponse.buttons[0])
	m = updated.(Model)
	first := m.aslResponse.pending.operationID
	if first == "" {
		t.Fatal("the first submission minted no operation ID")
	}

	// A timeout leaves the outcome unknown; the form must not retry on its own.
	m.aslResponse.inFlight = false
	updated, _ = m.submitASLResponse(m.aslResponse.buttons[0])
	m = updated.(Model)
	if m.aslResponse.pending.operationID != first {
		t.Fatal("a manual retry of the same call minted a new operation ID")
	}

	// Choosing a different closure is a different call and must not replay the
	// earlier receipt.
	m.aslResponse.inFlight = false
	updated, _ = m.submitASLResponse(m.aslResponse.buttons[1])
	if got := updated.(Model).aslResponse.pending.operationID; got == first {
		t.Fatal("a different closure reused the reply's operation ID")
	}
}

func TestASLResponseStaleTargetInvalidatesTheFormAndRefreshes(t *testing.T) {
	fx := aslRunsFixture()
	fx.ASLActionOutcomes = map[string]UITestASLOutcome{
		aslbridge.MethodQuestionReply: {ErrorCode: "stale-action-token"},
	}
	d, _ := NewUITestFixtureDriver(fx, UITestSize{})
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Key(keyASLRuns); err != nil {
		t.Fatalf("entering Runs: %v", err)
	}
	selectASLRun(t, d, "row-reply")
	if err := d.Key(keyASLRespond); err != nil {
		t.Fatalf("opening the form: %v", err)
	}
	typeASLAnswer(t, d, "answer")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting: %v", err)
	}

	m := d.Model()
	if m.mode != ModeASLRuns || m.aslResponse.active {
		t.Fatalf("a stale target left the form open: mode=%v active=%v", m.mode, m.aslResponse.active)
	}
	if !strings.Contains(m.aslNotice, "changed") {
		t.Fatalf("notice = %q, want a changed-target explanation", m.aslNotice)
	}
	if got := len(d.ASLActions()); got != 1 {
		t.Fatalf("submitted %d actions, want exactly 1 with no automatic retry", got)
	}
}

func TestASLResponseBridgeRefusalNeverEchoesBridgeText(t *testing.T) {
	fx := aslRunsFixture()
	fx.ASLActionOutcomes = map[string]UITestASLOutcome{
		aslbridge.MethodQuestionReply: {ErrorCode: "mutation-failed"},
	}
	d, _ := NewUITestFixtureDriver(fx, UITestSize{})
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Key(keyASLRuns); err != nil {
		t.Fatalf("entering Runs: %v", err)
	}
	selectASLRun(t, d, "row-reply")
	if err := d.Key(keyASLRespond); err != nil {
		t.Fatalf("opening the form: %v", err)
	}
	typeASLAnswer(t, d, "answer")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	m := d.Model()
	if !m.aslResponse.active {
		t.Fatal("a non-stale refusal closed the form")
	}
	if m.aslResponse.status != aslOpFailed {
		t.Fatalf("status = %q, want %q", m.aslResponse.status, aslOpFailed)
	}
	if strings.Contains(m.aslResponse.notice, "fixture refusal") {
		t.Fatalf("the bridge's own message was echoed: %q", m.aslResponse.notice)
	}
}

func TestASLResponseVanishedQuestionIsRefusedOnFetch(t *testing.T) {
	fx := aslRunsFixture()
	delete(fx.ASLQuestionBodies, "q-api-1")
	d, _ := NewUITestFixtureDriver(fx, UITestSize{})
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Key(keyASLRuns); err != nil {
		t.Fatalf("entering Runs: %v", err)
	}
	selectASLRun(t, d, "row-reply")
	if err := d.Key(keyASLRespond); err != nil {
		t.Fatalf("opening the form: %v", err)
	}
	m := d.Model()
	if m.aslResponse.active {
		t.Fatal("a vanished question left the form open")
	}
	if !strings.Contains(m.aslNotice, "changed") {
		t.Fatalf("notice = %q, want a changed-target explanation", m.aslNotice)
	}
}

func TestASLResponseDisconnectedBridgeOffersNoActions(t *testing.T) {
	fx := aslRunsFixture()
	fx.ASLStatus.MutationsEnabled = false
	d, _ := NewUITestFixtureDriver(fx, UITestSize{})
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Key(keyASLRuns); err != nil {
		t.Fatalf("entering Runs: %v", err)
	}
	selectASLRun(t, d, "row-reply")
	for _, key := range []string{keyASLRespond, keyASLStop, keyASLAbort} {
		if err := d.Key(key); err != nil {
			t.Fatalf("pressing %q: %v", key, err)
		}
		if got := d.Model().mode; got != ModeASLRuns {
			t.Fatalf("%q left mode at %v with mutations disabled", key, got)
		}
	}
	if !strings.Contains(d.Model().aslNotice, "disabled") {
		t.Fatalf("notice = %q, want the disabled-mutations explanation", d.Model().aslNotice)
	}
	if actions := d.ASLActions(); len(actions) != 0 {
		t.Fatalf("a disabled bridge still received %+v", actions)
	}
}

func TestASLResponseCachedRowsAreReadOnly(t *testing.T) {
	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-reply")
	m := d.Model()
	m.aslMeta.Stale = true
	if view := aslView(m); view != aslViewStale {
		t.Fatalf("view = %v, want aslViewStale", view)
	}
	row, _ := m.selectedASLRow()
	for _, method := range []string{
		aslbridge.MethodQuestionReply,
		aslbridge.MethodRunStopAfterCurrent,
		aslbridge.MethodRunAbort,
	} {
		allowed, reason := aslActionAllowed(m, row, method)
		if allowed {
			t.Fatalf("%s was allowed against a cached row", method)
		}
		if !strings.Contains(reason, "read-only") {
			t.Fatalf("reason for %s = %q, want a read-only explanation", method, reason)
		}
	}
}

func TestASLActionsFollowProjectedCapabilities(t *testing.T) {
	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-verify")
	m := d.Model()
	row, _ := m.selectedASLRow()

	if allowed, _ := aslActionAllowed(m, row, aslbridge.MethodRunStopAfterCurrent); !allowed {
		t.Fatal("a permitted stop was refused")
	}
	allowed, reason := aslActionAllowed(m, row, aslbridge.MethodRunAbort)
	if allowed {
		t.Fatal("abort was allowed on a run that does not permit it")
	}
	if !strings.Contains(reason, "no in-flight turn") {
		t.Fatalf("reason = %q, want the row's own disabled reason", reason)
	}

	// The footer must offer exactly what the handlers allow.
	if aslAbortAvailable(m) {
		t.Fatal("the footer offered abort on a run that refuses it")
	}
	if !aslStopAvailable(m) {
		t.Fatal("the footer hid a permitted stop")
	}
}

func TestASLStopAndAbortConfirmBeforeSubmitting(t *testing.T) {
	for _, tc := range []struct {
		key         string
		action      string
		method      string
		consequence string
	}{
		{keyASLStop, aslConfirmStop, aslbridge.MethodRunStopAfterCurrent, "runs to completion"},
		{keyASLAbort, aslConfirmAbort, aslbridge.MethodRunAbort, "120 seconds"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			d, _ := enterASLRunsDriver(t)
			selectASLRun(t, d, "row-reply")
			if err := d.Key(tc.key); err != nil {
				t.Fatalf("pressing %q: %v", tc.key, err)
			}
			m := d.Model()
			if m.mode != ModeConfirm || m.confirmAction != tc.action {
				t.Fatalf("mode=%v action=%q, want ModeConfirm/%s", m.mode, m.confirmAction, tc.action)
			}
			if m.aslActionTarget == nil || m.aslActionTarget.RowID != "row-reply" {
				t.Fatalf("capture = %+v, want row-reply", m.aslActionTarget)
			}
			if !strings.Contains(m.confirmConsequence, tc.consequence) {
				t.Fatalf("consequence = %q, want it to mention %q", m.confirmConsequence, tc.consequence)
			}
			if actions := d.ASLActions(); len(actions) != 0 {
				t.Fatalf("the confirmation already submitted %+v", actions)
			}

			if err := d.Key(keyY); err != nil {
				t.Fatalf("confirming: %v", err)
			}
			actions := d.ASLActions()
			if len(actions) != 1 || actions[0].Method != tc.method {
				t.Fatalf("actions = %+v, want one %s", actions, tc.method)
			}
			if d.Model().aslActionTarget != nil {
				t.Fatal("the capture survived its submission")
			}
			if d.Model().mode != ModeASLRuns {
				t.Fatalf("mode after confirming = %v, want ModeASLRuns", d.Model().mode)
			}
		})
	}
}

func TestASLControlCancellationSubmitsNothingAndDropsTheCapture(t *testing.T) {
	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-reply")
	if err := d.Key(keyASLStop); err != nil {
		t.Fatalf("pressing %q: %v", keyASLStop, err)
	}
	if err := d.Key(keyN); err != nil {
		t.Fatalf("declining: %v", err)
	}
	m := d.Model()
	if m.aslActionTarget != nil {
		t.Fatal("declining kept the capture")
	}
	if m.mode != ModeASLRuns {
		t.Fatalf("mode after declining = %v, want ModeASLRuns", m.mode)
	}
	if actions := d.ASLActions(); len(actions) != 0 {
		t.Fatalf("declining submitted %+v", actions)
	}
}

func TestASLConfirmationKeepsItsCaptureWhenTheListRefreshes(t *testing.T) {
	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-reply")
	if err := d.Key(keyASLStop); err != nil {
		t.Fatalf("pressing %q: %v", keyASLStop, err)
	}

	// A snapshot landing under the overlay drops the confirmed run entirely.
	m := d.Model()
	remaining := make([]aslbridge.Row, 0, len(m.aslRows))
	for _, row := range m.aslRows {
		if row.RowID != "row-reply" {
			remaining = append(remaining, row)
		}
	}
	d.Inject(aslSnapshotLoadedMsg{
		seq:      d.Model().aslRefreshSeq,
		snapshot: aslbridge.Snapshot{Rows: remaining},
		status:   aslRunsFixture().ASLStatus,
	})
	if target := d.Model().aslActionTarget; target == nil || target.RowID != "row-reply" {
		t.Fatalf("capture after a refresh = %+v, want row-reply", target)
	}
	if got := aslConfirmationTitle(d.Model().aslActionTarget); got != "api-refactor" {
		t.Fatalf("confirmation title = %q, want the captured label", got)
	}
}

func TestASLOperationReceiptsAdvanceAfterTheFormCloses(t *testing.T) {
	d := openASLResponseDriver(t, "row-paused")
	typeASLAnswer(t, d, "resume please")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	m := d.Model()
	if m.aslResponse.active {
		t.Fatal("the paused form stayed open after a resume")
	}
	if len(m.aslOperations) != 1 || m.aslOperations[0].State != aslOpAccepted {
		t.Fatalf("operations = %+v, want one accepted receipt", m.aslOperations)
	}
	operationID := m.aslOperations[0].OperationID

	d.Inject(aslSnapshotLoadedMsg{
		seq:      d.Model().aslRefreshSeq,
		snapshot: aslbridge.Snapshot{Rows: d.Model().aslRows},
		status:   aslRunsFixture().ASLStatus,
		receipts: []aslbridge.Operation{{OperationID: operationID, Status: aslbridge.OperationConsumed}},
	})
	ops := d.Model().aslOperations
	if len(ops) != 1 || ops[0].State != aslOpConsumed {
		t.Fatalf("operations after the receipt = %+v, want consumed", ops)
	}
	assertNoASLTextLeak(t, d, "resume please")
}

func TestASLOperationHistoryStaysBounded(t *testing.T) {
	var m Model
	for i := 0; i < aslOperationHistory+3; i++ {
		m.trackASLOperation(aslOperationView{
			OperationID: fmt.Sprintf("op-%d", i),
			Method:      aslbridge.MethodQuestionReply,
			State:       aslOpSubmitting,
		})
	}
	if len(m.aslOperations) != aslOperationHistory {
		t.Fatalf("tracked %d operations, want %d", len(m.aslOperations), aslOperationHistory)
	}
	if m.aslOperations[0].OperationID != "op-3" {
		t.Fatalf("oldest retained operation = %q, want op-3", m.aslOperations[0].OperationID)
	}
}

// A refusal the TUI does not recognize falls through to the generic "outcome is
// unknown" sentence, which is correct for a genuinely unknown outcome and wrong
// for a code the bridge already explains. Auditing against the shared vocabulary
// makes a bridge that grows a code fail here rather than silently degrade.
func TestASLRefusalTextCoversEveryDeclaredErrorCode(t *testing.T) {
	generic := aslRefusalText(errors.New("some-code-that-does-not-exist: detail"))

	for _, code := range aslbridge.ErrorCodes {
		if aslStaleCodes[code] == aslErrorCodes[code] {
			t.Errorf("%s: must be classified as exactly one of stale or error", code)
			continue
		}
		err := errors.New(code + ": drop the v1 column before the backfill lands")
		if got := aslErrorCode(err); got != code {
			t.Errorf("%s: recovered code = %q", code, got)
		}
		text := aslRefusalText(err)
		if strings.Contains(text, "backfill") {
			t.Errorf("%s: refusal text repeats the bridge message: %q", code, text)
		}
		if aslStaleCodes[code] {
			if !aslStaleTarget(err) {
				t.Errorf("%s: stale code is not treated as a moved target", code)
			}
			continue
		}
		if text == generic {
			t.Errorf("%s: falls through to the generic unknown-outcome sentence", code)
		}
	}

	declared := make(map[string]bool, len(aslbridge.ErrorCodes))
	for _, code := range aslbridge.ErrorCodes {
		declared[code] = true
	}
	for _, mapped := range []map[string]bool{aslStaleCodes, aslErrorCodes} {
		for code := range mapped {
			if !declared[code] {
				t.Errorf("%s: mapped here but absent from aslbridge.ErrorCodes", code)
			}
		}
	}
}

func TestASLResponseFitsNarrowTerminals(t *testing.T) {
	for _, size := range []UITestSize{{Width: 60, Height: 20}, {Width: 80, Height: 24}, {Width: 132, Height: 34}} {
		d, _ := NewUITestFixtureDriver(aslRunsFixture(), size)
		if err := d.Key(keyASLRuns); err != nil {
			t.Fatalf("entering Runs at %dx%d: %v", size.Width, size.Height, err)
		}
		selectASLRun(t, d, "row-reply")
		if err := d.Key(keyASLRespond); err != nil {
			t.Fatalf("opening the form at %dx%d: %v", size.Width, size.Height, err)
		}
		if err := d.Text("an answer that is long enough to need wrapping on a narrow terminal"); err != nil {
			t.Fatalf("typing at %dx%d: %v", size.Width, size.Height, err)
		}
		if issues := d.Observe().Screen.Issues; len(issues) != 0 {
			t.Fatalf("layout issues at %dx%d: %+v", size.Width, size.Height, issues)
		}
		_ = d.Close()
	}
}
