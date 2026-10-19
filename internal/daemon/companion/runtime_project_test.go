package companion

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
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
			tmuxSlot: {SessionID: tmuxRS, Backend: "tmux", Instance: tmuxAddress, Generation: "t1", Pane: "%3", Project: "API", Profile: "work"},
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
		Profile:      "work",
		Project:      "API",
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

func TestProject_CollidingKittyWindowNumbersKeepIndependentState(t *testing.T) {
	const otherSlot = 1 << 32
	in := Input{
		Kitty: tree(sockA, kitty.Tab{ID: 10, Title: "W:api", Windows: []kitty.Window{managed(7, "work", "claude")}}),
		Others: map[string][]kitty.OSWindow{
			sockB: tree(sockB, kitty.Tab{ID: 20, Title: "W:docs", Windows: []kitty.Window{managed(7, "work", "codex")}}).Windows,
		},
		States: map[int]protocol.WindowStateSnapshot{
			7:         {IconState: "bolt"},
			otherSlot: {IconState: "key"},
		},
		Sockets: map[int]string{7: sockA, otherSlot: sockB},
		KittySlots: map[KittyWindowKey]int{
			{Socket: sockA, WindowID: 7}: 7,
			{Socket: sockB, WindowID: 7}: otherSlot,
		},
		Runtime: map[int]RuntimeSlot{
			7:         {SessionID: kittyRS, Backend: "kitty", Instance: sockA, Pane: "7"},
			otherSlot: {SessionID: tmuxRS, Backend: "kitty", Instance: sockB, Pane: "7"},
		},
		SnapshotVersion: protocol.CompanionSnapshotVersionRuntime,
	}
	snap := Project(in)
	if len(snap.Sessions) != 2 {
		t.Fatalf("sessions = %+v, want both Kitty instances", snap.Sessions)
	}
	a := sessionByWindow(t, snap, sockA, 7)
	b := sessionByWindow(t, snap, sockB, 7)
	if a.IconState != "bolt" || b.IconState != "key" ||
		a.Runtime == nil || a.Runtime.SessionID != kittyRS ||
		b.Runtime == nil || b.Runtime.SessionID != tmuxRS {
		t.Fatalf("colliding states or runtime identities crossed: A=%+v B=%+v", a, b)
	}
}

func TestProject_NewKittyInstanceDoesNotBorrowAnotherInstancesASLRun(t *testing.T) {
	in := mixedInput(protocol.CompanionSnapshotVersionRuntime)
	in.Kitty = tree(sockB, kitty.Tab{ID: 20, Title: "W:docs", Windows: []kitty.Window{managed(7, "work", "codex")}})
	in.KittySlots = map[KittyWindowKey]int{{Socket: sockA, WindowID: 7}: 7}
	in.ASLTargets = []asl.SupervisorTarget{
		{WindowID: 7, Candidate: agentloop.Candidate{RunID: "run-a"}},
		{WindowID: 0, Candidate: agentloop.Candidate{RunID: "run-unbound"}},
	}
	session := sessionByWindow(t, Project(in), sockB, 7)
	if session.ASLRunID != "" || session.IconState != "" || session.Runtime != nil {
		t.Fatalf("new Kitty instance borrowed window 7 from %s: %+v", sockA, session)
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

// TestProject_ARuntimeSessionIsMutedByTheDaemonsReading: a tmux session has no
// title or window to read a mute from, so the row carries the daemon's own
// reading of its pane. A Kitty row keeps reading Kitty: the daemon's cache of
// that window never mutes it here.
func TestProject_ARuntimeSessionIsMutedByTheDaemonsReading(t *testing.T) {
	in := mixedInput(protocol.CompanionSnapshotVersionRuntime)
	if snap := Project(in); snap.Sessions[0].Muted || snap.Sessions[1].Muted {
		t.Fatalf("muted with no mute anywhere: %+v", snap.Sessions)
	}
	for slot, state := range in.States {
		state.Muted = true
		in.States[slot] = state
	}
	snap := Project(in)
	if !snap.Sessions[1].Muted || snap.Sessions[1].Runtime == nil || snap.Sessions[1].Runtime.SessionID != tmuxRS {
		t.Fatalf("the tmux session is not muted: %+v", snap.Sessions[1])
	}
	if snap.Sessions[0].Muted {
		t.Fatalf("the Kitty session took its mute from the daemon's cache instead of Kitty: %+v", snap.Sessions[0])
	}
}

// TestProject_V2ListsSessionsInTheDashboardsOrder: a v2 snapshot lists Kitty
// and tmux sessions together in the order the dashboard sorts the same
// sessions into, in either sort mode, whatever order Kitty's tabs or the
// runtime-session IDs are in. v1 still lists Kitty's tabs as they stand.
func TestProject_V2ListsSessionsInTheDashboardsOrder(t *testing.T) {
	star := palette.StarIcon()
	const (
		slotA = tmuxSlot + iota
		slotB
		slotC
	)
	input := func(version int, sort string) Input {
		return Input{
			Kitty: tree(sockA,
				kitty.Tab{ID: 10, Title: "web", Windows: []kitty.Window{managed(7, "home", "claude")}},
				kitty.Tab{ID: 11, Title: "docs", Windows: []kitty.Window{managed(8, "home", "claude")}},
			),
			States: map[int]protocol.WindowStateSnapshot{7: {}, 8: {}, slotA: {}, slotB: {}, slotC: {}},
			Runtime: map[int]RuntimeSlot{
				slotA: {SessionID: "rs-00000000000000000000000000000001", Backend: "tmux", Profile: "work", Title: "zeta"},
				slotB: {SessionID: "rs-00000000000000000000000000000002", Backend: "tmux", Profile: "work", Title: "api"},
				slotC: {SessionID: "rs-00000000000000000000000000000003", Backend: "tmux", Profile: "work", Title: star + "notes"},
			},
			Sort:            sort,
			SnapshotVersion: version,
		}
	}
	// The same sessions as the dashboard reads them.
	running := []session.RunningSession{
		{Key: "web", Account: "home", Base: "web"},
		{Key: "docs", Account: "home", Base: "docs"},
		{Key: "zeta", Account: "work", Base: "zeta"},
		{Key: "api", Account: "work", Base: "api"},
		{Key: "notes", Account: "work", Base: "notes", Starred: true},
	}
	name := func(s protocol.CompanionSession) string {
		switch {
		case s.WindowID == 7:
			return "web"
		case s.WindowID == 8:
			return "docs"
		case s.Runtime == nil:
			return "?"
		}
		return map[string]string{"1": "zeta", "2": "api", "3": "notes"}[s.Runtime.SessionID[len(s.Runtime.SessionID)-1:]]
	}
	for mode, want := range map[string][]string{
		"alpha":   {"notes", "api", "docs", "web", "zeta"},
		"account": {"notes", "docs", "web", "api", "zeta"},
	} {
		var dashboard, listed []string
		for _, rs := range session.SortRunning(running, mode) {
			dashboard = append(dashboard, rs.Key)
		}
		for _, s := range Project(input(protocol.CompanionSnapshotVersionRuntime, mode)).Sessions {
			listed = append(listed, name(s))
		}
		if !reflect.DeepEqual(listed, dashboard) || !reflect.DeepEqual(listed, want) {
			t.Fatalf("mode %q: v2 lists %v, the dashboard %v, want %v", mode, listed, dashboard, want)
		}
	}
	v1 := Project(input(protocol.CompanionSnapshotVersion, "alpha")).Sessions
	if len(v1) != 2 || v1[0].WindowID != 7 || v1[1].WindowID != 8 {
		t.Fatalf("v1 sessions = %+v, want Kitty's tabs in tab order", v1)
	}
}

// TestProject_V2TiedSessionOrderMatchesNavigation: sessions launched from one
// project under one profile have nothing to sort them by, and tmux lists them
// in an order unrelated to their runtime-session IDs. The v2 snapshot and
// terminal navigation, which reads the panes itself, still agree on which
// session follows which, wrapping at either end, in both sort modes.
func TestProject_V2TiedSessionOrderMatchesNavigation(t *testing.T) {
	const (
		idA   = "rs-0000000000000000000000000000000a"
		idB   = "rs-0000000000000000000000000000000b"
		idC   = "rs-0000000000000000000000000000000c"
		idZ   = "rs-00000000000000000000000000000001"
		slotA = tmuxSlot + iota
		slotB
		slotC
		slotZ
	)
	pane := func(id, profile, title string) backend.Pane {
		return backend.Pane{GroupTitle: title, Vars: map[string]string{"amx_profile": profile, session.RuntimeSessionVar: id}}
	}
	// tmux finds the three tied sessions in the reverse of their IDs, with a
	// session of another title, and the lowest ID, among them.
	panes := []backend.Pane{pane(idC, "work", "api"), pane(idZ, "work", "zeta"), pane(idB, "work", "api"), pane(idA, "work", "api")}
	want := []string{idA, idB, idC, idZ}
	// Slots are handed out as sessions are met, so they follow no order either.
	in := Input{
		States: map[int]protocol.WindowStateSnapshot{slotA: {}, slotB: {}, slotC: {}, slotZ: {}},
		Runtime: map[int]RuntimeSlot{
			slotA: {SessionID: idC, Backend: "tmux", Profile: "work", Title: "api"},
			slotB: {SessionID: idZ, Backend: "tmux", Profile: "work", Title: "zeta"},
			slotC: {SessionID: idA, Backend: "tmux", Profile: "work", Title: "api"},
			slotZ: {SessionID: idB, Backend: "tmux", Profile: "work", Title: "api"},
		},
		SnapshotVersion: protocol.CompanionSnapshotVersionRuntime,
	}
	// neighbours is the session after and the session before each one.
	neighbours := func(ordered []string) map[string][2]string {
		out := map[string][2]string{}
		for i, id := range ordered {
			out[id] = [2]string{ordered[(i+1)%len(ordered)], ordered[(i-1+len(ordered))%len(ordered)]}
		}
		return out
	}
	for _, mode := range []string{"alpha", "account"} {
		var navigation, listed []string
		running := session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, nil)
		for _, rs := range session.SortRunning(running, mode) {
			navigation = append(navigation, string(rs.RuntimeSessionID))
		}
		in.Sort = mode
		for _, s := range Project(in).Sessions {
			if s.Runtime == nil {
				t.Fatalf("mode %q: a session with no runtime block: %+v", mode, s)
			}
			listed = append(listed, s.Runtime.SessionID)
		}
		if !reflect.DeepEqual(navigation, want) || !reflect.DeepEqual(listed, want) {
			t.Fatalf("mode %q: navigation orders %v, v2 lists %v, want %v in both", mode, navigation, listed, want)
		}
		if got, wantN := neighbours(listed), neighbours(navigation); !reflect.DeepEqual(got, wantN) {
			t.Fatalf("mode %q: next and previous differ: v2 %v, navigation %v", mode, got, wantN)
		}
		if n := neighbours(listed); n[idZ][0] != idA || n[idA][1] != idZ || n[idA][0] != idB || n[idC][1] != idB {
			t.Fatalf("mode %q: next and previous = %v; want %s to wrap to %s and back, and %s between its equals", mode, n, idZ, idA, idB)
		}
	}
}
