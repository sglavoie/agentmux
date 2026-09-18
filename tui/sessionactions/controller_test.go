package sessionactions

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// The controller names no host model, so its tests supply one. host is the
// smallest model the seams can act on: the controller's own state, the facts
// availability reads, the list a captured target is resolved against, and a
// journal of every navigation and effect the controller asked for.
//
// It is a value type for the same reason the real host is one: Bubble Tea copies
// its model on every update, and a controller that held mutable state of its own
// would be stale in whichever copy the host went on to use. The journal fields
// are therefore slices the seams append to and return, not counters a pointer
// increments — recording through a pointer would make the test pass for a
// controller that threw its returned model away.
type host struct {
	state      State
	env        Env
	live       []session.RunningSession
	dismissals int
	restores   []Pending
	ran        []Dispatch
}

// hostEffects wires the seams. Every action named in run gets an effect that
// records its dispatch and returns a command, so a dispatch is observable
// without any real Kitty, harness, or clipboard work.
func hostEffects(run ...commands.ID) Effects[host] {
	effects := Effects[host]{
		Env: func(h host) Env { return h.env },
		Live: func(h host, target Target) (session.RunningSession, bool) {
			if !target.HasSession {
				return session.RunningSession{}, false
			}
			for _, candidate := range h.live {
				if candidate.WindowID == target.WindowID() {
					return candidate, true
				}
			}
			return session.RunningSession{}, false
		},
		Dismiss: func(h host) host {
			h.dismissals++
			return h
		},
		Restore: func(h host, pending Pending) host {
			h.restores = append(append([]Pending(nil), h.restores...), pending)
			return h
		},
		// Stamping preserves the command it was given rather than replacing it,
		// which is what the real host's seam does: the request identity rides
		// along with the work, it does not stand in for it.
		Stamp: func(cmd tea.Cmd, request RequestID) tea.Cmd {
			return tea.Batch(cmd, func() tea.Msg { return request })
		},
		State:    func(h host) State { return h.state },
		SetState: func(h host, state State) host { h.state = state; return h },
		Run:      map[commands.ID]Effect[host]{},
	}
	for _, id := range run {
		action := id
		effects.Run[action] = func(h host, d Dispatch) (host, tea.Cmd) {
			h.ran = append(append([]Dispatch(nil), h.ran...), d)
			return h, func() tea.Msg { return action }
		}
	}
	return effects
}

func testController(effects Effects[host]) Controller[host] {
	return NewController(effects)
}

// row is a listed, unlocked, attributed session: the ordinary target.
func row(windowID, tabID int) session.RunningSession {
	return session.RunningSession{
		WindowID:         windowID,
		TabID:            tabID,
		Base:             "api",
		Account:          "work",
		LaunchID:         "launch-1",
		HarnessSessionID: "sess-1",
	}
}

func confirmingEnv() Env {
	return Env{Config: &config.Config{Defaults: config.Defaults{ConfirmDestructive: true}}}
}

func quietEnv() Env {
	return Env{Config: &config.Config{}}
}

// TestBeginDispatchesDirectAction is the plain path: an action that needs
// neither a profile nor a confirmation runs immediately, leaves the surfaces,
// and leaves nothing pending.
func TestBeginDispatchesDirectAction(t *testing.T) {
	target := Capture(row(11, 21))
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.FocusSession))

	out, step, cmd := c.Begin(h, commands.FocusSession, target, Origin{})

	if step.Kind != StepDispatched {
		t.Fatalf("kind = %q, want %q (detail %q)", step.Kind, StepDispatched, step.Detail)
	}
	if cmd == nil {
		t.Fatal("dispatched action returned no command")
	}
	if len(out.ran) != 1 || out.ran[0].Action != commands.FocusSession {
		t.Fatalf("ran = %+v, want one FocusSession dispatch", out.ran)
	}
	if !reflect.DeepEqual(out.ran[0].Target, target) {
		t.Fatalf("dispatched target = %+v, want the captured %+v", out.ran[0].Target, target)
	}
	if out.state.Busy() {
		t.Fatal("controller is still busy after a direct dispatch")
	}
	if out.dismissals != 1 {
		t.Fatalf("dismissals = %d, want 1", out.dismissals)
	}
	if step.Request != out.ran[0].Request || !step.Request.Valid() {
		t.Fatalf("step request %d does not name the dispatch %d", step.Request, out.ran[0].Request)
	}
}

// TestBeginConfirmsDestructiveAction covers confirm_destructive being honored
// and the wording that comes with it. The prompt is the shared one, not a
// sentence the surface wrote.
func TestBeginConfirmsDestructiveAction(t *testing.T) {
	target := Capture(row(11, 21))
	h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.CloseSession))

	out, step, cmd := c.Begin(h, commands.CloseSession, target, Origin{Surface: SurfaceMenu})

	if step.Kind != StepConfirm {
		t.Fatalf("kind = %q, want %q (detail %q)", step.Kind, StepConfirm, step.Detail)
	}
	if cmd != nil {
		t.Fatal("a confirmation must not dispatch anything yet")
	}
	if step.Prompt.Verb != ConfirmClose || step.Prompt.Consequence != closeConsequence {
		t.Fatalf("prompt = %+v, want the shared close wording", step.Prompt)
	}
	if len(out.ran) != 0 {
		t.Fatalf("ran = %+v, want nothing before the answer", out.ran)
	}
	pending := out.state.Pending()
	if !pending.Active || pending.Stage != StageConfirm {
		t.Fatalf("pending = %+v, want an active confirm stage", pending)
	}
	if out.dismissals != 0 {
		t.Fatalf("dismissals = %d, want the menu left standing behind the prompt", out.dismissals)
	}
}

// TestConfirmDispatchesAndClears is the answered confirmation: the effect runs
// against the captured row, and the pending action is gone.
func TestConfirmDispatchesAndClears(t *testing.T) {
	target := Capture(row(11, 21))
	h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.CloseSession))

	h, _, _ = c.Begin(h, commands.CloseSession, target, Origin{Surface: SurfaceMenu})
	out, step, cmd := c.Confirm(h)

	if step.Kind != StepDispatched {
		t.Fatalf("kind = %q, want %q (detail %q)", step.Kind, StepDispatched, step.Detail)
	}
	if cmd == nil {
		t.Fatal("confirmed action returned no command")
	}
	if len(out.ran) != 1 || out.ran[0].Action != commands.CloseSession {
		t.Fatalf("ran = %+v, want one CloseSession dispatch", out.ran)
	}
	if out.state.Busy() {
		t.Fatal("controller is still busy after the confirmation was answered")
	}
	if out.dismissals != 1 {
		t.Fatalf("dismissals = %d, want 1", out.dismissals)
	}
}

// TestSecondConfirmIsNoOp is the duplicate-dispatch guard. The pending action is
// cleared before the effect runs, so a second yes arriving against the model the
// effect returned finds nothing to dispatch.
func TestSecondConfirmIsNoOp(t *testing.T) {
	target := Capture(row(11, 21))
	h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.CloseSession))

	h, _, _ = c.Begin(h, commands.CloseSession, target, Origin{})
	h, _, _ = c.Confirm(h)
	out, step, cmd := c.Confirm(h)

	if step.Kind != StepNone {
		t.Fatalf("kind = %q, want %q", step.Kind, StepNone)
	}
	if cmd != nil {
		t.Fatal("second confirmation produced a command")
	}
	if len(out.ran) != 1 {
		t.Fatalf("ran = %+v, want the action dispatched exactly once", out.ran)
	}
	if got := out.state.InFlight(); len(got) != 1 {
		t.Fatalf("in flight = %+v, want exactly one record", got)
	}
}

// TestUnconfirmedDestructiveActionDispatchesImmediately is the other half of the
// confirm_destructive rule: with the preference off, the same action runs
// without a prompt. Both surfaces read this one answer, which is the behavior
// the palette used not to have — it confirmed every restart regardless.
func TestUnconfirmedDestructiveActionDispatchesImmediately(t *testing.T) {
	target := Capture(row(11, 21))
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.RestartSession))

	out, step, _ := c.Begin(h, commands.RestartSession, target, Origin{Surface: SurfacePalette})

	if step.Kind != StepDispatched {
		t.Fatalf("kind = %q, want %q (detail %q)", step.Kind, StepDispatched, step.Detail)
	}
	if len(out.ran) != 1 {
		t.Fatalf("ran = %+v, want one dispatch", out.ran)
	}
}

// TestProfileReplacementPicksThenConfirms is the full two-step path: the picker
// comes first so the confirmation can name the profile it is confirming.
func TestProfileReplacementPicksThenConfirms(t *testing.T) {
	target := Capture(row(11, 21))
	h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.OverrideProfile))

	h, step, cmd := c.Begin(h, commands.OverrideProfile, target, Origin{Surface: SurfaceMenu})
	if step.Kind != StepProfile {
		t.Fatalf("kind = %q, want %q (detail %q)", step.Kind, StepProfile, step.Detail)
	}
	if cmd != nil {
		t.Fatal("opening the picker dispatched something")
	}
	if stage := h.state.Pending().Stage; stage != StageProfile {
		t.Fatalf("stage = %q, want %q", stage, StageProfile)
	}

	h, step, cmd = c.ChooseProfile(h, "personal")
	if step.Kind != StepConfirm {
		t.Fatalf("kind = %q, want %q (detail %q)", step.Kind, StepConfirm, step.Detail)
	}
	if cmd != nil {
		t.Fatal("the confirmation dispatched something")
	}
	if step.Prompt.Verb != ConfirmReplaceProfile {
		t.Fatalf("verb = %q, want %q", step.Prompt.Verb, ConfirmReplaceProfile)
	}
	if want := `"personal"`; !strings.Contains(step.Prompt.Consequence, want) {
		t.Fatalf("consequence %q does not name the chosen profile %s", step.Prompt.Consequence, want)
	}

	out, step, _ := c.Confirm(h)
	if step.Kind != StepDispatched {
		t.Fatalf("kind = %q, want %q", step.Kind, StepDispatched)
	}
	if len(out.ran) != 1 || out.ran[0].Profile != "personal" {
		t.Fatalf("ran = %+v, want one dispatch under profile personal", out.ran)
	}
}

// TestChoosingCurrentProfileIsNoOp keeps the picker's long-standing behavior:
// replacing a profile with the one already in use restarts nothing. The match is
// case-insensitive because the profile name is operator-typed configuration.
func TestChoosingCurrentProfileIsNoOp(t *testing.T) {
	target := Capture(row(11, 21))
	h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.OverrideProfile))

	h, _, _ = c.Begin(h, commands.OverrideProfile, target, Origin{Surface: SurfaceMenu})
	out, step, cmd := c.ChooseProfile(h, "WORK")

	if step.Kind != StepNone {
		t.Fatalf("kind = %q, want %q", step.Kind, StepNone)
	}
	if cmd != nil {
		t.Fatal("the no-op produced a command")
	}
	if len(out.ran) != 0 {
		t.Fatalf("ran = %+v, want nothing", out.ran)
	}
	if out.state.Busy() {
		t.Fatal("controller is still busy after the no-op")
	}
	if out.dismissals != 1 {
		t.Fatalf("dismissals = %d, want the picker closed", out.dismissals)
	}
}

// TestLaunchAnotherNeedsProfileButNeverConfirms separates the two reasons an
// action opens the picker. Launching another session needs a profile and ends
// nothing, so the profile answer dispatches directly even with
// confirm_destructive on.
func TestLaunchAnotherNeedsProfileButNeverConfirms(t *testing.T) {
	target := Capture(row(11, 21))
	h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.LaunchAnother))

	h, step, _ := c.Begin(h, commands.LaunchAnother, target, Origin{Surface: SurfaceMenu})
	if step.Kind != StepProfile {
		t.Fatalf("kind = %q, want %q", step.Kind, StepProfile)
	}
	// The same profile the row already runs under is an ordinary request here:
	// the session it launches is new.
	out, step, _ := c.ChooseProfile(h, "work")
	if step.Kind != StepDispatched {
		t.Fatalf("kind = %q, want %q (detail %q)", step.Kind, StepDispatched, step.Detail)
	}
	if len(out.ran) != 1 || out.ran[0].Profile != "work" {
		t.Fatalf("ran = %+v, want one launch under profile work", out.ran)
	}
}

// TestCancelRestoresOrigin is the cancellation contract at both entry points:
// the operator goes back to the surface and the selection they chose the action
// from, not to the bare list.
func TestCancelRestoresOrigin(t *testing.T) {
	cases := []struct {
		name   string
		action commands.ID
		origin Origin
	}{
		{
			name:   "menu confirmation",
			action: commands.CloseSession,
			origin: Origin{Surface: SurfaceMenu, MenuOffset: 3},
		},
		{
			name:   "palette confirmation",
			action: commands.CloseSession,
			origin: Origin{Surface: SurfacePalette, PaletteQuery: "rest", PaletteCursor: 2, PaletteOffset: 1},
		},
		{
			name:   "palette profile picker",
			action: commands.OverrideProfile,
			origin: Origin{Surface: SurfacePalette, PaletteQuery: "prof", PaletteCursor: 4, PaletteOffset: 2},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := Capture(row(11, 21))
			h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
			c := testController(hostEffects(tc.action))

			h, _, _ = c.Begin(h, tc.action, target, tc.origin)
			out, step := c.Cancel(h)

			if step.Kind != StepCancelled {
				t.Fatalf("kind = %q, want %q", step.Kind, StepCancelled)
			}
			if len(out.restores) != 1 {
				t.Fatalf("restores = %+v, want exactly one", out.restores)
			}
			if got := out.restores[0].Origin; got != tc.origin {
				t.Fatalf("restored origin = %+v, want %+v", got, tc.origin)
			}
			if !reflect.DeepEqual(out.restores[0].Target, target) {
				t.Fatalf("restored target = %+v, want the captured %+v", out.restores[0].Target, target)
			}
			if out.state.Busy() {
				t.Fatal("controller is still busy after a cancellation")
			}
			if len(out.ran) != 0 {
				t.Fatalf("ran = %+v, want nothing dispatched by a cancellation", out.ran)
			}
		})
	}
}

// TestCancelNestedProfileThenConfirmRestoresOnce covers the nesting the
// dashboard actually produces: a profile replacement cancelled from its
// confirmation, one step after the picker. There is one pending action, so there
// is one restore, and it names the origin the action was chosen from rather than
// the picker the operator just left.
func TestCancelNestedProfileThenConfirmRestoresOnce(t *testing.T) {
	origin := Origin{Surface: SurfaceMenu, MenuOffset: 5}
	target := Capture(row(11, 21))
	h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.OverrideProfile))

	h, _, _ = c.Begin(h, commands.OverrideProfile, target, origin)
	h, _, _ = c.ChooseProfile(h, "personal")
	out, step := c.Cancel(h)

	if step.Kind != StepCancelled {
		t.Fatalf("kind = %q, want %q", step.Kind, StepCancelled)
	}
	if len(out.restores) != 1 || out.restores[0].Origin != origin {
		t.Fatalf("restores = %+v, want one restore to %+v", out.restores, origin)
	}
	if out.restores[0].Profile != "personal" {
		t.Fatalf("restored profile = %q, want the chosen personal", out.restores[0].Profile)
	}
}

// TestCancelWithNothingPendingIsNoOp is what lets a key handler ask the
// controller first and fall through to its own behavior: the confirm overlay and
// the profile picker are multiplexed with the worktree, handoff, launcher, and
// ASL flows, and an esc that was never the controller's must reach them.
func TestCancelWithNothingPendingIsNoOp(t *testing.T) {
	h := host{env: confirmingEnv()}
	c := testController(hostEffects(commands.CloseSession))

	out, step := c.Cancel(h)

	if step.Kind != StepNone {
		t.Fatalf("kind = %q, want %q", step.Kind, StepNone)
	}
	if len(out.restores) != 0 {
		t.Fatalf("restores = %+v, want nothing touched", out.restores)
	}
}

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

// TestCompleteCorrelatesWithItsDispatch is the correlation contract: a reported
// completion names the dispatch it answers, and the record it clears is that
// dispatch's alone.
func TestCompleteCorrelatesWithItsDispatch(t *testing.T) {
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21), row(12, 22)}}
	c := testController(hostEffects(commands.CloseSession, commands.RestartSession))

	h, first, _ := c.Begin(h, commands.CloseSession, Capture(row(11, 21)), Origin{})
	h, second, _ := c.Begin(h, commands.RestartSession, Capture(row(12, 22)), Origin{})
	if first.Request == second.Request {
		t.Fatalf("both dispatches minted request %d", first.Request)
	}
	if got := h.state.InFlight(); len(got) != 2 {
		t.Fatalf("in flight = %+v, want two records", got)
	}

	h, result, ok := c.Complete(h, second.Request, Completion{})
	if !ok {
		t.Fatal("completion was not correlated")
	}
	if result.Action != commands.RestartSession || result.Request != second.Request {
		t.Fatalf("result = %+v, want the restart dispatch %d", result, second.Request)
	}
	if !result.Succeeded() {
		t.Fatalf("outcome = %q, want %q", result.Outcome, OutcomeSucceeded)
	}
	remaining := h.state.InFlight()
	if len(remaining) != 1 || remaining[0].Request != first.Request {
		t.Fatalf("in flight = %+v, want only the close dispatch %d", remaining, first.Request)
	}

	failure := errors.New("kitty refused")
	h, result, ok = c.Complete(h, first.Request, Completion{Err: failure})
	if !ok {
		t.Fatal("failing completion was not correlated")
	}
	if result.Outcome != OutcomeFailed || !errors.Is(result.Err, failure) || result.Detail != failure.Error() {
		t.Fatalf("result = %+v, want a failure carrying %v", result, failure)
	}
	if got := h.state.InFlight(); len(got) != 0 {
		t.Fatalf("in flight = %+v, want empty", got)
	}
}

// TestUnconfirmedCompletionIsNeitherSuccessNorFailure is the third answer: the
// action was accepted, nothing failed, and its effect was never observed. A
// surface that offered close recovery on this would be offering to undo a close
// that may not have happened.
func TestUnconfirmedCompletionIsNeitherSuccessNorFailure(t *testing.T) {
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.CloseSession))
	h, dispatched, _ := c.Begin(h, commands.CloseSession, Capture(row(11, 21)), Origin{})

	_, result, ok := c.Complete(h, dispatched.Request, Completion{Unconfirmed: true})
	if !ok {
		t.Fatal("unconfirmed completion was not correlated")
	}
	if result.Outcome != OutcomeUnconfirmed {
		t.Fatalf("outcome = %q, want %q", result.Outcome, OutcomeUnconfirmed)
	}
	if result.Succeeded() {
		t.Fatal("an unconfirmed action reported success")
	}
	if result.Err != nil || result.Detail != "" {
		t.Fatalf("result = %+v, want no failure to report", result)
	}

	// A failure is the stronger answer and wins.
	failure := errors.New("kitty refused")
	if got := (Completion{Err: failure, Unconfirmed: true}).Outcome(); got != OutcomeFailed {
		t.Fatalf("outcome = %q, want %q", got, OutcomeFailed)
	}
}

// TestCompleteIgnoresForeignCompletions is what lets the host keep its own
// handling for everything else the same message carries: an undo, a direct key,
// a reply already reported. None of them are this controller's.
func TestCompleteIgnoresForeignCompletions(t *testing.T) {
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.CloseSession))
	h, dispatched, _ := c.Begin(h, commands.CloseSession, Capture(row(11, 21)), Origin{})

	for _, request := range []RequestID{0, dispatched.Request + 1} {
		out, result, ok := c.Complete(h, request, Completion{})
		if ok {
			t.Fatalf("request %d was correlated: %+v", request, result)
		}
		if got := out.state.InFlight(); len(got) != 1 {
			t.Fatalf("request %d disturbed the record: %+v", request, got)
		}
	}

	// The same completion twice is a foreign one the second time, which is what
	// keeps a retried reply from being counted as a second action.
	h, _, ok := c.Complete(h, dispatched.Request, Completion{})
	if !ok {
		t.Fatal("first completion was not correlated")
	}
	if _, _, ok := c.Complete(h, dispatched.Request, Completion{}); ok {
		t.Fatal("the same completion was correlated twice")
	}
}

// TestInFlightRecordIsBounded is the leak guard: an effect that never reports
// back must not grow the state without limit.
func TestInFlightRecordIsBounded(t *testing.T) {
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.FocusSession))

	for i := 0; i < maxInFlight+5; i++ {
		h, _, _ = c.Begin(h, commands.FocusSession, Capture(row(11, 21)), Origin{})
	}

	got := h.state.InFlight()
	if len(got) != maxInFlight {
		t.Fatalf("in flight = %d records, want the bound %d", len(got), maxInFlight)
	}
	// The bound drops the oldest, so the newest dispatch is always correlatable.
	if got[len(got)-1].Request != RequestID(maxInFlight+5) {
		t.Fatalf("newest record = %d, want %d", got[len(got)-1].Request, maxInFlight+5)
	}
}

// TestInFlightIsCopied guards the value-copied host model: two copies must not
// write through one backing array.
func TestInFlightIsCopied(t *testing.T) {
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.FocusSession))
	h, _, _ = c.Begin(h, commands.FocusSession, Capture(row(11, 21)), Origin{})

	snapshot := h.state.InFlight()
	snapshot[0].Action = "tampered"
	if again := h.state.InFlight(); again[0].Action != commands.FocusSession {
		t.Fatalf("state was written through its own read projection: %+v", again)
	}
}

// TestHostHostsOnlyItsOwnTab pins the hosting-tab identity. It is asked of the
// captured target's tab and answers false whenever either side is unknown, so an
// unresolved host cannot claim every row.
func TestHostHostsOnlyItsOwnTab(t *testing.T) {
	target := Capture(row(11, 21))
	cases := []struct {
		name string
		host Host
		want bool
	}{
		{name: "same tab", host: Host{TabID: 21, WindowID: 99}, want: true},
		{name: "other tab", host: Host{TabID: 22, WindowID: 99}, want: false},
		{name: "unresolved host", host: Host{}, want: false},
		{name: "window only", host: Host{WindowID: 11}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.host.Hosts(target); got != tc.want {
				t.Fatalf("Hosts = %v, want %v", got, tc.want)
			}
		})
	}
	if (Host{TabID: 21}).Hosts(Target{}) {
		t.Fatal("a target with no session was claimed by the host tab")
	}
}

// TestConsequencesAreHarnessNeutral is the copy regression. The sentences used
// to name Claude, which was wrong on a Codex or Copilot row and had been since
// the second harness landed.
func TestConsequencesAreHarnessNeutral(t *testing.T) {
	for _, id := range []commands.ID{commands.CloseSession, commands.RestartSession, commands.OverrideProfile} {
		prompt, ok := ConfirmPromptFor(id, "personal")
		if !ok {
			t.Fatalf("%s declares no confirmation prompt", id)
		}
		if prompt.Consequence == "" {
			t.Fatalf("%s confirms with no consequence", id)
		}
		for _, harness := range []string{"Claude", "claude", "Codex", "Copilot"} {
			if strings.Contains(prompt.Consequence, harness) {
				t.Fatalf("%s consequence %q names the harness %q", id, prompt.Consequence, harness)
			}
		}
	}
	if _, ok := ConfirmPromptFor(commands.FocusSession, ""); ok {
		t.Fatal("a non-destructive action declares a confirmation prompt")
	}
}

// TestDestructiveAndNeedsProfileAgree documents the two independent questions
// the picker and the prompt answer, and in particular that launching another
// session needs a profile without being destructive.
func TestDestructiveAndNeedsProfileAgree(t *testing.T) {
	cases := []struct {
		id          commands.ID
		destructive bool
		needsProf   bool
	}{
		{id: commands.CloseSession, destructive: true},
		{id: commands.RestartSession, destructive: true},
		{id: commands.OverrideProfile, destructive: true, needsProf: true},
		{id: commands.LaunchAnother, needsProf: true},
		{id: commands.FocusSession},
		{id: commands.ToggleLockID},
	}
	for _, tc := range cases {
		if got := Destructive(tc.id); got != tc.destructive {
			t.Errorf("Destructive(%s) = %v, want %v", tc.id, got, tc.destructive)
		}
		if got := NeedsProfile(tc.id); got != tc.needsProf {
			t.Errorf("NeedsProfile(%s) = %v, want %v", tc.id, got, tc.needsProf)
		}
	}
}

// TestSameSessionIsConservativeInBothDirections pins the revalidation
// predicate. Identity evidence is used where it exists and ignored where it does
// not, because a row the daemon has not yet attributed carries none and refusing
// every such row would make the dashboard unusable during its first seconds.
func TestSameSessionIsConservativeInBothDirections(t *testing.T) {
	base := row(11, 21)
	cases := []struct {
		name string
		with func(session.RunningSession) session.RunningSession
		want bool
	}{
		{name: "identical", with: func(s session.RunningSession) session.RunningSession { return s }, want: true},
		{name: "different tab", with: func(s session.RunningSession) session.RunningSession { s.TabID = 99; return s }, want: false},
		{name: "different window", with: func(s session.RunningSession) session.RunningSession { s.WindowID = 99; return s }, want: false},
		{name: "unattributed window", with: func(s session.RunningSession) session.RunningSession { s.WindowID = 0; return s }, want: true},
		{name: "different launch", with: func(s session.RunningSession) session.RunningSession { s.LaunchID = "other"; return s }, want: false},
		{name: "unknown launch", with: func(s session.RunningSession) session.RunningSession { s.LaunchID = ""; return s }, want: true},
		{name: "different harness session", with: func(s session.RunningSession) session.RunningSession { s.HarnessSessionID = "other"; return s }, want: false},
		{name: "unknown harness session", with: func(s session.RunningSession) session.RunningSession { s.HarnessSessionID = ""; return s }, want: true},
		{name: "renamed", with: func(s session.RunningSession) session.RunningSession { s.Base = "other"; return s }, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameSession(base, tc.with(base)); got != tc.want {
				t.Fatalf("SameSession = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNilSeamsAreTolerated covers the host that has no surface to leave — the
// overview, or a test driving the controller alone. Every navigation seam is
// optional; the effect table is not.
func TestNilSeamsAreTolerated(t *testing.T) {
	effects := Effects[host]{
		State:    func(h host) State { return h.state },
		SetState: func(h host, state State) host { h.state = state; return h },
		Run: map[commands.ID]Effect[host]{
			commands.FocusSession: func(h host, d Dispatch) (host, tea.Cmd) {
				h.ran = append(append([]Dispatch(nil), h.ran...), d)
				return h, nil
			},
		},
	}
	c := testController(effects)

	out, step, cmd := c.Begin(host{}, commands.FocusSession, Capture(row(11, 21)), Origin{})
	if step.Kind != StepDispatched {
		t.Fatalf("kind = %q, want %q (detail %q)", step.Kind, StepDispatched, step.Detail)
	}
	if cmd != nil {
		t.Fatal("a nil Stamp seam invented a command")
	}
	if len(out.ran) != 1 {
		t.Fatalf("ran = %+v, want one dispatch", out.ran)
	}
}

// TestReviewLifecycleDispatchSingleFlight is the single-flight contract: at most
// one close, restart, or profile replacement may be in flight against one
// captured tab. Every subtest parks the first effect — the fixture's effects
// record a dispatch and never report a completion — because the whole failure is
// what a second keystroke does while the first is still running.
//
// Without the guard the a,r,a,r sequence validates the same unchanged cached row
// twice and runs Restart twice, and a restart launches its replacement before
// closing the original, so one row becomes two.
func TestReviewLifecycleDispatchSingleFlight(t *testing.T) {
	lifecycle := []commands.ID{commands.CloseSession, commands.RestartSession, commands.OverrideProfile}

	// begin runs one lifecycle action to its dispatch, answering whichever
	// surfaces it opens: the picker for a profile replacement, the confirmation
	// when confirm_destructive is on.
	begin := func(t *testing.T, c Controller[host], h host, action commands.ID, target Target) (host, Step) {
		t.Helper()
		h, step, _ := c.Begin(h, action, target, Origin{Surface: SurfaceMenu})
		if step.Kind == StepProfile {
			h, step, _ = c.ChooseProfile(h, "personal")
		}
		if step.Kind == StepConfirm {
			h, step, _ = c.Confirm(h)
		}
		return h, step
	}

	// assertRefusedBusy is the shared verdict: the request was turned away for
	// the in-flight operation, and nothing about it reached an effect. wantRan
	// and wantInFlight are separate because the last subtest plants its in-flight
	// record rather than running it.
	assertRefusedBusy := func(t *testing.T, step Step, h host, wantRan, wantInFlight int) {
		t.Helper()
		if step.Kind != StepRefused {
			t.Fatalf("kind = %q, want %q (detail %q)", step.Kind, StepRefused, step.Detail)
		}
		if step.Detail != ReasonLifecycleInFlight {
			t.Fatalf("detail = %q, want %q", step.Detail, ReasonLifecycleInFlight)
		}
		if len(h.ran) != wantRan {
			t.Fatalf("ran = %+v, want %d dispatch(es)", h.ran, wantRan)
		}
		if got := h.state.InFlight(); len(got) != wantInFlight {
			t.Fatalf("in flight = %+v, want %d record(s): a refusal must not mint a request", got, wantInFlight)
		}
	}
	assertRefusedAsBusy := func(t *testing.T, step Step, h host, wantRan int) {
		t.Helper()
		assertRefusedBusy(t, step, h, wantRan, wantRan)
	}

	// The reported finding, spelled as the keystrokes that produce it: a, r, a,
	// r on one row before the first restart reports back. Both confirmation
	// settings, because enabling confirmation only adds a y to each repeat.
	for _, confirming := range []bool{false, true} {
		name := "repeat restart quietly"
		env := quietEnv()
		if confirming {
			name = "repeat restart with confirmation"
			env = confirmingEnv()
		}
		t.Run(name, func(t *testing.T) {
			target := Capture(row(11, 21))
			h := host{env: env, live: []session.RunningSession{row(11, 21)}}
			c := testController(hostEffects(lifecycle...))

			h, step := begin(t, c, h, commands.RestartSession, target)
			if step.Kind != StepDispatched {
				t.Fatalf("first restart: kind = %q, want %q (detail %q)", step.Kind, StepDispatched, step.Detail)
			}

			// Reopening the menu and beginning again is exactly what the
			// existing double-Confirm test does not cover.
			for attempt := 0; attempt < 3; attempt++ {
				h, step = begin(t, c, h, commands.RestartSession, target)
				assertRefusedAsBusy(t, step, h, 1)
			}
		})
	}

	// Close, restart, and profile replacement exclude each other, not merely
	// repeats of themselves: all three end or replace the same harness process.
	t.Run("conflicting lifecycle actions", func(t *testing.T) {
		for _, confirming := range []bool{false, true} {
			for _, first := range lifecycle {
				for _, second := range lifecycle {
					name := string(first) + "-then-" + string(second)
					if confirming {
						name += "-confirming"
					}
					t.Run(name, func(t *testing.T) {
						target := Capture(row(11, 21))
						env := quietEnv()
						if confirming {
							env = confirmingEnv()
						}
						h := host{env: env, live: []session.RunningSession{row(11, 21)}}
						c := testController(hostEffects(lifecycle...))

						h, step := begin(t, c, h, first, target)
						if step.Kind != StepDispatched {
							t.Fatalf("%s: kind = %q, want %q (detail %q)", first, step.Kind, StepDispatched, step.Detail)
						}
						h, step = begin(t, c, h, second, target)
						assertRefusedAsBusy(t, step, h, 1)
					})
				}
			}
		}
	})

	// The tab is the unit, so a second row on the same tab is the same target
	// even though it is a different window.
	t.Run("same tab through another window", func(t *testing.T) {
		live := []session.RunningSession{row(11, 21), row(12, 21)}
		h := host{env: quietEnv(), live: live}
		c := testController(hostEffects(lifecycle...))

		h, step := begin(t, c, h, commands.CloseSession, Capture(row(11, 21)))
		if step.Kind != StepDispatched {
			t.Fatalf("kind = %q, want %q", step.Kind, StepDispatched)
		}
		h, step = begin(t, c, h, commands.RestartSession, Capture(row(12, 21)))
		assertRefusedAsBusy(t, step, h, 1)
	})

	// Control: independent targets stay usable. A dashboard that serialized
	// every lifecycle action would be a worse bug than the one being fixed.
	t.Run("distinct tabs are independent", func(t *testing.T) {
		live := []session.RunningSession{row(11, 21), row(12, 22), row(13, 23)}
		h := host{env: quietEnv(), live: live}
		c := testController(hostEffects(lifecycle...))

		for i, target := range []Target{Capture(row(11, 21)), Capture(row(12, 22)), Capture(row(13, 23))} {
			var step Step
			h, step = begin(t, c, h, lifecycle[i], target)
			if step.Kind != StepDispatched {
				t.Fatalf("%s on tab %d: kind = %q, want %q (detail %q)", lifecycle[i], target.TabID(), step.Kind, StepDispatched, step.Detail)
			}
		}
		if len(h.ran) != 3 {
			t.Fatalf("ran = %+v, want three dispatches", h.ran)
		}
	})

	// Control: an unattributed target names no tab, so two of them are not the
	// same tab. Conflating them would refuse unrelated rows.
	t.Run("unattributed targets are not conflated", func(t *testing.T) {
		live := []session.RunningSession{row(11, 0), row(12, 0)}
		h := host{env: quietEnv(), live: live}
		c := testController(hostEffects(lifecycle...))

		for _, target := range []Target{Capture(row(11, 0)), Capture(row(12, 0))} {
			var step Step
			h, step = begin(t, c, h, commands.CloseSession, target)
			if step.Kind != StepDispatched {
				t.Fatalf("kind = %q, want %q (detail %q)", step.Kind, StepDispatched, step.Detail)
			}
		}
		if len(h.ran) != 2 {
			t.Fatalf("ran = %+v, want both unattributed closes", h.ran)
		}
	})

	// Control: launching another session ends nothing, so it is neither blocked
	// by an in-flight restart nor blocks one. Two of them are two deliberate
	// launches.
	t.Run("launch another stays available", func(t *testing.T) {
		target := Capture(row(11, 21))
		h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
		c := testController(hostEffects(append(lifecycle, commands.LaunchAnother)...))

		h, step := begin(t, c, h, commands.RestartSession, target)
		if step.Kind != StepDispatched {
			t.Fatalf("restart: kind = %q, want %q", step.Kind, StepDispatched)
		}
		for attempt := 0; attempt < 2; attempt++ {
			h, step, _ = c.Begin(h, commands.LaunchAnother, target, Origin{Surface: SurfaceMenu})
			if step.Kind != StepProfile {
				t.Fatalf("launch another #%d: kind = %q, want %q (detail %q)", attempt+1, step.Kind, StepProfile, step.Detail)
			}
			h, step, _ = c.ChooseProfile(h, "personal")
			if step.Kind != StepDispatched {
				t.Fatalf("launch another #%d: kind = %q, want %q (detail %q)", attempt+1, step.Kind, StepDispatched, step.Detail)
			}
		}
		// A launch is recorded like any other dispatch but is not part of the
		// set the guard reads, so the restart is still the only thing holding
		// the tab and both launches ran.
		h, step = begin(t, c, h, commands.CloseSession, target)
		assertRefusedAsBusy(t, step, h, 3)
	})

	// Every way an operation can report back frees the tab, because the guard is
	// released by the completion rather than by success. A failed close that
	// locked its own row out of a retry would be worse than the double dispatch.
	t.Run("a reported completion frees the tab", func(t *testing.T) {
		completions := map[string]Completion{
			"succeeded":   {},
			"failed":      {Err: errors.New("kitty refused")},
			"unconfirmed": {Unconfirmed: true},
		}
		for name, completion := range completions {
			t.Run(name, func(t *testing.T) {
				target := Capture(row(11, 21))
				h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
				c := testController(hostEffects(lifecycle...))

				h, step := begin(t, c, h, commands.CloseSession, target)
				if step.Kind != StepDispatched {
					t.Fatalf("kind = %q, want %q", step.Kind, StepDispatched)
				}
				h, _, ok := c.Complete(h, step.Request, completion)
				if !ok {
					t.Fatalf("completion for request %d was not correlated", step.Request)
				}

				h, step = begin(t, c, h, commands.RestartSession, target)
				if step.Kind != StepDispatched {
					t.Fatalf("retry after %s: kind = %q, want %q (detail %q)", name, step.Kind, StepDispatched, step.Detail)
				}
				if len(h.ran) != 2 {
					t.Fatalf("ran = %+v, want the close and its retry", h.ran)
				}
			})
		}
	})

	// The guard lives in validate, which all three entry points run, so it holds
	// on the two that answer a surface the operator can sit in as well as on
	// Begin. The in-flight record is planted directly: one pending action at a
	// time is a separate invariant, and this asserts the guard does not depend
	// on it.
	t.Run("the picker and the confirmation ask again", func(t *testing.T) {
		parked := Dispatch{Action: commands.CloseSession, Target: Capture(row(11, 21)), Request: 99}

		t.Run("picker", func(t *testing.T) {
			h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
			c := testController(hostEffects(lifecycle...))
			h, step, _ := c.Begin(h, commands.OverrideProfile, Capture(row(11, 21)), Origin{Surface: SurfaceMenu})
			if step.Kind != StepProfile {
				t.Fatalf("kind = %q, want %q", step.Kind, StepProfile)
			}
			h.state.inflight = append(h.state.inflight, parked)

			h, step, _ = c.ChooseProfile(h, "personal")
			assertRefusedBusy(t, step, h, 0, 1)
		})

		t.Run("confirmation", func(t *testing.T) {
			h := host{env: confirmingEnv(), live: []session.RunningSession{row(11, 21)}}
			c := testController(hostEffects(lifecycle...))
			h, step, _ := c.Begin(h, commands.RestartSession, Capture(row(11, 21)), Origin{Surface: SurfaceMenu})
			if step.Kind != StepConfirm {
				t.Fatalf("kind = %q, want %q", step.Kind, StepConfirm)
			}
			h.state.inflight = append(h.state.inflight, parked)

			h, step, _ = c.Confirm(h)
			assertRefusedBusy(t, step, h, 0, 1)
		})
	})
}

// pressureTabs is how many distinct tabs the correlation-pressure cases drive.
// It is deliberately above maxInFlight and inside the plan's 8–20-session target:
// twenty sessions is a busy morning rather than a synthetic load, and it is
// already more accumulated lifecycle work than the record was willing to hold.
const pressureTabs = 20

// beginAction drives one action to whatever it settles on, answering the picker
// and the confirmation when they open. It is the package-level twin of the
// closure the single-flight test uses, spelled once here because the pressure
// cases need it from several helpers rather than from one t.Run body.
//
// Nothing it dispatches is ever completed: the fixture's effects record a
// dispatch and report nothing back, which is exactly the window in which a
// second keystroke arrives for an operator whose restart has not finished.
func beginAction(t *testing.T, c Controller[host], h host, action commands.ID, target Target, profile string) (host, Step) {
	t.Helper()
	h, step, _ := c.Begin(h, action, target, Origin{Surface: SurfaceMenu})
	if step.Kind == StepProfile {
		h, step, _ = c.ChooseProfile(h, profile)
	}
	if step.Kind == StepConfirm {
		h, step, _ = c.Confirm(h)
	}
	return h, step
}

// pressureRows are the twenty live rows the pressure cases act on, each on its
// own tab and its own window.
func pressureRows() []session.RunningSession {
	out := make([]session.RunningSession, 0, pressureTabs)
	for i := 0; i < pressureTabs; i++ {
		out = append(out, row(100+i, 200+i))
	}
	return out
}

// guardedTabs names the tabs the record currently holds a lifecycle exclusion
// for, which is the fact the bound used to be allowed to discard.
func guardedTabs(h host) map[int]bool {
	out := map[int]bool{}
	for _, dispatch := range h.state.InFlight() {
		if dispatch.holdsLifecycleGuard() {
			out[dispatch.Target.TabID()] = true
		}
	}
	return out
}

// applyCorrelationPressure runs every OTHER row's lifecycle action and then a
// long run of churn the guard does not read — focus, which ends nothing, and
// Launch another, which is recorded like any dispatch but is deliberately outside
// the single-flight set. None of it is completed, so all of it accumulates in the
// one record the held operation is correlated through.
//
// It returns the host and how many dispatches it drove, so a caller can state its
// own expectations about the record in terms of the work that produced it.
func applyCorrelationPressure(t *testing.T, c Controller[host], h host, held Target) (host, int) {
	t.Helper()
	lifecycle := []commands.ID{commands.CloseSession, commands.RestartSession, commands.OverrideProfile}
	drove := 0
	for i, live := range h.live {
		target := Capture(live)
		if target.TabID() == held.TabID() {
			continue
		}
		action := lifecycle[i%len(lifecycle)]
		var step Step
		h, step = beginAction(t, c, h, action, target, "personal")
		if step.Kind != StepDispatched {
			t.Fatalf("%s on independent tab %d: kind = %q, want %q (detail %q)", action, target.TabID(), step.Kind, StepDispatched, step.Detail)
		}
		drove++
	}
	// Churn on the held tab itself: neither of these ends or replaces the
	// process, so neither is refused and neither holds the tab — but both take a
	// place in the record the held operation is correlated through.
	for i := 0; i < maxInFlight; i++ {
		churn := commands.FocusSession
		if i%4 == 3 {
			churn = commands.LaunchAnother
		}
		var step Step
		h, step = beginAction(t, c, h, churn, held, "personal")
		if step.Kind != StepDispatched {
			t.Fatalf("%s #%d on the held tab: kind = %q, want %q (detail %q)", churn, i+1, step.Kind, StepDispatched, step.Detail)
		}
		drove++
	}
	if drove <= maxInFlight {
		t.Fatalf("the pressure drove %d dispatches, want more than the %d-entry record", drove, maxInFlight)
	}
	return h, drove
}

// pressured is the shared setup: one lifecycle action parked on the held tab,
// then more accumulated independent dispatches than the record can hold. It
// returns the host, the controller, the held target, and the parked operation's
// request identity.
func pressured(t *testing.T, held commands.ID, confirming bool) (host, Controller[host], Target, RequestID) {
	t.Helper()
	live := pressureRows()
	env := quietEnv()
	if confirming {
		env = confirmingEnv()
	}
	h := host{env: env, live: live}
	c := testController(hostEffects(commands.CloseSession, commands.RestartSession, commands.OverrideProfile, commands.LaunchAnother, commands.FocusSession))
	target := Capture(live[0])

	h, step := beginAction(t, c, h, held, target, "personal")
	if step.Kind != StepDispatched {
		t.Fatalf("the held %s: kind = %q, want %q (detail %q)", held, step.Kind, StepDispatched, step.Detail)
	}
	request := step.Request

	h, _ = applyCorrelationPressure(t, c, h, target)
	return h, c, target, request
}

// TestReviewLifecycleGuardSurvivesCorrelationPressure is the bound's half of the
// single-flight contract: the guard must outlive the correlation record's
// eviction policy, because the two are the same slice.
//
// The record used to drop its oldest entry at sixteen regardless of what that
// entry was holding. The oldest entry is the likeliest to be the unfinished close
// or restart, so sixteen unrelated dispatches released the tab the operator was
// already replacing — validate then permitted a second lifecycle effect on it,
// and Complete could no longer correlate the first one's result at all. The
// existing single-flight regression never reached that state because it never
// filled the record.
func TestReviewLifecycleGuardSurvivesCorrelationPressure(t *testing.T) {
	lifecycle := []commands.ID{commands.CloseSession, commands.RestartSession, commands.OverrideProfile}

	// Every lifecycle action, held against every lifecycle retry, under both
	// confirmation settings: the guard is one rule and the pressure must not
	// unmake it for any pair.
	for _, confirming := range []bool{false, true} {
		for _, held := range lifecycle {
			for _, retry := range lifecycle {
				name := string(held) + "-then-" + string(retry)
				if confirming {
					name += "-confirming"
				}
				t.Run(name, func(t *testing.T) {
					h, c, target, _ := pressured(t, held, confirming)
					ranBefore := len(h.ran)

					for attempt := 0; attempt < 3; attempt++ {
						var step Step
						h, step = beginAction(t, c, h, retry, target, "personal")
						if step.Kind != StepRefused {
							t.Fatalf("retry #%d: kind = %q, want %q (detail %q)", attempt+1, step.Kind, StepRefused, step.Detail)
						}
						if step.Detail != ReasonLifecycleInFlight {
							t.Fatalf("retry #%d: detail = %q, want %q", attempt+1, step.Detail, ReasonLifecycleInFlight)
						}
						if len(h.ran) != ranBefore {
							t.Fatalf("retry #%d reached an effect: ran %d dispatches, want the %d from before the retry", attempt+1, len(h.ran), ranBefore)
						}
					}
				})
			}
		}
	}

	// The record is allowed to shed ordinary dispatches under pressure and is not
	// allowed to shed a guard. Both halves are asserted, because retaining
	// everything would defeat the leak guard the bound exists to be.
	t.Run("every held tab keeps its exclusion", func(t *testing.T) {
		h, _, _, _ := pressured(t, commands.RestartSession, false)

		guarded := guardedTabs(h)
		if len(guarded) != pressureTabs {
			t.Fatalf("the record guards %d tabs, want all %d that have a lifecycle action parked", len(guarded), pressureTabs)
		}
		for _, live := range h.live {
			if !guarded[live.TabID] {
				t.Fatalf("tab %d lost its exclusion to the record's eviction policy: %+v", live.TabID, h.state.InFlight())
			}
		}
		ordinary := 0
		for _, dispatch := range h.state.InFlight() {
			if !dispatch.holdsLifecycleGuard() {
				ordinary++
			}
		}
		if ordinary >= maxInFlight {
			t.Fatalf("the record kept %d evictable dispatches, want fewer than the %d-entry bound", ordinary, maxInFlight)
		}
	})

	// The correlation is the other thing eviction took. A completion the record
	// can no longer name is one both hosts discard, so the close that ran would
	// have reported nothing and its recovery would never have been offered.
	t.Run("the held operation is still correlated", func(t *testing.T) {
		for _, held := range lifecycle {
			t.Run(string(held), func(t *testing.T) {
				h, c, target, request := pressured(t, held, false)

				h, result, ok := c.Complete(h, request, Completion{})
				if !ok {
					t.Fatalf("the held %s (request %d) was no longer correlated after the pressure", held, request)
				}
				if result.Action != held {
					t.Fatalf("the completion named %q, want the held %q", result.Action, held)
				}
				if result.Target.TabID() != target.TabID() {
					t.Fatalf("the completion named tab %d, want the held tab %d", result.Target.TabID(), target.TabID())
				}
				if result.Request != request {
					t.Fatalf("the completion named request %d, want %d", result.Request, request)
				}
				// And it is correlated exactly once: a second report of the same
				// request finds nothing, which is what keeps a recovery from
				// being reserved twice for one close.
				if _, _, again := c.Complete(h, request, Completion{}); again {
					t.Fatalf("request %d correlated a second time", request)
				}
			})
		}
	})

	// Release is by completion under pressure exactly as it is without it, for
	// every outcome: a failed or unconfirmed lifecycle action that could not be
	// retried would be a worse answer than the duplicate the guard refuses.
	t.Run("a reported completion frees the tab", func(t *testing.T) {
		for name, completion := range map[string]Completion{
			"succeeded":   {},
			"failed":      {Err: errors.New("kitty refused")},
			"unconfirmed": {Unconfirmed: true},
		} {
			t.Run(name, func(t *testing.T) {
				h, c, target, request := pressured(t, commands.CloseSession, false)

				h, _, ok := c.Complete(h, request, completion)
				if !ok {
					t.Fatalf("the held close was not correlated after the pressure")
				}
				ranBefore := len(h.ran)
				h, step := beginAction(t, c, h, commands.RestartSession, target, "personal")
				if step.Kind != StepDispatched {
					t.Fatalf("retry after a %s close: kind = %q, want %q (detail %q)", name, step.Kind, StepDispatched, step.Detail)
				}
				if len(h.ran) != ranBefore+1 {
					t.Fatalf("the retry ran %d dispatches, want one more than the %d before it", len(h.ran)-ranBefore, ranBefore)
				}
			})
		}
	})

	// Control: the pressure is other tabs' work, and it must stay other tabs'
	// work. A guard that spread across the record would refuse nineteen rows the
	// operator never touched.
	t.Run("independent targets stay usable", func(t *testing.T) {
		h, c, held, _ := pressured(t, commands.RestartSession, false)

		for _, live := range h.live {
			if live.TabID == held.TabID() {
				continue
			}
			target := Capture(live)
			// Its own lifecycle action is parked, so a second one is refused for
			// that tab's OWN operation rather than for the held one.
			var step Step
			h, step = beginAction(t, c, h, commands.CloseSession, target, "personal")
			if step.Kind != StepRefused || step.Detail != ReasonLifecycleInFlight {
				t.Fatalf("tab %d: kind = %q detail = %q, want its own parked operation to refuse it", live.TabID, step.Kind, step.Detail)
			}
			// Completing it frees that tab and nothing else.
			var request RequestID
			for _, dispatch := range h.state.InFlight() {
				if dispatch.holdsLifecycleGuard() && dispatch.Target.TabID() == live.TabID {
					request = dispatch.Request
				}
			}
			if !request.Valid() {
				t.Fatalf("tab %d holds no guard to complete: %+v", live.TabID, h.state.InFlight())
			}
			var ok bool
			h, _, ok = c.Complete(h, request, Completion{})
			if !ok {
				t.Fatalf("tab %d's parked operation (request %d) was not correlated", live.TabID, request)
			}
			h, step = beginAction(t, c, h, commands.RestartSession, target, "personal")
			if step.Kind != StepDispatched {
				t.Fatalf("tab %d after its own completion: kind = %q, want %q (detail %q)", live.TabID, step.Kind, StepDispatched, step.Detail)
			}
		}

		// The held tab is untouched by all of it.
		h, step := beginAction(t, c, h, commands.CloseSession, held, "personal")
		if step.Kind != StepRefused || step.Detail != ReasonLifecycleInFlight {
			t.Fatalf("the held tab: kind = %q detail = %q, want it still held", step.Kind, step.Detail)
		}
	})

	// Control: Launch another is a deliberate act, not a repeat, so the pressure
	// must not have made it the exception that proves the guard. Two more of them
	// run against the held tab while its restart is still parked.
	t.Run("launch another stays available", func(t *testing.T) {
		h, c, held, _ := pressured(t, commands.RestartSession, true)

		ranBefore := len(h.ran)
		for attempt := 0; attempt < 2; attempt++ {
			var step Step
			h, step = beginAction(t, c, h, commands.LaunchAnother, held, "personal")
			if step.Kind != StepDispatched {
				t.Fatalf("launch another #%d: kind = %q, want %q (detail %q)", attempt+1, step.Kind, StepDispatched, step.Detail)
			}
		}
		if len(h.ran) != ranBefore+2 {
			t.Fatalf("ran %d launches, want both", len(h.ran)-ranBefore)
		}
		h, step := beginAction(t, c, h, commands.CloseSession, held, "personal")
		if step.Kind != StepRefused || step.Detail != ReasonLifecycleInFlight {
			t.Fatalf("after the launches: kind = %q detail = %q, want the restart still holding the tab", step.Kind, step.Detail)
		}
	})
}
