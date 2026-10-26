package companion

import (
	"encoding/json"
	"fmt"
	"go/build"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const sockA = "unix:/tmp/kitty-100"
const sockB = "unix:/tmp/kitty-200"

func managed(id int, profile, harness string) kitty.Window {
	return kitty.Window{ID: id, UserVars: kitty.UserVars{Profile: profile, Harness: harness}}
}

func tree(socket string, tabs ...kitty.Tab) KittyTree {
	return KittyTree{Ready: true, Socket: socket, Windows: []kitty.OSWindow{{ID: 1, Tabs: tabs}}}
}

func sessionByWindow(t *testing.T, snap protocol.CompanionSnapshot, socket string, id int) protocol.CompanionSession {
	t.Helper()
	for _, s := range snap.Sessions {
		if s.WindowID == id && s.KittySocket == socket {
			return s
		}
	}
	t.Fatalf("no session for window %d on %q in %+v", id, socket, snap.Sessions)
	return protocol.CompanionSession{}
}

// TestProject_AttentionParityWithFocusNext pins the attention bit to the
// predicate focus-next cycles on: a title icon in protocol.AttentionStates, or
// Kitty's own needs-attention flag. With no daemon state the title is all
// either side sees, so every palette state must agree exactly.
func TestProject_AttentionParityWithFocusNext(t *testing.T) {
	states := []string{"", "bolt", "stop", "key", "warning", "error", "subagent", "compact", "question"}
	for _, state := range states {
		for _, flagged := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/flag=%v", state, flagged), func(t *testing.T) {
				title := palette.BuildTitleFull("W:api", state, false, false)
				tab := kitty.Tab{ID: 10, Title: title, NeedsAttention: flagged, Windows: []kitty.Window{managed(101, "work", "claude")}}
				snap := Project(Input{Kitty: tree(sockA, tab)})

				focusNext := protocol.AttentionStates[palette.DetectState(title)] || flagged
				got := sessionByWindow(t, snap, sockA, 101)
				if got.Attention != focusNext {
					t.Fatalf("attention = %v, focus-next would say %v", got.Attention, focusNext)
				}
				if got.IconState != state {
					t.Fatalf("icon_state = %q, want %q from the title", got.IconState, state)
				}
				wantTotal := 0
				if focusNext {
					wantTotal = 1
				}
				if snap.Attention.Total != wantTotal {
					t.Fatalf("attention.total = %d, want %d", snap.Attention.Total, wantTotal)
				}
			})
		}
	}
}

// TestProject_DaemonStateOutranksTitle covers the lag case: the daemon has
// moved a window to a permission prompt before its title repaints.
func TestProject_DaemonStateOutranksTitle(t *testing.T) {
	tab := kitty.Tab{ID: 10, Title: palette.BuildTitleFull("W:api", "bolt", false, false), Windows: []kitty.Window{managed(101, "work", "codex")}}
	conv := conversation.Identity{Harness: "codex", SessionID: "s-1"}
	snap := Project(Input{
		Kitty: tree(sockA, tab),
		States: map[int]protocol.WindowStateSnapshot{101: {
			IconState: "key", Reason: "command", Unseen: true, LastEventAt: 1_700_000_000,
			BusySinceAt: 1_699_999_000, PresentationChangedAtMS: 1_700_000_000_123,
			Branch: "main", Dirty: true, Conversation: &conv,
		}},
		Sockets: map[int]string{101: sockA},
	})
	got := sessionByWindow(t, snap, sockA, 101)
	want := protocol.CompanionSession{
		WindowID: 101, TabID: 10, OSWindowID: 1, KittySocket: sockA,
		Harness: "codex", Profile: "work", Project: "W:api", BaseTitle: "W:api",
		Conversation: &conv, IconState: "key", Reason: "command", Unseen: true, Attention: true,
		BusySinceAt: 1_699_999_000, LastEventAt: 1_700_000_000, PresentationChangedAtMS: 1_700_000_000_123,
		Branch: "main", Dirty: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session =\n%+v\nwant\n%+v", got, want)
	}
	if snap.Attention != (protocol.CompanionAttention{Total: 1, Key: 1, Unseen: 1}) {
		t.Fatalf("attention = %+v", snap.Attention)
	}
}

func TestProject_TitleAndUserVarFlags(t *testing.T) {
	locked := managed(102, "work", "claude")
	locked.UserVars.Locked = true
	muted := managed(103, "work", "claude")
	muted.UserVars.Mute = "1"
	snap := Project(Input{Kitty: tree(sockA,
		kitty.Tab{ID: 10, Title: palette.BuildTitleParts(palette.TitleParts{Base: "W:a", Muted: true, Looping: true, Starred: true}), Windows: []kitty.Window{managed(101, "work", "claude")}},
		kitty.Tab{ID: 20, Title: "W:b", Windows: []kitty.Window{locked}},
		kitty.Tab{ID: 30, Title: "W:c", Windows: []kitty.Window{muted, {ID: 104}}},
	)})
	if s := sessionByWindow(t, snap, sockA, 101); !s.Muted || !s.Looping || !s.Pinned || s.BaseTitle != "W:a" {
		t.Fatalf("title flags lost: %+v", s)
	}
	if s := sessionByWindow(t, snap, sockA, 102); !s.Locked || s.Pinned {
		t.Fatalf("amx_locked lost: %+v", s)
	}
	if s := sessionByWindow(t, snap, sockA, 103); !s.Muted {
		t.Fatalf("cc_mute lost: %+v", s)
	}
	if len(snap.Sessions) != 3 {
		t.Fatalf("sessions = %d, want 3: an unmanaged pane is not a session", len(snap.Sessions))
	}
}

func TestProject_DashboardDetection(t *testing.T) {
	dash := kitty.Window{ID: 900, ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"/usr/local/bin/amx"}}}}
	notDash := kitty.Window{ID: 901, ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"amx", "overview"}}}}
	cases := []struct {
		name string
		in   KittyTree
		want protocol.CompanionDashboard
	}{
		{"absent", tree(sockA, kitty.Tab{ID: 10, Windows: []kitty.Window{notDash}}), protocol.CompanionDashboard{}},
		{"present", KittyTree{Ready: true, Socket: sockA, Windows: []kitty.OSWindow{
			{ID: 1, Tabs: []kitty.Tab{{ID: 10, Windows: []kitty.Window{notDash}}}},
			{ID: 2, Tabs: []kitty.Tab{{ID: 20, Windows: []kitty.Window{dash}}}},
		}}, protocol.CompanionDashboard{Open: true, KittySocket: sockA, OSWindowID: 2, TabID: 20, WindowID: 900}},
		{"cold tree", KittyTree{Socket: sockA, Windows: []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{{ID: 10, Windows: []kitty.Window{dash}}}}}}, protocol.CompanionDashboard{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Project(Input{Kitty: tc.in}).Dashboard; got != tc.want {
				t.Fatalf("dashboard = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestProject_MixedSockets pins the instance rule. Window 101 exists in both
// Kitty instances; the daemon's state for it was recorded against the OTHER
// instance, so it must not decorate the tree window, and it surfaces as its
// own session on that instance instead.
func TestProject_MixedSockets(t *testing.T) {
	tab := kitty.Tab{ID: 10, Title: "W:api", Windows: []kitty.Window{managed(101, "work", "claude"), managed(102, "work", "claude")}}
	conv := conversation.Identity{Harness: "codex", SessionID: "other"}
	snap := Project(Input{
		Kitty: tree(sockA, tab),
		States: map[int]protocol.WindowStateSnapshot{
			101: {IconState: "error", Conversation: &conv},
			102: {IconState: "stop"},
			// Same instance as the tree but no longer in it: a closed window.
			103: {IconState: "key"},
		},
		Sockets: map[int]string{101: sockB, 102: sockA, 103: sockA},
	})
	if len(snap.Sessions) != 3 {
		t.Fatalf("sessions = %+v, want tree 101, tree 102, foreign 101", snap.Sessions)
	}
	if s := sessionByWindow(t, snap, sockA, 101); s.IconState != "" || s.Conversation != nil {
		t.Fatalf("a foreign state decorated the tree window: %+v", s)
	}
	if s := sessionByWindow(t, snap, sockA, 102); s.IconState != "stop" || !s.Attention {
		t.Fatalf("same-instance state not applied: %+v", s)
	}
	foreign := sessionByWindow(t, snap, sockB, 101)
	if foreign.IconState != "error" || !foreign.Attention || foreign.Harness != "codex" || foreign.TabID != 0 {
		t.Fatalf("foreign session = %+v", foreign)
	}
	if !reflect.DeepEqual(snap.Kitty.Sockets, []string{sockA, sockB}) {
		t.Fatalf("kitty.sockets = %v", snap.Kitty.Sockets)
	}
	if snap.Attention.Total != 2 || snap.Attention.Error != 1 || snap.Attention.Stop != 1 || snap.Attention.Key != 0 {
		t.Fatalf("attention = %+v", snap.Attention)
	}
}

// TestProject_OtherInstanceTakesItsOwnTree covers a session in a second Kitty
// instance whose tree the daemon also read: it is named and placed from that
// tree, as a session in the listed instance is, rather than shown as a bare
// window number.
func TestProject_OtherInstanceTakesItsOwnTree(t *testing.T) {
	listed := kitty.Tab{ID: 10, Title: "W:api", Windows: []kitty.Window{managed(101, "work", "claude")}}
	other := kitty.Window{ID: 4, UserVars: kitty.UserVars{Profile: "personal", Harness: "claude", Mute: "1"}}
	otherTree := []kitty.OSWindow{{ID: 2, Tabs: []kitty.Tab{{ID: 3, Title: palette.BuildTitle("W:notes", "", false), Windows: []kitty.Window{other}}}}}
	snap := Project(Input{
		Kitty:   tree(sockA, listed),
		States:  map[int]protocol.WindowStateSnapshot{4: {IconState: "stop"}, 5: {IconState: "key"}},
		Sockets: map[int]string{4: sockB, 5: sockB},
		Others:  map[string][]kitty.OSWindow{sockB: otherTree},
	})
	s := sessionByWindow(t, snap, sockB, 4)
	if s.TabID != 3 || s.OSWindowID != 2 || s.BaseTitle != "W:notes" || s.Profile != "personal" || s.Harness != "claude" || !s.Muted {
		t.Fatalf("other-instance session not taken from its tree: %+v", s)
	}
	if s.IconState != "stop" || !s.Attention {
		t.Fatalf("daemon state not applied over the other tree: %+v", s)
	}
	// Window 5 has state on the same instance but is not in its tree yet.
	if bare := sessionByWindow(t, snap, sockB, 5); bare.TabID != 0 || bare.BaseTitle != "" || bare.IconState != "key" {
		t.Fatalf("a window missing from its tree should carry state only: %+v", bare)
	}
}

// TestProject_UnattributedSocketsJoin covers a single-instance install, where
// hooks record no socket: refusing the join would blank every session.
func TestProject_UnattributedSocketsJoin(t *testing.T) {
	tab := kitty.Tab{ID: 10, Title: "W:api", Windows: []kitty.Window{managed(101, "work", "claude")}}
	for _, tc := range []struct{ treeSocket, stateSocket string }{{sockA, ""}, {"", sockA}, {"", ""}} {
		snap := Project(Input{
			Kitty:   tree(tc.treeSocket, tab),
			States:  map[int]protocol.WindowStateSnapshot{101: {IconState: "bolt"}},
			Sockets: map[int]string{101: tc.stateSocket},
		})
		if len(snap.Sessions) != 1 || snap.Sessions[0].IconState != "bolt" {
			t.Fatalf("tree=%q state=%q: sessions = %+v", tc.treeSocket, tc.stateSocket, snap.Sessions)
		}
		if snap.Attention.Working != 1 {
			t.Fatalf("working = %d, want 1", snap.Attention.Working)
		}
	}
}

func TestProject_ColdTreeHasNoTreeSessions(t *testing.T) {
	snap := Project(Input{
		Kitty:   KittyTree{Socket: sockA},
		States:  map[int]protocol.WindowStateSnapshot{1: {IconState: "stop"}, 2: {IconState: "stop"}},
		Sockets: map[int]string{1: sockA, 2: sockB},
	})
	if len(snap.Sessions) != 1 || snap.Sessions[0].KittySocket != sockB {
		t.Fatalf("sessions = %+v, want only the other instance's state", snap.Sessions)
	}
	if snap.Sessions == nil {
		t.Fatal("sessions must encode as [] rather than null")
	}
	if empty := Project(Input{}); empty.Sessions == nil {
		t.Fatal("an empty projection must still publish an empty session list")
	}
}

// TestProject_Truncation caps the list at CompanionSessionCap, keeps every
// session needing attention ahead of the rest, publishes in tree order, and
// still counts attention over the whole set.
func TestProject_Truncation(t *testing.T) {
	const total = protocol.CompanionSessionCap + 40
	var tabs []kitty.Tab
	for i := 1; i <= total; i++ {
		state := "bolt"
		if i > total-20 {
			state = "stop"
		}
		tabs = append(tabs, kitty.Tab{ID: i, Title: palette.BuildTitleFull(fmt.Sprintf("W:%d", i), state, false, false), Windows: []kitty.Window{managed(i, "work", "claude")}})
	}
	snap := Project(Input{Kitty: tree(sockA, tabs...)})
	if !snap.Truncated || len(snap.Sessions) != protocol.CompanionSessionCap {
		t.Fatalf("truncated=%v len=%d, want true and %d", snap.Truncated, len(snap.Sessions), protocol.CompanionSessionCap)
	}
	stops := 0
	for i, s := range snap.Sessions {
		if s.IconState == "stop" {
			stops++
		}
		if i > 0 && s.WindowID <= snap.Sessions[i-1].WindowID {
			t.Fatalf("sessions out of tree order at %d", i)
		}
	}
	if stops != 20 {
		t.Fatalf("kept %d attention sessions, want all 20", stops)
	}
	if snap.Attention.Stop != 20 || snap.Attention.Working != total-20 {
		t.Fatalf("attention = %+v, want counts over all %d sessions", snap.Attention, total)
	}

	under := Project(Input{Kitty: tree(sockA, tabs[:10]...)})
	if under.Truncated || len(under.Sessions) != 10 {
		t.Fatalf("an uncapped list was truncated: %v %d", under.Truncated, len(under.Sessions))
	}
}

// TestProject_FrameStaysUnderCap fills every field with hostile text — long,
// and made of characters JSON escapes six bytes wide — and checks that the
// framed snapshot still fits in one stream frame.
func TestProject_FrameStaysUnderCap(t *testing.T) {
	hostile := strings.Repeat("<", 4096)
	var tabs []kitty.Tab
	states := map[int]protocol.WindowStateSnapshot{}
	for i := 1; i <= protocol.CompanionSessionCap; i++ {
		w := kitty.Window{ID: i, UserVars: kitty.UserVars{Profile: hostile, Harness: hostile, Project: hostile}}
		tabs = append(tabs, kitty.Tab{ID: i, Title: hostile, Windows: []kitty.Window{w}})
		states[i] = protocol.WindowStateSnapshot{IconState: hostile, Reason: hostile, Branch: hostile}
	}
	snap := Project(Input{Kitty: tree(sockA, tabs...), States: states})
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal(protocol.StreamFrame{V: protocol.CompanionProtocolVersion, Seq: 1 << 62, Type: protocol.FrameSnapshot, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) > protocol.CompanionFrameMaxBytes {
		t.Fatalf("frame = %d bytes, over the %d cap", len(frame), protocol.CompanionFrameMaxBytes)
	}
	if !snap.Truncated || len(snap.Sessions) == 0 {
		t.Fatalf("expected a budget-trimmed, non-empty list; truncated=%v len=%d", snap.Truncated, len(snap.Sessions))
	}
	if len(snap.Sessions[0].BaseTitle) != displayMax {
		t.Fatalf("base_title = %d bytes, want clamped to %d", len(snap.Sessions[0].BaseTitle), displayMax)
	}
}

func TestProject_ClampingIsRuneSafeAndIdentifiersAreWhole(t *testing.T) {
	long := strings.Repeat("é", displayMax) // two bytes each
	if got := display(long); len(got) > displayMax || !strings.HasPrefix(long, got) || strings.ContainsRune(got, '�') {
		t.Fatalf("display clamp broke a rune: %d bytes", len(got))
	}
	if identifier(strings.Repeat("x", identifierMax+1)) != "" {
		t.Fatal("an over-long identifier must be omitted, not shortened")
	}
	conv := conversation.Identity{Harness: "claude", SessionID: strings.Repeat("s", identifierMax+1)}
	snap := Project(Input{
		Kitty:  tree(sockA, kitty.Tab{ID: 1, Windows: []kitty.Window{managed(1, "p", "claude")}}),
		States: map[int]protocol.WindowStateSnapshot{1: {Conversation: &conv}},
	})
	if snap.Sessions[0].Conversation != nil {
		t.Fatal("a conversation whose identity cannot be published whole must be omitted")
	}
}

func TestProject_ASLWaitingAndRunBinding(t *testing.T) {
	created := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	target := func(window int, run string) asl.SupervisorTarget {
		return asl.SupervisorTarget{WindowID: window, Candidate: agentloop.Candidate{RunID: run, PlanDir: "/plans/" + run, StateDir: "/state/" + run}}
	}
	targets := []asl.SupervisorTarget{target(1, "run-z"), target(1, "run-y"), target(2, "run-b"), target(2, "run-a")}
	pending := []asl.PendingQuestion{
		{Target: target(2, "run-b"), Projection: agentloop.SupervisorQuestionProjection{RunID: "run-b", UnansweredQuestions: 2, LatestQuestionID: "q-9", LatestQuestionCreatedAt: created}},
		{Target: target(1, "run-z"), Projection: agentloop.SupervisorQuestionProjection{RunID: "run-z", UnansweredQuestions: 1}},
		{Target: target(3, "run-c"), Projection: agentloop.SupervisorQuestionProjection{RunID: "run-c"}},
	}
	snap := Project(Input{
		Kitty:      tree(sockA, kitty.Tab{ID: 10, Windows: []kitty.Window{managed(1, "p", "claude"), managed(2, "p", "codex"), managed(4, "p", "codex")}}),
		ASLTargets: targets,
		ASLPending: pending,
	})
	want := []protocol.CompanionASLWaiting{
		{RunID: "run-z", PlanDir: "/plans/run-z", WindowID: 1, Unanswered: 1},
		{RunID: "run-b", PlanDir: "/plans/run-b", WindowID: 2, Unanswered: 2, LatestQuestionID: "q-9", LatestQuestionCreatedAt: created.Unix()},
	}
	if !reflect.DeepEqual(snap.ASL.Waiting, want) {
		t.Fatalf("waiting = %+v\nwant %+v", snap.ASL.Waiting, want)
	}
	// A waiting run outranks the lexical choice; otherwise the first run wins.
	for id, run := range map[int]string{1: "run-z", 2: "run-b", 4: ""} {
		if got := sessionByWindow(t, snap, sockA, id).ASLRunID; got != run {
			t.Fatalf("window %d asl_run_id = %q, want %q", id, got, run)
		}
	}
	idle := Project(Input{Kitty: tree(sockA, kitty.Tab{ID: 10, Windows: []kitty.Window{managed(1, "p", "claude")}}), ASLTargets: targets})
	if got := idle.Sessions[0].ASLRunID; got != "run-y" {
		t.Fatalf("asl_run_id with nothing waiting = %q, want the lexically first run-y", got)
	}
}

func TestProject_WaitingIsCapped(t *testing.T) {
	var pending []asl.PendingQuestion
	for i := range protocol.CompanionWaitingCap + 5 {
		pending = append(pending, asl.PendingQuestion{
			Target:     asl.SupervisorTarget{WindowID: i + 1, Candidate: agentloop.Candidate{RunID: fmt.Sprintf("run-%03d", i)}},
			Projection: agentloop.SupervisorQuestionProjection{UnansweredQuestions: 1},
		})
	}
	if got := len(Project(Input{ASLPending: pending}).ASL.Waiting); got != protocol.CompanionWaitingCap {
		t.Fatalf("waiting = %d, want %d", got, protocol.CompanionWaitingCap)
	}
}

func TestProject_Notification(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 3, h, m, 0, 0, time.Local) }
	cases := []struct {
		name  string
		quiet string
		now   time.Time
		want  protocol.CompanionNotification
	}{
		{"none", "", at(23, 0), protocol.CompanionNotification{Backend: "alerter"}},
		{"inside overnight", "22:00-07:00", at(23, 0), protocol.CompanionNotification{Backend: "alerter", QuietHours: "22:00-07:00", QuietNow: true}},
		{"outside", "22:00-07:00", at(12, 0), protocol.CompanionNotification{Backend: "alerter", QuietHours: "22:00-07:00"}},
		{"unparseable", "soon", at(12, 0), protocol.CompanionNotification{Backend: "alerter"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Project(Input{Now: tc.now, Notification: Notification{Backend: "alerter", QuietHours: tc.quiet}}).Notification
			if got != tc.want {
				t.Fatalf("notification = %+v, want %+v", got, tc.want)
			}
		})
	}
	snoozed := Project(Input{Notification: Notification{Backend: "app", AppConnected: true, Snooze: protocol.SnoozeState{SnoozedUntil: 1_700_003_600}}}).Notification
	if snoozed != (protocol.CompanionNotification{Backend: "app", AppConnected: true, SnoozedUntil: 1_700_003_600}) {
		t.Fatalf("snoozed = %+v", snoozed)
	}
}

// TestDigest_StableAcrossTimeMovesWithContent is the reason the snapshot holds
// timestamps rather than ages: the same world projected a minute apart must
// digest identically, and a real change must move the digest.
func TestDigest_StableAcrossTimeMovesWithContent(t *testing.T) {
	in := Input{
		Kitty:   tree(sockA, kitty.Tab{ID: 10, Title: "W:api", Windows: []kitty.Window{managed(101, "work", "claude")}}),
		States:  map[int]protocol.WindowStateSnapshot{101: {IconState: "bolt", BusySinceAt: 1_700_000_000}},
		Sockets: map[int]string{101: sockA},
		Now:     time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
	}
	first := Digest(Project(in))
	in.Now = in.Now.Add(time.Minute)
	if again := Digest(Project(in)); again != first {
		t.Fatal("digest moved with the clock alone")
	}
	in.States = map[int]protocol.WindowStateSnapshot{101: {IconState: "stop", BusySinceAt: 1_700_000_000}}
	if changed := Digest(Project(in)); changed == first {
		t.Fatal("digest did not move when the icon changed")
	}
	if first == "" {
		t.Fatal("digest is empty")
	}
}

// TestProject_DoesNotMutateInput guards the hub's contract: it projects a copy
// taken under stateMu, and the copy must come back untouched.
func TestProject_DoesNotMutateInput(t *testing.T) {
	conv := conversation.Identity{Harness: "claude", SessionID: "s"}
	states := map[int]protocol.WindowStateSnapshot{101: {IconState: "stop", Conversation: &conv}}
	Project(Input{Kitty: tree(sockA, kitty.Tab{ID: 10, Windows: []kitty.Window{managed(101, "w", "claude")}}), States: states})
	snap := Project(Input{Kitty: tree(sockA, kitty.Tab{ID: 10, Windows: []kitty.Window{managed(101, "w", "claude")}}), States: states})
	snap.Sessions[0].Conversation.SessionID = "changed"
	if conv.SessionID != "s" {
		t.Fatal("a projected conversation aliases the caller's state")
	}
}

// TestCompanionPackageDoesNoIO is the package doc's boundary claim, asserted.
// The projection runs off-lock in the hub precisely because it cannot block;
// an import of the daemon root, a transport, or an I/O package would end that.
func TestCompanionPackageDoesNoIO(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("reading the package's imports: %v", err)
	}
	if len(pkg.Imports) == 0 {
		t.Fatal("the package reported no imports at all, so this check would pass vacuously")
	}
	forbidden := map[string]bool{
		"github.com/sglavoie/agentmux/internal/daemon":              true,
		"github.com/sglavoie/agentmux/internal/daemon/ipc":          true,
		"github.com/sglavoie/agentmux/internal/daemon/process":      true,
		"github.com/sglavoie/agentmux/internal/diagnostics":         true,
		"github.com/sglavoie/agentmux/internal/daemon/eventlog":     true,
		"github.com/sglavoie/agentmux/internal/daemon/notification": true,
		"os": true, "os/exec": true, "net": true, "io": true, "io/fs": true, "path/filepath": true,
	}
	for _, path := range pkg.Imports {
		if forbidden[path] {
			t.Errorf("internal/daemon/companion imports %q; the projection must stay pure", path)
		}
	}
}
