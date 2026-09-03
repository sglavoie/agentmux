package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// TestResponse_RoundtripCarriesKittySnapshot pins the whole optional block
// across a marshal/unmarshal pair, topology included. The dashboard renders
// from exactly this decode, so a field that silently fails to survive it would
// surface as a window that vanished rather than as an error.
func TestResponse_RoundtripCarriesKittySnapshot(t *testing.T) {
	in := Response{OK: true, KittySnapshot: &KittySnapshot{
		Ready:      true,
		Stale:      true,
		Generation: 12,
		CapturedAt: 1_700_000_000,
		AgeMS:      6_500,
		Coordinator: &KittyListCoordinatorStatus{
			Socket:              "unix:/tmp/kitty-756",
			Attempts:            9,
			Failures:            2,
			Timeouts:            1,
			CoalescedWaiters:    4,
			InFlight:            true,
			ConsecutiveFailures: 1,
			LastLatencyMS:       10_000,
			MaxLatencyMS:        10_000,
			LastFailureAt:       1_699_999_000,
			LastErrorClass:      string(kitty.ListErrorTimeout),
		},
		Windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{{
				ID:    100,
				Title: "W:api",
				Windows: []kitty.Window{{
					ID:       42,
					Title:    "codex",
					CWD:      "/home/user/projects/api",
					Cmdline:  []string{"codex"},
					UserVars: kitty.UserVars{Profile: "work", Harness: "codex"},
				}},
			}},
		}},
	}}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal(Response): %v", err)
	}
	var out Response
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal(Response): %v", err)
	}
	if out.KittySnapshot == nil {
		t.Fatal("kitty snapshot block lost in roundtrip")
	}
	if !reflect.DeepEqual(out.KittySnapshot.Coordinator, in.KittySnapshot.Coordinator) {
		t.Fatalf("coordinator = %#v, want %#v", out.KittySnapshot.Coordinator, in.KittySnapshot.Coordinator)
	}
	if out.KittySnapshot.Ready != true || !out.KittySnapshot.Stale ||
		out.KittySnapshot.Generation != 12 || out.KittySnapshot.CapturedAt != 1_700_000_000 ||
		out.KittySnapshot.AgeMS != 6_500 {
		t.Fatalf("freshness fields = %+v, want %+v", *out.KittySnapshot, *in.KittySnapshot)
	}
	if !reflect.DeepEqual(out.KittySnapshot.Windows, in.KittySnapshot.Windows) {
		t.Fatalf("windows = %#v, want %#v", out.KittySnapshot.Windows, in.KittySnapshot.Windows)
	}
}

// TestResponse_OldDaemonOmitsKittySnapshot is one of the two compatibility
// directions: a response produced before the block existed decodes into the new
// shape with a nil block. Nil is what tells a new client to fall back to its own
// Kitty I/O, so it must survive as nil rather than as a zero-valued cold block.
func TestResponse_OldDaemonOmitsKittySnapshot(t *testing.T) {
	// Verbatim shape of a pre-block state_query response.
	const legacy = `{"ok":true,"activity":{"42":1700000000},` +
		`"states":{"42":{"icon_state":"bolt","last_action":"active"}},"ready":true}`

	var out Response
	if err := json.Unmarshal([]byte(legacy), &out); err != nil {
		t.Fatalf("decoding a pre-block response: %v", err)
	}
	if out.KittySnapshot != nil {
		t.Fatalf("kitty snapshot = %+v; want nil from a daemon that never published one", out.KittySnapshot)
	}
	if !out.OK || !out.Ready || out.States[42].IconState != "bolt" || out.Activity[42] != 1_700_000_000 {
		t.Fatalf("legacy fields lost: %+v", out)
	}

	// And a Response with no block marshals without the key at all, so an old
	// client is never handed a field it would have to ignore.
	data, err := json.Marshal(Response{OK: true})
	if err != nil {
		t.Fatalf("Marshal(Response): %v", err)
	}
	if strings.Contains(string(data), "kitty_snapshot") {
		t.Fatalf("empty response emitted the optional block: %s", data)
	}
}

// legacyStateQueryResponse is a fixture of the Response fields a client built
// before the snapshot block existed. Decoding a new daemon's response into it is
// the other compatibility direction: unknown keys must be ignored and every
// field the old client actually reads must arrive intact.
type legacyStateQueryResponse struct {
	OK       bool                        `json:"ok"`
	Error    string                      `json:"error,omitempty"`
	Activity map[int]int64               `json:"activity,omitempty"`
	Watchdog map[int]WatchdogState       `json:"watchdog,omitempty"`
	States   map[int]WindowStateSnapshot `json:"states,omitempty"`
	Ready    bool                        `json:"ready"`
}

// TestResponse_LegacyClientIgnoresKittySnapshot pins that adding the block did
// not change anything an old client reads. It decodes a full new response —
// topology and all — into the pre-block fixture above.
func TestResponse_LegacyClientIgnoresKittySnapshot(t *testing.T) {
	current := Response{
		OK:       true,
		Ready:    true,
		Activity: map[int]int64{42: 1_700_000_000},
		Watchdog: map[int]WatchdogState{42: {LastAction: "active", LastEventAt: 1_700_000_000, ThresholdSec: 300}},
		States:   map[int]WindowStateSnapshot{42: {IconState: "bolt", LastAction: "active"}},
		KittySnapshot: &KittySnapshot{
			Ready: true, Generation: 3, CapturedAt: 1_700_000_000, AgeMS: 250,
			Coordinator: &KittyListCoordinatorStatus{Socket: "unix:/tmp/kitty-756", Attempts: 3},
			Windows:     []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{{ID: 100, Title: "W:api"}}}},
		},
	}
	data, err := json.Marshal(current)
	if err != nil {
		t.Fatalf("Marshal(Response): %v", err)
	}

	var legacy legacyStateQueryResponse
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatalf("a pre-block client failed to decode a current response: %v", err)
	}
	if !legacy.OK || !legacy.Ready || legacy.Error != "" {
		t.Fatalf("legacy decode = %+v; want the unchanged status fields", legacy)
	}
	if !reflect.DeepEqual(legacy.Activity, current.Activity) ||
		!reflect.DeepEqual(legacy.Watchdog, current.Watchdog) ||
		!reflect.DeepEqual(legacy.States, current.States) {
		t.Fatalf("legacy decode = %+v; want every pre-block field intact", legacy)
	}
}

// TestKittySnapshotStale pins the shared threshold, including its boundary. Six
// seconds exactly is stale — the contract is "age >= 6s", not "age > 6s" — and
// a cold cache is never stale whatever age it is asked about, because coldness
// and staleness call for different remedies.
func TestKittySnapshotStale(t *testing.T) {
	if KittySnapshotStaleAfter != 6*time.Second {
		t.Fatalf("KittySnapshotStaleAfter = %v; want the contract's six seconds", KittySnapshotStaleAfter)
	}
	tests := []struct {
		name  string
		ready bool
		age   time.Duration
		want  bool
	}{
		{name: "fresh", ready: true, age: 5 * time.Second, want: false},
		{name: "just under the threshold", ready: true, age: 5999 * time.Millisecond, want: false},
		{name: "exactly at the threshold", ready: true, age: 6 * time.Second, want: true},
		{name: "well past", ready: true, age: time.Minute, want: true},
		{name: "cold", ready: false, age: 0, want: false},
		{name: "cold and asked about an old age", ready: false, age: time.Hour, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := KittySnapshotStale(test.ready, test.age); got != test.want {
				t.Fatalf("KittySnapshotStale(%v, %v) = %v, want %v", test.ready, test.age, got, test.want)
			}
		})
	}
}

// TestNewKittyListCoordinatorStatus pins the two encoding rules the block
// promises: durations become whole milliseconds and the failure instant becomes
// a Unix second, with a never-failed socket reporting neither.
func TestNewKittyListCoordinatorStatus(t *testing.T) {
	failure := time.Unix(1_699_999_000, 0)
	got := NewKittyListCoordinatorStatus(kitty.ListStatus{
		Socket:              "unix:/tmp/kitty-756",
		Attempts:            9,
		Failures:            2,
		Timeouts:            1,
		CoalescedWaiters:    4,
		InFlight:            true,
		ConsecutiveFailures: 1,
		LastLatency:         1500 * time.Millisecond,
		MaxLatency:          10 * time.Second,
		LastFailure:         failure,
		LastErrorClass:      kitty.ListErrorTimeout,
	})
	want := KittyListCoordinatorStatus{
		Socket: "unix:/tmp/kitty-756", Attempts: 9, Failures: 2, Timeouts: 1,
		CoalescedWaiters: 4, InFlight: true, ConsecutiveFailures: 1,
		LastLatencyMS: 1500, MaxLatencyMS: 10_000,
		LastFailureAt: failure.Unix(), LastErrorClass: "timeout",
	}
	if got != want {
		t.Fatalf("projection = %+v, want %+v", got, want)
	}

	healthy := NewKittyListCoordinatorStatus(kitty.ListStatus{Socket: "unix:/tmp/kitty-756", Attempts: 1})
	if healthy.LastFailureAt != 0 || healthy.LastErrorClass != "" {
		t.Fatalf("never-failed socket = %+v; want no failure evidence", healthy)
	}
}

// TestDurationMillis pins the truncation and the clamp. Sub-millisecond
// latencies floor to zero rather than rounding up, and a negative duration —
// only reachable through a backwards wall clock — reports no time at all.
func TestDurationMillis(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want int64
	}{
		{in: 0, want: 0},
		{in: -time.Hour, want: 0},
		{in: 999 * time.Microsecond, want: 0},
		{in: time.Millisecond, want: 1},
		{in: 1500 * time.Microsecond, want: 1},
		{in: 6 * time.Second, want: 6000},
	}
	for _, test := range tests {
		if got := DurationMillis(test.in); got != test.want {
			t.Fatalf("DurationMillis(%v) = %d, want %d", test.in, got, test.want)
		}
	}
}
