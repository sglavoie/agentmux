package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const claudeContextLimitFixtureFile = "claude_context_limit_full.txt"

func loadClaudeContextLimitFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "contextlimit", "testdata", claudeContextLimitFixtureFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return string(data)
}

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
	notified := h.store[42].ContextLimitNotified
	lastAction := h.store[42].LastAction
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

// TestProbeClaudeContextLimit_PublishesReducerErrorAndPreservesItAcrossIdle pins
// the F1 repair: a detected context-limit banner applies a SourceProbe
// stop_failure fact THROUGH the reducer — advancing the window-wide receipt
// generation (ShadowSeq) and publishing the actual reduced result — so the
// rendered error stays consistent with the authoritative reducer and durable
// shadow (DesiredIcon, Effective, and the exported Shadow effective fields all
// agree). A passive idle after detection preserves the probe-owned error, while
// an explicit user acknowledgement (user_clear) and a new prompt both clear it
// through the normal reducer recovery paths.
func TestProbeClaudeContextLimit_PublishesReducerErrorAndPreservesItAcrossIdle(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:      100,
					Title:   palette.BuildTitle("api", "bolt", false),
					Windows: []kitty.Window{{ID: 42, UserVars: kitty.UserVars{Profile: "work", Harness: "claude"}}},
				},
				{
					ID:      101,
					Title:   palette.BuildTitle("web", "bolt", false),
					Windows: []kitty.Window{{ID: 43, UserVars: kitty.UserVars{Profile: "work", Harness: "claude"}}},
				},
			},
		}},
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
			kitty.MatchWindowByID(43): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	now := time.Unix(1_700_000_000, 0)
	h.ProbeClaudeContextLimit(now)

	probeSource := SourceID{Kind: SourceProbe, Key: claudeContextLimitFactSource}

	h.stateMu.Lock()
	st := h.store[42]
	seqAfterProbe := st.ShadowSeq
	reduced := st.Reducer.Reduce(now.Unix())
	desired := st.DesiredIcon
	stEffective := st.Effective.Icon
	shadowIcon := h.windowMetadata[42].Shadow.EffectiveIcon
	h.stateMu.Unlock()

	if seqAfterProbe == 0 {
		t.Fatalf("ShadowSeq = 0 after probe; want a bumped receipt generation")
	}
	// The banner is a real reducer fact, not a bare scalar write.
	if reduced.Icon != "error" || reduced.Source != probeSource {
		t.Fatalf("reduced = %+v, want error owned by %+v", reduced, probeSource)
	}
	// The published visible state agrees across every consumer.
	if desired != "error" || stEffective != "error" || shadowIcon != "error" {
		t.Fatalf("inconsistent published state: DesiredIcon=%q Effective=%q Shadow=%q, want all error",
			desired, stEffective, shadowIcon)
	}

	// A passive idle from another source must not withdraw the probe-owned error.
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "idle", Harness: "claude"})
	if !resp.OK {
		t.Fatalf("idle state update failed: %s", resp.Error)
	}
	h.stateMu.Lock()
	afterIdleEffective := h.store[42].Effective.Icon
	afterIdleDesired := h.store[42].DesiredIcon
	h.stateMu.Unlock()
	if afterIdleEffective != "error" || afterIdleDesired != "error" {
		t.Fatalf("after passive idle: Effective=%q DesiredIcon=%q, want error preserved", afterIdleEffective, afterIdleDesired)
	}

	// An explicit user acknowledgement clears the sticky error.
	resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "user_clear", Harness: "claude"})
	if !resp.OK {
		t.Fatalf("user_clear state update failed: %s", resp.Error)
	}
	h.stateMu.Lock()
	afterClear := h.store[42].Effective.Icon
	h.stateMu.Unlock()
	if afterClear != "" {
		t.Fatalf("after user_clear: Effective=%q, want cleared", afterClear)
	}

	// A new prompt on the sibling window is authoritative recovery: it
	// acknowledges the sticky error and the session's ⚡ returns.
	resp = h.Handle(Request{
		Type:         TypeStateUpdate,
		WindowID:     43,
		Action:       "active",
		Event:        "UserPromptSubmit",
		IsPrompt:     true,
		Harness:      "claude",
		SessionTitle: "web",
	})
	if !resp.OK {
		t.Fatalf("prompt state update failed: %s", resp.Error)
	}
	h.stateMu.Lock()
	afterPrompt := h.store[43].Effective.Icon
	h.stateMu.Unlock()
	if afterPrompt != "bolt" {
		t.Fatalf("after new prompt: Effective=%q, want bolt (error cleared)", afterPrompt)
	}
}

func TestProbeClaudeContextLimit_EmptyStateSwapsToError(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "", false), kitty.UserVars{
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
	if want := palette.BuildTitle("api", "error", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}

	resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(resp.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.Events))
	}
	ev := resp.Events[0]
	if ev.Action != "stop_failure" || ev.ResultIconState != "error" || ev.CurrentIconState != "" {
		t.Fatalf("event = %+v, want stop_failure (empty)->error", ev)
	}
	if !ev.ContextLimit {
		t.Fatalf("event.ContextLimit = false, want true")
	}

	h.stateMu.Lock()
	notified := h.store[42].ContextLimitNotified
	h.stateMu.Unlock()
	if !notified {
		t.Fatalf("contextLimitNotified[42] = false, want true")
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

func TestProbeClaudeContextLimit_PreservesLoopMarkerAndHarnessEmoji(t *testing.T) {
	h := newTestHandler()
	tabTitle := palette.BuildTitleParts(palette.TitleParts{
		Base:    "🦊api",
		State:   "bolt",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, tabTitle, kitty.UserVars{
			Profile: "work",
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
	want := palette.BuildTitleParts(palette.TitleParts{
		Base:    "🦊api",
		State:   "error",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	if calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
}

func TestProbeClaudeContextLimit_SplitTabOwnerStillDetectsContextLimit(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeSplitWindows(
			palette.BuildTitle("api", "bolt", false),
			kitty.Window{
				ID:       42,
				IsActive: true,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
			},
			kitty.Window{
				ID:       43,
				UserVars: kitty.UserVars{Profile: "scratch", Harness: "claude"},
			},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
			kitty.MatchWindowByID(43): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

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

	if resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}); len(resp.Events) != 1 {
		t.Fatalf("owner events = %d, want 1", len(resp.Events))
	}
	if resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43}); len(resp.Events) != 0 {
		t.Fatalf("non-owner events = %d, want 0", len(resp.Events))
	}
}

func TestProbeClaudeContextLimit_SplitTabNonOwnerCannotOverwriteManagedOwner(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeSplitWindows(
			palette.BuildTitle("api", "bolt", false),
			kitty.Window{
				ID:       42,
				IsActive: true,
				UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
			},
			kitty.Window{
				ID:       43,
				UserVars: kitty.UserVars{Profile: "scratch", Harness: "claude"},
			},
		),
		getText: map[string]string{
			kitty.MatchWindowByID(42): "ordinary shell output\n",
			kitty.MatchWindowByID(43): claudeContextLimitScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeClaudeContextLimit(time.Unix(1_700_000_000, 0))

	if got := len(fake.titleCalls()); got != 0 {
		t.Fatalf("SetTabTitle calls = %d, want 0", got)
	}
	if resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42}); len(resp.Events) != 0 {
		t.Fatalf("owner events = %d, want 0", len(resp.Events))
	}
	if resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 43}); len(resp.Events) != 0 {
		t.Fatalf("non-owner events = %d, want 0", len(resp.Events))
	}
}

func TestProbeClaudeContextLimit_UnmanagedClaudeForegroundNoOp(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeForegroundWindows(
			42,
			palette.BuildTitle("api", "bolt", false),
			[]string{"/opt/homebrew/bin/claude"},
		),
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
	h.stateMu.Lock()
	st, ok := h.store[42]
	notified := ok && st.ContextLimitNotified
	h.stateMu.Unlock()
	if notified {
		t.Fatalf("contextLimitNotified[42] = true, want false")
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

// TestProbeClaudeContextLimit_PreRecordedPipeModeReachesNotifier pins the Bug 5
// fix: the probe must capture lastPipeMode BEFORE recordClaudeContextLimitProbeStateLocked
// resets it to false, so MaybeNotify sees the value hooks last reported. A
// pre-recorded pipe-mode window therefore gets the ❗ icon swap but no dispatch.
func TestProbeClaudeContextLimit_PreRecordedPipeModeReachesNotifier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, fake, rec := newClaudeContextLimitProbeTestHandler()
		fake.windows = codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		})
		fake.getText = map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		}
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).LastPipeMode = true
		h.stateMu.Unlock()

		h.ProbeClaudeContextLimit(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("pipe-mode window dispatches = %d; want 0 (pre-recorded pipeMode must reach MaybeNotify)", got)
		}
		if got := len(fake.titleCalls()); got != 1 {
			t.Fatalf("SetTabTitle calls = %d, want 1 (icon swap is independent of notification)", got)
		}
	})
}

// TestProbeClaudeContextLimit_DiscardsObservationAfterConcurrentReducerEvent
// pins the window-wide receipt contract for the context-limit path: a reducer
// event landing during the (slow) off-lock screen read advances the receipt
// past the value the probe captured, so the complete banner observation is
// discarded. A discarded observation performs no reducer, guard, audit,
// renderer, or notification mutation — a concurrent prompt, terminal, error,
// or clear always outranks the stale banner evidence, and in particular a
// stale read can never reassert an error after a prompt or a /clear.
func TestProbeClaudeContextLimit_DiscardsObservationAfterConcurrentReducerEvent(t *testing.T) {
	cases := []struct {
		name string
		// inject is the concurrent state update landed while the probe's screen
		// read (which shows the context-limit banner) is in flight.
		inject Request
		// wantIcon is the concurrent event's reduced icon, which must survive.
		wantIcon string
		// wantGuard is the concurrent event's guard entry, which the discarded
		// probe must not replace with its own stop_failure guard.
		wantGuard string
		// wantDispatches counts notifications from the injected event alone
		// (stop_failure notifications are enabled by default); the discarded
		// probe must add none.
		wantDispatches int
		// checkNoErrorTitle asserts the renderer never painted the probe's error
		// icon (skipped when the injected event itself legitimately renders it).
		checkNoErrorTitle bool
	}{
		{
			name:              "prompt",
			inject:            Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Event: "UserPromptSubmit", IsPrompt: true, Harness: "claude"},
			wantIcon:          "bolt",
			wantGuard:         "active",
			checkNoErrorTitle: true,
		},
		{
			name:              "terminal",
			inject:            Request{Type: TypeStateUpdate, WindowID: 42, Action: "stop", Event: "Stop", Harness: "claude"},
			wantIcon:          "stop",
			wantGuard:         "stop",
			checkNoErrorTitle: true,
		},
		{
			name:           "error",
			inject:         Request{Type: TypeStateUpdate, WindowID: 42, Action: "stop_failure", Event: "Stop", Harness: "claude"},
			wantIcon:       "error",
			wantGuard:      "stop_failure",
			wantDispatches: 1,
		},
		{
			name:              "clear",
			inject:            Request{Type: TypeStateUpdate, WindowID: 42, Action: "user_clear", Harness: "claude"},
			wantIcon:          "",
			wantGuard:         "",
			checkNoErrorTitle: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, fake, rec := newClaudeContextLimitProbeTestHandler()
				fake.windows = codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
					Profile: "work",
					Harness: "claude",
				})
				fake.getText = map[string]string{
					kitty.MatchWindowByID(42): claudeContextLimitScreen,
				}
				// Seed a live interactive bolt so the window has reducer state and
				// a nonzero receipt before the probe captures it.
				seed := h.Handle(Request{Type: TypeStateUpdate, WindowID: 42, Action: "active", Event: "UserPromptSubmit", IsPrompt: true, Harness: "claude", SessionTitle: "api"})
				if !seed.OK {
					t.Fatalf("seed active rejected: %+v", seed)
				}

				// Land the concurrent reducer event during the probe's screen read:
				// after the probe captured the receipt, before it applies the banner.
				injected := false
				fake.beforeGetText = func(match, extent string) {
					if injected || match != kitty.MatchWindowByID(42) || extent != "screen" {
						return
					}
					injected = true
					if resp := h.Handle(tc.inject); !resp.OK {
						t.Errorf("inject %s rejected: %+v", tc.name, resp)
					}
				}

				h.ProbeClaudeContextLimit(time.Now())
				synctest.Wait()

				// The concurrent event's reduced state stands: the stale banner
				// never reasserts the error.
				if eff, _ := windowEffective(t, h, 42); eff.Icon != tc.wantIcon {
					t.Fatalf("effective icon = %q, want %q (concurrent %s must outrank the stale banner)",
						eff.Icon, tc.wantIcon, tc.name)
				}
				// No reducer mutation: the discarded banner left no probe fact and
				// never marked the stall episode as handled.
				h.stateMu.Lock()
				probeFact := h.store[42].Reducer.facts[SourceID{Kind: SourceProbe, Key: claudeContextLimitFactSource}]
				notified := h.store[42].ContextLimitNotified
				h.stateMu.Unlock()
				if probeFact != nil {
					t.Fatalf("discarded observation left a probe fact: %+v", probeFact)
				}
				if notified {
					t.Fatalf("contextLimitNotified = true after a discarded observation, want false")
				}
				// No guard mutation: the concurrent event's guard entry survives.
				if got := h.guard.Current(42); got != tc.wantGuard {
					t.Fatalf("guard.Current = %q, want %q", got, tc.wantGuard)
				}
				// No audit mutation: no ScreenContextLimitProbe row at all.
				resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
				for _, ev := range resp.Events {
					if ev.Event == claudeContextLimitProbeEvent {
						t.Fatalf("discarded observation recorded an audit row: %+v", ev)
					}
				}
				// No renderer mutation from the stale banner.
				if tc.checkNoErrorTitle {
					stale := palette.BuildTitle("api", "error", false)
					for _, call := range fake.titleCalls() {
						if call.title == stale {
							t.Fatalf("discarded observation rendered the stale error icon %q", call.title)
						}
					}
				}
				// No notification beyond what the injected event itself dispatched.
				if got := rec.len(); got != tc.wantDispatches {
					t.Fatalf("dispatches = %d; want %d (discarded observation must add none)", got, tc.wantDispatches)
				}
			})
		})
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
	st, ok := h.store[42]
	notified := ok && st.ContextLimitNotified
	h.stateMu.Unlock()
	if notified {
		t.Fatalf("contextLimitNotified[42] = true, want false")
	}
}

func TestProbeClaudeContextLimit_RealScreen_BoltToError(t *testing.T) {
	fixture := loadClaudeContextLimitFixture(t)

	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): fixture,
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
	notified := h.store[42].ContextLimitNotified
	lastAction := h.store[42].LastAction
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

func TestProbeClaudeContextLimit_RealScreen_EmptyToError(t *testing.T) {
	fixture := loadClaudeContextLimitFixture(t)

	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("api", "", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): fixture,
		},
	}
	h.SetKittyClient(fake)

	now := time.Unix(1_700_000_000, 0)
	h.ProbeClaudeContextLimit(now)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
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
	if ev.Action != "stop_failure" || ev.ResultIconState != "error" || ev.CurrentIconState != "" {
		t.Fatalf("event = %+v, want stop_failure (empty)->error", ev)
	}
	if !ev.ContextLimit {
		t.Fatalf("event.ContextLimit = false, want true")
	}

	h.stateMu.Lock()
	notified := h.store[42].ContextLimitNotified
	h.stateMu.Unlock()
	if !notified {
		t.Fatalf("contextLimitNotified[42] = false, want true")
	}
}
