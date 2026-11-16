package flightlog

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func requireEligibility(t *testing.T, report Report, id string, want *bool) {
	t.Helper()
	for _, a := range report.Achievements {
		if a.ID == id {
			if !reflect.DeepEqual(a.CelebrationEligible, want) {
				t.Fatalf("%s eligibility = %v, want %v", id, a.CelebrationEligible, want)
			}
			data, err := json.Marshal(a)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			value, present := fields["celebration_eligible"]
			if present != (want != nil) || (want != nil && value != *want) {
				t.Fatalf("eligibility JSON: %s", data)
			}
			return
		}
	}
	t.Fatalf("achievement %s missing", id)
}

func TestEligibilitySurvivesUnobservedOffPeriodAndRestart(t *testing.T) {
	for _, tracking := range []bool{false, true} {
		t.Run(fmt.Sprintf("tracking=%v", tracking), func(t *testing.T) {
			ctx := context.Background()
			clock := at(0)
			store := Store{Path: filepath.Join(t.TempDir(), "flight.jsonl"), clock: func() time.Time { return clock }}
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(store.Initialize(ctx, clock))
			baseline, err := store.Load(ctx)
			must(err)
			requireEligibility(t, Build(baseline, Filters{}, at(1), nil), "first-flight", nil)
			on, off := true, false
			clock = at(10)
			if tracking {
				must(store.Configure(ctx, &off, nil))
			} else {
				must(store.Configure(ctx, nil, &off))
			}
			launch := event("off-launch", "rs", "launch", "", 15)
			launch.LaunchID = "off"
			must(store.Append(ctx, launch))
			// A restarted process has no in-memory evidence of the off interval.
			store = Store{Path: store.Path, clock: func() time.Time { return clock }}
			clock = at(20)
			must(store.Configure(ctx, &on, &on))
			// Tracking suppresses recording; late imports still use their event time.
			if tracking {
				launch.ID = "import:off-launch"
				must(store.Append(ctx, launch))
			}
			after := event("on-launch", "rs-two", "launch", "", 25)
			after.LaunchID = "on"
			after.Operation = "fork"
			must(store.Append(ctx, after))
			snap, err := store.Load(ctx)
			must(err)
			if len(snap.CelebrationHistory) != 3 {
				t.Fatalf("history: %+v", snap.CelebrationHistory)
			}
			for _, filters := range []Filters{{Period: "all"}, {Period: "today", Project: "no-match"}} {
				report := Build(snap, filters, at(30), nil)
				if !report.Settings.Enabled || !report.Settings.Celebrations {
					t.Fatal("expected settings reenabled")
				}
				requireEligibility(t, report, "first-flight", &off)
				requireEligibility(t, report, "branching-out", &on)
				requireEligibility(t, report, "frequent-flyer", nil)
			}
			// Mission and compaction rewrites must carry the header history intact.
			_, err = store.SaveMission(ctx, Mission{Title: "Ship", Project: "API", Status: "shipped"}, at(27))
			must(err)
			must(store.Compact(ctx))
			reopened, err := (Store{Path: store.Path}).Load(ctx)
			must(err)
			if !reflect.DeepEqual(snap.CelebrationHistory, reopened.CelebrationHistory) {
				t.Fatal("header rewrite lost history")
			}
			report := Build(reopened, Filters{}, at(30), nil)
			requireEligibility(t, report, "first-flight", &off)
			for _, a := range report.Achievements {
				if len(a.ID) >= 8 && a.ID[:8] == "mission:" && a.CelebrationEligible != nil {
					t.Fatal("mission became eligible")
				}
			}
			clock = at(40)
			must(store.Configure(ctx, nil, &off))
			must(store.Reset(ctx, at(50)))
			reset, err := store.Load(ctx)
			must(err)
			if len(reset.Events) != 0 || len(reset.CelebrationHistory) != 1 || !reset.CelebrationHistory[0].EffectiveAt.Equal(at(50)) || *reset.CelebrationHistory[0].Celebrations {
				t.Fatalf("reset history: %+v", reset)
			}
		})
	}
}

func TestCelebrationHistoryBoundariesAndInvalidEvidence(t *testing.T) {
	on, off := true, false
	enabled := Settings{Enabled: true, Celebrations: true}
	disabled := Settings{Enabled: true, Celebrations: false}
	cases := []struct {
		name    string
		history []CelebrationSetting
		at      time.Time
		want    *bool
	}{
		{"legacy", nil, at(5), nil},
		{"before-history", []CelebrationSetting{celebrationSetting(at(10), enabled)}, at(5), nil},
		{"initial-boundary", []CelebrationSetting{celebrationSetting(at(0), enabled)}, at(0), &on},
		{"disable-boundary", []CelebrationSetting{celebrationSetting(at(0), enabled), celebrationSetting(at(10), disabled)}, at(10), &off},
		{"enable-boundary", []CelebrationSetting{celebrationSetting(at(0), disabled), celebrationSetting(at(10), enabled)}, at(10), &on},
		{"equal-time-disable-enable", []CelebrationSetting{celebrationSetting(at(0), enabled), celebrationSetting(at(10), disabled), celebrationSetting(at(10), enabled)}, at(10), &off},
		{"after-equal-time", []CelebrationSetting{celebrationSetting(at(0), enabled), celebrationSetting(at(10), disabled), celebrationSetting(at(10), enabled)}, at(11), &on},
		{"non-monotonic-even-after-unlock", []CelebrationSetting{celebrationSetting(at(0), enabled), celebrationSetting(at(10), enabled), celebrationSetting(at(9), enabled)}, at(5), nil},
		{"missing-setting", []CelebrationSetting{{EffectiveAt: at(0), Enabled: &on}}, at(5), nil},
		{"missing-time", []CelebrationSetting{{Enabled: &on, Celebrations: &on}}, at(5), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			launch := event("launch", "rs", "launch", "", 0)
			launch.At = tc.at
			snap := testSnapshot(launch)
			snap.CelebrationHistory = tc.history
			requireEligibility(t, Build(snap, Filters{}, at(30), nil), "first-flight", tc.want)
		})
	}
}

func TestLegacyCelebrationMigrationPreservesBytesAndDoesNotBackfill(t *testing.T) {
	for _, path := range []string{"initialize", "append", "configure"} {
		t.Run(path, func(t *testing.T) {
			ctx := context.Background()
			store := Store{Path: filepath.Join(t.TempDir(), "flight.jsonl"), clock: func() time.Time { return at(20) }}
			rawHeader := `{"schema_version":1,"tracking_since":"2026-10-26T10:00:00Z","settings":{"enabled":true,"celebrations":true}}` + "\n"
			launch := event("old", "rs", "launch", "", 5)
			line, err := json.Marshal(launch)
			if err != nil {
				t.Fatal(err)
			}
			tail := append(append(line, '\n'), []byte("{broken-event\n")...)
			original := append([]byte(rawHeader), tail...)
			if err := os.WriteFile(store.Path, original, 0o600); err != nil {
				t.Fatal(err)
			}
			snap, err := store.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			requireEligibility(t, Build(snap, Filters{}, at(30), nil), "first-flight", nil)
			data, err := os.ReadFile(store.Path)
			if err != nil || !bytes.Equal(original, data) {
				t.Fatal("report read mutated legacy ledger")
			}
			off := false
			switch path {
			case "initialize":
				err = store.Initialize(ctx, at(-100))
			case "append":
				err = store.Append(ctx, event("late", "rs", "state", "bolt", 6))
			case "configure":
				err = store.Configure(ctx, nil, &off)
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(store.Path)
			if err != nil || !bytes.Contains(data, tail) {
				t.Fatal("migration changed event bytes")
			}
			snap, err = store.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !snap.TrackingSince.Equal(at(0)) || !snap.CelebrationHistory[0].EffectiveAt.Equal(at(20)) || !*snap.CelebrationHistory[0].Celebrations || snap.CorruptLines != 1 {
				t.Fatalf("bad migration: %+v", snap)
			}
			requireEligibility(t, Build(snap, Filters{}, at(30), nil), "first-flight", nil)
		})
	}
}

func TestConcurrentConfigureAppendRetainsCelebrationHistory(t *testing.T) {
	ctx := context.Background()
	store := Store{Path: filepath.Join(t.TempDir(), "flight.jsonl"), clock: func() time.Time { return at(10) }}
	if err := store.Initialize(ctx, at(0)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 20 {
			value := i%2 != 0
			if err := store.Configure(ctx, nil, &value); err != nil {
				t.Error(err)
			}
		}
	})
	for i := range 20 {
		wg.Go(func() {
			if err := store.Append(ctx, event(fmt.Sprintf("launch-%d", i), "rs", "launch", "", 10)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	snap, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.CelebrationHistory) != 21 || len(snap.Events) != 20 || !snap.Settings.Celebrations {
		t.Fatalf("lost concurrent writes: %+v", snap)
	}
	off := false
	requireEligibility(t, Build(snap, Filters{}, at(30), nil), "first-flight", &off)
	// No-op settings do not add transitions.
	on := true
	if err := store.Configure(ctx, &on, &on); err != nil {
		t.Fatal(err)
	}
	next, err := store.Load(ctx)
	if err != nil || !reflect.DeepEqual(snap.CelebrationHistory, next.CelebrationHistory) {
		t.Fatal("no-op changed history")
	}
}

func TestMalformedCelebrationHistoryIsUnknownAndPreserved(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{}`, `{"effective_at":"2026-10-26T10:00:00Z","enabled":true,"celebrations":true}`, `[{"effective_at":"invalid","enabled":true,"celebrations":true}]`, `[{"effective_at":"2026-10-26T10:00:00Z","enabled":"true","celebrations":true}]`, `[{"effective_at":"2026-10-26T10:00:00Z","enabled":true}]`} {
		t.Run(raw, func(t *testing.T) {
			ctx := context.Background()
			store := Store{Path: filepath.Join(t.TempDir(), "flight.jsonl"), clock: func() time.Time { return at(20) }}
			headerJSON := `{"schema_version":1,"tracking_since":"2026-10-26T10:00:00Z","settings":{"enabled":true,"celebrations":true},"celebration_history":` + raw + "}\n"
			eventJSON, err := json.Marshal(event("launch", "rs", "launch", "", 5))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(store.Path, append([]byte(headerJSON), append(eventJSON, '\n')...), 0o600); err != nil {
				t.Fatal(err)
			}
			initial, err := store.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			preserveRaw := initial.CelebrationHistory[0].invalidHistory != nil
			assertUnknown := func() {
				t.Helper()
				snap, err := (Store{Path: store.Path}).Load(ctx)
				if err != nil {
					t.Fatal(err)
				}
				report := Build(snap, Filters{}, at(30), nil)
				if report.Lifetime.Launches == 0 {
					t.Fatal("malformed history hid the launch")
				}
				requireEligibility(t, report, "first-flight", nil)
				requireEligibility(t, report, "branching-out", nil)
				if preserveRaw {
					data, err := os.ReadFile(store.Path)
					if err != nil || !bytes.Contains(data, []byte(raw)) {
						t.Fatalf("malformed metadata lost: %s (read error: %v)", data, err)
					}
				}
			}
			assertUnknown()
			for i := range 2 {
				off, on := false, true
				if err := store.Configure(ctx, nil, &off); err != nil {
					t.Fatal(err)
				}
				assertUnknown()
				if err := store.Configure(ctx, nil, &on); err != nil {
					t.Fatal(err)
				}
				assertUnknown()
				fork := event(fmt.Sprintf("fork-%d", i), "rs-fork", "launch", "", 25)
				fork.Operation = "fork"
				if err := store.Append(ctx, fork); err != nil {
					t.Fatal(err)
				}
				assertUnknown()
				_, err := store.SaveMission(ctx, Mission{Title: "Ship", Project: "API", Status: "shipped"}, at(27))
				if err != nil {
					t.Fatal(err)
				}
				assertUnknown()
				if err := store.Compact(ctx); err != nil {
					t.Fatal(err)
				}
				assertUnknown()
			}
		})
	}
}

var updateCelebrationFixture = flag.Bool("update-celebration-fixture", false, "rewrite the shared Swift eligibility fixture")

func TestCelebrationEligibilitySwiftFixture(t *testing.T) {
	// The same encoded achievements feed Swift's receipt watch regression.
	first := event("first", "rs-first", "launch", "", 15)
	first.LaunchID = "first"
	fork := event("fork", "rs-fork", "launch", "", 25)
	fork.LaunchID = "fork"
	fork.Operation = "fork"
	snapshot := testSnapshot(first, fork)
	snapshot.CelebrationHistory = CelebrationHistory{
		celebrationSetting(at(0), Settings{Enabled: true, Celebrations: true}),
		celebrationSetting(at(10), Settings{Enabled: true, Celebrations: false}),
		celebrationSetting(at(20), Settings{Enabled: true, Celebrations: true}),
	}
	report := Build(snapshot, Filters{}, at(30), nil)
	data, err := json.MarshalIndent(report.Achievements, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := "../../macos/AgentMuxTests/Fixtures/flight-celebration-eligibility.json"
	if *updateCelebrationFixture {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, expected) {
		t.Fatal("Swift eligibility fixture is stale; review changes before regenerating with -update-celebration-fixture")
	}
}

func TestCelebrationChangesWhileTrackingDisabledPersist(t *testing.T) {
	ctx := context.Background()
	clock := at(0)
	store := Store{Path: filepath.Join(t.TempDir(), "flight.jsonl"), clock: func() time.Time { return clock }}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Initialize(ctx, clock))
	on, off := true, false
	clock = at(5)
	must(store.Configure(ctx, &off, nil))
	clock = at(10)
	must(store.Configure(ctx, nil, &off))
	clock = at(15)
	must(store.Configure(ctx, nil, &on))
	clock = at(20)
	must(store.Configure(ctx, &on, nil))
	snap, err := store.Load(ctx)
	must(err)
	if len(snap.CelebrationHistory) != 5 {
		t.Fatalf("missing disabled tracking changes: %+v", snap.CelebrationHistory)
	}
	for _, instant := range []int{5, 10, 15, 19} {
		eligibility := celebrationEligibility(snap.CelebrationHistory, at(instant))
		if eligibility == nil || *eligibility {
			t.Fatalf("tracking disabled at %d: %v", instant, eligibility)
		}
	}
}
