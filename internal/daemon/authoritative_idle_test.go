package daemon

import (
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/transcript"
)

// screenProbeMutationFingerprint captures every state family a discarded
// screen/transcript observation is forbidden to touch. Encoding the value
// gives tests a simple exact comparison while retaining reducer facts, audit
// rows, durable metadata, mute state, notification state, and render state.
func screenProbeMutationFingerprint(t *testing.T, h *Handler, windowID int) string {
	t.Helper()
	type fingerprint struct {
		WindowState         string
		Events              []SessionEvent
		EventSeq            uint64
		Metadata            WindowDebugMetadata
		Guard               string
		RenderPending       bool
		PendingNotification bool
		IdleSamples         [3]int
		IdleGenerations     [3]int64
	}

	h.stateMu.Lock()
	st := h.store[windowID]
	if st == nil {
		h.stateMu.Unlock()
		t.Fatalf("window %d has no state", windowID)
	}
	windowState, err := st.MarshalUserVar()
	if err != nil {
		h.stateMu.Unlock()
		t.Fatalf("marshal window %d state: %v", windowID, err)
	}
	refs := [3]idleSampleRef{
		{windowID: windowID, key: screenPromptProbeSource},
		{windowID: windowID, key: claudeScreenStopProbeSource},
		{windowID: windowID, key: copilotTranscriptStopProbeSource},
	}
	got := fingerprint{
		WindowState: windowState,
		Events:      append([]SessionEvent(nil), st.EventLog...),
		EventSeq:    h.eventSeq,
		Metadata:    h.windowMetadata[windowID],
	}
	for i, ref := range refs {
		got.IdleSamples[i] = h.idleSampleStreak[ref]
		got.IdleGenerations[i] = h.idleSampleGen[ref]
	}
	h.stateMu.Unlock()

	got.Guard = h.guard.Current(windowID)
	h.renderQueue.mu.Lock()
	got.RenderPending = h.renderQueue.pending[windowID]
	h.renderQueue.mu.Unlock()
	if h.notifier != nil {
		h.notifier.mu.Lock()
		_, got.PendingNotification = h.notifier.pending[windowID]
		h.notifier.mu.Unlock()
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal screen-probe fingerprint: %v", err)
	}
	return string(encoded)
}

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
// Kitty snapshot-generation gate for every authoritative-idle adapter. Each
// probe pauses in external I/O, a newer muted snapshot is published without a
// reducer event, and the old observation then resumes. The stale observation
// must not overwrite the mute cache, settle the reducer, or dispatch a stop
// notification.
func TestAuthoritativeIdleAdapters_DiscardStaleMuteSnapshot(t *testing.T) {
	for _, harnessName := range []string{"codex", "claude", "copilot"} {
		t.Run(harnessName, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				guard := NewStateGuard(time.Hour)
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
				case "codex":
					reads := 0
					fake.beforeGetText = func(string, string) {
						reads++
						if reads == 2 { // prompt classification first, idle evidence second
							close(entered)
							<-release
						}
					}
				case "claude":
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
						h.probeCodexScreenPromptsWithSnapshot(base.Add(time.Second), snapshot)
					case "claude":
						h.probeClaudeScreenStopWithSnapshot(base.Add(time.Second), snapshot)
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

				if receiptDuringIO != snapshot.receipt(42) {
					t.Fatalf("receipt changed during %s probe I/O = %d, want %d", harnessName, receiptDuringIO, snapshot.receipt(42))
				}
				if receiptAfterPublish != snapshot.receipt(42) {
					t.Fatalf("mute-only publication advanced receipt = %d, want %d", receiptAfterPublish, snapshot.receipt(42))
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

// idleSeedHarness folds a harness event into a window through the normal state
// path so the window has a live reducer and effective state before an
// authoritative idle observation is applied.
func idleSeedHarness(t *testing.T, h *Handler, windowID int, session, action string, at int64) {
	t.Helper()
	ev := NormalizedEvent{
		Source:    SourceID{Kind: SourceHarness, Key: "claude:" + session},
		Action:    action,
		SessionID: session,
		At:        at,
		Event:     "Evt",
	}
	resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: windowID, Action: action, Event: "Evt", normalized: &ev})
	if !resp.OK {
		t.Fatalf("seed harness %s/%s rejected: %+v", session, action, resp)
	}
}

// idleSeedHarnessSeq folds a harness event carrying an EXPLICIT per-source
// sequence into a window, so a test can land an accepted mutation whose wire
// sequence is at or below the current window receipt generation.
func idleSeedHarnessSeq(t *testing.T, h *Handler, windowID int, session, action string, seq uint64, at int64) {
	t.Helper()
	ev := NormalizedEvent{
		Source:    SourceID{Kind: SourceHarness, Key: "claude:" + session},
		Action:    action,
		SessionID: session,
		Seq:       seq,
		At:        at,
		Event:     "Evt",
	}
	resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: windowID, Action: action, Event: "Evt", normalized: &ev})
	if !resp.OK {
		t.Fatalf("seed harness %s/%s seq %d rejected: %+v", session, action, seq, resp)
	}
}

// idleShadowSeq reads a window's current reducer receipt sequence, the value an
// adapter captures before its screen read.
func idleShadowSeq(t *testing.T, h *Handler, windowID int) uint64 {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[windowID]
	if !ok {
		t.Fatalf("window %d has no state", windowID)
	}
	return st.ShadowSeq
}

// TestAuthoritativeIdleSettlesStaleInteractiveBolt is the core done-when: an
// idle observation retires an older interactive session's stranded ⚡ and the
// tab converges to 🛑.
func TestAuthoritativeIdleSettlesStaleInteractiveBolt(t *testing.T) {
	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "old", "active", base)

	obs := AuthoritativeIdle{
		WindowID:        7,
		Source:          AuthoritativeIdleSource("claude"),
		Harness:         "claude",
		CapturedSeq:     idleShadowSeq(t, h, 7),
		At:              base + 5,
		RequiredSamples: 1,
	}
	if !h.applyAuthoritativeIdle(obs) {
		t.Fatalf("authoritative idle was not applied")
	}
	if eff, visible := windowEffective(t, h, 7); eff.Icon != "stop" || visible != "stop" {
		t.Fatalf("effective = %+v visible %q, want stop/stop", eff, visible)
	}
	h.stateMu.Lock()
	old := h.store[7].Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:old"}]
	h.stateMu.Unlock()
	if old == nil || !old.acked {
		t.Fatalf("older interactive bolt was not retired: %+v", old)
	}
}

// TestAuthoritativeIdleDiscardedWhenSequenceMoved covers the new-prompt race:
// evidence captured before a later reducer event lands is discarded, so newer
// activity is never overwritten.
func TestAuthoritativeIdleDiscardedWhenSequenceMoved(t *testing.T) {
	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "cur", "active", base)
	captured := idleShadowSeq(t, h, 7)

	// A newer prompt/activity lands after the evidence was captured.
	idleSeedHarness(t, h, 7, "cur", "active", base+1)

	obs := AuthoritativeIdle{
		WindowID:        7,
		Source:          AuthoritativeIdleSource("claude"),
		Harness:         "claude",
		CapturedSeq:     captured,
		At:              base + 5,
		RequiredSamples: 1,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("stale-sequence observation was applied")
	}
	if eff, visible := windowEffective(t, h, 7); eff.Icon != "bolt" || visible != "bolt" {
		t.Fatalf("effective = %+v visible %q, want the newer bolt preserved", eff, visible)
	}
}

// TestAuthoritativeIdleDiscardedWhenLowerExplicitSequenceLands covers the
// explicit-sequence variant of the new-prompt race: idle evidence is captured
// at a high window receipt generation, then a newer accepted event from a
// DIFFERENT source lands carrying a per-source wire sequence BELOW that
// generation. The reducer accepts it (per-source monotonicity is scoped to the
// new source), so the window mutated after capture — the receipt generation
// must advance even though the event's own sequence did not raise it, and the
// stale idle observation must be discarded rather than retire the newer work.
func TestAuthoritativeIdleDiscardedWhenLowerExplicitSequenceLands(t *testing.T) {
	h := newTestHandler()
	base := time.Now().Unix()
	// Push the window receipt generation up with sequence-less activity from the
	// first interactive session, so a low explicit sequence from another source
	// cannot raise it.
	idleSeedHarness(t, h, 7, "old", "active", base)
	idleSeedHarness(t, h, 7, "old", "active", base+1)
	idleSeedHarness(t, h, 7, "old", "active", base+2)
	captured := idleShadowSeq(t, h, 7)
	if captured < 2 {
		t.Fatalf("receipt generation = %d, want a high value to shadow a low explicit sequence", captured)
	}

	// A newer accepted event from a different source with an explicit per-source
	// sequence at or below the captured generation. The reducer accepts it (this
	// source has no prior fact), so the window changed after capture.
	idleSeedHarnessSeq(t, h, 7, "new", "active", 1, base+3)
	if moved := idleShadowSeq(t, h, 7); moved <= captured {
		t.Fatalf("receipt generation = %d, want it advanced past the captured %d after the accepted mutation", moved, captured)
	}

	obs := AuthoritativeIdle{
		WindowID:        7,
		Source:          AuthoritativeIdleSource("claude"),
		Harness:         "claude",
		CapturedSeq:     captured,
		At:              base + 5,
		RequiredSamples: 1,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("stale observation applied after an accepted lower-sequence mutation")
	}
	if eff, visible := windowEffective(t, h, 7); eff.Icon != "bolt" || visible != "bolt" {
		t.Fatalf("effective = %+v visible %q, want the newer interactive bolt preserved", eff, visible)
	}
	h.stateMu.Lock()
	newFact := h.store[7].Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:new"}]
	h.stateMu.Unlock()
	if newFact == nil || newFact.acked {
		t.Fatalf("newer interactive work was retired by the stale probe: %+v", newFact)
	}
}

// TestAuthoritativeIdleDiscardedWhenWatchdogExpiryLands proves a watchdog fact
// expiry — which acknowledges live source facts in place and carries no
// per-source sequence of its own — advances the window-wide receipt generation,
// so an idle observation captured before the sweep is discarded rather than
// allowed to settle a synthetic stop over the freshly self-healed state.
func TestAuthoritativeIdleDiscardedWhenWatchdogExpiryLands(t *testing.T) {
	h := newTestHandler()
	// A small active threshold so a lone stale bolt fact self-heals in one sweep.
	h.SetStaleThresholds(map[string]time.Duration{"active": time.Second}, nil)
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "cur", "active", base)
	captured := idleShadowSeq(t, h, 7)

	// A watchdog sweep whose observed age exceeds the threshold expires the bolt
	// fact, mutating the reducer after the idle evidence was captured.
	h.SweepStaleWithSnapshot(time.Unix(base, 0).Add(time.Hour), nil)
	if moved := idleShadowSeq(t, h, 7); moved <= captured {
		t.Fatalf("receipt generation = %d, want it advanced past the captured %d after watchdog expiry", moved, captured)
	}

	obs := AuthoritativeIdle{
		WindowID:        7,
		Source:          AuthoritativeIdleSource("claude"),
		Harness:         "claude",
		CapturedSeq:     captured,
		At:              base + 5,
		RequiredSamples: 1,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("stale observation applied after a watchdog expiry mutated the window")
	}
}

// TestAuthoritativeIdleDiscardedWhenPersistedMergeLands proves a degraded-startup
// persisted-fact merge advances the window-wide receipt generation even when the
// imported facts' per-source sequences do not exceed the current counter, so an
// idle observation captured before the merge is discarded rather than allowed to
// settle over the newly surfaced disjoint state.
func TestAuthoritativeIdleDiscardedWhenPersistedMergeLands(t *testing.T) {
	h := newTestHandler()
	now := time.Now()
	base := now.Unix()

	// The degraded-window hook created in-process state for the interactive
	// harness source only.
	idleSeedHarness(t, h, 42, "cur", "active", base)
	captured := idleShadowSeq(t, h, 42)

	// A persisted amx_state carries a DISJOINT durable fact (a live between-turns
	// ASL lease) whose per-source sequence is at or below the current window
	// generation, so the merge cannot advance the receipt generation via MaxSeq
	// alone.
	persisted := NewReducer(ReducerConfig{})
	persisted.Apply(ReducerEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", Seq: 1, At: base})
	win := encodeMergeWindow(t, persisted, 1, now)[0].Tabs[0].Windows[0]

	h.stateMu.Lock()
	st := h.store[42]
	h.mergePersistedFactsLocked(42, st, win, now)
	moved := st.ShadowSeq
	merged := st.Reducer.MaxSeq()
	h.stateMu.Unlock()
	if moved <= captured {
		t.Fatalf("receipt generation = %d, want it advanced past the captured %d after a persisted merge", moved, captured)
	}
	if exists, live := h.store[42].Reducer.LeaseState("run-1"); !exists || !live {
		t.Fatalf("disjoint persisted ASL lease not merged: exists=%v live=%v (maxSeq %d)", exists, live, merged)
	}

	obs := AuthoritativeIdle{
		WindowID:        42,
		Source:          AuthoritativeIdleSource("claude"),
		Harness:         "claude",
		CapturedSeq:     captured,
		At:              base + 5,
		RequiredSamples: 1,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("stale observation applied after a persisted merge mutated the window")
	}
}

// TestAuthoritativeIdleDiscardedWhenASLLeaseSeedLands proves a first-time ASL
// lease seed — which stamps its fact at sequence 0 so the run's own future events
// supersede it — still advances the window-wide receipt generation, so an idle
// observation captured before the seed is discarded rather than allowed to retire
// the freshly seeded lease.
func TestAuthoritativeIdleDiscardedWhenASLLeaseSeedLands(t *testing.T) {
	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "cur", "active", base)
	captured := idleShadowSeq(t, h, 7)

	// Reconciliation discovers a brand-new run and seeds its lease onto the
	// window for the first time (no prior fact), at sequence 0.
	h.stateMu.Lock()
	seeded := h.seedASLLeaseLocked(time.Unix(base+1, 0), aslSeed{
		windowID:  7,
		key:       "run-1",
		candidate: agentloop.Candidate{RunID: "run-1", Phase: agentloop.PhaseBetween},
	})
	moved := h.store[7].ShadowSeq
	h.stateMu.Unlock()
	if !seeded {
		t.Fatalf("first-time ASL lease seed did not apply")
	}
	if moved <= captured {
		t.Fatalf("receipt generation = %d, want it advanced past the captured %d after a first-time ASL seed", moved, captured)
	}

	obs := AuthoritativeIdle{
		WindowID:        7,
		Source:          AuthoritativeIdleSource("claude"),
		Harness:         "claude",
		CapturedSeq:     captured,
		At:              base + 5,
		RequiredSamples: 1,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("stale observation applied after a first-time ASL lease seed mutated the window")
	}
}

// TestAuthoritativeIdleBlockedByResetFlow proves a /clear, /reset, or /new flow
// keeps its cleared-state semantics: no synthetic stop surfaces during one.
func TestAuthoritativeIdleBlockedByResetFlow(t *testing.T) {
	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "cur", "active", base)

	h.stateMu.Lock()
	h.store[7].PendingReset = resetFlowRequested
	h.stateMu.Unlock()

	obs := AuthoritativeIdle{
		WindowID:        7,
		Source:          AuthoritativeIdleSource("claude"),
		Harness:         "claude",
		CapturedSeq:     idleShadowSeq(t, h, 7),
		At:              base + 5,
		RequiredSamples: 1,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("observation applied during a reset flow")
	}
	if eff, _ := windowEffective(t, h, 7); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want the bolt preserved during reset", eff)
	}
}

// TestAuthoritativeIdleRequiresConsecutiveSamples proves the stabilization
// gate: an adapter that requires N consecutive idle frames only settles once
// the streak reaches the threshold.
func TestAuthoritativeIdleRequiresConsecutiveSamples(t *testing.T) {
	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "old", "active", base)

	captured := idleShadowSeq(t, h, 7)
	obs := AuthoritativeIdle{
		WindowID:        7,
		Source:          AuthoritativeIdleSource("claude"),
		Harness:         "claude",
		CapturedSeq:     captured,
		At:              base + 5,
		RequiredSamples: 3,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("first sample settled before reaching the threshold")
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("second sample settled before reaching the threshold")
	}
	if eff, _ := windowEffective(t, h, 7); eff.Icon != "bolt" {
		t.Fatalf("effective = %+v, want the bolt to still hold mid-stabilization", eff)
	}
	if !h.applyAuthoritativeIdle(obs) {
		t.Fatalf("third consecutive sample did not settle")
	}
	if eff, _ := windowEffective(t, h, 7); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want stop after the threshold", eff)
	}
}

// TestAuthoritativeIdleStreakClearedByActivity proves fresh activity resets a
// partial stabilization streak so the adapter cannot settle stale work over the
// new state, and the captured sequence check backs it up.
func TestAuthoritativeIdleStreakClearedByActivity(t *testing.T) {
	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "cur", "active", base)

	captured := idleShadowSeq(t, h, 7)
	obs := AuthoritativeIdle{
		WindowID:        7,
		Source:          AuthoritativeIdleSource("claude"),
		Harness:         "claude",
		CapturedSeq:     captured,
		At:              base + 5,
		RequiredSamples: 2,
	}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("first sample settled prematurely")
	}
	// Real activity between samples must reset the streak.
	idleSeedHarness(t, h, 7, "cur", "active", base+1)
	h.stateMu.Lock()
	_, streaking := h.idleSampleStreak[idleSampleRef{windowID: 7, key: obs.Source.Key}]
	h.stateMu.Unlock()
	if streaking {
		t.Fatalf("activity did not clear the idle-sample streak")
	}
}

// TestAuthoritativeIdlePreservesProtectedAndBackgroundState proves the
// supersession is narrow: a live ASL lease masks the stop, and an interactive
// attention icon, a pipe-mode child, and a subagent all survive.
func TestAuthoritativeIdlePreservesProtectedAndBackgroundState(t *testing.T) {
	t.Run("live ASL lease masks the stop", func(t *testing.T) {
		h := newTestHandler()
		base := time.Now().Unix()
		idleSeedHarness(t, h, 7, "old", "active", base)
		lease := NormalizedEvent{Source: SourceID{Kind: SourceASL, Key: "run-1"}, Phase: ASLPhaseBetween, RunID: "run-1", At: base, Event: agentSessionLoopStarted}
		h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 7, Action: "loop_marker", Event: agentSessionLoopStarted, normalized: &lease})

		obs := AuthoritativeIdle{WindowID: 7, Source: AuthoritativeIdleSource("claude"), Harness: "claude", CapturedSeq: idleShadowSeq(t, h, 7), At: base + 5, RequiredSamples: 1}
		if !h.applyAuthoritativeIdle(obs) {
			t.Fatalf("observation not applied")
		}
		eff, _ := windowEffective(t, h, 7)
		if eff.Icon != "" || !eff.Looping {
			t.Fatalf("effective = %+v, want the live lease to mask the stop (loop only)", eff)
		}
	})

	t.Run("interactive attention icon outranks the stop", func(t *testing.T) {
		h := newTestHandler()
		base := time.Now().Unix()
		idleSeedHarness(t, h, 7, "old", "active", base)
		idleSeedHarness(t, h, 7, "perm", "permission", base+1)

		obs := AuthoritativeIdle{WindowID: 7, Source: AuthoritativeIdleSource("claude"), Harness: "claude", CapturedSeq: idleShadowSeq(t, h, 7), At: base + 5, RequiredSamples: 1}
		if !h.applyAuthoritativeIdle(obs) {
			t.Fatalf("observation not applied")
		}
		eff, _ := windowEffective(t, h, 7)
		if eff.Icon != "key" {
			t.Fatalf("effective = %+v, want the permission prompt preserved", eff)
		}
		h.stateMu.Lock()
		perm := h.store[7].Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:perm"}]
		h.stateMu.Unlock()
		if perm == nil || perm.acked {
			t.Fatalf("permission fact was retired: %+v", perm)
		}
	})

	t.Run("pipe-mode child work is preserved", func(t *testing.T) {
		h := newTestHandler()
		base := time.Now().Unix()
		pipe := NormalizedEvent{Source: SourceID{Kind: SourceHarness, Key: "claude:pipe"}, Action: "active", SessionID: "pipe", PipeMode: true, At: base, Event: "Evt"}
		h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 7, Action: "active", Event: "Evt", normalized: &pipe})

		obs := AuthoritativeIdle{WindowID: 7, Source: AuthoritativeIdleSource("claude"), Harness: "claude", CapturedSeq: idleShadowSeq(t, h, 7), At: base + 5, RequiredSamples: 1}
		if !h.applyAuthoritativeIdle(obs) {
			t.Fatalf("observation not applied")
		}
		if eff, _ := windowEffective(t, h, 7); eff.Icon != "bolt" {
			t.Fatalf("effective = %+v, want the pipe-mode bolt preserved", eff)
		}
		h.stateMu.Lock()
		pf := h.store[7].Reducer.facts[SourceID{Kind: SourceHarness, Key: "claude:pipe"}]
		h.stateMu.Unlock()
		if pf == nil || pf.acked {
			t.Fatalf("pipe-mode fact was retired: %+v", pf)
		}
	})

	t.Run("running subagent is preserved", func(t *testing.T) {
		h := newTestHandler()
		base := time.Now().Unix()
		idleSeedHarness(t, h, 7, "old", "active", base)
		sa := NormalizedEvent{Source: SourceID{Kind: SourceSubagent, Key: "agent-1"}, Action: "subagent", At: base + 1, Event: "SubagentStart"}
		h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: 7, Action: "subagent", Event: "SubagentStart", normalized: &sa})

		obs := AuthoritativeIdle{WindowID: 7, Source: AuthoritativeIdleSource("claude"), Harness: "claude", CapturedSeq: idleShadowSeq(t, h, 7), At: base + 5, RequiredSamples: 1}
		if !h.applyAuthoritativeIdle(obs) {
			t.Fatalf("observation not applied")
		}
		if eff, _ := windowEffective(t, h, 7); eff.Icon != "subagent" {
			t.Fatalf("effective = %+v, want the running subagent preserved", eff)
		}
	})
}

// TestAuthoritativeIdleRecordsRenderGeneration proves an applied observation
// advances the effective generation and stamps the shadow export metadata with
// the actual reduced result.
func TestAuthoritativeIdleRecordsRenderGeneration(t *testing.T) {
	h := newTestHandler()
	base := time.Now().Unix()
	idleSeedHarness(t, h, 7, "old", "active", base)

	h.stateMu.Lock()
	genBefore := h.store[7].EffectiveGen
	h.stateMu.Unlock()

	obs := AuthoritativeIdle{WindowID: 7, Source: AuthoritativeIdleSource("claude"), Harness: "claude", CapturedSeq: idleShadowSeq(t, h, 7), At: base + 5, RequiredSamples: 1}
	if !h.applyAuthoritativeIdle(obs) {
		t.Fatalf("observation not applied")
	}

	h.stateMu.Lock()
	genAfter := h.store[7].EffectiveGen
	h.stateMu.Unlock()
	if genAfter <= genBefore {
		t.Fatalf("effective generation did not advance: %d -> %d", genBefore, genAfter)
	}

	meta := shadowMeta(t, h, 7)
	if meta.EffectiveIcon != "stop" || meta.EffectiveGen != genAfter {
		t.Fatalf("shadow meta = %+v, want stop at generation %d", meta, genAfter)
	}
}

// TestAuthoritativeIdleNotifiesOnTransitionOnlyAndDedupes proves synthetic stop
// notifications fire only when the reduced icon actually becomes stop and are
// deduplicated by upstream source event ID.
func TestAuthoritativeIdleNotifiesOnTransitionOnlyAndDedupes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		guard := NewStateGuard(time.Hour)
		notifier, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
		h := NewHandler(guard, notifier)
		base := time.Now().Unix()
		tick := int64(0)

		settle := func(sourceEventID string) {
			tick += 10
			idleSeedHarness(t, h, 7, "cur", "active", base+tick)
			tick += 10
			obs := AuthoritativeIdle{
				WindowID:      7,
				Source:        AuthoritativeIdleSource("claude"),
				Harness:       "claude",
				CapturedSeq:   idleShadowSeq(t, h, 7),
				At:            base + tick,
				SessionTitle:  "W:api",
				SourceEventID: sourceEventID,
			}
			if !h.applyAuthoritativeIdle(obs) {
				t.Fatalf("observation %q not applied", sourceEventID)
			}
			if eff, _ := windowEffective(t, h, 7); eff.Icon != "stop" {
				t.Fatalf("observation %q did not settle to stop: %+v", sourceEventID, eff)
			}
			// Out-run the notifier's per-action cooldown so only the source-event
			// dedup stands between a retry and a duplicate toast.
			time.Sleep(11 * time.Second)
		}

		settle("evt-1")
		settle("evt-1")
		synctest.Wait()
		if got := rec.len(); got != 1 {
			t.Fatalf("retried source event produced %d notifications, want 1: %+v", got, rec.snapshot())
		}

		settle("evt-2")
		synctest.Wait()
		if got := rec.len(); got != 2 {
			t.Fatalf("new source event produced %d notifications, want 2: %+v", got, rec.snapshot())
		}
	})
}

// TestAuthoritativeIdleNoWindowIsNoop proves an observation for an unknown
// window (no reducer facts) is dropped without panicking.
func TestAuthoritativeIdleNoWindowIsNoop(t *testing.T) {
	h := newTestHandler()
	obs := AuthoritativeIdle{WindowID: 99, Source: AuthoritativeIdleSource("claude"), CapturedSeq: 0, At: time.Now().Unix(), RequiredSamples: 1}
	if h.applyAuthoritativeIdle(obs) {
		t.Fatalf("observation for an unknown window was applied")
	}
}

// TestIdleSampleExportedTrackingAndWindowClose covers the reusable
// consecutive-sample API and the window-closure cleanup.
func TestIdleSampleExportedTrackingAndWindowClose(t *testing.T) {
	h := newTestHandler()
	src := AuthoritativeIdleSource("claude")
	// Give the window live state so window-closure GC recognizes it as closed.
	idleSeedHarness(t, h, 7, "cur", "active", time.Now().Unix())

	if n := h.RecordIdleSample(7, src); n != 1 {
		t.Fatalf("first RecordIdleSample = %d, want 1", n)
	}
	if n := h.RecordIdleSample(7, src); n != 2 {
		t.Fatalf("second RecordIdleSample = %d, want 2", n)
	}
	// A classifier failure resets the streak.
	h.ClearIdleSample(7, src)
	if n := h.RecordIdleSample(7, src); n != 1 {
		t.Fatalf("RecordIdleSample after clear = %d, want 1", n)
	}

	// Window closure drops the streak with the rest of the window's state.
	h.pruneClosedWindows(time.Now(), map[int]bool{})
	h.stateMu.Lock()
	_, present := h.idleSampleStreak[idleSampleRef{windowID: 7, key: src.Key}]
	h.stateMu.Unlock()
	if present {
		t.Fatalf("window closure did not drop the idle-sample streak")
	}
}
