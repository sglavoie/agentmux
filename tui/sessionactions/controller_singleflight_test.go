package sessionactions

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

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
			"failed":      {Err: errors.New("tmux refused")},
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
