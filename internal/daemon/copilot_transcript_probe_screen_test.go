package daemon

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// These are the production-caller tests for the Copilot screen adapter. Every
// other assertion about it lives in internal/screenstate and grades
// copilotAdapter.Classify directly, which proves the classifier and nothing
// about whether any daemon path consults it — the gap this file closes. Each
// case therefore drives the real probe entry point over the real committed
// captures, and asserts an ICON, a notification, and a provenance row rather
// than an Observation.
//
// The composition under test is: THE SCREEN REFUSES AND THE TRANSCRIPT SETTLES.
// Copilot's own session transcript is the positive evidence a turn ended; the
// screen contributes only the fact the transcript structurally cannot carry,
// which is that a human is being waited on. A permission prompt is not a
// transcript event, so a blocked pane's tail reads exactly like a finished
// one's.

// assertCopilotCaptureVetoes guards a capture against becoming vacuous. A
// re-capture for a new Copilot release, or a pattern rename, could leave these
// tests driving a screen that no longer poses the trap it was chosen for — and
// a refusal test whose screen stopped vetoing passes for the wrong reason only
// until someone deletes the wiring. Asserting the reading here means the
// capture's role in this package is checked rather than assumed.
func assertCopilotCaptureVetoes(t *testing.T, capture string, wantVetoes bool) string {
	t.Helper()
	screen := copilotScreenCapture(t, capture)
	got := activity.ClassifyScreenState(harness.Copilot, screen)
	if wantVetoes && got.Vetoes == 0 {
		t.Fatalf("%s now raises no Copilot veto (state %s); it no longer poses the trap this test needs",
			capture, got.State)
	}
	if !wantVetoes && got.Vetoes != 0 {
		t.Fatalf("%s now raises vetoes %s; it is no longer the veto-free screen this test needs",
			capture, got.Vetoes)
	}
	return screen
}

// TestProbeCopilotTranscriptStop_ScreenRefusesTerminalSettle proves the refusal
// reaches production. Each case gives the probe terminal transcript evidence —
// a user-initiated abort, which is whole-session terminal and settles on ONE
// observation with no generation or freshness gate — so the pane's screen is
// the only thing left that can stop the synthetic 🛑. Without the wiring every
// case below settles.
//
// The notifier is wired and asserted because a false stop's real cost is not
// the icon: a stop notification is dispatched asynchronously with no
// cancellable pending entry, so a pane told "Session stopped" while it waits on
// a permission answer cannot be un-told.
func TestProbeCopilotTranscriptStop_ScreenRefusesTerminalSettle(t *testing.T) {
	cases := []struct {
		name    string
		capture string
		// readErr models Kitty failing the capture outright. It refuses for the
		// same fail-closed reason a veto does: a screen that cannot be consulted
		// is not a screen that agreed.
		readErr bool
	}{
		{name: "tool permission prompt", capture: copilotPermissionPromptScreen},
		{name: "folder trust prompt", capture: "copilot_trust_prompt.txt"},
		{name: "composer holding queued input", capture: "copilot_queued_input.txt"},
		{name: "live working turn", capture: "copilot_working.txt"},
		{name: "unreadable pane", readErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.readErr {
				assertCopilotCaptureVetoes(t, tc.capture, true)
			}
			synctest.Test(t, func(t *testing.T) {
				home := t.TempDir()
				writeCopilotTranscript(t, home, "sess-1", copilotAbortTranscript)
				h, fake, rec := copilotProbeHandlerNotifier(t, 42, "bolt", home)
				base := time.Now()
				copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())
				if tc.readErr {
					fake.mu.Lock()
					fake.getTextErr = errors.New("kitty get-text failed")
					fake.mu.Unlock()
				} else {
					copilotSetScreen(t, fake, 42, tc.capture)
				}

				h.ProbeCopilotTranscriptStop(base)
				synctest.Wait()

				if got := len(fake.titleCalls()); got != 0 {
					t.Fatalf("SetTabTitle calls = %d, want 0 (the pane refused the settle)", got)
				}
				if eff, visible := windowEffective(t, h, 42); eff.Icon != "bolt" || visible != "bolt" {
					t.Fatalf("effective = %+v visible %q, want bolt/bolt preserved by the screen refusal", eff, visible)
				}
				if got := rec.len(); got != 0 {
					t.Fatalf("stop notifications = %d, want 0: %+v", got, rec.snapshot())
				}
				resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
				if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok {
					t.Fatalf("TranscriptStopProbe row recorded over a refusing screen: %+v", ev)
				}
			})
		})
	}

	// A refusal RESETS the stabilization streak rather than merely skipping one
	// observation. The distinction is the whole point: the samples collected
	// before a prompt appeared described a pane that is now waiting on a human,
	// so carrying them forward would let a single later veto-free frame settle
	// on evidence gathered before the prompt existed.
	t.Run("resets a pending transcript settle", func(t *testing.T) {
		home := t.TempDir()
		writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
		h, fake := copilotProbeHandler(t, 42, "bolt", home)
		base := time.Now()
		copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

		// Sample 1 of the two an assistant.turn_end requires.
		h.ProbeCopilotTranscriptStop(base)
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective = %+v, want bolt after a single stabilization sample", eff)
		}

		// A prompt appears on the pane. The transcript has not changed at all.
		copilotSetScreen(t, fake, 42, copilotPermissionPromptScreen)
		h.ProbeCopilotTranscriptStop(base)

		// The prompt is answered and the pane is veto-free again. Had the
		// refusal merely skipped, this observation would be sample 2 and the tab
		// would settle here.
		copilotSetScreen(t, fake, 42, copilotVetoFreeScreen)
		h.ProbeCopilotTranscriptStop(base)
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective = %+v, want bolt: the refusal must have discarded the earlier sample", eff)
		}

		// The streak restarted rather than being poisoned: one more agreeing
		// observation settles as usual.
		h.ProbeCopilotTranscriptStop(base)
		if eff, visible := windowEffective(t, h, 42); eff.Icon != "stop" || visible != "stop" {
			t.Fatalf("effective = %+v visible %q, want stop once the restarted streak completes", eff, visible)
		}
	})

	// The refusal is FORWARD-ONLY. Recovery of this probe's own stranded
	// synthetic 🛑 is deliberately transcript-driven: it un-settles rather than
	// settles, so a vetoing screen must not block it — refusing there would
	// leave a pane wearing a stop its own transcript disagrees with, which is
	// the failure direction the probe exists to avoid.
	t.Run("does not block recovery of a synthetic stop", func(t *testing.T) {
		home := t.TempDir()
		writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
		h, fake := copilotProbeHandler(t, 42, "bolt", home)
		base := time.Now()
		copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

		// A veto-free pane lets the transcript settle a synthetic stop.
		settleCopilotTurnEndProbe(h, base)
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
			t.Fatalf("effective = %+v, want stop settled before the recovery leg", eff)
		}

		// The post-stat append that strands it, and a pane that now shows a
		// blocking prompt — every veto the forward path would refuse on.
		appendCopilotTranscript(t, home, "sess-1", `{"type":"assistant.turn_start","data":{"turnId":"7"}}`+"\n")
		copilotSetScreen(t, fake, 42, copilotPermissionPromptScreen)

		h.ProbeCopilotTranscriptStop(base)

		if eff, visible := windowEffective(t, h, 42); eff.Icon != "bolt" || visible != "bolt" {
			t.Fatalf("effective = %+v visible %q, want bolt: recovery is transcript-driven and the screen must not veto it",
				eff, visible)
		}
	})
}

// TestProbeCopilotTranscriptStop_ScreenAllowsVetoFreeTerminalSettle is the
// positive twin, and the guard that keeps the refusal from becoming a blanket
// one. A finished Copilot turn renders no completion marker of any kind — its
// screen is just output plus the ordinary idle footer — so the veto-free
// reading is the common case, and a wrong blocking_prompt or composer_line
// clause would refuse EVERY Copilot settle and strand panes on a permanent ⚡,
// which is worse than the stuck bolt this probe exists to fix.
//
// It also asserts the pane was actually READ. Without that, deleting the
// wiring entirely would leave this test passing: a probe that never consults
// the screen settles just as happily as one that consults a screen with nothing
// to say.
func TestProbeCopilotTranscriptStop_ScreenAllowsVetoFreeTerminalSettle(t *testing.T) {
	assertCopilotCaptureVetoes(t, copilotVetoFreeScreen, false)
	synctest.Test(t, func(t *testing.T) {
		home := t.TempDir()
		writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
		h, fake, rec := copilotProbeHandlerNotifier(t, 42, "bolt", home)
		base := time.Now()
		copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

		var reads []string
		fake.mu.Lock()
		fake.beforeGetText = func(match, extent string) {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if extent == "screen" {
				reads = append(reads, match)
			}
		}
		fake.mu.Unlock()

		settleCopilotTurnEndProbe(h, base)
		synctest.Wait()

		if eff, visible := windowEffective(t, h, 42); eff.Icon != "stop" || visible != "stop" {
			t.Fatalf("effective = %+v visible %q, want stop/stop: a veto-free pane must not refuse", eff, visible)
		}
		if calls := fake.titleCalls(); len(calls) != 1 || calls[0].title != palette.BuildTitle("W:api", "stop", false) {
			t.Fatalf("SetTabTitle calls = %+v, want one stop title", calls)
		}
		calls := rec.snapshot()
		if len(calls) != 1 || calls[0].subtitle != "Session stopped" {
			t.Fatalf("stop notifications = %+v, want exactly one %q", calls, "Session stopped")
		}
		resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
		if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); !ok || ev.Action != "stop" {
			t.Fatalf("TranscriptStopProbe row = %+v ok=%v, want stop", ev, ok)
		}

		fake.mu.Lock()
		got := append([]string(nil), reads...)
		fake.mu.Unlock()
		if len(got) == 0 {
			t.Fatal("the probe settled without reading the pane at all; the screen refusal is not wired in")
		}
		for i, match := range got {
			if match != kitty.MatchWindowByID(42) {
				t.Fatalf("screen read %d = %q, want the Copilot producer pane 42", i, match)
			}
		}
	})
}
