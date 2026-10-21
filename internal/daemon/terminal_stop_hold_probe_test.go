package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// TestTerminalStopHold_ProbeConfirmsHeldStop is the feature's headline path: the
// harness's own idle probe — the same evidence that settles a lost-hook stop
// when nothing is held — releases the withheld native stop instead of settling a
// stop of its own. The committed stop keeps its SourceHarness provenance and
// records which probe confirmed it, and no SourceProbe fact is ever created.
func TestTerminalStopHold_ProbeConfirmsHeldStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, rec := newTerminalHoldProbeHandler(t, harness.Claude, claudeIdleScreen)
		base := time.Now()
		armTerminalHold(t, h)

		// Claude requires two agreeing screen reads: one sample confirms nothing.
		h.ProbeClaudeScreenStop(base)
		if pending, ok := terminalHoldPending(t, h, 42); !ok || pending.confirmed {
			t.Fatalf("hold after one sample = %+v/%v, want an unconfirmed hold", pending, ok)
		}

		h.ProbeClaudeScreenStop(base.Add(time.Second))
		pending, ok := terminalHoldPending(t, h, 42)
		if !ok || !pending.confirmed || pending.confirmedBy != protocol.StopConfirmedByClaudeScreen {
			t.Fatalf("hold after two samples = %+v/%v, want confirmed by %s", pending, ok, protocol.StopConfirmedByClaudeScreen)
		}
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("icon while confirmed inside the quiet period = %q, want bolt", eff.Icon)
		}

		time.Sleep(terminalHoldTestDelay)
		synctest.Wait()

		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("icon after the confirmed hold committed = %q, want stop", eff.Icon)
		}
		if got := rec.len(); got != 1 {
			t.Fatalf("notifications = %d, want 1 (%+v)", got, rec.snapshot())
		}
		ev := lastStopEvent(t, h, 42)
		if ev.StopHoldOutcome != protocol.StopHoldOutcomeConfirmed || ev.StopConfirmedBy != protocol.StopConfirmedByClaudeScreen {
			t.Fatalf("released stop = outcome %q by %q, want confirmed/%s", ev.StopHoldOutcome, ev.StopConfirmedBy, protocol.StopConfirmedByClaudeScreen)
		}
		detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
		if probe, ok := lastEventNamed(detail.Events, screenStopProbeEvent); ok {
			t.Fatalf("%q row recorded while a native stop was held: %+v", screenStopProbeEvent, probe)
		}
		assertNoProbeStopFact(t, h, 42)
		facts := windowFacts(t, h, 42)
		nativeStop := false
		for _, fact := range facts {
			nativeStop = nativeStop || (fact.Kind == protocol.SourceHarness && fact.Action == "stop")
		}
		if !nativeStop {
			t.Fatalf("facts = %+v, want the committed stop to be a %s fact", facts, protocol.SourceHarness)
		}
	})
}

// TestConfirmStopHoldLocked_OnlyTerminalHoldsClaimProbeEvidence pins the
// lost-hook non-regression at the seam itself: with nothing armed, or with only
// the reset-class debounce armed, the probe's evidence is not claimed and the
// probe settles its own stop exactly as it did before the gate existed.
func TestConfirmStopHoldLocked_OnlyTerminalHoldsClaimProbeEvidence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		arm  func(*testing.T, *Handler)
		want bool
	}{
		{name: "no_hold"},
		{
			name: "reset_class_hold",
			arm: func(t *testing.T, h *Handler) {
				h.SetStopConfirm(terminalHoldTestDelay, terminalHoldTestTimeout, false)
				req := protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "stop", Event: "Stop", Harness: harness.Claude}
				if !h.holdStopForConfirmation(req, hookevent.HookPayload{}) {
					t.Fatal("reset-sensitive stop was not held")
				}
			},
		},
		{
			name: "terminal_class_hold",
			arm: func(t *testing.T, h *Handler) {
				req := protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "stop", Event: "Stop", Harness: harness.Claude}
				if !h.holdStopForConfirmation(req, hookevent.HookPayload{}) {
					t.Fatal("terminal stop was not held")
				}
			},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTerminalHoldTestHandler(t)
			if tc.arm != nil {
				tc.arm(t, h)
			}
			t.Cleanup(func() {
				if pending, ok := terminalHoldPending(t, h, 42); ok && pending.timer != nil {
					pending.timer.Stop()
				}
			})
			if got := confirmTerminalHold(t, h, 42, claudeScreenProbeSource); got != tc.want {
				t.Fatalf("confirmStopHoldLocked = %v, want %v", got, tc.want)
			}
			if !tc.want {
				return
			}
			pending, _ := terminalHoldPending(t, h, 42)
			if !pending.confirmed || pending.confirmedBy != protocol.StopConfirmedByClaudeScreen {
				t.Fatalf("confirmed hold = %+v, want confirmed by %s", pending, protocol.StopConfirmedByClaudeScreen)
			}
		})
	}
}

// TestStopConfirmedByProbeCoversEveryIdleProbeSource pins the coupling between
// the three idle probe source keys and the exported release vocabulary: a
// renamed probe source must not silently start writing an unknown
// StopConfirmedBy value into diagnostics.
func TestStopConfirmedByProbeCoversEveryIdleProbeSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		source protocol.SourceID
		want   protocol.StopConfirmedBy
	}{
		{protocol.SourceID{Kind: protocol.SourceProbe, Key: claudeScreenStopProbeSource}, protocol.StopConfirmedByClaudeScreen},
		{protocol.SourceID{Kind: protocol.SourceProbe, Key: screenPromptProbeSource}, protocol.StopConfirmedByCodexScreen},
		{protocol.SourceID{Kind: protocol.SourceProbe, Key: copilotTranscriptStopProbeSource}, protocol.StopConfirmedByCopilotTranscript},
	}
	for _, tc := range cases {
		if got := stopConfirmedByProbe(tc.source); got != tc.want {
			t.Fatalf("stopConfirmedByProbe(%+v) = %q, want %q", tc.source, got, tc.want)
		}
	}
}
