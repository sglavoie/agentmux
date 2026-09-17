package sessionactions

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

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

// testClock is the injected clock. A pending action records when it started, and
// a test that had to sleep to observe that would be a test of the sleep.
var testClock = time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

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
	return NewController(effects, func() time.Time { return testClock })
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
	if !pending.StartedAt.Equal(testClock) {
		t.Fatalf("StartedAt = %v, want the injected clock %v", pending.StartedAt, testClock)
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

	h, result, ok := c.Complete(h, second.Request, nil)
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
	h, result, ok = c.Complete(h, first.Request, failure)
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

// TestCompleteIgnoresForeignCompletions is what lets the host keep its own
// handling for everything else the same message carries: an undo, a direct key,
// a reply already reported. None of them are this controller's.
func TestCompleteIgnoresForeignCompletions(t *testing.T) {
	h := host{env: quietEnv(), live: []session.RunningSession{row(11, 21)}}
	c := testController(hostEffects(commands.CloseSession))
	h, dispatched, _ := c.Begin(h, commands.CloseSession, Capture(row(11, 21)), Origin{})

	for _, request := range []RequestID{0, dispatched.Request + 1} {
		out, result, ok := c.Complete(h, request, nil)
		if ok {
			t.Fatalf("request %d was correlated: %+v", request, result)
		}
		if got := out.state.InFlight(); len(got) != 1 {
			t.Fatalf("request %d disturbed the record: %+v", request, got)
		}
	}

	// The same completion twice is a foreign one the second time, which is what
	// keeps a retried reply from being counted as a second action.
	h, _, ok := c.Complete(h, dispatched.Request, nil)
	if !ok {
		t.Fatal("first completion was not correlated")
	}
	if _, _, ok := c.Complete(h, dispatched.Request, nil); ok {
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
	if (Host{}).Known() {
		t.Fatal("the zero host reports itself resolved")
	}
	if !(Host{TabID: 21}).Known() {
		t.Fatal("a resolved host reports itself unknown")
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
