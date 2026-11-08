package sessionactions

import (
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

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
			"failed":      {Err: errors.New("tmux refused")},
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
