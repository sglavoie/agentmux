package companion

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// These tests pin the negotiated mixed-backend contract: a v1 projection is
// exactly what it was before runtime sessions existed, and a v2 projection
// adds tmux sessions without ever giving them a Kitty identity.

const (
	tmuxSlot    = 1 << 40
	kittyRS     = "rs-0123456789abcdef0123456789abcdef"
	tmuxRS      = "rs-fedcba9876543210fedcba9876543210"
	tmuxAddress = "/tmp/amx-tmux.sock"
)

func mixedInput(version int) Input {
	conv := conversation.Identity{Harness: "codex", SessionID: "tmux-conv"}
	target := func(window int, run string) asl.SupervisorTarget {
		return asl.SupervisorTarget{WindowID: window, Candidate: agentloop.Candidate{RunID: run, PlanDir: "/plans/" + run}}
	}
	return Input{
		Kitty: tree(sockA, kitty.Tab{ID: 10, Title: "W:api", Windows: []kitty.Window{managed(7, "work", "claude")}}),
		States: map[int]protocol.WindowStateSnapshot{
			7:        {IconState: "bolt"},
			tmuxSlot: {IconState: "key", Conversation: &conv, LastEventAt: 1_700_000_000},
		},
		Sockets: map[int]string{7: sockA},
		Runtime: map[int]RuntimeSlot{
			7:        {SessionID: kittyRS, Backend: "kitty", Instance: sockA, Generation: "k1", Pane: "7"},
			tmuxSlot: {SessionID: tmuxRS, Backend: "tmux", Instance: tmuxAddress, Generation: "t1", Pane: "%3"},
		},
		ASLTargets: []asl.SupervisorTarget{target(tmuxSlot, "run-t")},
		ASLPending: []asl.PendingQuestion{{
			Target:     target(tmuxSlot, "run-t"),
			Projection: agentloop.SupervisorQuestionProjection{RunID: "run-t", UnansweredQuestions: 1},
		}},
		SnapshotVersion: version,
	}
}

// TestProject_V1HidesRuntimeSessions: an old client sees the Kitty session,
// unchanged, and nothing of the tmux one — no session, no attention count, no
// waiting run under a slot number posing as a window ID.
func TestProject_V1HidesRuntimeSessions(t *testing.T) {
	for _, version := range []int{0, protocol.CompanionSnapshotVersion} {
		snap := Project(mixedInput(version))
		if snap.V != protocol.CompanionSnapshotVersion {
			t.Fatalf("v = %d", snap.V)
		}
		if len(snap.Sessions) != 1 || snap.Sessions[0].WindowID != 7 || snap.Sessions[0].Runtime != nil {
			t.Fatalf("v1 sessions = %+v; want only Kitty window 7 without a runtime block", snap.Sessions)
		}
		if snap.Attention.Total != 0 || snap.Attention.Key != 0 || snap.Attention.Working != 1 {
			t.Fatalf("v1 attention = %+v; the tmux session must not be counted", snap.Attention)
		}
		if len(snap.ASL.Waiting) != 0 {
			t.Fatalf("v1 waiting = %+v; a run on a tmux slot has no window to name", snap.ASL.Waiting)
		}
		// Byte-level: the v1 projection of a mixed world equals the projection
		// of the same world with the tmux session and the runtime table removed.
		kittyOnly := mixedInput(version)
		delete(kittyOnly.States, tmuxSlot)
		kittyOnly.Runtime = nil
		kittyOnly.ASLTargets, kittyOnly.ASLPending = nil, nil
		if Digest(snap) != Digest(Project(kittyOnly)) {
			got, _ := json.Marshal(snap)
			want, _ := json.Marshal(Project(kittyOnly))
			t.Fatalf("v1 drifted with runtime sessions present:\n got %s\nwant %s", got, want)
		}
	}
}

// TestProject_V2ListsRuntimeSessionsWithoutKittyIDs: the tmux session appears
// addressed by its runtime block alone, and the Kitty session gains its own.
func TestProject_V2ListsRuntimeSessionsWithoutKittyIDs(t *testing.T) {
	snap := Project(mixedInput(protocol.CompanionSnapshotVersionRuntime))
	if snap.V != protocol.CompanionSnapshotVersionRuntime || len(snap.Sessions) != 2 {
		t.Fatalf("v2 = %+v", snap)
	}
	kittySession := snap.Sessions[0]
	if kittySession.WindowID != 7 || kittySession.Runtime == nil || kittySession.Runtime.SessionID != kittyRS ||
		kittySession.Runtime.Backend != "kitty" || kittySession.Runtime.Pane != "7" {
		t.Fatalf("Kitty session = %+v (runtime %+v)", kittySession, kittySession.Runtime)
	}
	tmux := snap.Sessions[1]
	want := protocol.CompanionSession{
		Harness:      "codex",
		Conversation: &conversation.Identity{Harness: "codex", SessionID: "tmux-conv"},
		ASLRunID:     "run-t",
		IconState:    "key",
		Attention:    true,
		LastEventAt:  1_700_000_000,
		Runtime:      &protocol.CompanionRuntime{SessionID: tmuxRS, Backend: "tmux", Instance: tmuxAddress, Generation: "t1", Pane: "%3"},
	}
	if !reflect.DeepEqual(tmux, want) {
		t.Fatalf("tmux session = %+v\nwant %+v", tmux, want)
	}
	if snap.Attention.Total != 1 || snap.Attention.Key != 1 {
		t.Fatalf("v2 attention = %+v", snap.Attention)
	}
	wantWaiting := []protocol.CompanionASLWaiting{{RunID: "run-t", PlanDir: "/plans/run-t", Unanswered: 1, RuntimeSessionID: tmuxRS}}
	if !reflect.DeepEqual(snap.ASL.Waiting, wantWaiting) {
		t.Fatalf("v2 waiting = %+v", snap.ASL.Waiting)
	}
	if contains(snap.Kitty.Sockets, tmuxAddress) {
		t.Fatalf("a tmux server was listed as a Kitty socket: %v", snap.Kitty.Sockets)
	}
}

// TestProject_V2RuntimeBlockNeedsTheSameKittyInstance: a runtime session of
// another Kitty instance that happens to share the window ID is not attached
// to this instance's window.
func TestProject_V2RuntimeBlockNeedsTheSameKittyInstance(t *testing.T) {
	in := mixedInput(protocol.CompanionSnapshotVersionRuntime)
	in.Runtime[7] = RuntimeSlot{SessionID: kittyRS, Backend: "kitty", Instance: sockB, Pane: "7"}
	if s := sessionByWindow(t, Project(in), sockA, 7); s.Runtime != nil {
		t.Fatalf("window 7 of %s took a runtime of %s: %+v", sockA, sockB, s.Runtime)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
