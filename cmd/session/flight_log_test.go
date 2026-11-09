package session

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/flightlog"
)

func TestFlightLogCommandsShareVersionedReportsAndExplicitReset(t *testing.T) {
	store := flightlog.Store{Path: filepath.Join(t.TempDir(), "flight.jsonl")}
	now := time.Now()
	launch := flightlog.Event{ID: "one", At: now.Add(-time.Minute), Kind: "launch", LaunchID: "one", RuntimeSessionID: "rs-one", Project: "API", Harness: "codex", Operation: "resume"}
	if err := store.Append(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	deps := flightLogDependencies{store: func() (flightlog.Store, error) { return store, nil }, now: func() time.Time { return now }, harnesses: func() []string { return []string{"codex"} }}
	run := func(args ...string) (string, error) {
		cmd := newFlightLogCommand(deps)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	output, err := run("--period", "all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		SchemaVersion int              `json:"schema_version"`
		Data          flightlog.Report `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != cli.JSONSchemaVersion || envelope.Data.Totals.Resumes != 1 {
		t.Fatalf("bad report: %s", output)
	}
	output, err = run("--period", "invalid", "--json")
	if err == nil || !strings.Contains(output, `"code":"invalid_arguments"`) {
		t.Fatalf("unstructured argument error: %v %s", err, output)
	}
	output, err = run("reset", "--json")
	if err == nil || !strings.Contains(output, "confirmation_required") {
		t.Fatalf("reset lacked explicit confirmation: %v %s", err, output)
	}
	snapshot, _ := store.Load(context.Background())
	if len(snapshot.Events) != 1 {
		t.Fatal("refused reset changed history")
	}
	output, err = run("settings", "--celebrations=false", "--json")
	if err != nil || !strings.Contains(output, `"celebrations":false`) {
		t.Fatalf("settings: %v %s", err, output)
	}
	output, err = run("export", "--json")
	if err != nil || !strings.Contains(output, `"runtime_session_id":"rs-one"`) {
		t.Fatalf("export lost session identities: %v %s", err, output)
	}
	output, err = run("reset", "--yes", "--json")
	if err != nil {
		t.Fatalf("reset: %v %s", err, output)
	}
	snapshot, _ = store.Load(context.Background())
	if len(snapshot.Events) != 0 || !snapshot.TrackingSince.Equal(now) || snapshot.Settings.Celebrations {
		t.Fatalf("bad reset: %+v", snapshot)
	}
}

func TestFlightLogImportsOnlyOlderHistoryAndNeverDuplicatesIt(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := flightlog.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ctx := context.Background()
	if err := store.Initialize(ctx, now); err != nil {
		t.Fatal(err)
	}
	paths, _ := diagnostics.DefaultPaths()
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	old := diagnostics.NewEnvelope("session.launch")
	old.ObservedAt = now.Add(-time.Hour)
	old.LaunchID = "old-launch"
	old.Harness = "codex"
	old.Outcome = "success"
	old.Evidence = map[string]string{"project": "API", "operation": "resume", "runtime_session_id": "rs-old"}
	state := diagnostics.NewEnvelope("daemon.decision")
	state.ObservedAt = now.Add(-time.Minute)
	state.Harness = "codex"
	state.Outcome = "applied"
	state.Evidence = map[string]string{"runtime_session_id": "rs-old", "replay.before_icon": "bolt", "replay.after_icon": "stop"}
	recent := old
	recent.SourceSequence = diagnostics.NewEnvelope("session.launch").SourceSequence
	recent.LaunchID = "after-tracking"
	recent.ObservedAt = now.Add(time.Minute)
	for _, record := range []diagnostics.TraceEnvelope{old, state, recent} {
		if err := journal.Append(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	first, err := importFlightLog(ctx, store, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := importFlightLog(ctx, store, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Imported != 2 || second.Imported != 0 || !first.Partial {
		t.Fatalf("bad import results: %+v %+v", first, second)
	}
	snapshot, _ := store.Load(ctx)
	report := flightlog.Build(snapshot, flightlog.Filters{Period: "all"}, now.Add(2*time.Minute), []string{"codex"})
	if report.Totals.Launches != 1 || report.Totals.TurnsFinished != 1 || report.Totals.WorkingSeconds != 0 || !report.Sessions[0].Partial {
		t.Fatalf("import invented complete history: %+v", report.Totals)
	}
}

func TestMissionCLIUpdatesRetainFieldsAndShipExplicitly(t *testing.T) {
	store := flightlog.Store{Path: filepath.Join(t.TempDir(), "flight.jsonl")}
	now := time.Now()
	deps := flightLogDependencies{store: func() (flightlog.Store, error) { return store, nil }, now: func() time.Time { now = now.Add(time.Second); return now }, harnesses: func() []string { return nil }}
	run := func(args ...string) string {
		t.Helper()
		cmd := newFlightLogCommand(deps)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %s", err, out.String())
		}
		return out.String()
	}
	var receipt struct{ Data flightlog.Mission }
	if err := json.Unmarshal([]byte(run("mission", "create", "--title", "Ship auth", "--project", "API", "--reference", "codex:abc", "--json")), &receipt); err != nil {
		t.Fatal(err)
	}
	id := receipt.Data.ID
	if err := json.Unmarshal([]byte(run("mission", "update", "--id", id, "--status", "shipped", "--json")), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Data.Title != "Ship auth" || receipt.Data.ShippedAt == nil || len(receipt.Data.References) != 1 {
		t.Fatalf("bad receipt: %+v", receipt.Data)
	}
	if !strings.Contains(run("--json"), "Mission patch") {
		t.Fatal("patch missing")
	}
}
