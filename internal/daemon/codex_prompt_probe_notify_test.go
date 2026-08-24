package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestProbeCodexScreenPrompts_DetectedPromptNotifiesOnceOnTransition pins the
// probe notification path: a screen-probe-detected prompt (the probe exists
// precisely because Codex hooks miss these) must fire exactly one macOS
// notification on the state transition, and a second tick with the prompt
// still visible must not re-dispatch.
func TestProbeCodexScreenPrompts_DetectedPromptNotifiesOnceOnTransition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Permission: true})
		h := NewHandler(guard, notifier)
		fake := &fakeKittyClient{
			windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
				Profile: "work",
				Harness: "codex",
			}),
			getText: map[string]string{
				kitty.MatchWindowByID(42): codexRequestUserInputScreen,
			},
		}
		h.SetKittyClient(fake)
		h.guard.TryUpdate(42, "active", false)

		now := time.Now()
		h.ProbeCodexScreenPrompts(now)
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("first probe dispatches = %d; want 1", got)
		}
		call := rec.snapshot()[0]
		if call.windowID != 42 || call.account != "work" || call.title != "W:api" {
			t.Fatalf("dispatch = %+v, want window 42 / work / W:api", call)
		}
		if call.subtitle != "User input requested" {
			t.Fatalf("dispatch subtitle = %q, want %q", call.subtitle, "User input requested")
		}

		// Second tick: same prompt still on screen, tab title now shows ❓ —
		// no transition, so no additional dispatch.
		fake.mu.Lock()
		fake.windows[0].Tabs[0].Title = palette.BuildTitle("W:api", "question", false)
		fake.mu.Unlock()
		h.ProbeCodexScreenPrompts(now.Add(2 * time.Second))
		synctest.Wait()

		if got := rec.len(); got != 1 {
			t.Fatalf("second probe total dispatches = %d; want 1 (no per-tick spam)", got)
		}
	})
}

// TestProbeCodexScreenPrompts_DetectedPromptMutedWindowNoDispatch verifies the
// probe notification honors the mute gate: the icon still swaps, but the
// notifier's isMuted closure (wired to Handler.IsMuted, as in daemon.Run)
// suppresses the dispatch.
func TestProbeCodexScreenPrompts_DetectedPromptMutedWindowNoDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Permission: true})
		h := NewHandler(guard, notifier)
		notifier.SetIsMuted(h.IsMuted)
		fake := &fakeKittyClient{
			windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
				Profile: "work",
				Harness: "codex",
			}),
			getText: map[string]string{
				kitty.MatchWindowByID(42): codexRequestUserInputScreen,
			},
		}
		h.SetKittyClient(fake)
		h.stateMu.Lock()
		h.ensureWindowStateLocked(42).Muted = true
		h.stateMu.Unlock()

		h.ProbeCodexScreenPrompts(time.Now())
		synctest.Wait()

		if got := rec.len(); got != 0 {
			t.Fatalf("muted window dispatches = %d; want 0", got)
		}
		if got := len(fake.titleCalls()); got != 1 {
			t.Fatalf("SetTabTitle calls = %d, want 1 (icon swap must still happen)", got)
		}
	})
}

// TestProbeCodexScreenPrompts_DiscardsStaleMuteSnapshot pins the Kitty
// tab-epoch boundary independently of the reducer receipt. A newer
// mute-only snapshot leaves ShadowSeq unchanged while the old prompt read is
// blocked; resuming that read must discard the complete observation before it
// mutates prompt state or schedules a notification.
func TestProbeCodexScreenPrompts_DiscardsStaleMuteSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Permission: true})
		h := NewHandler(guard, notifier)
		notifier.SetIsMuted(h.IsMuted)

		base := time.Now()
		oldWindows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		})
		fake := &fakeKittyClient{
			windows: oldWindows,
			getText: map[string]string{
				kitty.MatchWindowByID(42): codexRequestUserInputScreen,
			},
		}
		h.SetKittyClient(fake)
		codexSeedActiveBolt(t, h, 42, "old", base.Unix())
		h.publishSnapshot(oldWindows, base)
		snapshot, ready := h.cachedScreenProbeSnapshot()
		if !ready {
			t.Fatal("cached probe snapshot is not ready")
		}

		entered := make(chan struct{})
		release := make(chan struct{})
		fake.beforeGetText = func(string, string) {
			close(entered)
			<-release
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.probeCodexScreenPromptsWithSnapshot(base.Add(time.Second), snapshot, h.screenTextCacheFor())
		}()
		<-entered

		receiptDuringIO := h.captureWindowReceipt(42)
		newWindows := codexProbeWindows(42, palette.BuildTitleFull("W:api", "bolt", false, true), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		})
		h.publishSnapshot(newWindows, base.Add(2*time.Second))
		// Keep the test explicit about the mute-only state change even though
		// publication now refreshes the cache in the same critical section.
		h.refreshMutedFromSnapshot(newWindows)
		receiptAfterPublish := h.captureWindowReceipt(42)
		mutedAfterPublish := h.IsMuted(42)
		want := screenProbeMutationFingerprint(t, h, 42)

		close(release)
		<-done
		synctest.Wait()

		if receiptDuringIO != snapshot.receipt(42) {
			t.Fatalf("receipt changed during prompt I/O = %d, want %d", receiptDuringIO, snapshot.receipt(42))
		}
		if receiptAfterPublish != snapshot.receipt(42) {
			t.Fatalf("mute-only publication advanced receipt = %d, want %d", receiptAfterPublish, snapshot.receipt(42))
		}
		if !mutedAfterPublish {
			t.Fatal("new snapshot did not mute window 42")
		}
		if got := screenProbeMutationFingerprint(t, h, 42); got != want {
			t.Fatalf("stale prompt mutated state:\n got  %s\n want %s", got, want)
		}
		if !h.IsMuted(42) {
			t.Fatal("stale prompt overwrote the newer muted cache value")
		}
		if got := rec.len(); got != 0 {
			t.Fatalf("stale muted prompt dispatches = %d, want 0", got)
		}
	})
}

func TestProbeCodexScreenPrompts_PreservesLoopMarkerAndHarnessEmoji(t *testing.T) {
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
			Harness: "codex",
		}),
		getText: map[string]string{
			kitty.MatchWindowByID(42): codexRequestUserInputScreen,
		},
	}
	h.SetKittyClient(fake)

	h.ProbeCodexScreenPrompts(time.Unix(1_700_000_000, 0))

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	want := palette.BuildTitleParts(palette.TitleParts{
		Base:    "🦊api",
		State:   "question",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	if calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
}

func TestProbeCodexScreenPrompts_DoesNotOverwriteProtectedIcons(t *testing.T) {
	protected := []string{"stop", "warning", "error", "subagent", "compact"}
	for _, state := range protected {
		t.Run(state, func(t *testing.T) {
			h := newTestHandler()
			fake := &fakeKittyClient{
				windows: codexProbeWindows(42, palette.BuildTitle("W:api", state, false), kitty.UserVars{
					Harness: "codex",
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
			resp := h.Handle(Request{Type: TypeSessionDetail, WindowID: 42})
			if len(resp.Events) != 0 {
				t.Fatalf("events = %d, want 0", len(resp.Events))
			}
		})
	}
}
