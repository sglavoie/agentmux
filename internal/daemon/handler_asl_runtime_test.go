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
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// These tests pin ASL routing by runtime-session identity: a run's lifecycle,
// its supervisor question, its notification, and the clearing of its attention
// all land on the slot of the session whose hooks delivered them — a tmux pane. They also pin that none of it needs a viewer to be running.

const runtimeRunID = "run-runtime"

// aslRuntimeFixture is a daemon with a runtime registry and a recording notifier.
func aslRuntimeFixture(t *testing.T) (*Handler, *fakeRuntimeDirectory, *callRecorder, string) {
	t.Helper()
	notifier, calls := newTestNotifier(&config.NotificationConfig{Permission: true})
	h := newTestHandler()
	h.notifier = notifier
	h.SetStopConfirm(0, 0, false)
	directory := newFakeRuntimeDirectory()
	h.SetRuntimeSessionDirectory(directory)
	stubDaemonLoopDiscover(t, nil, nil)
	return h, directory, calls, t.TempDir()
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
// runtime session lands its lease, question, and notification on that session's slot.
func TestASLRunInTmuxSessionRoutesToItsRuntimeSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, directory, calls, projectDir := aslRuntimeFixture(t)
		onTmux := newRuntimeID(t)
		directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%3")
		directory.setProject(onTmux, "API")
		statePath := filepath.Join(t.TempDir(), "state.json")

		requireAccepted(t, h.Handle(aslRuntimeHook(t, onTmux, events.AgentSessionLoopStarted, hookevent.HookPayload{
			CWD: projectDir, RunID: runtimeRunID, StatePath: statePath,
		})))
		slot := slotOf(t, h, onTmux)
		if !isRuntimeSyntheticSlot(slot) {
			t.Fatalf("tmux run landed on an invalid slot %d", slot)
		}
		if looping, _, _ := slotState(h, slot); !looping || !liveASLLease(h, slot, runtimeRunID) {
			t.Fatalf("tmux slot facts = %+v; want a live lease and the loop marker", slotFacts(h, slot))
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

// TestASLRunStaysRoutedWithoutViewer exercises lifecycle and supervision with
// no UI observing the daemon.
func TestASLRunStaysRoutedWithoutViewer(t *testing.T) {
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
			t.Fatalf("slot = looping %v icon %q; want question + loop without a viewer", looping, icon)
		}
		if got := calls.snapshot(); len(got) != 1 || got[0].windowID != slot {
			t.Fatalf("notifications without a viewer = %+v; want one for slot %d", got, slot)
		}

		// The notification's focus target is the session, and no terminal lookup is needed.
		target := h.notificationTarget(slot, "")
		if !target.Navigable || target.RuntimeSessionID != string(onTmux) || target.Backend != "tmux" {
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

// TestOperatorClearReachesARuntimeSlot clears the selected runtime session's attention.
func TestOperatorClearReachesARuntimeSlot(t *testing.T) {
	t.Parallel()
	h, directory := newRuntimeTestHandler(t)
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
}

// TestASLHooksTranslatedBeforeTheirTmuxSessionEndsLeaveTheSlotClosed: a hook
// translated while its session was live can still be handled after the
// maintenance pass ended the session and closed its slot. Neither a lifecycle
// hook (a loop marker) nor a supervisor question may bring the slot's state
// back, claim the run for it, register its supervision, or notify: nothing
// would ever drop any of it again.
func TestASLHooksTranslatedBeforeTheirTmuxSessionEndsLeaveTheSlotClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, directory, calls, projectDir := aslRuntimeFixture(t)
		onTmux := newRuntimeID(t)
		directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%3")
		statePath := filepath.Join(t.TempDir(), "state.json")
		requireAccepted(t, h.Handle(aslRuntimeHook(t, onTmux, events.AgentSessionLoopTurnStarted, hookevent.HookPayload{
			CWD: projectDir, RunID: runtimeRunID, StatePath: statePath,
		})))
		slot := slotOf(t, h, onTmux)

		translate := func(event string, payload hookevent.HookPayload) protocol.Request {
			t.Helper()
			req, rejection := h.translateHookOrigin(aslRuntimeHook(t, onTmux, event, payload))
			if rejection != nil {
				t.Fatalf("%s translation refused: %+v", event, rejection)
			}
			return req
		}
		heartbeat := translate(events.AgentSessionLoopTurnHeartbeat, hookevent.HookPayload{
			CWD: projectDir, RunID: runtimeRunID, StatePath: statePath,
		})
		question := translate(events.AgentSessionLoopMailboxQuestion,
			aslRuntimeQuestion(projectDir, statePath, supervisorQuestionID, 7, time.Now().UTC().Truncate(time.Second)))

		directory.setState(onTmux, runtimesession.StateEnded)
		h.retireEndedRuntimeSessions(time.Now())
		h.handleHookEvent(heartbeat)
		h.handleHookEvent(question)
		synctest.Wait()

		h.stateMu.Lock()
		_, revived := h.store[slot]
		_, claimed := h.aslRuntimeRuns[runtimeRunID]
		_, supervised := h.aslSupervisors.Target(asl.LeaseRef{WindowID: slot, Key: runtimeRunID})
		h.stateMu.Unlock()
		if revived {
			t.Fatalf("a late ASL hook brought the closed slot's state back: %+v", slotFacts(h, slot))
		}
		if claimed || supervised {
			t.Fatalf("closed slot kept the run: claimed %v, supervised %v", claimed, supervised)
		}
		if got := calls.len(); got != 0 {
			t.Fatalf("notifications = %d; want 0 (%+v)", got, calls.snapshot())
		}
	})
}
