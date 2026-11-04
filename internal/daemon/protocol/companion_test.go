package protocol

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/conversation"
)

var updateCompanionGolden = flag.Bool("update-companion-golden", false, "rewrite testdata/companion_snapshot_v2.json")

const companionGoldenPath = "testdata/companion_snapshot_v2.json"

func goldenCompanionSnapshot() CompanionSnapshot {
	id := "rs-0123456789abcdef0123456789abcdef"
	return CompanionSnapshot{
		V: CompanionSnapshotVersion,
		Daemon: CompanionDaemon{PID: 4242, StartedAt: 1_700_000_000, Build: &buildinfo.Info{
			Version: "1.2.3", Revision: "abc1234", Dirty: true, BuildTime: "2026-10-03T12:00:00Z",
			GoVersion: "go1.26.2", BinaryHash: "sha256:00ff",
		}, DegradedReason: "restore_timeout"},
		Notification: CompanionNotification{Backend: "alerter", AppConnected: true, SnoozedUntil: 1_700_003_600, Indefinite: true, QuietHours: "22:00-07:00", QuietNow: true},
		Attention:    CompanionAttention{Total: 2, Stop: 1, Question: 1, Working: 2, Unseen: 1},
		ASL:          CompanionASL{Waiting: []CompanionASLWaiting{{RunID: "run-1", PlanDir: "/plans/p", RuntimeSessionID: id, Unanswered: 2, LatestQuestionID: "q-1", LatestQuestionCreatedAt: 1_700_000_100}}},
		Sessions: []CompanionSession{{
			StatusEvidence: &StatusEvidence{Kind: "asl", At: 1_700_000_050},
			Harness:        "claude", Profile: "work", Project: "API", Conversation: &conversation.Identity{Harness: "claude", SessionID: "0b5c-session"},
			ASLRunID: "run-1", IconState: "question", Reason: "supervisor", Unseen: true, Attention: true,
			Muted: true, Looping: true, Pinned: true, BusySinceAt: 1_699_999_000, LastEventAt: 1_700_000_050,
			PresentationChangedAtMS: 1_700_000_050_123, Branch: "main", Dirty: true,
			TabColor: "#8ab7e9", Name: "flaky login", HarnessIcon: "🦊",
			Runtime: &CompanionRuntime{SessionID: id, Backend: "tmux", Instance: "/tmp/amx-tmux.sock", Generation: "tmux-gen-1", Pane: "%7"},
		}},
		Truncated: true,
	}
}

func TestCompanionSnapshotGolden(t *testing.T) {
	snapshot := goldenCompanionSnapshot()
	got, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *updateCompanionGolden {
		if err := os.WriteFile(companionGoldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(companionGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("snapshot drifted from %s:\n got: %s\nwant: %s", companionGoldenPath, got, want)
	}
	var decoded CompanionSnapshot
	if err := json.Unmarshal(want, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, snapshot) {
		t.Fatalf("decoded golden = %+v", decoded)
	}
	var raw struct {
		Sessions []map[string]json.RawMessage `json:"sessions"`
		ASL      struct {
			Waiting []map[string]json.RawMessage `json:"waiting"`
		} `json:"asl"`
	}
	if err := json.Unmarshal(want, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"window_id", "tab_id", "os_window_id"} {
		if _, ok := raw.Sessions[0][key]; ok {
			t.Errorf("runtime session carries retired key %q", key)
		}
	}
	if _, ok := raw.ASL.Waiting[0]["window_id"]; ok {
		t.Error("waiting run carries retired window ID")
	}
}

func TestCompanionSnapshotToleratesUnknownFields(t *testing.T) {
	raw := `{"v":2,"future_block":{"x":1},"daemon":{"pid":7,"ready":true,"new_flag":true},"notification":{},"attention":{"total":1,"stop":1,"later":3},"asl":{"waiting":[{"run_id":"r","runtime_session_id":"rs-1","unanswered":1,"body":"never"}]},"sessions":[{"runtime":{"session_id":"rs-1","backend":"tmux"},"icon_state":"stop","attention":true,"new_session_field":"x"}]}`
	var s CompanionSnapshot
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	if s.Daemon.PID != 7 || !s.Daemon.Ready || s.Attention.Stop != 1 || len(s.Sessions) != 1 || s.Sessions[0].Runtime.SessionID != "rs-1" || s.ASL.Waiting[0].RunID != "r" {
		t.Fatalf("known fields lost: %+v", s)
	}
}

func TestCompanionSnapshotEmptySessionsEncodeAsArray(t *testing.T) {
	data, err := json.Marshal(CompanionSnapshot{V: CompanionSnapshotVersion, Sessions: []CompanionSession{}})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["sessions"]) != "[]" {
		t.Fatalf("sessions = %s", raw["sessions"])
	}
}
