package sessionactions

import (
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
// without any real terminal, harness, or clipboard work.
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
