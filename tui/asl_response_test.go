package tui

// Form flow: opening the response form against a captured target, editing and
// navigating drafts, and submitting them. Refusal handling lives in
// asl_refusal_test.go and the control commands in asl_control_test.go.

import (
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
