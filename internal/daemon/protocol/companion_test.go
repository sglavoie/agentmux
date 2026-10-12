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

var updateCompanionGolden = flag.Bool("update-companion-golden", false, "rewrite testdata/companion_snapshot_v*.json")

const (
	companionGoldenPath   = "testdata/companion_snapshot_v1.json"
	companionGoldenV2Path = "testdata/companion_snapshot_v2.json"
)

// goldenCompanionSnapshot sets every field of a v1 snapshot, so the committed
// golden names every key a v1 client may read.
func goldenCompanionSnapshot() CompanionSnapshot {
	return CompanionSnapshot{
		V: CompanionSnapshotVersion,
		Daemon: CompanionDaemon{
			PID:       4242,
			StartedAt: 1_700_000_000,
			Build: &buildinfo.Info{
				Version: "1.2.3", Revision: "abc1234", Dirty: true, BuildTime: "2026-10-03T12:00:00Z",
				GoVersion: "go1.26.2", BinaryHash: "sha256:00ff",
			},
			Ready:          false,
			DegradedReason: "restore_timeout",
		},
		Kitty: CompanionKitty{
			Ready: true, Stale: true, Socket: "unix:/tmp/kitty-100",
			Sockets: []string{"unix:/tmp/kitty-100", "unix:/tmp/kitty-200"},
		},
		Notification: CompanionNotification{
			Backend: "alerter", AppConnected: true, SnoozedUntil: 1_700_003_600, Indefinite: true,
			QuietHours: "22:00-07:00", QuietNow: true,
		},
		Dashboard: CompanionDashboard{Open: true, KittySocket: "unix:/tmp/kitty-100", OSWindowID: 1, TabID: 5, WindowID: 50},
		Attention: CompanionAttention{Total: 4, Stop: 1, Key: 1, Error: 1, Question: 1, Working: 2, Unseen: 1},
		ASL: CompanionASL{Waiting: []CompanionASLWaiting{{
			RunID: "run-1", PlanDir: "/plans/p", WindowID: 101, Unanswered: 2,
			LatestQuestionID: "q-1", LatestQuestionCreatedAt: 1_700_000_100,
		}}},
		Sessions: []CompanionSession{{
			WindowID: 101, TabID: 10, OSWindowID: 1, KittySocket: "unix:/tmp/kitty-100",
			Harness: "claude", Profile: "work", Project: "API", BaseTitle: "W:api",
			Conversation: &conversation.Identity{Harness: "claude", SessionID: "0b5c-session"},
			ASLRunID:     "run-1", IconState: "question", Reason: "supervisor", Unseen: true, Attention: true,
			Muted: true, Locked: true, Looping: true, BusySinceAt: 1_699_999_000, LastEventAt: 1_700_000_050,
			PresentationChangedAtMS: 1_700_000_050_123, Branch: "main", Dirty: true,
		}},
		Truncated: true,
	}
}

// goldenCompanionSnapshotV2 sets every field of a v2 snapshot: the v1 fixture
// with its Kitty session owned by a runtime session, plus a tmux session that
// carries no Kitty identity at all and a run waiting on it.
func goldenCompanionSnapshotV2() CompanionSnapshot {
	s := goldenCompanionSnapshot()
	s.V = CompanionSnapshotVersionRuntime
	s.Sessions[0].Runtime = &CompanionRuntime{
		SessionID: "rs-0123456789abcdef0123456789abcdef", Backend: "kitty",
		Instance: "unix:/tmp/kitty-100", Generation: "kitty-gen-1", Pane: "101",
	}
	s.Sessions[0].TabColor = "#5aa9ff"
	s.Sessions[0].Name = "flaky login"
	s.Sessions[0].HarnessIcon = "🦊"
	s.Sessions = append(s.Sessions, CompanionSession{
		Harness: "codex", Profile: "work", Project: "API",
		Conversation: &conversation.Identity{Harness: "codex", SessionID: "019a-session"},
		ASLRunID:     "run-2", IconState: "stop", Attention: true, LastEventAt: 1_700_000_060,
		TabColor: "#ff6b5e", Name: "billing fix", HarnessIcon: "📟",
		Runtime: &CompanionRuntime{
			SessionID: "rs-fedcba9876543210fedcba9876543210", Backend: "tmux",
			Instance: "/tmp/amx-tmux.sock", Generation: "tmux-gen-1", Pane: "%7",
		},
	})
	s.ASL.Waiting[0].RuntimeSessionID = "rs-0123456789abcdef0123456789abcdef"
	s.ASL.Waiting = append(s.ASL.Waiting, CompanionASLWaiting{
		RunID: "run-2", Unanswered: 1, RuntimeSessionID: "rs-fedcba9876543210fedcba9876543210",
	})
	return s
}

func checkCompanionGolden(t *testing.T, path string, snapshot CompanionSnapshot) {
	t.Helper()
	got, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *updateCompanionGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the committed golden (regenerate with -update-companion-golden): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("CompanionSnapshot drifted from %s:\n got: %s\nwant: %s", path, got, want)
	}
	var decoded CompanionSnapshot
	if err := json.Unmarshal(want, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, snapshot) {
		t.Fatalf("golden %s decodes to %+v", path, decoded)
	}
}

// TestCompanionSnapshotV2_Golden pins the negotiated mixed-backend shape. The
// tmux session's object holds no window_id, tab_id, os_window_id, or
// kitty_socket key, and the run waiting on it no window_id.
func TestCompanionSnapshotV2_Golden(t *testing.T) {
	checkCompanionGolden(t, companionGoldenV2Path, goldenCompanionSnapshotV2())

	var raw struct {
		Sessions []map[string]json.RawMessage `json:"sessions"`
		ASL      struct {
			Waiting []map[string]json.RawMessage `json:"waiting"`
		} `json:"asl"`
	}
	data, err := os.ReadFile(companionGoldenV2Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"window_id", "tab_id", "os_window_id", "kitty_socket"} {
		if _, ok := raw.Sessions[1][key]; ok {
			t.Errorf("tmux session carries Kitty key %q", key)
		}
	}
	if _, ok := raw.ASL.Waiting[1]["window_id"]; ok {
		t.Error("run waiting on a tmux session carries a window_id")
	}
}

// TestCompanionSnapshot_Golden pins the v1 wire shape byte for byte. A change
// here is a change to what every shipped companion reads: renaming a key is a
// new snapshot version, adding one is additive and needs only the golden
// regenerated with -update-companion-golden.
func TestCompanionSnapshot_Golden(t *testing.T) {
	checkCompanionGolden(t, companionGoldenPath, goldenCompanionSnapshot())
}

// TestCompanionSnapshot_GoldenSetsEveryField guards the golden against going
// vacuous: a field added to the shape but left zero in the fixture would be
// omitted from the file, and the golden would stop describing it.
func TestCompanionSnapshot_GoldenSetsEveryField(t *testing.T) {
	var walk func(path string, v reflect.Value)
	walk = func(path string, v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				t.Errorf("%s is nil in the golden fixture", path)
				return
			}
			walk(path, v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(path+"."+v.Type().Field(i).Name, v.Field(i))
			}
		case reflect.Slice:
			if v.Len() == 0 {
				t.Errorf("%s is empty in the golden fixture", path)
				return
			}
			walk(path+"[0]", v.Index(0))
		default:
			// Ready is deliberately false to pin that it is never omitted.
			if v.IsZero() && path != "CompanionSnapshot.Daemon.Ready" {
				t.Errorf("%s is zero in the golden fixture", path)
			}
		}
	}
	// The v2 fixture is the v1 fixture plus every runtime-only field, so
	// walking it covers both shapes; the v1 golden itself stays free of them.
	walk("CompanionSnapshot", reflect.ValueOf(goldenCompanionSnapshotV2()))
}

// TestCompanionSnapshot_ToleratesUnknownFields is the forward direction: a
// snapshot from a newer daemon with keys this build does not know decodes
// cleanly, keeping every key it does know.
func TestCompanionSnapshot_ToleratesUnknownFields(t *testing.T) {
	raw := `{"v":1,"future_block":{"x":1},"daemon":{"pid":7,"ready":true,"new_flag":true},
		"kitty":{"ready":true},"notification":{},"dashboard":{"open":false},"attention":{"total":1,"stop":1,"later":3},
		"asl":{"waiting":[{"run_id":"r","window_id":1,"unanswered":1,"body":"never"}]},
		"sessions":[{"window_id":3,"icon_state":"stop","attention":true,"new_session_field":"x"}]}`
	var s CompanionSnapshot
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unknown fields broke decoding: %v", err)
	}
	if s.Daemon.PID != 7 || !s.Daemon.Ready || s.Attention.Stop != 1 || len(s.Sessions) != 1 || s.Sessions[0].IconState != "stop" || s.ASL.Waiting[0].RunID != "r" {
		t.Fatalf("known fields lost alongside unknown ones: %+v", s)
	}
}

// TestCompanionSnapshot_OmittedFieldsDecodeToZero is the backward direction:
// a minimal snapshot, as an older or sparser producer might send, decodes to
// zero values rather than failing.
func TestCompanionSnapshot_OmittedFieldsDecodeToZero(t *testing.T) {
	var s CompanionSnapshot
	if err := json.Unmarshal([]byte(`{"v":1}`), &s); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, CompanionSnapshot{V: 1}) {
		t.Fatalf("minimal snapshot = %+v", s)
	}
	// The required keys are always present on encode, even at zero, so a
	// client can tell an empty session list from a missing one.
	data, err := json.Marshal(CompanionSnapshot{V: 1, Sessions: []CompanionSession{}})
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"v", "daemon", "kitty", "notification", "dashboard", "attention", "asl", "sessions"} {
		if _, ok := keys[key]; !ok {
			t.Errorf("key %q omitted from an empty snapshot", key)
		}
	}
	if string(keys["sessions"]) != "[]" {
		t.Errorf("sessions = %s, want []", keys["sessions"])
	}
}

// TestCompanionRequests_Roundtrip carries every companion parameter block
// across the Request/Response wire.
func TestCompanionRequests_Roundtrip(t *testing.T) {
	requests := []Request{
		{Type: TypeSubscribe, Subscribe: &SubscribeRequest{
			Protocol: CompanionProtocolVersion, Client: "AgentMux.app", ClientVersion: "0.1.0",
			AMXBuild: &buildinfo.Info{Version: "1.2.3"}, Features: []string{"notifications"},
			NotificationBackend: true, NotificationsAuthorized: true,
		}},
		{Type: TypeFocusWindow, FocusWindow: &FocusWindowRequest{KittySocket: "unix:/tmp/kitty-1", WindowID: 9}},
		{Type: TypeNotificationSnooze, Snooze: &SnoozeRequest{Mode: SnoozeModeUntil, Until: 1_700_003_600}},
		{Type: TypeNotificationDisposition, Disposition: &DispositionRequest{NotificationID: "n-1", Disposition: "clicked", Error: "x"}},
	}
	for _, in := range requests {
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var out Request
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(out, in) {
			t.Fatalf("%s request = %+v, want %+v", in.Type, out, in)
		}
	}

	resp := Response{
		OK:           true,
		Subscription: &SubscriptionAck{Protocol: 1, ServerProtocolMax: 1, SubscriberID: "sub-1", HeartbeatMS: 15_000, Features: []string{"snapshot"}},
		Focused:      &FocusWindowResult{WindowID: 9, TabID: 3, OSWindowID: 1},
		Snooze:       &SnoozeState{SnoozedUntil: 1_700_003_600},
		Companion: &CompanionHealth{Subscribers: 1, NotificationBackendConnected: true, Clients: []CompanionClient{{
			SubscriberID: "sub-1", Client: "AgentMux.app", ClientVersion: "0.1.0",
			AMXBuild: &buildinfo.Info{Version: "1.2.3"}, NotificationBackend: true, ConnectedAt: 1_700_000_000,
		}}},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var out Response
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, resp) {
		t.Fatalf("response = %+v, want %+v", out, resp)
	}
}

// TestCompanionResponse_OldDaemonOmitsBlocks is the compatibility direction for
// health and one-shot replies: a daemon predating the companion protocol
// leaves every block nil, which a client reads as "not supported".
func TestCompanionResponse_OldDaemonOmitsBlocks(t *testing.T) {
	var out Response
	if err := json.Unmarshal([]byte(`{"ok":true,"ready":true,"pid":1}`), &out); err != nil {
		t.Fatal(err)
	}
	if out.Subscription != nil || out.Focused != nil || out.Snooze != nil || out.Companion != nil {
		t.Fatalf("an old reply grew companion blocks: %+v", out)
	}
	data, err := json.Marshal(Response{OK: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"subscription"`, `"focused"`, `"snooze"`, `"companion"`} {
		if bytes.Contains(data, []byte(key)) {
			t.Errorf("an ordinary response encodes %s", key)
		}
	}
}

func TestStreamFrame_Roundtrip(t *testing.T) {
	bye, err := json.Marshal(Bye{Reason: ByeSlowConsumer})
	if err != nil {
		t.Fatal(err)
	}
	in := StreamFrame{V: CompanionProtocolVersion, Seq: 42, Type: FrameBye, Data: bye}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"v":1,"seq":42,"type":"bye","data":{"reason":"slow_consumer"}}` {
		t.Fatalf("frame = %s", data)
	}
	var out StreamFrame
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	var reason Bye
	if err := json.Unmarshal(out.Data, &reason); err != nil || reason.Reason != ByeSlowConsumer || out.Seq != 42 || out.Type != FrameBye {
		t.Fatalf("frame decoded to %+v / %+v (%v)", out, reason, err)
	}

	heartbeat, err := json.Marshal(StreamFrame{V: 1, Seq: 1, Type: FrameHeartbeat})
	if err != nil {
		t.Fatal(err)
	}
	if string(heartbeat) != `{"v":1,"seq":1,"type":"heartbeat"}` {
		t.Fatalf("heartbeat = %s", heartbeat)
	}

	// A frame type this build does not know still decodes, so a reader can
	// skip it rather than drop the stream.
	var future StreamFrame
	if err := json.Unmarshal([]byte(`{"v":2,"seq":3,"type":"later","data":{"x":1},"extra":true}`), &future); err != nil {
		t.Fatalf("future frame: %v", err)
	}
	if future.Type != "later" || future.Seq != 3 {
		t.Fatalf("future frame = %+v", future)
	}
}

func TestAttentionStates_Membership(t *testing.T) {
	want := map[string]bool{"stop": true, "key": true, "error": true, "question": true}
	if !reflect.DeepEqual(AttentionStates, want) {
		t.Fatalf("AttentionStates = %v, want %v", AttentionStates, want)
	}
}
