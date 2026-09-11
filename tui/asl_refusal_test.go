package tui

// Refusals: what the Runs view does when the bridge declines an action, when the
// run moves under a modal, and when the snapshot on screen is read-only. The
// vocabulary under test lives in tui/asl.

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/tui/asl"
	"github.com/sglavoie/agentmux/tui/commands"
)

func TestASLResponseStaleTargetInvalidatesTheFormAndRefreshes(t *testing.T) {
	fx := aslRunsFixture()
	fx.ASLActionOutcomes = map[string]uitestASLOutcome{
		aslbridge.MethodQuestionReply: {ErrorCode: "stale-action-token"},
	}
	d, _ := newUITestFixtureDriver(fx, UITestSize{})
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Command(commands.ShowRuns); err != nil {
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
	if m.mode != ModeASLRuns || m.aslResponse.Active {
		t.Fatalf("a stale target left the form open: mode=%v active=%v", m.mode, m.aslResponse.Active)
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
	fx.ASLActionOutcomes = map[string]uitestASLOutcome{
		aslbridge.MethodQuestionReply: {ErrorCode: "mutation-failed"},
	}
	d, _ := newUITestFixtureDriver(fx, UITestSize{})
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Command(commands.ShowRuns); err != nil {
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
	if !m.aslResponse.Active {
		t.Fatal("a non-stale refusal closed the form")
	}
	if m.aslResponse.Status != asl.OpFailed {
		t.Fatalf("status = %q, want %q", m.aslResponse.Status, asl.OpFailed)
	}
	if strings.Contains(m.aslResponse.Notice, "fixture refusal") {
		t.Fatalf("the bridge's own message was echoed: %q", m.aslResponse.Notice)
	}
}

func TestASLResponseVanishedQuestionIsRefusedOnFetch(t *testing.T) {
	fx := aslRunsFixture()
	delete(fx.ASLQuestionBodies, "q-api-1")
	d, _ := newUITestFixtureDriver(fx, UITestSize{})
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Command(commands.ShowRuns); err != nil {
		t.Fatalf("entering Runs: %v", err)
	}
	selectASLRun(t, d, "row-reply")
	if err := d.Key(keyASLRespond); err != nil {
		t.Fatalf("opening the form: %v", err)
	}
	m := d.Model()
	if m.aslResponse.Active {
		t.Fatal("a vanished question left the form open")
	}
	if !strings.Contains(m.aslNotice, "changed") {
		t.Fatalf("notice = %q, want a changed-target explanation", m.aslNotice)
	}
}

func TestASLResponseDisconnectedBridgeOffersNoActions(t *testing.T) {
	fx := aslRunsFixture()
	fx.ASLStatus.MutationsEnabled = false
	d, _ := newUITestFixtureDriver(fx, UITestSize{})
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Command(commands.ShowRuns); err != nil {
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
	if view := aslView(m); view != asl.ViewStale {
		t.Fatalf("view = %v, want asl.ViewStale", view)
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
