package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestProbeCodexScreenPrompts_IgnoresNonCodexManagedWindows(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexEditApprovalScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0", len(resp.Events))
	}
}

// TestProbeCodexScreenPrompts_ConcurrentPromptInvalidatesStopProbe proves the
// captured-sequence guard: a fresh prompt landing during the (slow) screen read
// advances the reducer receipt past the value the probe captured, so the stale
// completed-turn read is discarded and the newer interactive bolt is preserved.
func TestProbeCodexScreenPrompts_ConcurrentPromptInvalidatesStopProbe(t *testing.T) {
	h := newTestHandler()
	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexCompletedTurnScreen,
		},
	}
	codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	// The prompt classifier and the idle classifier share each tick's single
	// screen capture, so there is exactly one GetText per pass to hang the
	// injection on. Seeding a fresh prompt during it models a concurrent
	// UserPromptSubmit landing after the probe captured the receipt sequence and
	// before the idle evidence is applied — the receipt has already moved by the
	// time the idle path rechecks it.
	//
	// It fires on EVERY read, not just the first: the probe drives a whole
	// stabilization run below, and a run of agreeing completed-turn screens would
	// otherwise settle on its last pass, leaving the receipt guard untested.
	var screenReads int
	fake.beforeGetText = func(match, extent string) {
		if match != kitty.MatchWindowByID(42) || extent != "screen" {
			return
		}
		screenReads++
		codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix()+int64(screenReads))
	}
	h.SetKittyClient(fake)

	// A whole run, so the receipt guard — not an unfinished sample count — is
	// what holds the bolt.
	probeCodexUntilSettled(t, h, base)

	if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want the newer bolt preserved against a stale stop probe", eff)
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, screenStopProbeEvent); ok {
		t.Fatalf("ScreenStopProbe row recorded despite a concurrent prompt: %+v", ev)
	}
}

// TestProbeCodexScreenPrompt_DiscardsObservationAfterConcurrentReducerEvent
// pins the window-wide receipt contract for the prompt-classification path: a
// reducer event landing during the (slow) off-lock screen read advances the
// receipt past the value the probe captured, so the complete observation is
// discarded. A discarded observation performs no reducer, guard, audit,
// renderer, or notification mutation — a concurrent prompt, terminal, error,
// or clear always outranks the stale screen prompt.
func TestProbeCodexScreenPrompt_DiscardsObservationAfterConcurrentReducerEvent(t *testing.T) {
	cases := []struct {
		name string
		// inject lands a concurrent reducer event while the probe's screen read
		// (which classifies as a request_user_input prompt) is in flight.
		inject func(t *testing.T, h *Handler, at int64)
		// wantIcon is the concurrent event's reduced icon, which must survive.
		wantIcon string
		// wantGuard is the concurrent event's guard entry, which the discarded
		// probe must not replace with its own prompt guard.
		wantGuard string
		// wantSessionAcked is whether the concurrent event itself retired the
		// seeded interactive fact; the stale prompt (applied with Prompt: true)
		// must never acknowledge a newer terminal fact on its own.
		wantSessionAcked bool
	}{
		{
			name: "prompt",
			inject: func(t *testing.T, h *Handler, at int64) {
				codexInjectSessionEvent(t, h, 42, "sess-1", "active", "UserPromptSubmit", at)
			},
			wantIcon:  "bolt",
			wantGuard: "active",
		},
		{
			name: "terminal",
			inject: func(t *testing.T, h *Handler, at int64) {
				codexInjectSessionEvent(t, h, 42, "sess-1", "stop", "Stop", at)
			},
			wantIcon:  "stop",
			wantGuard: "stop",
		},
		{
			name: "error",
			inject: func(t *testing.T, h *Handler, at int64) {
				codexInjectSessionEvent(t, h, 42, "sess-1", "stop_failure", "Stop", at)
			},
			wantIcon:  "error",
			wantGuard: "stop_failure",
		},
		{
			name: "clear",
			inject: func(t *testing.T, h *Handler, at int64) {
				resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 42, Action: "user_clear", Harness: "codex"})
				if !resp.OK {
					t.Fatalf("inject user_clear rejected: %+v", resp)
				}
			},
			wantIcon:         "",
			wantGuard:        "",
			wantSessionAcked: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				guard := NewStateGuard(time.Hour)
				// Permission notifications enabled so an applied (non-discarded)
				// prompt WOULD dispatch; stop_failure disabled so the injected
				// error event cannot dispatch on its own.
				no := false
				notifier, rec := newTestNotifier(&config.NotificationConfig{Permission: true, StopFailure: &no})
				h := NewHandler(guard, notifier)
				base := time.Now()
				fake := &fakeKittyClient{
					windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
						Profile: "work",
						Harness: "codex",
					}),
					getText: map[string]string{
						kitty.MatchWindowByID(42): codexRequestUserInputScreen,
					},
				}
				codexSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

				// Land the concurrent reducer event during the prompt probe's
				// screen read: after the probe captured the receipt, before it
				// applies the classified prompt.
				injected := false
				fake.beforeGetText = func(match, extent string) {
					if injected || match != kitty.MatchWindowByID(42) || extent != "screen" {
						return
					}
					injected = true
					tc.inject(t, h, base.Unix()+1)
				}
				h.SetKittyClient(fake)

				h.ProbeCodexScreenPrompts(base)
				synctest.Wait()

				// The concurrent event's reduced state stands.
				if eff, _ := windowEffective(t, h, 42); eff.Icon != tc.wantIcon {
					t.Fatalf("effective icon = %q, want %q (concurrent %s must outrank the stale screen prompt)",
						eff.Icon, tc.wantIcon, tc.name)
				}
				// No reducer mutation: the discarded prompt left no probe fact and
				// did not acknowledge the session's own fact.
				h.stateMu.Lock()
				probeFact := factOf(h.store[42].Reducer, protocol.SourceID{Kind: protocol.SourceProbe, Key: screenPromptProbeSource})
				sessionFact := factOf(h.store[42].Reducer, protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:sess-1"})
				h.stateMu.Unlock()
				if probeFact != nil {
					t.Fatalf("discarded observation left a probe fact: %+v", probeFact)
				}
				if sessionFact == nil || sessionFact.Acked != tc.wantSessionAcked {
					t.Fatalf("session fact = %+v, want acked=%v (only the concurrent event may retire it)",
						sessionFact, tc.wantSessionAcked)
				}
				// No guard mutation: the concurrent event's guard entry survives.
				if got := h.guard.Current(42); got != tc.wantGuard {
					t.Fatalf("guard.Current = %q, want %q", got, tc.wantGuard)
				}
				// No audit mutation: not even a suppressed ScreenPromptProbe row.
				resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
				for _, ev := range resp.Events {
					if ev.Event == screenPromptProbeEvent {
						t.Fatalf("discarded observation recorded an audit row: %+v", ev)
					}
				}
				// No renderer mutation from the stale prompt: the title never
				// shows the discarded question icon.
				stale := palette.BuildTitle("W:api", "question", false)
				for _, call := range fake.titleCalls() {
					if call.title == stale {
						t.Fatalf("discarded observation rendered the stale prompt icon %q", call.title)
					}
				}
				// No notification for the discarded prompt (or anything else).
				if got := rec.len(); got != 0 {
					t.Fatalf("dispatches = %d; want 0 after a discarded observation", got)
				}
			})
		})
	}
}
