package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
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

	// A pane going active stays tab-scoped: it can move FindTabTitleOwner
	// without touching the flags of the pane a probe is actually reading.
	s.publish(screenProbeEpochWindows("first changed", "second", true, ""), base.Add(3*time.Second))
	_, _, _, focusedProbeTabs, _ := s.loadForScreenProbes()
	if got := focusedProbeTabs[101].epoch; got == secondTabEpoch {
		t.Fatalf("second-tab focus change retained epoch %d", got)
	}
	focusedTabEpoch := focusedProbeTabs[101].epoch
	focusedWindowEpoch := focusedProbeTabs[101].windows[42].epoch
	if focusedWindowEpoch == 0 {
		t.Fatal("second-tab window epoch is zero")
	}

	// Mute is per-pane, so it must leave the tab epoch alone.
	s.publish(screenProbeEpochWindows("first changed", "second", true, "1"), base.Add(4*time.Second))
	_, _, _, mutedProbeTabs, _ := s.loadForScreenProbes()
	if got := mutedProbeTabs[101].epoch; got != focusedTabEpoch {
		t.Fatalf("mute change advanced tab epoch %d -> %d", focusedTabEpoch, got)
	}
	if got := mutedProbeTabs[101].windows[42].epoch; got == focusedWindowEpoch {
		t.Fatalf("mute change retained window epoch %d", got)
	}
}

// TestSnapshotStore_ScreenProbeWindowEpochsIsolateSiblingChurn covers the usual
// layout: a managed harness pane beside a plain terminal. Every command run in
// the plain pane rewrites its foreground processes, and folding that raw argv
// into the tab signature starved the managed pane's in-flight probes.
func TestSnapshotStore_ScreenProbeWindowEpochsIsolateSiblingChurn(t *testing.T) {
	var s snapshotStore
	base := time.Unix(1_700_000_000, 0)

	s.publish(screenProbeSplitTabWindows([]string{"/bin/ls", "-la"}), base)
	_, _, _, first, ready := s.loadForScreenProbes()
	if !ready {
		t.Fatal("first screen-probe snapshot is not ready")
	}
	tabEpoch := first[100].epoch
	managedEpoch := first[100].windows[41].epoch
	plainEpoch := first[100].windows[42].epoch
	if tabEpoch == 0 || managedEpoch == 0 || plainEpoch == 0 {
		t.Fatalf("initial epochs = tab %d managed %d plain %d; want non-zero", tabEpoch, managedEpoch, plainEpoch)
	}

	s.publish(screenProbeSplitTabWindows([]string{"/usr/bin/rg", "needle"}), base.Add(time.Second))
	_, _, _, churned, _ := s.loadForScreenProbes()
	if got := churned[100].epoch; got != tabEpoch {
		t.Fatalf("sibling shell command advanced tab epoch %d -> %d", tabEpoch, got)
	}
	if got := churned[100].windows[41].epoch; got != managedEpoch {
		t.Fatalf("sibling shell command advanced managed window epoch %d -> %d", managedEpoch, got)
	}
	if got := churned[100].windows[42].epoch; got != plainEpoch {
		t.Fatalf("sibling shell command advanced its own window epoch %d -> %d", plainEpoch, got)
	}

	// Starting a harness is the one foreground fact probes consume, so the
	// plain pane's own epoch advances while the managed sibling stays fresh.
	s.publish(screenProbeSplitTabWindows([]string{"/opt/homebrew/bin/claude", "-p", "say hi"}), base.Add(2*time.Second))
	_, _, _, harnessed, _ := s.loadForScreenProbes()
	if got := harnessed[100].epoch; got != tabEpoch {
		t.Fatalf("sibling harness start advanced tab epoch %d -> %d", tabEpoch, got)
	}
	if got := harnessed[100].windows[41].epoch; got != managedEpoch {
		t.Fatalf("sibling harness start advanced managed window epoch %d -> %d", managedEpoch, got)
	}
	if got := harnessed[100].windows[42].epoch; got == plainEpoch {
		t.Fatalf("sibling harness start retained window epoch %d", got)
	}
}

func TestSnapshotStore_ClaudeBusyTitleUsesNormalizedWindowEpoch(t *testing.T) {
	var s snapshotStore
	base := time.Unix(1_700_000_000, 0)

	s.publish(claudeTitleEpochWindows("\u2733 Fix drawer drag resistance"), base)
	_, _, _, first, ready := s.loadForScreenProbes()
	if !ready {
		t.Fatal("first Claude title snapshot is not ready")
	}
	tabEpoch := first[100].epoch
	busyEpoch := first[100].windows[42].epoch
	if tabEpoch == 0 || busyEpoch == 0 {
		t.Fatalf("initial epochs = tab %d window %d, want non-zero", tabEpoch, busyEpoch)
	}

	// Spinner frame and task text changes remain in the same busy class, so they
	// must not continuously invalidate an otherwise stable screen probe binding.
	s.publish(claudeTitleEpochWindows("\u2802 Run focused tests"), base.Add(time.Second))
	_, _, _, stillBusy, _ := s.loadForScreenProbes()
	if got := stillBusy[100].epoch; got != tabEpoch {
		t.Fatalf("busy title change advanced tab epoch %d -> %d", tabEpoch, got)
	}
	if got := stillBusy[100].windows[42].epoch; got != busyEpoch {
		t.Fatalf("busy title change advanced window epoch %d -> %d", busyEpoch, got)
	}

	// Busy-to-idle is probe-relevant evidence and must invalidate an in-flight
	// observation, while remaining isolated from the containing tab's epoch.
	s.publish(claudeTitleEpochWindows("Run focused tests"), base.Add(2*time.Second))
	_, _, _, idle, _ := s.loadForScreenProbes()
	if got := idle[100].epoch; got != tabEpoch {
		t.Fatalf("busy-to-idle title change advanced tab epoch %d -> %d", tabEpoch, got)
	}
	if got := idle[100].windows[42].epoch; got == busyEpoch {
		t.Fatalf("busy-to-idle title change retained window epoch %d", got)
	}
}

func screenProbeSplitTabWindows(plainForeground []string) []kitty.OSWindow {
	return []kitty.OSWindow{{
		ID:        1,
		IsActive:  true,
		IsFocused: true,
		Tabs: []kitty.Tab{{
			ID:       100,
			IsActive: true,
			Title:    "api",
			Windows: []kitty.Window{
				{
					ID:       41,
					IsActive: true,
					UserVars: kitty.UserVars{Profile: "work", Harness: "codex"},
				},
				{
					ID:                  42,
					ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: plainForeground}},
				},
			},
		}},
	}}
}

func claudeTitleEpochWindows(windowTitle string) []kitty.OSWindow {
	return []kitty.OSWindow{{
		ID:        1,
		IsActive:  true,
		IsFocused: true,
		Tabs: []kitty.Tab{{
			ID:       100,
			IsActive: true,
			Title:    "api",
			Windows: []kitty.Window{{
				ID:                  42,
				IsActive:            true,
				Title:               windowTitle,
				ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"/opt/homebrew/bin/claude"}}},
			}},
		}},
	}}
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

	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "PreToolUse"})
	if !resp.OK {
		t.Fatalf("hook response = %+v", resp)
	}
	detail := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
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

	resp := h.Handle(protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: "user_clear"})
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
