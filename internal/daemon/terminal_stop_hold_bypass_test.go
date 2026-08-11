package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// TestTerminalStopHold_ProcessExitBypassesGate covers the one release the gate
// must never delay: the process that produced the held stop is itself verifiably
// gone, so the window is terminal no matter what a screen probe would say. That
// exact exit settles the stop directly and retires the hold, and the cancelled
// generation must stay inert for the rest of what would have been its hold
// window.
//
// A second window exits in the same pass having never received a native
// terminal, so the two exported evidence values are discriminated per candidate
// rather than per pass.
func TestTerminalStopHold_ProcessExitBypassesGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, rec := newTerminalHoldTestHandler(t)
		identity := HarnessProcessIdentity{Harness: harness.Claude, PID: 7101, StartToken: "Thu Jul 16 10:20:30 2026"}
		unheld := HarnessProcessIdentity{Harness: harness.Claude, PID: 7102, StartToken: "Thu Jul 16 10:20:31 2026"}
		applyLifecycleEvent(t, h, 42, identity, "session", "active", 1, false)
		applyLifecycleEvent(t, h, 43, unheld, "session-43", "active", 1, false)
		h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{
			statuses: map[HarnessProcessIdentity]HarnessProcessInspection{
				identity: HarnessProcessMissing,
				unheld:   HarnessProcessMissing,
			},
		})

		deliverProcessStop(t, h, 42, identity, "session")

		time.Sleep(terminalHoldTestDelay / 2)
		if committed := h.processLifecycleTick(t.Context(), time.Now()); committed != 2 {
			t.Fatalf("process exit committed = %d, want 2", committed)
		}
		if _, stillHeld := terminalHoldPending(t, h, 42); stillHeld {
			t.Fatal("verified process exit left the held stop addressable")
		}
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("icon after the exit = %q, want stop, without waiting for the gate", eff.Icon)
		}

		// Ride past the quiet period and the hard deadline the hold would have had.
		time.Sleep(2 * terminalHoldTestTimeout)
		synctest.Wait()

		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("icon after the cancelled hold window = %q, want stop", eff.Icon)
		}
		if got := rec.len(); got != 0 {
			t.Fatalf("notifications = %d, want 0: a synthetic exit stop is silent (%+v)", got, rec.snapshot())
		}
		outcomes := stopHoldOutcomes(t, h, 42)
		want := []StopHoldOutcome{StopHoldOutcomeHeld, StopHoldOutcomeBypassed}
		if len(outcomes) != len(want) {
			t.Fatalf("hold rows = %v, want %v", outcomes, want)
		}
		for i, outcome := range outcomes {
			if outcome != want[i] {
				t.Fatalf("hold rows = %v, want %v", outcomes, want)
			}
		}
		if settled := settledStopEvents(t, h, 42); settled != 1 {
			t.Fatalf("settled stop events = %d, want exactly 1", settled)
		}
		// The bypassed hold proves the native hook was received even though the
		// exit is what synthesized the reducer stop.
		bypassedExit := lifecycleExitEvent(t, h, 42)
		if bypassedExit.TerminalHookEvidence != processTerminalHookEvidenceObserved ||
			bypassedExit.SynthesizedStop == nil || !*bypassedExit.SynthesizedStop {
			t.Fatalf("bypassed-hold exit = %+v, want observed evidence with a synthesized stop", bypassedExit)
		}
		unheldExit := lifecycleExitEvent(t, h, 43)
		if unheldExit.TerminalHookEvidence != processTerminalHookEvidenceAbsent ||
			unheldExit.SynthesizedStop == nil || !*unheldExit.SynthesizedStop {
			t.Fatalf("exit with no native terminal = %+v, want absent evidence with a synthesized stop", unheldExit)
		}
		terminalFacts := 0
		for _, fact := range windowFacts(t, h, 42) {
			if fact.Action == "stop" {
				terminalFacts++
			}
		}
		if terminalFacts != 1 {
			t.Fatalf("terminal facts = %d, want exactly 1 (%+v)", terminalFacts, windowFacts(t, h, 42))
		}
	})
}

// TestTerminalStopHold_UnrelatedProcessExitPreservesGate covers the exits that
// prove nothing about the held stop. One window routinely carries several
// ordinary process groups at once — an interactive parent plus the `claude -p`
// children it spawned, or a second session sharing the tab — so settling one of
// them says nothing about whether the parent is done. Retiring the hold by
// window ID would paint 🛑 over a session still working, discard the deadline
// that is the parent's only convergence bound, and stamp observed terminal
// evidence on an exit that never received a native hook.
func TestTerminalStopHold_UnrelatedProcessExitPreservesGate(t *testing.T) {
	held := HarnessProcessIdentity{Harness: harness.Claude, PID: 7201, StartToken: "Thu Jul 16 10:20:30 2026"}
	cases := []struct {
		name      string
		identity  HarnessProcessIdentity
		sessionID string
		pipeMode  bool
	}{
		{
			name:      "pipe_child",
			identity:  HarnessProcessIdentity{Harness: harness.Claude, PID: 7202, StartToken: "Thu Jul 16 10:21:30 2026"},
			sessionID: "pipe",
			pipeMode:  true,
		},
		{
			name:      "sibling_session",
			identity:  HarnessProcessIdentity{Harness: harness.Claude, PID: 7203, StartToken: "Thu Jul 16 10:22:30 2026"},
			sessionID: "sibling",
			pipeMode:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, rec := newTerminalHoldTestHandler(t)
				applyLifecycleEvent(t, h, 42, held, "session", "active", 1, false)
				applyLifecycleEvent(t, h, 42, tc.identity, tc.sessionID, "active", 1, tc.pipeMode)
				// Only the unrelated process is gone; the held stop's own process is
				// left unpolled so this pass can never settle it.
				h.SetHarnessProcessInspector(&fakeHarnessProcessInspector{
					statuses: map[HarnessProcessIdentity]HarnessProcessInspection{
						tc.identity: HarnessProcessMissing,
					},
				})

				deliverProcessStop(t, h, 42, held, "session")
				armed, _ := terminalHoldPending(t, h, 42)

				time.Sleep(terminalHoldTestDelay / 2)
				if committed := h.processLifecycleTick(t.Context(), time.Now()); committed != 1 {
					t.Fatalf("unrelated exit committed = %d, want 1", committed)
				}

				pending, stillHeld := terminalHoldPending(t, h, 42)
				if !stillHeld {
					t.Fatal("an unrelated process exit discarded the held stop of a process that is still running")
				}
				if pending.seq != armed.seq ||
					!pending.quietUntil.Equal(armed.quietUntil) || !pending.deadline.Equal(armed.deadline) {
					t.Fatalf("hold after the unrelated exit = %+v, want its original gate %+v", pending, armed)
				}
				if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
					t.Fatalf("icon after the unrelated exit = %q, want bolt: the held session is still working", eff.Icon)
				}
				if outcomes := stopHoldOutcomes(t, h, 42); len(outcomes) != 1 || outcomes[0] != StopHoldOutcomeHeld {
					t.Fatalf("hold rows = %v, want just [%v]: nothing bypassed this gate", outcomes, StopHoldOutcomeHeld)
				}
				// The withheld hook belongs to the surviving process, so it is not
				// evidence about the one that exited.
				exit := lifecycleExitEvent(t, h, 42)
				if exit.TerminalHookEvidence != processTerminalHookEvidenceAbsent {
					t.Fatalf("unrelated exit = %+v, want absent terminal evidence", exit)
				}
				if got := rec.len(); got != 0 {
					t.Fatalf("notifications = %d, want 0 while the stop is still held (%+v)", got, rec.snapshot())
				}

				// The gate still owns the held stop, so it converges on its own hard
				// deadline exactly as if the unrelated exit had never happened.
				time.Sleep(2 * terminalHoldTestTimeout)
				synctest.Wait()
				if _, neverReleased := terminalHoldPending(t, h, 42); neverReleased {
					t.Fatal("the preserved hold never reached its deadline")
				}
				if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
					t.Fatalf("icon after the deadline = %q, want stop", eff.Icon)
				}
			})
		})
	}
}

func TestSupersedesStopHold(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		want bool
	}{
		{"prompt", Request{Action: "active", IsPrompt: true}, true},
		{"user_clear", Request{Action: "user_clear"}, true},
		{"clear", Request{Action: "clear"}, true},
		{"active", Request{Action: "active"}, true},
		{"subagent", Request{Action: "subagent"}, true},
		{"compact", Request{Action: "compact"}, true},
		{"permission", Request{Action: "permission"}, true},
		{"elicitation", Request{Action: "elicitation"}, true},
		{"failure", Request{Action: "failure"}, true},
		{"stop", Request{Action: "stop"}, false},
		{"loop_marker", Request{Action: "loop_marker"}, false},
		// Reset-flow cleanup keeps the hold so the stop can fold into the same
		// user_clear when it commits.
		{"reset_flow_user_clear", Request{Action: "user_clear", ResetFlow: true}, false},
		{"reset_flow_prompt", Request{Action: "active", IsPrompt: true, ResetFlow: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := supersedesStopHold(tc.req); got != tc.want {
				t.Fatalf("supersedesStopHold(%+v) = %v, want %v", tc.req, got, tc.want)
			}
		})
	}
}

// TestTerminalStopHold_SupersededByPromptAndClear drives the two events that
// retire a held terminal stop through the live hook path rather than the
// predicate: a fresh turn and an F8 wipe.
func TestTerminalStopHold_SupersededByPromptAndClear(t *testing.T) {
	for _, event := range []string{"UserPromptSubmit", "user_clear"} {
		t.Run(event, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, rec := newTerminalHoldTestHandler(t)
				armTerminalHold(t, h)

				time.Sleep(terminalHoldTestDelay / 3)
				if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: event}); !resp.OK {
					t.Fatalf("%s response = %+v", event, resp)
				}

				time.Sleep(2 * terminalHoldTestDelay)
				synctest.Wait()

				if eff, _ := windowEffective(t, h, 42); eff.Icon == "stop" {
					t.Fatalf("%s left the superseded hold free to paint stop", event)
				}
				if got := rec.len(); got != 0 {
					t.Fatalf("notifications = %d, want 0 (%+v)", got, rec.snapshot())
				}
			})
		})
	}
}

// TestTerminalStopHold_BypassMatrix pins the arm-time decisions that keep a
// stop on its pre-gate behaviour. A bypassed stop is not "released
// immediately": a reset-sensitive one still falls back to the reset class,
// exactly as before the gate existed.
func TestTerminalStopHold_BypassMatrix(t *testing.T) {
	baseReq := Request{
		Type:     TypeStateUpdate,
		WindowID: 42,
		Action:   "stop",
		Event:    "Stop",
		Harness:  harness.Claude,
	}
	cases := []struct {
		name      string
		handler   func(*Handler)
		mutate    func(*Request)
		wantHeld  bool
		wantClass stopHoldClass
	}{
		{
			name:      "armable",
			wantHeld:  true,
			wantClass: stopHoldClassTerminal,
		},
		{
			name:      "stop_confirm_disabled",
			handler:   func(h *Handler) { h.SetStopConfirm(terminalHoldTestDelay, terminalHoldTestTimeout, false) },
			wantHeld:  true,
			wantClass: stopHoldClassReset,
		},
		{
			name:      "zero_delay",
			handler:   func(h *Handler) { h.SetStopConfirm(0, terminalHoldTestTimeout, true) },
			wantHeld:  true,
			wantClass: stopHoldClassReset,
		},
		{
			name:      "no_kitty_client",
			handler:   func(h *Handler) { h.SetKittyClient(nil) },
			wantHeld:  true,
			wantClass: stopHoldClassReset,
		},
		{
			name:      "pipe_mode",
			mutate:    func(req *Request) { req.PipeMode = true },
			wantHeld:  true,
			wantClass: stopHoldClassReset,
		},
		{
			name:      "harness_without_stop_probe",
			mutate:    func(req *Request) { req.Harness = "" },
			wantHeld:  true,
			wantClass: stopHoldClassReset,
		},
		{
			name:     "context_limit",
			mutate:   func(req *Request) { req.ContextLimit = true },
			wantHeld: false,
		},
		{
			name:     "reset_flow",
			mutate:   func(req *Request) { req.ResetFlow = true },
			wantHeld: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTerminalHoldTestHandler(t)
			if tc.handler != nil {
				tc.handler(h)
			}
			req := baseReq
			if tc.mutate != nil {
				tc.mutate(&req)
			}

			held := h.holdStopForConfirmation(req, hookevent.HookPayload{})
			pending, armed := terminalHoldPending(t, h, 42)
			if armed && pending.timer != nil {
				pending.timer.Stop()
			}
			if held != tc.wantHeld || armed != tc.wantHeld {
				t.Fatalf("held = %v (pending %v), want %v", held, armed, tc.wantHeld)
			}
			if tc.wantHeld && pending.class != tc.wantClass {
				t.Fatalf("hold class = %v, want %v", pending.class, tc.wantClass)
			}
		})
	}
}

// TestTerminalStopHold_LiveASLLeaseBypassesTerminalClass covers the remaining
// bypass, which needs a real lease rather than a synthesized request: Reduce
// already masks "stop" while a loop lease is live, so holding would only add
// latency for an identical icon.
func TestTerminalStopHold_LiveASLLeaseBypassesTerminalClass(t *testing.T) {
	h, windows, projectDir := reconcileTestFixture(t)
	h.SetKittyClient(&fakeKittyClient{windows: windows})
	stubDaemonLoopDiscover(t, nil, nil)

	aslLifecycleHookRequest(t, h, "AgentSessionLoopStarted", projectDir, hookevent.HookPayload{RunID: "run-1"})
	if eff, _ := windowEffective(t, h, 42); !eff.Looping {
		t.Fatalf("effective = %+v, want a live loop lease before the stop", eff)
	}

	if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
		t.Fatalf("Stop response = %+v", resp)
	}
	pending, armed := terminalHoldPending(t, h, 42)
	if armed && pending.timer != nil {
		pending.timer.Stop()
	}
	if !armed || pending.class != stopHoldClassReset {
		t.Fatalf("pending hold = %+v/%v, want the reset class under a live lease", pending, armed)
	}
	if rows := stopHoldOutcomes(t, h, 42); len(rows) != 0 {
		t.Fatalf("hold rows = %v, want none for a bypassed stop", rows)
	}
}

// TestTerminalStopHold_ShadowSeqBumpsOncePerHold pins the receipt contract:
// arming withholds a real transition and must invalidate off-lock screen reads
// once, but a re-arm during the same hold must not discard the idle evidence
// the gate is accumulating.
func TestTerminalStopHold_ShadowSeqBumpsOncePerHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, _ := newTerminalHoldTestHandler(t)
		if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "UserPromptSubmit"}); !resp.OK {
			t.Fatalf("UserPromptSubmit response = %+v", resp)
		}
		before := h.captureWindowReceipt(42)

		if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
			t.Fatalf("first Stop response = %+v", resp)
		}
		armed := h.captureWindowReceipt(42)
		if armed != before+1 {
			t.Fatalf("receipt after arm = %d, want %d", armed, before+1)
		}
		first, ok := terminalHoldPending(t, h, 42)
		if !ok {
			t.Fatal("first Stop was not held")
		}

		time.Sleep(terminalHoldTestDelay / 3)
		if resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "Stop"}); !resp.OK {
			t.Fatalf("second Stop response = %+v", resp)
		}
		if got := h.captureWindowReceipt(42); got != armed {
			t.Fatalf("receipt after re-arm = %d, want %d unchanged", got, armed)
		}
		second, ok := terminalHoldPending(t, h, 42)
		if !ok {
			t.Fatal("re-arm dropped the hold")
		}
		if second.seq == first.seq {
			t.Fatalf("re-arm reused generation %d; the superseded timer must be unable to commit", first.seq)
		}
		if !second.armedAt.Equal(first.armedAt) {
			t.Fatalf("re-arm reset armedAt from %v to %v; the window has been withholding continuously", first.armedAt, second.armedAt)
		}
		if rows := stopHoldOutcomes(t, h, 42); len(rows) != 1 || rows[0] != StopHoldOutcomeHeld {
			t.Fatalf("hold rows = %v, want exactly one held row across arm and re-arm", rows)
		}

		time.Sleep(2 * terminalHoldTestDelay)
		synctest.Wait()
	})
}
