package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/companion"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// runtimeCompanionSessions is the v2 snapshot's sessions, by runtime session.
func runtimeCompanionSessions(t *testing.T, h *Handler) map[string]protocol.CompanionSession {
	t.Helper()
	h.stateMu.Lock()
	in := h.copyCompanionStateLocked(time.Now())
	h.stateMu.Unlock()
	in.SnapshotVersion = protocol.CompanionSnapshotVersionRuntime
	out := map[string]protocol.CompanionSession{}
	for _, s := range companion.Project(in).Sessions {
		if s.Runtime != nil {
			out[s.Runtime.SessionID] = s
		}
	}
	return out
}

// A harness waiting at its own trust prompt has run no hook, and one whose
// hooks are not installed never will. Its tmux session is running all the
// same, so a companion lists it from the first pass that reads its pane,
// under the harness and profile it was launched with; its first hook then
// lands in the slot it was given.
func TestALiveTmuxSessionIsListedBeforeItsFirstHook(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	quiet, kittyOnly, pending := newRuntimeID(t), newRuntimeID(t), newRuntimeID(t)
	directory.put(quiet, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%7")
	directory.put(kittyOnly, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "7")
	directory.put(pending, runtimesession.StatePending, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%8")
	h.SetRuntimeSessionLabeler(func(runtimesession.Record) (RuntimeSessionLabel, error) {
		return RuntimeSessionLabel{Project: "LogScanner", Profile: "work-codex", Harness: "codex"}, nil
	})
	h.SetRuntimeSessionReconciler(func(time.Time) (tmux.Report, error) {
		return tmux.Report{Mutes: []tmux.Muted{{ID: quiet}, {ID: kittyOnly}, {ID: pending}, {ID: newRuntimeID(t)}}}, nil
	})

	now := time.Now()
	h.reconcileRuntimeSessions(now)
	if got := runtimeCompanionSessions(t, h); len(got) != 0 {
		t.Fatalf("sessions = %+v; a pass alone adopts nothing, the restores come first", got)
	}
	h.adoptRuntimeSessions()
	h.labelRuntimeSessions(now)

	sessions := runtimeCompanionSessions(t, h)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %+v; want the live tmux session alone", sessions)
	}
	listed, ok := sessions[string(quiet)]
	if !ok || listed.Harness != "codex" || listed.Profile != "work-codex" || listed.Project != "LogScanner" {
		t.Fatalf("listed = %+v; want the launch's harness, profile, and project", listed)
	}
	if listed.Attention || listed.IconState != "" || listed.Conversation != nil {
		t.Fatalf("listed = %+v; a session that said nothing has no state", listed)
	}
	slot := slotOf(t, h, quiet)

	// Adopting again changes nothing, and the session's first hook is its own.
	h.adoptRuntimeSessions()
	requireAccepted(t, h.Handle(runtimeHook(t, quiet, "PermissionRequest", hookevent.HookPayload{SessionID: "conv-quiet"})))
	if got := slotOf(t, h, quiet); got != slot {
		t.Fatalf("slot = %d after the first hook, adopted at %d", got, slot)
	}
	if got := runtimeCompanionSessions(t, h)[string(quiet)]; !got.Attention || got.Conversation == nil {
		t.Fatalf("after its first hook = %+v; want the hook's state", got)
	}
}

// A slot another live session holds is not taken by adoption.
func TestAdoptionLeavesAnOccupiedSlotAlone(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	holder, late := newRuntimeID(t), newRuntimeID(t)
	directory.put(holder, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%7")
	requireAccepted(t, h.Handle(runtimeHook(t, holder, "PermissionRequest", hookevent.HookPayload{SessionID: "conv-holder"})))
	slot := slotOf(t, h, holder)
	directory.put(late, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%7")

	h.runtimeSeenLive = []string{string(late)}
	h.adoptRuntimeSessions()
	if got := slotOf(t, h, holder); got != slot {
		t.Fatalf("holder's slot = %d, was %d", got, slot)
	}
	if _, listed := runtimeCompanionSessions(t, h)[string(late)]; listed {
		t.Fatal("a session was adopted into a slot a live one holds")
	}
}
