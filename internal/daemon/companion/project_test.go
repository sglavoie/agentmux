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
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

func runtimeInput(count int) Input {
	in := Input{States: make(map[int]protocol.WindowStateSnapshot, count), Runtime: make(map[int]RuntimeSlot, count)}
	for slot := 1; slot <= count; slot++ {
		in.States[slot] = protocol.WindowStateSnapshot{}
		in.Runtime[slot] = RuntimeSlot{SessionID: fmt.Sprintf("rs-%04d", slot), Backend: "tmux", Instance: "/tmp/amx.sock", Generation: "g1", Pane: fmt.Sprintf("%%%d", slot), Profile: "work", Project: fmt.Sprintf("project-%04d", slot)}
	}
	return in
}

func TestProjectRuntimeSessionAndAttention(t *testing.T) {
	in := runtimeInput(2)
	conv := conversation.Identity{Harness: "codex", SessionID: "conversation"}
	in.States[1] = protocol.WindowStateSnapshot{IconState: "key", Reason: "command", Unseen: true, Muted: true, Conversation: &conv}
	in.Runtime[1] = RuntimeSlot{SessionID: "rs-0001", Backend: "tmux", Instance: "/tmp/amx.sock", Generation: "g1", Pane: "%1", Profile: "work", Project: "api", Harness: "codex"}
	snap := Project(in)
	if snap.V != protocol.CompanionSnapshotVersion || len(snap.Sessions) != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}
	var found *protocol.CompanionSession
	for i := range snap.Sessions {
		if snap.Sessions[i].Runtime != nil && snap.Sessions[i].Runtime.SessionID == "rs-0001" {
			found = &snap.Sessions[i]
		}
	}
	if found == nil || found.IconState != "key" || !found.Attention || !found.Muted || !found.Unseen || found.Conversation == nil || found.Conversation.SessionID != "conversation" {
		t.Fatalf("runtime session = %+v", found)
	}
	if snap.Attention != (protocol.CompanionAttention{Total: 1, Key: 1, Unseen: 1}) {
		t.Fatalf("attention = %+v", snap.Attention)
	}
	found.Conversation.SessionID = "changed"
	if conv.SessionID != "conversation" {
		t.Fatal("projection aliased the input conversation")
	}
}

func TestProjectOmitsUnboundAndNonTmuxState(t *testing.T) {
	in := runtimeInput(2)
	in.States[3] = protocol.WindowStateSnapshot{IconState: "stop"}
	in.Runtime[2] = RuntimeSlot{SessionID: "other", Backend: "other"}
	snap := Project(in)
	if len(snap.Sessions) != 1 || snap.Sessions[0].Runtime.SessionID != "rs-0001" || snap.Attention.Total != 0 {
		t.Fatalf("sessions = %+v, attention = %+v", snap.Sessions, snap.Attention)
	}
	if Project(Input{}).Sessions == nil {
		t.Fatal("empty sessions must encode as []")
	}
}

func TestProjectOrderMatchesRuntimeNavigation(t *testing.T) {
	star := palette.StarIcon()
	in := runtimeInput(4)
	rows := []struct {
		profile string
		title   string
	}{
		{"work", "zeta"},
		{"home", "docs"},
		{"work", "api"},
		{"work", star + "notes"},
	}
	var running []session.RunningSession
	for slot, row := range rows {
		r := in.Runtime[slot+1]
		r.Profile, r.Title = row.profile, row.title
		in.Runtime[slot+1] = r
		parts := palette.ParseTitleParts(row.title)
		running = append(running, session.RunningSession{
			Key: r.SessionID, Account: row.profile, Base: parts.Base, Starred: parts.Starred,
		})
	}
	for _, mode := range []string{"alpha", "account"} {
		in.Sort = mode
		var got, want []string
		for _, s := range Project(in).Sessions {
			got = append(got, s.Runtime.SessionID)
		}
		for _, s := range session.SortRunning(running, mode) {
			want = append(want, s.Key)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("mode %q: companion order %v, navigation order %v", mode, got, want)
		}
	}
}

func TestProjectTruncationKeepsAttention(t *testing.T) {
	const total = protocol.CompanionSessionCap + 40
	in := runtimeInput(total)
	for slot := total - 19; slot <= total; slot++ {
		in.States[slot] = protocol.WindowStateSnapshot{IconState: "stop"}
	}
	snap := Project(in)
	if !snap.Truncated || len(snap.Sessions) != protocol.CompanionSessionCap || snap.Attention.Stop != 20 {
		t.Fatalf("truncation = %v, sessions = %d, attention = %+v", snap.Truncated, len(snap.Sessions), snap.Attention)
	}
	kept := 0
	for _, s := range snap.Sessions {
		if s.IconState == "stop" {
			kept++
		}
	}
	if kept != 20 {
		t.Fatalf("kept %d attention sessions, want 20", kept)
	}
}

func TestProjectFrameStaysUnderCap(t *testing.T) {
	in := runtimeInput(protocol.CompanionSessionCap)
	hostile := strings.Repeat("<", 4096)
	for slot := range in.Runtime {
		r := in.Runtime[slot]
		r.Project, r.Profile, r.Name = hostile, hostile, hostile
		in.Runtime[slot] = r
		in.States[slot] = protocol.WindowStateSnapshot{IconState: hostile, Reason: hostile, Branch: hostile}
	}
	snap := Project(in)
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal(protocol.StreamFrame{V: protocol.CompanionProtocolVersion, Seq: 1 << 62, Type: protocol.FrameSnapshot, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) > protocol.CompanionFrameMaxBytes || !snap.Truncated || len(snap.Sessions) == 0 {
		t.Fatalf("frame = %d, truncated = %v, sessions = %d", len(frame), snap.Truncated, len(snap.Sessions))
	}
}

func TestProjectWaitingBoundToRuntime(t *testing.T) {
	in := runtimeInput(2)
	target := func(slot int, run string) asl.SupervisorTarget {
		return asl.SupervisorTarget{WindowID: slot, Candidate: agentloop.Candidate{RunID: run, PlanDir: "/plans/" + run}}
	}
	in.ASLTargets = []asl.SupervisorTarget{target(1, "run-z"), target(1, "run-y")}
	in.ASLPending = []asl.PendingQuestion{{Target: target(1, "run-z"), Projection: agentloop.SupervisorQuestionProjection{UnansweredQuestions: 2}}, {Target: target(3, "unbound"), Projection: agentloop.SupervisorQuestionProjection{UnansweredQuestions: 1}}}
	snap := Project(in)
	want := []protocol.CompanionASLWaiting{{RunID: "run-z", PlanDir: "/plans/run-z", RuntimeSessionID: "rs-0001", Unanswered: 2}}
	if !reflect.DeepEqual(snap.ASL.Waiting, want) {
		t.Fatalf("waiting = %+v, want %+v", snap.ASL.Waiting, want)
	}
	for _, s := range snap.Sessions {
		if s.Runtime.SessionID == "rs-0001" && s.ASLRunID != "run-z" {
			t.Fatalf("ASL run = %q", s.ASLRunID)
		}
	}
}

func TestProjectNotification(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 10, 3, h, 0, 0, 0, time.Local) }
	for _, tc := range []struct {
		quiet string
		now   time.Time
		want  bool
	}{{"22:00-07:00", at(23), true}, {"22:00-07:00", at(12), false}, {"soon", at(12), false}} {
		got := Project(Input{Now: tc.now, Notification: Notification{Backend: "alerter", QuietHours: tc.quiet}}).Notification
		if got.QuietNow != tc.want || got.Backend != "alerter" {
			t.Fatalf("notification = %+v", got)
		}
	}
}

func TestDigestStableAcrossTimeMovesWithContent(t *testing.T) {
	in := runtimeInput(1)
	in.Now = time.Now()
	first := Digest(Project(in))
	in.Now = in.Now.Add(time.Minute)
	if Digest(Project(in)) != first {
		t.Fatal("digest moved with the clock alone")
	}
	in.States[1] = protocol.WindowStateSnapshot{IconState: "stop"}
	if Digest(Project(in)) == first {
		t.Fatal("digest ignored state change")
	}
}

func TestCompanionPackageDoesNoIO(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{"os": true, "os/exec": true, "net": true, "io": true, "io/fs": true, "path/filepath": true, "github.com/sglavoie/agentmux/internal/daemon": true, "github.com/sglavoie/agentmux/internal/daemon/ipc": true}
	for _, path := range pkg.Imports {
		if forbidden[path] {
			t.Errorf("companion imports %q", path)
		}
	}
}
