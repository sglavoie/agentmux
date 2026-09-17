package sessionactions

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

func TestRequestIDValidity(t *testing.T) {
	var zero RequestID
	if zero.Valid() {
		t.Error("the zero request ID names a dispatch")
	}
	if !RequestID(1).Valid() {
		t.Error("a minted request ID reports itself invalid")
	}
}

// TestResultCarriesActionRequestAndTarget is the contract a surface needs to
// route a late reply: which action, which dispatch, and which row — all read
// off the result rather than held in the surface's own state.
func TestResultCarriesActionRequestAndTarget(t *testing.T) {
	row := session.RunningSession{TabID: 4, WindowID: 44, Project: "api"}
	result := Result{
		Action:  commands.CloseSession,
		Request: RequestID(9),
		Target:  Capture(row),
		Outcome: OutcomeSucceeded,
	}
	if !result.Succeeded() {
		t.Fatal("a succeeded result does not report success")
	}
	if result.Action != commands.CloseSession {
		t.Errorf("action = %q", result.Action)
	}
	if !result.Request.Valid() {
		t.Error("result carries no dispatch identity")
	}
	if result.Target.WindowID() != 44 {
		t.Errorf("target window = %d, want 44", result.Target.WindowID())
	}
}

// TestNonSuccessOutcomesAreDistinct keeps the four outcomes apart. Refused and
// failed in particular must not collapse: one is the dashboard declining to
// try, the other an attempt that reached Kitty and did not work.
func TestNonSuccessOutcomesAreDistinct(t *testing.T) {
	refused := Result{Action: commands.CloseSession, Outcome: OutcomeRefused, Detail: commands.ReasonLocked}
	failed := Result{Action: commands.CloseSession, Outcome: OutcomeFailed, Detail: "kitty refused", Err: errors.New("closed")}
	cancelled := Result{Action: commands.OverrideProfile, Outcome: OutcomeCancelled}

	for _, result := range []Result{refused, failed, cancelled, {}} {
		if result.Succeeded() {
			t.Errorf("%+v reports success", result)
		}
	}
	if refused.Outcome == failed.Outcome || failed.Outcome == cancelled.Outcome {
		t.Fatal("outcomes collapsed into one another")
	}
	if refused.Err != nil {
		t.Error("a refusal carries an error")
	}
	if failed.Err == nil {
		t.Error("a failure carries no error")
	}
}
