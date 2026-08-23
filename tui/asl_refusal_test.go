package tui

// Refusals: what the Runs view does when the bridge declines an action, when the
// run moves under a modal, and when the snapshot on screen is read-only. The
// vocabulary under test lives in model_asl_refusal.go.

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

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
