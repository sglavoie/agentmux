package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// newClaudeContextLimitProbeTestHandler builds a Handler whose notifier
// records dispatches via callRecorder so probe tests can assert that
// MaybeNotify fired exactly once per stall episode.
func newClaudeContextLimitProbeTestHandler() (*Handler, *fakeKittyClient, *callRecorder) {
	guard := NewStateGuard(time.Hour)
	notifier, rec := newTestNotifier(&config.NotificationConfig{})
	h := NewHandler(guard, notifier)
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	return h, fake, rec
}

const claudeContextLimitScreen = `earlier line

Context limit reached · /compact or /clear to continue

✻ Cooked for 17m 6s
`

func TestProbeClaudeContextLimit_BoltSwapsToError(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	now := time.Unix(1_700_000_000, 0)
	h.ProbeClaudeContextLimit(now)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if calls[0].match != kitty.MatchTabByWindowID(42) {
		t.Fatalf("SetTabTitle match = %q, want %q", calls[0].match, kitty.MatchTabByWindowID(42))
	}
	if want := palette.BuildTitle("api", "error", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.Event != claudeContextLimitProbeEvent || ev.Source != claudeContextLimitProbeSource {
		t.Fatalf("event identity = (%q, %q), want (%q, %q)", ev.Event, ev.Source, claudeContextLimitProbeEvent, claudeContextLimitProbeSource)
	}
	if ev.Action != "stop_failure" || ev.ResultIconState != "error" || ev.CurrentIconState != "bolt" {
		t.Fatalf("event = %+v, want stop_failure bolt->error", ev)
	}
	if !ev.ContextLimit {
		t.Fatalf("event.ContextLimit = false, want true")
	}

	h.stateMu.Lock()
	meta := h.windowMetadata[42]
	notified := h.contextLimitNotified[42]
	lastAction := h.lastAction[42]
	h.stateMu.Unlock()
	if meta.Durable.Harness != "claude" || meta.Durable.Profile != "work" || meta.Durable.LastAction != "stop_failure" {
		t.Fatalf("durable metadata = %+v, want claude/work/stop_failure", meta.Durable)
	}
	if !notified {
		t.Fatalf("contextLimitNotified[42] = false, want true")
	}
	if lastAction != "stop_failure" {
		t.Fatalf("lastAction[42] = %q, want stop_failure", lastAction)
	}
}

func TestProbeClaudeContextLimit_SubagentSwapsToError(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "subagent", false), kitty.UserVars{
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("api", "error", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
}

func TestProbeClaudeContextLimit_DedupesWithinEpisode(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	now := time.Unix(1_700_000_000, 0)
	h.ProbeClaudeContextLimit(now)
	// Simulate the title actually changing on the next tab listing so the
	// probe's title-overwrite guard reflects reality.
	fake.mu.Lock()
	fake.windows[0].Tabs[0].Title = palette.BuildTitle("api", "error", false)
	fake.mu.Unlock()
	h.ProbeClaudeContextLimit(now.Add(2 * time.Second))

	if got := len(fake.titleCalls()); got != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1 (dedup should skip the second call)", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1 (dedup should skip the second event)", len(resp.Events))
	}
}

func TestProbeClaudeContextLimit_NoMarker_NoOp(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): "ordinary shell output\n",
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0", len(resp.Events))
	}
	h.stateMu.Lock()
	notified := h.contextLimitNotified[42]
	h.stateMu.Unlock()
	if notified {
		t.Fatalf("contextLimitNotified[42] = true, want false")
	}
}

func TestProbeClaudeContextLimit_IgnoresNonClaudeHarnesses(t *testing.T) {
	for _, harnessName := range []string{"codex", "copilot"} {
		t.Run(harnessName, func(t *testing.T) {
			h := newTestHandler()
			fake := &fakeKittyClient{
				windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
					Harness: harnessName,
				}),
				getText: map[string]string{
					kitty.MatchWindowByID(42): claudeContextLimitScreen,
				},
			}
			h.SetKittyClient(fake)

			h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

			if got := len(fake.titleCalls()); got != 0 {
				t.Fatalf("SetTabTitle calls = %d, want 0", got)
			}
			resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
			if len(resp.Events) != 0 {
				t.Fatalf("events = %d, want 0", len(resp.Events))
			}
		})
	}
}

func TestProbeClaudeContextLimit_IgnoresProtectedIcons(t *testing.T) {
	protected := []string{"", "stop", "warning", "error", "key", "question", "compact"}
	for _, state := range protected {
		t.Run("state="+state, func(t *testing.T) {
			h := newTestHandler()
			fake := &fakeKittyClient{
				windows: codexProbeWindows(42, palette.BuildTitle("api", state, false), kitty.UserVars{
					Harness: "claude",
				}),
				getText: map[string]string{
					kitty.MatchWindowByID(42): claudeContextLimitScreen,
				},
			}
			h.SetKittyClient(fake)

			h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

			if got := len(fake.titleCalls()); got != 0 {
				t.Fatalf("SetTabTitle calls = %d, want 0", got)
			}
			resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
			if len(resp.Events) != 0 {
				t.Fatalf("events = %d, want 0", len(resp.Events))
			}
		})
	}
}

func TestProbeClaudeContextLimit_NilKittyClient_NoPanic(t *testing.T) {
	h := newTestHandler()
	// Do NOT call SetKittyClient — h.kittyClient stays nil.

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))
	// Reaching this point without panic is the assertion.
}

func TestProbeClaudeContextLimit_DisabledByConfigSkipsProbe(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)
	h.SetClaudeContextLimitProbe(false)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0 (probe should be a no-op when disabled)", got)
	}
	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 0 {
		t.Fatalf("events = %d, want 0", len(resp.Events))
	}
}

func TestProbeClaudeContextLimit_NotifiesOncePerEpisode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newClaudeContextLimitProbeTestHandler()
		fake.windows = codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		})
		fake.getText = map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		}

		now := time.Now()
		h.ProbeClaudeContextLimit(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("first probe dispatches = %d; want 1", got)
		}
		got := rec.snapshot()[0]
		if got.windowID != 42 {
			t.Errorf("dispatch windowID = %d; want 42", got.windowID)
		}
		if got.account != "work" {
			t.Errorf("dispatch account = %q; want work", got.account)
		}
		if got.title != "api" {
			t.Errorf("dispatch title = %q; want api", got.title)
		}
		if got.subtitle != "Session stopped (failure)" {
			t.Errorf("dispatch subtitle = %q; want %q", got.subtitle, "Session stopped (failure)")
		}

		// Second tick within the same stall episode must not fire again.
		fake.mu.Lock()
		fake.windows[0].Tabs[0].Title = palette.BuildTitle("api", "error", false)
		fake.mu.Unlock()
		h.ProbeClaudeContextLimit(now.Add(2 * time.Second))
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("second probe total dispatches = %d; want 1 (dedup should suppress)", got)
		}
	})
}

// TestProbeClaudeContextLimit_RecoveryReArmsNotifier verifies that once an
// active-class hook event lands on a window currently flagged as
// context-limit-notified, the dedup flag is cleared and a subsequent
// stall fires a fresh notification.
func TestProbeClaudeContextLimit_RecoveryReArmsNotifier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newClaudeContextLimitProbeTestHandler()
		fake.windows = codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		})
		fake.getText = map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		}

		now := time.Now()
		h.ProbeClaudeContextLimit(now)
		synctest.Wait()
		if got := rec.len(); got != 1 {
			t.Fatalf("first stall dispatches = %d; want 1", got)
		}

		// In production the stop_failure guard expires after
		// defaults.guard_duration (~1s) before the next hook event lands. The
		// test handler uses a 1h guard duration to avoid timing flakiness, so
		// clear the guard explicitly to simulate that natural expiry. Without
		// this the priority-4 stop_failure guard would block the priority-1
		// active and the recovery branch would never run.
		h.guard.Clear(42)

		// User runs /compact or /clear; Claude resumes and a new hook event
		// for the same window lands with action=active. That should clear
		// contextLimitNotified so a future stall can re-notify.
		resp := h.Handle(Request{
			Type:         TypeStateUpdate,
			WindowID:     42,
			Action:       "active",
			Event:        "UserPromptSubmit",
			IsPrompt:     true,
			Account:      "work",
			Harness:      "claude",
			SessionTitle: "api",
		})
		if !resp.OK {
			t.Fatalf("active state update failed: %s", resp.Error)
		}

		h.stateMu.Lock()
		notified := h.contextLimitNotified[42]
		h.stateMu.Unlock()
		if notified {
			t.Fatalf("contextLimitNotified[42] = true after active recovery; want false")
		}

		// Simulate the tab title flipping back to bolt as Claude resumes.
		fake.mu.Lock()
		fake.windows[0].Tabs[0].Title = palette.BuildTitle("api", "bolt", false)
		fake.mu.Unlock()

		// Advance past the notifier's 10s per-(window, action) cooldown so the
		// second dispatch is not silently dropped by the cooldown map.
		time.Sleep(11 * time.Second)
		h.ProbeClaudeContextLimit(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 2 {
			t.Fatalf("second stall dispatches = %d; want 2 (re-arm should fire fresh notification)", got)
		}
	})
}

// TestProbeClaudeContextLimit_UserClearReArmsNotifier verifies that an
// explicit user_clear action through the TUI also resets the dedup flag,
// mirroring the terminal/cleanup branch in handler_state_update.go.
func TestProbeClaudeContextLimit_UserClearReArmsNotifier(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	h.stateMu.Lock()
	notified := h.contextLimitNotified[42]
	h.stateMu.Unlock()
	if !notified {
		t.Fatalf("contextLimitNotified[42] = false after probe; want true")
	}

	resp := h.Handle(Request{
		Type:     TypeStateUpdate,
		WindowID: 42,
		Action:   "user_clear",
		Account:  "work",
		Harness:  "claude",
	})
	if !resp.OK {
		t.Fatalf("user_clear state update failed: %s", resp.Error)
	}

	h.stateMu.Lock()
	notified = h.contextLimitNotified[42]
	h.stateMu.Unlock()
	if notified {
		t.Fatalf("contextLimitNotified[42] = true after user_clear; want false")
	}
}

// TestProbeClaudeContextLimit_ClosedWindowGCDropsFlag verifies that when
// pruneClosedWindows runs against a live-set that no longer contains the
// probed window, the contextLimitNotified entry is GC'd alongside the rest
// of the per-window state.
func TestProbeClaudeContextLimit_ClosedWindowGCDropsFlag(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	h.stateMu.Lock()
	notified := h.contextLimitNotified[42]
	h.stateMu.Unlock()
	if !notified {
		t.Fatalf("contextLimitNotified[42] = false after probe; want true")
	}

	// Kitty no longer reports window 42 → pruneClosedWindows should drop it.
	h.pruneClosedWindows(map[int]bool{})

	h.stateMu.Lock()
	_, stillThere := h.contextLimitNotified[42]
	h.stateMu.Unlock()
	if stillThere {
		t.Fatalf("contextLimitNotified[42] survived pruneClosedWindows; want GC'd")
	}
}
