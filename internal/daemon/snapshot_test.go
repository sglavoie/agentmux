package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestSnapshotStore_PublishAndLoad(t *testing.T) {
	var s snapshotStore
	if _, gen, ready := s.load(); ready || gen != 0 {
		t.Fatalf("cold cache = (gen %d, ready %v); want (0, false)", gen, ready)
	}
	windows := fakeOSWindowsWithTab(42, "api")
	s.publish(windows, time.Unix(1_700_000_000, 0))
	got, gen, ready := s.load()
	if !ready || gen != 1 || len(got) != 1 {
		t.Fatalf("after publish = (gen %d, ready %v, windows %d); want (1, true, 1)", gen, ready, len(got))
	}
	s.publish(windows, time.Unix(1_700_000_001, 0))
	if _, gen, _ := s.load(); gen != 2 {
		t.Fatalf("second publish gen = %d; want 2", gen)
	}
}

func TestSnapshotStore_ScreenProbeEpochsAreTabScoped(t *testing.T) {
	var s snapshotStore
	base := time.Unix(1_700_000_000, 0)
	windows := screenProbeEpochWindows("first", "second", false, "")
	s.publish(windows, base)

	_, _, _, firstProbeTabs, ready := s.loadForScreenProbes()
	if !ready {
		t.Fatal("first screen-probe snapshot is not ready")
	}
	firstTabEpoch := firstProbeTabs[100].epoch
	secondTabEpoch := firstProbeTabs[101].epoch
	if firstTabEpoch == 0 || secondTabEpoch == 0 || firstTabEpoch == secondTabEpoch {
		t.Fatalf("initial tab epochs = first %d second %d; want distinct non-zero epochs", firstTabEpoch, secondTabEpoch)
	}

	s.publish(screenProbeEpochWindows("first", "second", false, ""), base.Add(time.Second))
	_, _, _, unchangedProbeTabs, _ := s.loadForScreenProbes()
	if got := unchangedProbeTabs[100].epoch; got != firstTabEpoch {
		t.Fatalf("identical publish advanced first tab epoch %d -> %d", firstTabEpoch, got)
	}
	if got := unchangedProbeTabs[101].epoch; got != secondTabEpoch {
		t.Fatalf("identical publish advanced second tab epoch %d -> %d", secondTabEpoch, got)
	}

	s.publish(screenProbeEpochWindows("first changed", "second", false, ""), base.Add(2*time.Second))
	_, _, _, changedFirstProbeTabs, _ := s.loadForScreenProbes()
	if got := changedFirstProbeTabs[100].epoch; got == firstTabEpoch {
		t.Fatalf("changed first tab retained epoch %d", got)
	}
	if got := changedFirstProbeTabs[101].epoch; got != secondTabEpoch {
		t.Fatalf("first-tab change advanced unrelated second tab epoch %d -> %d", secondTabEpoch, got)
	}

	s.publish(screenProbeEpochWindows("first changed", "second", true, "1"), base.Add(3*time.Second))
	_, _, _, changedSecondProbeTabs, _ := s.loadForScreenProbes()
	if got := changedSecondProbeTabs[101].epoch; got == secondTabEpoch {
		t.Fatalf("second-tab focus/mute change retained epoch %d", got)
	}
}

func screenProbeEpochWindows(firstTitle, secondTitle string, secondActive bool, secondMute string) []kitty.OSWindow {
	return []kitty.OSWindow{{
		ID:        1,
		IsActive:  true,
		IsFocused: true,
		Tabs: []kitty.Tab{
			{
				ID:       100,
				IsActive: true,
				Title:    firstTitle,
				Windows: []kitty.Window{{
					ID:       41,
					IsActive: true,
					UserVars: kitty.UserVars{Profile: "work", Harness: "codex"},
				}},
			},
			{
				ID:    101,
				Title: secondTitle,
				Windows: []kitty.Window{{
					ID:       42,
					IsActive: secondActive,
					UserVars: kitty.UserVars{Profile: "work", Harness: "codex", Mute: secondMute},
				}},
			},
		},
	}}
}

// TestHookRoutingReadsCacheNotLiveList proves the hook path resolves ownership
// from the published snapshot and never calls ListWindows: after priming the
// cache, the live client is set to fail every list, yet a hook still enriches
// correctly from the cache (current icon, profile, harness).
func TestHookRoutingReadsCacheNotLiveList(t *testing.T) {
	h := newTestHandler()
	fake := &fakeKittyClient{
		windows: hookEventWindows(palette.BuildTitle("api", "key", false), []kitty.Window{{
			ID:       42,
			UserVars: kitty.UserVars{Profile: "work", Harness: "claude"},
		}}),
	}
	// SetKittyClient primes the cache from the good windows above.
	h.SetKittyClient(fake)

	// Now make any live ListWindows fail. If the hook path listed live, routing
	// would degrade; instead it must read the primed cache.
	fake.mu.Lock()
	fake.listErr = errors.New("kitty is wedged")
	fake.mu.Unlock()

	resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "PreToolUse"})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}
	detail := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
	if len(detail.Events) != 1 {
		t.Fatalf("events = %d, want 1 (%+v)", len(detail.Events), detail.Events)
	}
	ev := detail.Events[0]
	if ev.Action != "active" || ev.CurrentIconState != "key" {
		t.Fatalf("event = %+v; want active enriched with current icon key from the cache", ev)
	}
	h.stateMu.Lock()
	st := h.store[42]
	h.stateMu.Unlock()
	if st == nil || st.LastAccount != "work" {
		t.Fatalf("state = %+v; want account work resolved from the cached snapshot", st)
	}
}

// TestHookRoutingColdCacheFallsBackToSourcePane confirms that before any
// snapshot is published, a user_clear cannot silently vanish: an unreadable tab
// falls back to wiping the source pane rather than being dropped.
func TestHookRoutingColdCacheFallsBackToSourcePane(t *testing.T) {
	h := newTestHandler()
	// Install a client but never prime the cache: use the field-only setter so
	// the snapshot stays cold, modelling a degraded startup that has not yet
	// reconciled.
	h.useKittyClient(&fakeKittyClient{})

	resp := h.Handle(Request{Type: TypeHookEvent, WindowID: 42, Event: "user_clear"})
	if !resp.OK {
		t.Fatalf("user_clear response = %+v", resp)
	}
	// The cold-cache fallback wipes the source pane, so it must have a state entry.
	h.stateMu.Lock()
	_, ok := h.store[42]
	h.stateMu.Unlock()
	if !ok {
		t.Fatal("cold-cache user_clear should fall back to wiping the source pane, not drop silently")
	}
}
