package daemon

import (
	"fmt"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// These tests pin ASL routing by runtime-session identity: a run's lifecycle,
// its supervisor question, its notification, and the clearing of its attention
// all land on the slot of the session whose hooks delivered them — a tmux pane
// or a Kitty window alike — and never on a Kitty tab that merely shares the
// run's directory. They also pin that none of it needs Kitty to be running.

const runtimeRunID = "run-runtime"

// aslRuntimeFixture is a daemon with one managed Kitty tab in projectDir (the
// workspace-matching decoy) and a registry holding whatever the test adds.
func aslRuntimeFixture(t *testing.T) (*Handler, *fakeRuntimeDirectory, *callRecorder, []kitty.OSWindow, string) {
	t.Helper()
	notifier, calls := newTestNotifier(&config.NotificationConfig{Permission: true})
	h, windows, projectDir := reconcileTestFixture(t)
	h.notifier = notifier
	h.SetStopConfirm(0, 0, false)
	h.SetKittyClient(&fakeKittyClient{windows: windows})
	directory := newFakeRuntimeDirectory()
	h.SetRuntimeSessionDirectory(directory)
	stubDaemonLoopDiscover(t, nil, nil)
	return h, directory, calls, windows, projectDir
}

func aslRuntimeHook(t *testing.T, runtime runtimesession.ID, event string, payload hookevent.HookPayload) protocol.Request {
	t.Helper()
	payload.Source = events.ASLHookSource
	if payload.Harness == "" {
		payload.Harness = "claude"
	}
	return runtimeHook(t, runtime, event, payload)
}

func aslRuntimeQuestion(projectDir, statePath, questionID string, seq uint64, createdAt time.Time) hookevent.HookPayload {
	payload := supervisorMailboxQuestionPayload(projectDir, statePath, questionID, seq, seq, createdAt)
	payload.RunID = runtimeRunID
	payload.KittyWindowID = 0
	payload.SourceEventID = fmt.Sprintf("%s:mailbox-question:%d", runtimeRunID, seq)
	return payload
}

func slotState(h *Handler, slot int) (looping bool, icon string, present bool) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[slot]
	if !ok || st.Reducer == nil {
		return false, "", ok
	}
	effective := st.Reducer.Reduce(time.Now().Unix())
	return effective.Looping, effective.Icon, true
}

func liveASLLease(h *Handler, slot int, runID string) bool {
	for _, fact := range slotFacts(h, slot) {
		if fact.Kind == protocol.SourceASL && fact.RunID == runID && fact.LeaseLive {
			return true
		}
	}
	return false
}

// TestASLRunInTmuxSessionRoutesToItsRuntimeSlot: a run whose hooks name a tmux
// runtime session lands on that session's slot. The managed Kitty tab in the
// same directory and harness — which workspace matching would have chosen —
// receives nothing: no lease, no question, no notification.
func TestASLRunInTmuxSessionRoutesToItsRuntimeSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, directory, calls, _, projectDir := aslRuntimeFixture(t)
		onTmux := newRuntimeID(t)
		directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%3")
		directory.setProject(onTmux, "API")
		statePath := filepath.Join(t.TempDir(), "state.json")

		requireAccepted(t, h.Handle(aslRuntimeHook(t, onTmux, events.AgentSessionLoopStarted, hookevent.HookPayload{
			CWD: projectDir, RunID: runtimeRunID, StatePath: statePath,
			// An inherited Kitty window ID naming the decoy must not route.
			KittyWindowID: 42,
		})))
		slot := slotOf(t, h, onTmux)
		if !isRuntimeSyntheticSlot(slot) {
			t.Fatalf("tmux run landed on Kitty-range slot %d", slot)
		}
		if looping, _, _ := slotState(h, slot); !looping || !liveASLLease(h, slot, runtimeRunID) {
			t.Fatalf("tmux slot facts = %+v; want a live lease and the loop marker", slotFacts(h, slot))
		}
		if _, _, present := slotState(h, 42); present {
			t.Fatalf("the Kitty tab sharing the run's directory received state: %+v", slotFacts(h, 42))
		}
		for _, fact := range slotFacts(h, slot) {
			if fact.Kind == protocol.SourceASL && fact.OriginWindowID != slot {
				t.Fatalf("lease origin = %d; want the session's own slot %d", fact.OriginWindowID, slot)
			}
		}

		now := time.Now().UTC().Truncate(time.Second)
		question := aslRuntimeQuestion(projectDir, statePath, supervisorQuestionID, 7, now)
		requireAccepted(t, h.Handle(aslRuntimeHook(t, onTmux, events.AgentSessionLoopMailboxQuestion, question)))
		synctest.Wait()
		if looping, icon, _ := slotState(h, slot); icon != "question" || !looping {
			t.Fatalf("tmux slot = looping %v icon %q; want question + loop", looping, icon)
		}
		if _, _, present := slotState(h, 42); present {
			t.Fatal("the supervisor question reached the Kitty tab sharing the run's directory")
		}
		got := calls.snapshot()
		if len(got) != 1 || got[0].windowID != slot || got[0].subtitle != "User input requested" {
			t.Fatalf("notifications = %+v; want exactly one for slot %d", got, slot)
		}
		// With no tab title to read, the notice is named by the session's project.
		if got[0].title != "API" {
			t.Fatalf("notification title = %q; want the session's project API", got[0].title)
		}

		// A redelivered handoff is the same question: no second notification.
		requireAccepted(t, h.Handle(aslRuntimeHook(t, onTmux, events.AgentSessionLoopMailboxQuestion, question)))
		synctest.Wait()
		if calls.len() != 1 {
			t.Fatalf("redelivered handoff dispatched %d notifications; want 1", calls.len())
		}

		// The reply closes the question on the slot that showed it.
		reply := supervisorMailboxReplyPayload(asl.SupervisorTarget{Candidate: agentloop.Candidate{
			RunID: runtimeRunID, StateDir: filepath.Dir(statePath),
		}}, 7, 8)
		requireAccepted(t, h.Handle(aslRuntimeHook(t, onTmux, events.AgentSessionLoopMailboxReply, reply)))
		if looping, icon, _ := slotState(h, slot); icon != "" || !looping {
			t.Fatalf("after reply: looping %v icon %q; want the bare loop marker", looping, icon)
		}

		requireAccepted(t, h.Handle(aslRuntimeHook(t, onTmux, events.AgentSessionLoopFinished, hookevent.HookPayload{
			CWD: projectDir, RunID: runtimeRunID, StatePath: statePath,
		})))
		if looping, _, _ := slotState(h, slot); looping || liveASLLease(h, slot, runtimeRunID) {
			t.Fatalf("finished run still holds its lease: %+v", slotFacts(h, slot))
		}
		h.stateMu.Lock()
		_, claimed := h.aslRuntimeRuns[runtimeRunID]
		h.stateMu.Unlock()
		if claimed {
			t.Fatal("a finished run kept its runtime claim")
		}
	})
}

// TestASLRunInKittyRuntimeSessionRoutesToItsOwnWindow: a Kitty runtime session
// is routed by the same identity. Its run lands on the session's registered
// window, not on the title owner of whichever tab shares its directory.
func TestASLRunInKittyRuntimeSessionRoutesToItsOwnWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		notifier, calls := newTestNotifier(&config.NotificationConfig{Permission: true})
		h := newTestHandler()
		h.notifier = notifier
		h.SetStopConfirm(0, 0, false)
		projectDir := t.TempDir()
		// Two managed tabs in one directory: workspace matching cannot tell
		// them apart, the runtime session can.
		windows := []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{
			{ID: 10, Title: palette.BuildTitleParts(palette.TitleParts{Base: "api"}), Windows: []kitty.Window{
				{ID: 42, UserVars: kitty.UserVars{Profile: "api", Harness: "claude", AMXDirectory: projectDir}},
			}},
			{ID: 11, Title: palette.BuildTitleParts(palette.TitleParts{Base: "api-2"}), Windows: []kitty.Window{
				{ID: 43, UserVars: kitty.UserVars{Profile: "api", Harness: "claude", AMXDirectory: projectDir}},
			}},
		}}}
		h.SetKittyClient(&fakeKittyClient{windows: windows})
		h.publishSnapshotFrom(probes.Publication{Windows: windows, Socket: kittyA, CapturedAt: time.Now()})
		directory := newFakeRuntimeDirectory()
		h.SetRuntimeSessionDirectory(directory)
		stubDaemonLoopDiscover(t, nil, nil)
		onKitty := newRuntimeID(t)
		directory.put(onKitty, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "43")
		statePath := filepath.Join(t.TempDir(), "state.json")

		requireAccepted(t, h.Handle(aslRuntimeHook(t, onKitty, events.AgentSessionLoopStarted, hookevent.HookPayload{
			CWD: projectDir, RunID: runtimeRunID, StatePath: statePath,
		})))
		if slot := slotOf(t, h, onKitty); slot != 43 {
			t.Fatalf("Kitty runtime session bound to slot %d; want its window 43", slot)
		}
		if !liveASLLease(h, 43, runtimeRunID) {
			t.Fatalf("window 43 facts = %+v; want the run's lease", slotFacts(h, 43))
		}
		if liveASLLease(h, 42, runtimeRunID) {
			t.Fatal("the sibling tab in the same directory received the run's lease")
		}

		now := time.Now().UTC().Truncate(time.Second)
		requireAccepted(t, h.Handle(aslRuntimeHook(t, onKitty, events.AgentSessionLoopMailboxQuestion,
			aslRuntimeQuestion(projectDir, statePath, supervisorQuestionID, 7, now))))
		synctest.Wait()
		if _, icon, _ := slotState(h, 43); icon != "question" {
			t.Fatalf("window 43 icon = %q; want question", icon)
		}
		if _, icon, _ := slotState(h, 42); icon == "question" {
			t.Fatal("the sibling tab shows the other session's question")
		}
		got := calls.snapshot()
		if len(got) != 1 || got[0].windowID != 43 || got[0].title != "api-2" {
			t.Fatalf("notifications = %+v; want one for window 43 titled api-2", got)
		}

		// The session's notification focuses its own Kitty window through the
		// instance the registry names.
		target := h.notificationTarget(43, "api")
		if !target.Navigable || target.KittyListenOn != kittyA || target.RuntimeSessionID != string(onKitty) || target.Backend != "kitty" {
			t.Fatalf("Kitty notification target = %+v", target)
		}
	})
}

func TestASLRunInCollidingKittyInstanceKeepsSlotAsOrigin(t *testing.T) {
	h := newTestHandler()
	h.SetStopConfirm(0, 0, false)
	directory := newFakeRuntimeDirectory()
	h.SetRuntimeSessionDirectory(directory)
	stubDaemonLoopDiscover(t, nil, nil)
	first, second := newRuntimeID(t), newRuntimeID(t)
	directory.put(first, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "7")
	directory.put(second, runtimesession.StateLive, runtimesession.BackendKitty, kittyB, "kitty-b-1", "7")
	for _, runtime := range []runtimesession.ID{first, second} {
		requireAccepted(t, h.Handle(aslRuntimeHook(t, runtime, events.AgentSessionLoopStarted, hookevent.HookPayload{
			CWD: t.TempDir(), RunID: string(runtime), KittyWindowID: 7,
		})))
	}
	firstSlot, secondSlot := slotOf(t, h, first), slotOf(t, h, second)
	if firstSlot == secondSlot {
		t.Fatal("colliding Kitty runs share a reducer slot")
	}
	for runtime, slot := range map[runtimesession.ID]int{first: firstSlot, second: secondSlot} {
		found := false
		for _, fact := range slotFacts(h, slot) {
			if fact.Kind != protocol.SourceASL || fact.RunID != string(runtime) {
				continue
			}
			found = true
			if fact.OriginWindowID != slot {
				t.Fatalf("runtime %s origin = %d; want slot %d", runtime, fact.OriginWindowID, slot)
			}
		}
		if !found {
			t.Fatalf("runtime %s has no ASL fact in slot %d", runtime, slot)
		}
	}
}

func TestLegacyASLWindowIDSelectsOnlyItsCollidingKittyTab(t *testing.T) {
	h := newTestHandler()
	projectDir := t.TempDir()
	h.stateMu.Lock()
	firstSlot := h.kittySlotLocked(kittyA, 42)
	h.ensureWindowStateLocked(firstSlot)
	h.rememberKittySocketLocked(firstSlot, kittyA)
	h.stateMu.Unlock()
	raw := []kitty.OSWindow{{Tabs: []kitty.Tab{
		{ID: 1, Title: "api", Windows: []kitty.Window{{
			ID: 42, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXDirectory: projectDir},
		}}},
		{ID: 2, Title: "api-other", Windows: []kitty.Window{{
			ID: 44, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXDirectory: projectDir},
		}}},
	}}}
	windows := h.slotMappedKittyWindows(raw, kittyB, true)
	targets, _ := h.aslHookTargets(protocol.Request{KittyListenOn: kittyB}, windows,
		hookevent.HookPayload{KittyWindowID: 42, CWD: projectDir, Harness: "claude"})
	h.stateMu.Lock()
	secondSlot := h.runtimeRoutes.kittySlotOf(kittyB, 42)
	h.stateMu.Unlock()
	if secondSlot == firstSlot || len(targets) != 1 || targets[0].windowID != secondSlot {
		t.Fatalf("colliding legacy ASL targets = %+v; want only slot %d (first %d)", targets, secondSlot, firstSlot)
	}
}

// TestASLRunStaysRoutedWithNoKitty: with no Kitty client at all — no viewer of
// any kind — the run's lifecycle, question, and notification still arrive, and
// the maintenance tick still polls its supervisor projection.
func TestASLRunStaysRoutedWithNoKitty(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		notifier, calls := newTestNotifier(&config.NotificationConfig{Permission: true})
		h := newTestHandler()
		h.notifier = notifier
		h.SetStopConfirm(0, 0, false)
		directory := newFakeRuntimeDirectory()
		h.SetRuntimeSessionDirectory(directory)
		h.SetRuntimeCheckpointPath(filepath.Join(t.TempDir(), runtimeCheckpointFileName))
		onTmux := newRuntimeID(t)
		directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%3")
		projectDir := t.TempDir()
		stateDir := t.TempDir()
		statePath := filepath.Join(stateDir, "state.json")
		candidate := agentloop.Candidate{
			CWD: projectDir, Harness: "claude", RunID: runtimeRunID, StateDir: stateDir, Phase: agentloop.PhaseTurn,
		}
		sweeps := 0
		old := daemonLoopDiscoverPlans
		daemonLoopDiscoverPlans = func(agentloop.DiscoverOptions) (agentloop.DiscoveryResult, error) {
			sweeps++
			return agentloop.DiscoveryResult{StateRoot: "/state", RootPresent: true, Observations: []agentloop.PlanObservation{{
				StateDir: stateDir, RunID: runtimeRunID, Presence: agentloop.PresencePresent,
				Reason: agentloop.ReasonLiveProcess, Candidate: candidate,
			}}}, nil
		}
		t.Cleanup(func() { daemonLoopDiscoverPlans = old })

		// With nothing to reconcile, a snapshot-less tick does not sweep.
		h.maintenanceTick(time.Now())
		if sweeps != 0 {
			t.Fatalf("a daemon with no runtime slots swept %d times without a snapshot", sweeps)
		}

		requireAccepted(t, h.Handle(aslRuntimeHook(t, onTmux, events.AgentSessionLoopTurnStarted, hookevent.HookPayload{
			CWD: projectDir, RunID: runtimeRunID, StatePath: statePath,
		})))
		slot := slotOf(t, h, onTmux)
		now := time.Now().UTC().Truncate(time.Second)
		requireAccepted(t, h.Handle(aslRuntimeHook(t, onTmux, events.AgentSessionLoopMailboxQuestion,
			aslRuntimeQuestion(projectDir, statePath, supervisorQuestionID, 7, now))))
		synctest.Wait()
		if looping, icon, _ := slotState(h, slot); icon != "question" || !looping {
			t.Fatalf("slot = looping %v icon %q; want question + loop without Kitty", looping, icon)
		}
		if got := calls.snapshot(); len(got) != 1 || got[0].windowID != slot {
			t.Fatalf("notifications without Kitty = %+v; want one for slot %d", got, slot)
		}

		// The notification's focus target is the session, and nothing Kitty
		// could be asked for.
		target := h.notificationTarget(slot, "")
		if !target.Navigable || target.KittyListenOn != "" || target.RuntimeSessionID != string(onTmux) || target.Backend != "tmux" {
			t.Fatalf("tmux notification target = %+v", target)
		}

		// The snapshot-less tick now reconciles the run by its claim and keeps
		// its supervision registered.
		h.maintenanceTick(time.Now().Add(asl.ReconcileInterval))
		if sweeps != 1 {
			t.Fatalf("snapshot-less tick swept %d times; want 1", sweeps)
		}
		h.stateMu.Lock()
		supervised, registered := h.aslSupervisors.Target(asl.LeaseRef{WindowID: slot, Key: runtimeRunID})
		h.stateMu.Unlock()
		if !registered || supervised.Candidate.StateDir != stateDir || supervised.OriginWindowID != slot {
			t.Fatalf("supervisor target = %+v (registered %v)", supervised, registered)
		}
		if !liveASLLease(h, slot, runtimeRunID) {
			t.Fatal("a present run lost its lease on a snapshot-less sweep")
		}
		if calls.len() != 1 {
			t.Fatalf("maintenance redelivered the notification: %d", calls.len())
		}
	})
}

// TestASLReconcileNeverSeedsAClaimedRunOntoAKittyTab: discovery sees only a
// directory and a harness. A run a runtime session claimed is attributed to
// that session's slot, and a lease workspace matching placed on a Kitty tab
// before the claim existed is withdrawn rather than left to show a second 🔁.
func TestASLReconcileNeverSeedsAClaimedRunOntoAKittyTab(t *testing.T) {
	h, directory, _, windows, projectDir := aslRuntimeFixture(t)
	onTmux := newRuntimeID(t)
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%3")
	stateDir := t.TempDir()
	candidate := agentloop.Candidate{
		CWD: projectDir, Harness: "claude", RunID: runtimeRunID, StateDir: stateDir, Phase: agentloop.PhaseTurn,
	}
	stubDaemonLoopDiscover(t, []agentloop.Candidate{candidate}, nil)

	// The run's first hook was lost: discovery places it by workspace.
	now := time.Now()
	h.ReconcileASLLeases(now, windows)
	if !liveASLLease(h, 42, runtimeRunID) {
		t.Fatal("fixture: workspace matching did not seed the unclaimed run")
	}

	// Its next hook names the session it really runs in.
	requireAccepted(t, h.Handle(aslRuntimeHook(t, onTmux, events.AgentSessionLoopTurnHeartbeat, hookevent.HookPayload{
		CWD: projectDir, RunID: runtimeRunID, StatePath: filepath.Join(stateDir, "state.json"),
	})))
	slot := slotOf(t, h, onTmux)
	h.ReconcileASLLeases(now.Add(asl.ReconcileInterval), windows)
	if liveASLLease(h, 42, runtimeRunID) {
		t.Fatalf("the Kitty tab kept a lease for a run claimed elsewhere: %+v", slotFacts(h, 42))
	}
	if looping, _, _ := slotState(h, 42); looping {
		t.Fatal("the Kitty tab still shows the loop marker of a run living in tmux")
	}
	if !liveASLLease(h, slot, runtimeRunID) {
		t.Fatal("the claiming session lost its lease")
	}
	h.stateMu.Lock()
	_, onKitty := h.aslSupervisors.Target(asl.LeaseRef{WindowID: 42, Key: runtimeRunID})
	_, onSlot := h.aslSupervisors.Target(asl.LeaseRef{WindowID: slot, Key: runtimeRunID})
	h.stateMu.Unlock()
	if onKitty || !onSlot {
		t.Fatalf("supervision: Kitty tab %v, runtime slot %v; want only the slot", onKitty, onSlot)
	}

	// Later sweeps keep it there.
	h.ReconcileASLLeases(now.Add(2*asl.ReconcileInterval), windows)
	if liveASLLease(h, 42, runtimeRunID) || !liveASLLease(h, slot, runtimeRunID) {
		t.Fatal("a later sweep moved the claimed run")
	}
}

// TestASLReconcileWithoutSnapshotLeavesKittyLeasesAlone: an empty snapshot is
// not evidence about a Kitty window. A sweep that runs for the sake of a
// runtime slot retires that slot's absent run after two misses and never
// touches the Kitty lease it cannot see.
func TestASLReconcileWithoutSnapshotLeavesKittyLeasesAlone(t *testing.T) {
	h, directory, _, windows, projectDir := aslRuntimeFixture(t)
	onTmux := newRuntimeID(t)
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%3")
	kittyState := t.TempDir()
	stubDaemonLoopDiscover(t, []agentloop.Candidate{{
		CWD: projectDir, Harness: "claude", RunID: "run-kitty", StateDir: kittyState, Phase: agentloop.PhaseTurn,
	}}, nil)
	now := time.Now()
	h.ReconcileASLLeases(now, windows)
	if !liveASLLease(h, 42, "run-kitty") {
		t.Fatal("fixture: Kitty run not seeded")
	}
	requireAccepted(t, h.Handle(aslRuntimeHook(t, onTmux, events.AgentSessionLoopTurnStarted, hookevent.HookPayload{
		CWD: t.TempDir(), RunID: runtimeRunID, StatePath: filepath.Join(t.TempDir(), "state.json"),
	})))
	slot := slotOf(t, h, onTmux)

	// Both runs vanish from discovery; no Kitty snapshot is available.
	stubDaemonLoopDiscover(t, nil, nil)
	h.ReconcileASLLeases(now.Add(asl.ReconcileInterval), nil)
	if !liveASLLease(h, slot, runtimeRunID) {
		t.Fatal("one miss released the runtime slot's lease")
	}
	h.ReconcileASLLeases(now.Add(2*asl.ReconcileInterval), nil)
	if liveASLLease(h, slot, runtimeRunID) {
		t.Fatal("two confirmed misses left the runtime slot's lease live")
	}
	if !liveASLLease(h, 42, "run-kitty") {
		t.Fatal("a snapshot-less sweep released a Kitty window's lease")
	}
}

// TestOperatorClearReachesARuntimeSlot: F8 from a session with no Kitty tab
// behind it clears that session's attention, with Kitty running and without.
func TestOperatorClearReachesARuntimeSlot(t *testing.T) {
	for _, withKitty := range []bool{true, false} {
		t.Run(fmt.Sprintf("kitty=%v", withKitty), func(t *testing.T) {
			h := newTestHandler()
			h.SetStopConfirm(0, 0, false)
			if withKitty {
				h.SetKittyClient(&fakeKittyClient{windows: hookEventWindows("api", []kitty.Window{
					{ID: 7, IsActive: true, UserVars: kitty.UserVars{Profile: "managed", Harness: "claude"}},
				})})
			}
			directory := newFakeRuntimeDirectory()
			h.SetRuntimeSessionDirectory(directory)
			onTmux := newRuntimeID(t)
			directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%3")

			requireAccepted(t, h.Handle(runtimeHook(t, onTmux, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))
			slot := slotOf(t, h, onTmux)
			if icon := slotIcon(h, slot); icon == "" {
				t.Fatal("fixture: the permission request raised no attention icon")
			}
			requireAccepted(t, h.Handle(runtimeHook(t, onTmux, "user_clear", hookevent.HookPayload{})))
			if icon := slotIcon(h, slot); icon != "" {
				t.Fatalf("after user_clear the slot still shows %q", icon)
			}
			if withKitty {
				if _, _, present := slotState(h, 7); present {
					t.Fatal("the clear reached a Kitty window")
				}
			}
		})
	}
}
