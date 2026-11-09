package flightlog

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMissionLifecycle(t *testing.T) {
	ctx := context.Background()
	store := Store{Path: filepath.Join(t.TempDir(), "flight.jsonl")}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	m, err := store.SaveMission(ctx, Mission{Title: "Ship auth", Project: "API", Status: "briefed", References: []string{"codex:one", "/tmp/worktree", "run-1"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	m.Status = "shipped"
	m, err = store.SaveMission(ctx, m, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	first := *m.ShippedAt
	stale := m
	m.Status = "in_progress"
	m, err = store.SaveMission(ctx, m, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveMission(ctx, stale, now.Add(3*time.Hour)); err == nil {
		t.Fatal("stale update accepted")
	}
	m.Status = "shipped"
	if _, err := store.SaveMission(ctx, m, now.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	report := Build(snapshot, Filters{}, now.Add(5*time.Hour), nil)
	if len(report.Missions) != 1 || !report.Missions[0].ShippedAt.Equal(first) {
		t.Fatalf("lost mission: %+v", report.Missions)
	}
	if report.Totals.Launches != 0 || len(report.Sessions) != 0 {
		t.Fatal("missions contaminated session counts")
	}
	patches := 0
	for _, a := range report.Achievements {
		if a.ID == "mission:"+m.ID {
			patches++
		}
	}
	if patches != 1 {
		t.Fatalf("patches=%d", patches)
	}
	other := Build(snapshot, Filters{Project: "Other"}, now.Add(5*time.Hour), nil)
	if len(other.Missions) != 0 || len(other.Achievements) != len(report.Achievements) {
		t.Fatal("filter revoked patch or leaked mission")
	}
	if err := store.Reset(ctx, now.Add(6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(Build(snapshot, Filters{}, now.Add(7*time.Hour), nil).Missions) != 0 {
		t.Fatal("reset kept missions")
	}
}

func TestMissionValidationAndPause(t *testing.T) {
	ctx := context.Background()
	store := Store{Path: filepath.Join(t.TempDir(), "flight.jsonl")}
	now := time.Now()
	for _, m := range []Mission{
		{Title: "", Project: "API", Status: "briefed"},
		{Title: "Ship", Project: "API", Status: "done"},
		{Title: "Ship\nInjected", Project: "API", Status: "briefed"},
	} {
		if _, err := store.SaveMission(ctx, m, now); err == nil {
			t.Fatal("invalid mission accepted")
		}
	}
	disabled := false
	if err := store.Configure(ctx, &disabled, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveMission(ctx, Mission{Title: "Ship", Project: "API", Status: "briefed"}, time.Now()); err == nil {
		t.Fatal("paused mutation accepted")
	}
}
