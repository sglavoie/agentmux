package sessionactions

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// TestStaleTargetsAreRefused is the revalidation contract. Each case is a way
// the row stops being the row that was chosen, and none of them may be answered
// by retargeting whatever is at the old address.
func TestStaleTargetsAreRefused(t *testing.T) {
	cases := []struct {
		name string
		live []session.RunningSession
		want string
	}{
		{
			name: "gone",
			live: nil,
			want: ReasonSessionGone,
		},
		{
			name: "replaced",
			live: []session.RunningSession{func() session.RunningSession {
				replaced := row(11, 21)
				replaced.HarnessSessionID = "sess-2"
				replaced.LaunchID = "launch-2"
				return replaced
			}()},
			want: ReasonSessionReplaced,
		},
		{
			name: "newly locked",
			live: []session.RunningSession{func() session.RunningSession {
				locked := row(11, 21)
				locked.Locked = true
				return locked
			}()},
			want: commands.ReasonLocked,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := Capture(row(11, 21))
			h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
			c := testController(hostEffects(commands.CloseSession))

			// The confirmation is where staleness is most likely: the operator has
			// been sitting in a modal while the list moved underneath it. The row is
			// therefore healthy when the action is chosen and stale only by the time
			// it is answered, which is the sequence a refresh actually produces.
			h, step, _ := c.Begin(h, commands.CloseSession, target, Origin{Surface: SurfaceMenu})
			if step.Kind != StepConfirm {
				t.Fatalf("setup: kind = %q, want %q (detail %q)", step.Kind, StepConfirm, step.Detail)
			}
			h.live = tc.live
			out, step, cmd := c.Confirm(h)

			if step.Kind != StepRefused {
				t.Fatalf("kind = %q, want %q", step.Kind, StepRefused)
			}
			if step.Detail != tc.want {
				t.Fatalf("detail = %q, want %q", step.Detail, tc.want)
			}
			if cmd != nil {
				t.Fatal("a refusal produced a command")
			}
			if len(out.ran) != 0 {
				t.Fatalf("ran = %+v, want nothing dispatched against a stale row", out.ran)
			}
			if out.state.Busy() {
				t.Fatal("controller is still busy after a refusal")
			}
			// A refusal is an answer, so it dismisses rather than restoring: the
			// operator is not put back in a menu they would only have to leave.
			if out.dismissals != 1 || len(out.restores) != 0 {
				t.Fatalf("dismissals = %d, restores = %+v, want a dismissal and no restore", out.dismissals, out.restores)
			}
		})
	}
}

// TestStaleTargetIsRefusedAtTheProfilePickerToo pins the other modal the
// operator can sit in. The picker asked once when it opened; what matters is
// whether the answer is still true when they choose.
func TestStaleTargetIsRefusedAtTheProfilePickerToo(t *testing.T) {
	target := Capture(row(11, 21))
	h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.OverrideProfile))

	h, _, _ = c.Begin(h, commands.OverrideProfile, target, Origin{Surface: SurfaceMenu})
	h.live = nil
	out, step, _ := c.ChooseProfile(h, "personal")

	if step.Kind != StepRefused || step.Detail != ReasonSessionGone {
		t.Fatalf("step = %+v, want a refusal naming %q", step, ReasonSessionGone)
	}
	if len(out.ran) != 0 {
		t.Fatalf("ran = %+v, want nothing", out.ran)
	}
}

// TestActionWithNoEffectIsRefused is the programming-error path. A declared
// action the host supplied no effect for must be refused rather than silently
// dropped, because a silent drop reads to the operator as a keystroke that did
// nothing.
func TestActionWithNoEffectIsRefused(t *testing.T) {
	target := Capture(row(11, 21))
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects())

	out, step, _ := c.Begin(h, commands.FocusSession, target, Origin{})

	if step.Kind != StepRefused || step.Detail != ReasonNoEffect {
		t.Fatalf("step = %+v, want a refusal naming %q", step, ReasonNoEffect)
	}
	if out.state.Busy() {
		t.Fatal("controller is still busy after a refusal")
	}
}

// TestUnavailableActionIsRefusedBeforeItStarts covers the surface's own verdict
// being re-asked at choice time: a row that was already locked when the action
// was chosen never reaches a confirmation.
func TestUnavailableActionIsRefusedBeforeItStarts(t *testing.T) {
	locked := row(11, 21)
	locked.Locked = true
	h := host{env: confirmingEnv(), live: []session.RunningSession{locked}}
	c := testController(hostEffects(commands.CloseSession))

	out, step, _ := c.Begin(h, commands.CloseSession, Capture(locked), Origin{Surface: SurfaceMenu})

	if step.Kind != StepRefused || step.Detail != commands.ReasonLocked {
		t.Fatalf("step = %+v, want a refusal naming %q", step, commands.ReasonLocked)
	}
	if out.state.Busy() {
		t.Fatal("controller is still busy after a refusal")
	}
}

// The three tests below are about a lock that CHANGES between the moment a row
// was captured and the moment the action runs, which the availability matrix
// cannot cover: every case there holds one lock state still. A lock is the one
// guard an operator is expected to apply from somewhere else — another window,
// another surface — while a menu or a confirmation is sitting open here, so
// which of the two readings decides is worth pinning rather than inferring from
// validate's shape.
//
// The answer is that BOTH must permit, in both directions, which is why
// SameSession deliberately ignores the lock: a lock change is not a different
// session, so treating it as one would refuse with the wrong sentence.

// TestALockTakenAfterCaptureIsCaughtAtTheLiveRow is the case the captured
// snapshot cannot see: the row was unlocked when the operator chose the action,
// so the surface drew the entry available and the captured verdict passes.
// Only the second, live reading refuses it.
func TestALockTakenAfterCaptureIsCaughtAtTheLiveRow(t *testing.T) {
	captured := Capture(row(11, 21))
	lockedSince := row(11, 21)
	lockedSince.Locked = true
	h := host{env: quietEnv(), live: []session.RunningSession{lockedSince}}
	c := testController(hostEffects(commands.RestartSession))

	out, step, cmd := c.Begin(h, commands.RestartSession, captured, Origin{Surface: SurfaceMenu})

	if step.Kind != StepRefused || step.Detail != commands.ReasonLocked {
		t.Fatalf("step = %+v, want a refusal naming %q", step, commands.ReasonLocked)
	}
	if cmd != nil || len(out.ran) != 0 {
		t.Fatalf("ran %d effects with cmd %v; want a refusal to dispatch nothing", len(out.ran), cmd != nil)
	}
}

// TestALockTakenUnderAConfirmationIsCaughtBeforeDispatch is the same change
// arriving later still. The confirmation is a surface the operator sits in, so
// the whole verdict is re-asked when they answer it rather than trusting the
// one that raised the prompt.
func TestALockTakenUnderAConfirmationIsCaughtBeforeDispatch(t *testing.T) {
	captured := Capture(row(11, 21))
	h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.CloseSession))

	h, step, _ := c.Begin(h, commands.CloseSession, captured, Origin{Surface: SurfaceMenu})
	if step.Kind != StepConfirm {
		t.Fatalf("step = %+v, want the confirmation to be raised first", step)
	}

	lockedSince := row(11, 21)
	lockedSince.Locked = true
	h.live = []session.RunningSession{lockedSince}

	out, step, cmd := c.Confirm(h)

	if step.Kind != StepRefused || step.Detail != commands.ReasonLocked {
		t.Fatalf("step = %+v, want a refusal naming %q", step, commands.ReasonLocked)
	}
	if cmd != nil || len(out.ran) != 0 {
		t.Fatalf("ran %d effects with cmd %v; want the yes to close nothing", len(out.ran), cmd != nil)
	}
	if out.state.Busy() {
		t.Fatal("controller is still busy after a refusal")
	}
}

// TestALockReleasedAfterCaptureStillRefuses is the other direction, and it fails
// closed on purpose: the captured verdict is asked first, so a row the operator
// saw as locked stays refused even if the lock has since been dropped. The
// refusal costs one redraw and a second keypress; honoring the stale capture the
// other way would restart a session someone locked and has not yet seen
// unlocked.
func TestALockReleasedAfterCaptureStillRefuses(t *testing.T) {
	locked := row(11, 21)
	locked.Locked = true
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.RestartSession))

	out, step, cmd := c.Begin(h, commands.RestartSession, Capture(locked), Origin{Surface: SurfaceMenu})

	if step.Kind != StepRefused || step.Detail != commands.ReasonLocked {
		t.Fatalf("step = %+v, want a refusal naming %q", step, commands.ReasonLocked)
	}
	if cmd != nil || len(out.ran) != 0 {
		t.Fatalf("ran %d effects with cmd %v; want a refusal to dispatch nothing", len(out.ran), cmd != nil)
	}
}

// TestBeginWithUnknownActionIsNoOp keeps an identity the shared vocabulary does
// not declare from being dispatched by a host that happens to have an effect
// under that key.
func TestBeginWithUnknownActionIsNoOp(t *testing.T) {
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects("not-an-action"))

	out, step, cmd := c.Begin(h, "not-an-action", Capture(row(11, 21)), Origin{})

	if step.Kind != StepNone {
		t.Fatalf("kind = %q, want %q", step.Kind, StepNone)
	}
	if cmd != nil || len(out.ran) != 0 {
		t.Fatalf("cmd = %v, ran = %+v, want neither", cmd, out.ran)
	}
}
