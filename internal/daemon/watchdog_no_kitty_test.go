package daemon

// The watchdog over tmux sessions in a daemon that has no Kitty. Its sweeps
// were once reached only past a published Kitty snapshot, so a session on
// another backend never went stale, never reported silent hooks, and never
// learned its branch unless a Kitty happened to be running beside it.

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/gitstate"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// noKittyRunner fails the test for any `kitten @` it is asked to run.
type noKittyRunner struct{ t *testing.T }

func (r noKittyRunner) Run(_ string, args ...string) ([]byte, []byte, error) {
	r.t.Errorf("a daemon with no Kitty ran kitten %v", args)
	return []byte("{}"), nil, nil
}

// noKittyWatchdogWorld is a daemon started with no Kitty socket and one tmux
// session bound to a slot by its first hook, labeled as its pane says.
type noKittyWatchdogWorld struct {
	h       *Handler
	rec     *callRecorder
	runtime runtimesession.ID
	slot    int
}

func newNoKittyWatchdogWorld(t *testing.T, label RuntimeSessionLabel) noKittyWatchdogWorld {
	t.Helper()
	stubLoopDiscovery(t)
	notifier, rec := newTestNotifier(&config.NotificationConfig{})
	h := NewHandler(activity.NewStateGuard(time.Hour), notifier)
	h.SetStopConfirm(0, 0, false)
	h.useKittyClient(kitty.NewClientWithSocket(noKittyRunner{t}, ""))
	directory := newFakeRuntimeDirectory()
	h.SetRuntimeSessionDirectory(directory)
	h.SetRuntimeSessionLabeler(func(runtimesession.Record) (RuntimeSessionLabel, error) { return label, nil })
	h.MarkReadyWithoutKitty()
	if _, _, ready := h.cachedSnapshot(); ready {
		t.Fatal("a daemon with no Kitty published a Kitty snapshot")
	}

	runtime := newRuntimeID(t)
	directory.put(runtime, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%7")
	hook := runtimeHook(t, runtime, "UserPromptSubmit", hookevent.HookPayload{SessionID: "conv-1"})
	hook.Account = label.Profile
	requireAccepted(t, h.Handle(hook))
	h.labelRuntimeSessions(time.Now())
	slot := slotOf(t, h, runtime)
	if !isRuntimeSyntheticSlot(slot) {
		t.Fatalf("slot = %d; want a tmux session's slot", slot)
	}
	return noKittyWatchdogWorld{h: h, rec: rec, runtime: runtime, slot: slot}
}

func TestWatchdogWithNoKittySweepsStaleTmuxSlots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newNoKittyWatchdogWorld(t, RuntimeSessionLabel{Project: "API", Profile: "work", Harness: "claude"})
		h := w.h
		h.SetStaleThresholds(map[string]time.Duration{"active": 5 * time.Second}, nil)
		base := time.Now()
		h.stateMu.Lock()
		st := h.store[w.slot]
		st.LastAction = "active"
		st.Activity = base
		h.stateMu.Unlock()
		seedReducerFact(h, w.slot, base, protocol.ReducerEvent{
			Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "main"},
			Action: "active", Seq: 100, At: base.Unix(),
		})
		if got := effectiveState(h, w.slot).Icon; got != "bolt" {
			t.Fatalf("icon before any tick = %q; want bolt", got)
		}

		now := base
		for i := 0; i < 6; i++ {
			now = now.Add(watchdogInterval)
			h.watchdogTick(now, watchdogInterval)
		}
		synctest.Wait()

		if got := effectiveState(h, w.slot).Icon; got != "" {
			t.Fatalf("icon after the threshold = %q; a tmux session with no Kitty never went stale", got)
		}
		if !containsEvent(windowEventNames(h, w.slot), eventWatchdogStale) {
			t.Fatalf("events = %v; want %s", windowEventNames(h, w.slot), eventWatchdogStale)
		}
		if got := w.rec.len(); got != 1 {
			t.Fatalf("stale notifications = %d; want 1", got)
		}
	})
}

func TestWatchdogWithNoKittyReportsSilentHooksOfATmuxProfile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newNoKittyWatchdogWorld(t, RuntimeSessionLabel{Project: "API", Profile: "Work", Harness: "claude"})
		h := w.h
		h.SetHookSilenceThresholds(time.Hour, nil)
		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		h.stateMu.Unlock()

		h.watchdogTick(now, watchdogInterval)
		h.watchdogTick(now.Add(watchdogInterval), watchdogInterval)
		synctest.Wait()

		calls := w.rec.snapshot()
		if len(calls) != 1 || calls[0].title != "Work" || calls[0].subtitle != "Hook silence detected" {
			t.Fatalf("dispatches = %+v; want one hook-silence notice for the session's profile", calls)
		}
	})
}

// A profile is running only while a session of it is bound: one whose session
// ended is not reported silent.
func TestHookSilenceIgnoresAProfileWithNoBoundTmuxSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newNoKittyWatchdogWorld(t, RuntimeSessionLabel{Project: "API", Profile: "work", Harness: "claude"})
		h := w.h
		h.SetHookSilenceThresholds(time.Hour, nil)
		now := time.Now()
		h.stateMu.Lock()
		h.lastSeenEventAt["work"] = now.Add(-2 * time.Hour)
		h.lastSeenEventAt["personal"] = now.Add(-2 * time.Hour)
		closed := h.releaseEndedRuntimeLocked(map[int]string{w.slot: string(w.runtime)}, now)
		h.stateMu.Unlock()
		h.finishClosedWindows(closed)

		h.watchdogTick(now, watchdogInterval)
		synctest.Wait()

		if got := w.rec.snapshot(); len(got) != 0 {
			t.Fatalf("dispatches = %+v; no session of either profile is running", got)
		}
	})
}

func TestGitStateOfATmuxSlotComesFromItsLabelWithNoKitty(t *testing.T) {
	git := &scriptedGitState{branches: map[string]string{"/repo/api": "amx/feature"}, dirty: map[string]bool{"/repo/api": true}}
	w := newNoKittyWatchdogWorld(t, RuntimeSessionLabel{Project: "API", Profile: "work", Harness: "claude", Directory: "/repo/api"})
	h := w.h
	h.setGitStateCache(gitstate.New(gitstate.Options{Git: git.run}))
	git.before = func(directory string) {
		if !h.stateMu.TryLock() {
			t.Errorf("stateMu was held across the git subprocess for %s", directory)
			return
		}
		h.stateMu.Unlock()
	}

	h.probeRunner = func(fn func()) { fn() }

	h.watchdogTick(time.Now(), watchdogInterval)

	got := windowGitState(t, h, w.slot)
	if got.GitDirectory != "/repo/api" || got.GitBranch != "amx/feature" || !got.GitDirty {
		t.Fatalf("slot git state = %q %q dirty=%v; want amx/feature, dirty, in /repo/api", got.GitDirectory, got.GitBranch, got.GitDirty)
	}
}

// The probes run with no lock held, so a slot can have been released, or its
// session's directory relabeled, before the results are written back.
func TestGitStateOfATmuxSlotIsNotWrittenOverAChangedLabel(t *testing.T) {
	git := &scriptedGitState{branches: map[string]string{"/repo/api": "main"}}
	w := newNoKittyWatchdogWorld(t, RuntimeSessionLabel{Project: "API", Profile: "work", Harness: "claude", Directory: "/repo/api"})
	h := w.h
	h.setGitStateCache(gitstate.New(gitstate.Options{Git: git.run}))
	h.runGitStateRefresh(nil)
	if got := windowGitState(t, h, w.slot); got.GitBranch != "main" {
		t.Fatalf("branch = %q; want main", got.GitBranch)
	}

	targets := h.gitStateTargets(nil)
	h.stateMu.Lock()
	label := h.runtimeLabels[string(w.runtime)]
	label.directory = "/repo/other"
	h.runtimeLabels[string(w.runtime)] = label
	h.stateMu.Unlock()
	h.applyGitState(targets, map[string]gitstate.State{"/repo/api": {Branch: "main"}})

	if got := windowGitState(t, h, w.slot); got.GitDirectory != "" || got.GitBranch != "" {
		t.Fatalf("git state = %q %q; a result for a directory the session left was kept", got.GitDirectory, got.GitBranch)
	}
}

// With no snapshot there is no evidence about a Kitty window, so the sweeps
// that now run for tmux sessions still leave it alone.
func TestWatchdogWithNoSnapshotLeavesKittySlotsAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newNoKittyWatchdogWorld(t, RuntimeSessionLabel{Project: "API", Profile: "work", Harness: "claude"})
		h := w.h
		h.SetStaleThresholds(map[string]time.Duration{"active": 5 * time.Second}, nil)
		base := time.Now()
		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(42)
		st.LastAction = "active"
		st.Activity = base
		h.stateMu.Unlock()
		seedReducerFact(h, 42, base, protocol.ReducerEvent{
			Source: protocol.SourceID{Kind: protocol.SourceHarness, Key: "main"},
			Action: "active", Seq: 1, At: base.Unix(),
		})

		now := base
		for i := 0; i < 10; i++ {
			now = now.Add(watchdogInterval)
			h.watchdogTick(now, watchdogInterval)
		}
		synctest.Wait()

		if got := effectiveState(h, 42).Icon; got != "bolt" {
			t.Fatalf("icon of a Kitty slot with no snapshot = %q; want bolt", got)
		}
	})
}
