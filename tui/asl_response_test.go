package tui

// Form flow: opening the response form against a captured target, editing and
// navigating drafts, and submitting them. Refusal handling lives in
// asl_refusal_test.go and the control commands in asl_control_test.go.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/tui/asl"
	"github.com/sglavoie/agentmux/tui/commands"
)

// openASLResponseDriver enters the Runs view, selects a run by ID, and opens its
// response form.
func openASLResponseDriver(t *testing.T, rowID string) *uitestDriver {
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

func typeASLAnswer(t *testing.T, d *uitestDriver, text string) {
	t.Helper()
	if err := d.Text(text); err != nil {
		t.Fatalf("typing the answer: %v", err)
	}
}

// assertNoASLTextLeak checks the surfaces an answer must never reach: the
// rendered screen, the model's own formatting, and the observation a scenario
// serializes.
func assertNoASLTextLeak(t *testing.T, d *uitestDriver, text string) {
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
	t.Parallel()

	d := openASLResponseDriver(t, "row-reply")
	m := d.Model()
	if m.aslResponse.Kind != asl.ResponseLive {
		t.Fatalf("kind = %v, want asl.ResponseLive", m.aslResponse.Kind)
	}
	if m.aslResponse.Target.ActionToken != "token-reply" || m.aslResponse.Target.QuestionID != "q-api-1" {
		t.Fatalf("capture = %+v, want the row's token and first question", m.aslResponse.Target)
	}
	if m.aslResponse.Body.Reveal() == "" {
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
	t.Parallel()

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
			button, ok := state.Button()
			if !ok || button.Resolution != tc.resolution {
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
	t.Parallel()

	d := openASLResponseDriver(t, "row-paused")
	m := d.Model()
	if m.aslResponse.Kind != asl.ResponsePaused {
		t.Fatalf("kind = %v, want asl.ResponsePaused", m.aslResponse.Kind)
	}
	if len(m.aslResponse.Buttons) != 1 || m.aslResponse.Buttons[0].Method != aslbridge.MethodRunResume {
		t.Fatalf("buttons = %+v, want a single run.resume", m.aslResponse.Buttons)
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
	if d.Model().aslResponse.Active {
		t.Fatal("the form survived a resume")
	}
}

func TestASLResponseNavigatesQuestionsAndFetchesEachBodySeparately(t *testing.T) {
	t.Parallel()

	d := openASLResponseDriver(t, "row-reply")
	first := d.Model().aslResponse.Body.Reveal()
	typeASLAnswer(t, d, "draft for one")

	if err := d.Key(keyCtrlN); err != nil {
		t.Fatalf("moving to the next question: %v", err)
	}
	m := d.Model()
	if m.aslResponse.Target.QuestionID != "q-api-2" {
		t.Fatalf("question = %q, want q-api-2", m.aslResponse.Target.QuestionID)
	}
	if body := m.aslResponse.Body.Reveal(); body == first || body == "" {
		t.Fatalf("the second question reused the first body: %q", body)
	}
	if m.aslResponse.Target.ActionToken != "token-reply" {
		t.Fatalf("navigation rewrote the captured token to %q", m.aslResponse.Target.ActionToken)
	}
	if got := m.aslResponse.Drafts["q-api-1"].Reveal(); got != "draft for one" {
		t.Fatalf("draft for the first question = %q, want it kept", got)
	}
	if got := m.aslResponse.Drafts[m.aslResponse.Target.QuestionID].Reveal(); got != "" {
		t.Fatalf("second question opened with draft %q, want empty", got)
	}
}

func TestASLResponseAdvancesToTheRemainingQuestionAfterClosure(t *testing.T) {
	t.Parallel()

	d := openASLResponseDriver(t, "row-reply")
	typeASLAnswer(t, d, "answered")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting: %v", err)
	}
	m := d.Model()
	if !m.aslResponse.Active {
		t.Fatal("the form closed while a captured question was still open")
	}
	if m.aslResponse.Target.QuestionID != "q-api-2" {
		t.Fatalf("advanced to %q, want q-api-2", m.aslResponse.Target.QuestionID)
	}
	if _, kept := m.aslResponse.Drafts["q-api-1"]; kept {
		t.Fatal("the answered question's draft survived its submission")
	}
}

func TestASLResponseSubmitsTwoQuestionsEndToEnd(t *testing.T) {
	t.Parallel()

	d := openASLResponseDriver(t, "row-reply")
	typeASLAnswer(t, d, "answer for one")
	if err := d.Key(keyCtrlS); err != nil {
		t.Fatalf("submitting the first question: %v", err)
	}

	m := d.Model()
	if !m.aslResponse.Active || m.aslResponse.Target.QuestionID != "q-api-2" {
		t.Fatalf("after the first closure the form was %+v, want q-api-2 open", m.aslResponse)
	}
	if m.aslResponse.Target.ActionToken != "token-reply-2" {
		t.Fatalf("token = %q, want the remaining question's refreshed token", m.aslResponse.Target.ActionToken)
	}
	if m.aslResponse.Reauthorize {
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
	if m.aslNotice != "" || m.aslResponse.Notice != "" {
		t.Fatalf("a closure was refused: list notice %q, form notice %q", m.aslNotice, m.aslResponse.Notice)
	}
	if m.mode != ModeASLRuns || m.aslResponse.Active {
		t.Fatalf("the form survived its last question: mode=%v active=%v", m.mode, m.aslResponse.Active)
	}
}

func TestASLResponseEnterInsertsANewlineAndTabMovesFocus(t *testing.T) {
	t.Parallel()

	d := openASLResponseDriver(t, "row-reply")
	typeASLAnswer(t, d, "one")
	if err := d.Key(keyEnter); err != nil {
		t.Fatalf("pressing enter in the editor: %v", err)
	}
	typeASLAnswer(t, d, "two")
	editor := d.Model().aslResponse
	if got := editor.Draft().Reveal(); got != "one\ntwo" {
		t.Fatalf("draft = %q, want a newline between the lines", got)
	}
	if actions := d.ASLActions(); len(actions) != 0 {
		t.Fatalf("enter submitted the form: %+v", actions)
	}

	if err := d.Key(keyTab); err != nil {
		t.Fatalf("pressing tab: %v", err)
	}
	if !d.Model().aslResponse.FocusButtons {
		t.Fatal("tab did not move focus to the buttons")
	}
	if err := d.Key(keyShiftTab); err != nil {
		t.Fatalf("pressing shift+tab: %v", err)
	}
	if d.Model().aslResponse.FocusButtons {
		t.Fatal("shift+tab did not return focus to the editor")
	}
}

// Esc closes the form and drops its fetched body, but keeps the typed reply
// for that question, the way the send form keeps its drafts: reopening the same
// question restores it, and nothing is submitted on the way.
func TestASLResponseEscapeKeepsTheDraftAndDropsTheBody(t *testing.T) {
	t.Parallel()

	d := openASLResponseDriver(t, "row-reply")
	typeASLAnswer(t, d, "secret answer text")
	if err := d.Key(keyEsc); err != nil {
		t.Fatalf("closing: %v", err)
	}
	m := d.Model()
	if m.mode != ModeASLRuns {
		t.Fatalf("mode after esc = %v, want ModeASLRuns", m.mode)
	}
	if m.aslResponse.Active || len(m.aslResponse.Drafts) != 0 || m.aslResponse.Body != "" {
		t.Fatalf("esc left form state behind: %+v", m.aslResponse)
	}
	if actions := d.ASLActions(); len(actions) != 0 {
		t.Fatalf("esc submitted something: %+v", actions)
	}
	assertNoASLTextLeak(t, d, "secret answer text")

	if err := d.Key(keyASLRespond); err != nil {
		t.Fatalf("reopening: %v", err)
	}
	if state := d.Model().aslResponse; state.Draft().Reveal() != "secret answer text" {
		got := state.Draft().Reveal()
		t.Fatalf("reopened draft = %q, want the kept reply", got)
	}
}

func TestASLResponseRedactsDraftsAndBodiesInFormattedOutput(t *testing.T) {
	t.Parallel()

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
	if got := fmt.Sprintf("%+v", d.Model().aslResponse.Body); got != "[redacted]" {
		t.Fatalf("formatted body = %q, want [redacted]", got)
	}
}

func TestASLResponseDoubleSubmitIsRefusedWhileInFlight(t *testing.T) {
	t.Parallel()

	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-reply")
	m := d.Model()
	updated, _ := m.openASLResponse()
	m = updated.(Model)
	m.aslResponse.SetDraft("an answer")
	m.aslResponse.InFlight = true

	updated, cmd := m.submitASLResponse(m.aslResponse.Buttons[0])
	if cmd != nil {
		t.Fatal("a second submission was dispatched while one was in flight")
	}
	if updated.(Model).aslResponse.ActionSeq != m.aslResponse.ActionSeq {
		t.Fatal("the refused submission still advanced the action sequence")
	}
}

func TestASLResponseRetryReusesTheOperationIDForTheSameCall(t *testing.T) {
	t.Parallel()

	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-reply")
	m := d.Model()
	updated, _ := m.openASLResponse()
	m = updated.(Model)
	m.aslResponse.SetDraft("an answer")

	updated, _ = m.submitASLResponse(m.aslResponse.Buttons[0])
	m = updated.(Model)
	first := m.aslResponse.Pending.OperationID
	if first == "" {
		t.Fatal("the first submission minted no operation ID")
	}

	// A timeout leaves the outcome unknown; the form must not retry on its own.
	m.aslResponse.InFlight = false
	updated, _ = m.submitASLResponse(m.aslResponse.Buttons[0])
	m = updated.(Model)
	if m.aslResponse.Pending.OperationID != first {
		t.Fatal("a manual retry of the same call minted a new operation ID")
	}

	// Choosing a different closure is a different call and must not replay the
	// earlier receipt.
	m.aslResponse.InFlight = false
	updated, _ = m.submitASLResponse(m.aslResponse.Buttons[1])
	if got := updated.(Model).aslResponse.Pending.OperationID; got == first {
		t.Fatal("a different closure reused the reply's operation ID")
	}
}

func TestASLResponseFitsNarrowTerminals(t *testing.T) {
	t.Parallel()

	for _, size := range []UITestSize{{Width: 60, Height: 20}, {Width: 80, Height: 24}, {Width: 132, Height: 34}} {
		d, _ := newUITestFixtureDriver(aslRunsFixture(), size)
		if err := d.Command(commands.ShowRuns); err != nil {
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
