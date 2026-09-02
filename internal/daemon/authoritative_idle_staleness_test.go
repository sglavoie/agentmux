package daemon

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// TestScreenProbesDiscardSnapshotPredatingReducerEvent proves the cached
// snapshot receipt is the first freshness boundary for every adapter. Each
// subtest publishes eligible Kitty evidence, lands a reducer event before the
// per-window adapter starts, then verifies the stale snapshot causes neither
// external I/O nor any state-family mutation.
func TestScreenProbesDiscardSnapshotPredatingReducerEvent(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	t.Run("codex prompt and idle preserve protected terminal", func(t *testing.T) {
		h := newTestHandler()
		windows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "codex",
		})
		fake := &fakeKittyClient{windows: windows, getText: map[string]string{
			kitty.MatchWindowByID(42): codexCompletedTurnScreen,
		}}
		h.SetKittyClient(fake)
		codexSeedActiveBolt(t, h, 42, "old", base.Unix())
		h.publishSnapshot(windows, base)
		snapshot, ready := h.cachedScreenProbeSnapshot()
		if !ready {
			t.Fatal("cached probe snapshot is not ready")
		}
		codexInjectSessionEvent(t, h, 42, "old", "stop_failure", "Stop", base.Unix()+1)

		reads := 0
		fake.beforeGetText = func(string, string) { reads++ }
		before := screenProbeMutationFingerprint(t, h, 42)
		h.runScreenProbesWithSnapshot(base.Add(time.Second), snapshot)
		if reads != 0 {
			t.Fatalf("GetText calls = %d, want 0 for stale Codex prompt/idle routing", reads)
		}
		if after := screenProbeMutationFingerprint(t, h, 42); after != before {
			t.Fatalf("state mutated after stale Codex snapshot:\n before %s\n after  %s", before, after)
		}
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "error" {
			t.Fatalf("effective = %+v, want protected error preserved", eff)
		}
	})

	t.Run("claude context and idle preserve attention", func(t *testing.T) {
		h := newTestHandler()
		windows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "claude",
		})
		fake := &fakeKittyClient{windows: windows, getText: map[string]string{
			kitty.MatchWindowByID(42): claudeContextLimitScreen,
		}}
		h.SetKittyClient(fake)
		claudeSeedActiveBolt(t, h, 42, "old", base.Unix())
		h.publishSnapshot(windows, base)
		snapshot, ready := h.cachedScreenProbeSnapshot()
		if !ready {
			t.Fatal("cached probe snapshot is not ready")
		}
		idleSeedHarness(t, h, 42, "old", "elicitation", base.Unix()+1)

		reads := 0
		fake.beforeGetText = func(string, string) { reads++ }
		before := screenProbeMutationFingerprint(t, h, 42)
		h.runScreenProbesWithSnapshot(base.Add(time.Second), snapshot)
		if reads != 0 {
			t.Fatalf("GetText calls = %d, want 0 for stale Claude context/idle routing", reads)
		}
		if after := screenProbeMutationFingerprint(t, h, 42); after != before {
			t.Fatalf("state mutated after stale Claude snapshot:\n before %s\n after  %s", before, after)
		}
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "question" {
			t.Fatalf("effective = %+v, want attention question preserved", eff)
		}
	})

	t.Run("copilot transcript preserves newer prompt owner", func(t *testing.T) {
		h := newTestHandler()
		home := t.TempDir()
		h.SetCopilotTranscriptHomes([]string{home}, "")
		windows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: "copilot",
		})
		fake := &fakeKittyClient{windows: windows}
		h.SetKittyClient(fake)
		copilotSeedActiveBolt(t, h, 42, "old", base.Unix())
		h.publishSnapshot(windows, base)
		snapshot, ready := h.cachedScreenProbeSnapshot()
		if !ready {
			t.Fatal("cached probe snapshot is not ready")
		}
		copilotSeedActiveBolt(t, h, 42, "new", base.Unix()+1)

		reads := 0
		h.copilotLifecycleReader = func(string) (transcript.CopilotLifecycleEvidence, error) {
			reads++
			return transcript.CopilotLifecycleEvidence{State: transcript.CopilotLifecycleTerminal, EventType: "abort"}, nil
		}
		before := screenProbeMutationFingerprint(t, h, 42)
		h.runScreenProbesWithSnapshot(base.Add(time.Second), snapshot)
		if reads != 0 {
			t.Fatalf("transcript reads = %d, want 0 for stale Copilot routing", reads)
		}
		if after := screenProbeMutationFingerprint(t, h, 42); after != before {
			t.Fatalf("state mutated after stale Copilot snapshot:\n before %s\n after  %s", before, after)
		}
		h.stateMu.Lock()
		identity, ok := h.store[42].CurrentInteractiveConversation(base.Unix() + 1)
		h.stateMu.Unlock()
		if !ok || identity.SessionID != "new" {
			t.Fatalf("current conversation = %+v ok=%v, want newer session new", identity, ok)
		}
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective = %+v, want newer prompt bolt preserved", eff)
		}
	})
}

// TestAuthoritativeIdleAdapters_DiscardStaleMuteSnapshot proves the independent
// Kitty tab-epoch gate for every authoritative-idle adapter. Each
// probe pauses in external I/O, a newer muted snapshot is published without a
// reducer event, and the old observation then resumes. The stale observation
// must not overwrite the mute cache, settle the reducer, or dispatch a stop
// notification.
func TestAuthoritativeIdleAdapters_DiscardStaleMuteSnapshot(t *testing.T) {
	for _, harnessName := range []string{"codex", "claude", "copilot"} {
		t.Run(harnessName, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				guard := activity.NewStateGuard(time.Hour)
				notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
				h := NewHandler(guard, notifier)
				notifier.SetIsMuted(h.IsMuted)

				base := time.Now()
				oldWindows := codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
					Profile: "work",
					Harness: harnessName,
				})
				fake := &fakeKittyClient{windows: oldWindows}
				switch harnessName {
				case "codex":
					fake.getText = map[string]string{kitty.MatchWindowByID(42): codexCompletedTurnScreen}
				case "claude":
					fake.getText = map[string]string{kitty.MatchWindowByID(42): claudeInterruptedScreen}
				case "copilot":
					h.SetCopilotTranscriptHomes(nil, t.TempDir())
				}
				h.SetKittyClient(fake)
				switch harnessName {
				case "codex":
					codexSeedActiveBolt(t, h, 42, "old", base.Unix())
				case "claude":
					claudeSeedActiveBolt(t, h, 42, "old", base.Unix())
				case "copilot":
					copilotSeedActiveBolt(t, h, 42, "old", base.Unix())
				}
				h.publishSnapshot(oldWindows, base)
				snapshot, ready := h.cachedScreenProbeSnapshot()
				if !ready {
					t.Fatal("cached probe snapshot is not ready")
				}

				entered := make(chan struct{})
				release := make(chan struct{})
				switch harnessName {
				case "codex", "claude":
					// Both harnesses take exactly one screen capture per probe
					// tick now: the per-tick cache hands the same frame to
					// Codex's prompt classifier and its idle classifier, so
					// pausing the first read pauses all of the probe's I/O.
					fake.beforeGetText = func(string, string) {
						close(entered)
						<-release
					}
				case "copilot":
					h.copilotLifecycleReader = func(string) (transcript.CopilotLifecycleEvidence, error) {
						close(entered)
						<-release
						return transcript.CopilotLifecycleEvidence{
							State:     transcript.CopilotLifecycleTerminal,
							EventType: "abort",
						}, nil
					}
				}

				done := make(chan struct{})
				go func() {
					defer close(done)
					switch harnessName {
					case "codex":
						h.probeCodexScreenPromptsWithSnapshot(base.Add(time.Second), snapshot, h.screenTextCacheFor())
					case "claude":
						h.probeClaudeScreenStopWithSnapshot(base.Add(time.Second), snapshot, h.screenTextCacheFor())
					case "copilot":
						h.probeCopilotTranscriptStopWithSnapshot(base.Add(time.Second), snapshot)
					}
				}()
				<-entered

				receiptDuringIO := h.captureWindowReceipt(42)
				newWindows := codexProbeWindows(42, palette.BuildTitleFull("W:api", "bolt", false, true), kitty.UserVars{
					Profile: "work",
					Harness: harnessName,
				})
				h.publishSnapshot(newWindows, base.Add(2*time.Second))
				// Keep the mute-only change explicit; publication itself now performs
				// this refresh atomically with the generation increment.
				h.refreshMutedFromSnapshot(newWindows)
				receiptAfterPublish := h.captureWindowReceipt(42)
				mutedAfterPublish := h.IsMuted(42)
				want := screenProbeMutationFingerprint(t, h, 42)

				close(release)
				<-done
				synctest.Wait()

				if receiptDuringIO != snapshot.Receipt(42) {
					t.Fatalf("receipt changed during %s probe I/O = %d, want %d", harnessName, receiptDuringIO, snapshot.Receipt(42))
				}
				if receiptAfterPublish != snapshot.Receipt(42) {
					t.Fatalf("mute-only publication advanced receipt = %d, want %d", receiptAfterPublish, snapshot.Receipt(42))
				}
				if !mutedAfterPublish {
					t.Fatal("new snapshot did not mute window 42")
				}
				if got := screenProbeMutationFingerprint(t, h, 42); got != want {
					t.Fatalf("stale %s authoritative-idle observation mutated state:\n got  %s\n want %s", harnessName, got, want)
				}
				if !h.IsMuted(42) {
					t.Fatalf("stale %s observation overwrote the newer muted cache value", harnessName)
				}
				if got := rec.len(); got != 0 {
					t.Fatalf("stale muted %s dispatches = %d, want 0", harnessName, got)
				}
			})
		})
	}
}
