package flightlog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFirstEventSurvivesInitializationAndConcurrentWriters(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "flight.jsonl")}
	ctx := context.Background()
	first := event("first", "rs", "launch", "", 0)
	first.LaunchID = "first"
	if err := store.Append(ctx, first); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Go(func() {
			e := event(fmt.Sprint(i), "rs", "state", "bolt", i+1)
			if err := store.Append(ctx, e); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	snapshot, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events) != 25 || !snapshot.TrackingSince.Equal(first.At) {
		t.Fatalf("lost first/concurrent event: %+v", snapshot)
	}
	info, err := os.Stat(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("not private: %s", info.Mode())
	}
}

func TestSettingsResetAndOldQueuedEvents(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "flight.jsonl")}
	ctx := context.Background()
	if err := store.Initialize(ctx, at(-1)); err != nil {
		t.Fatal(err)
	}
	off := false
	if err := store.Configure(ctx, &off, &off); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, event("ignored", "rs", "state", "bolt", 0)); err != nil {
		t.Fatal(err)
	}
	on := true
	if err := store.Configure(ctx, &on, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, event("old", "rs", "state", "bolt", 1)); err != nil {
		t.Fatal(err)
	}
	if err := store.Reset(ctx, at(10)); err != nil {
		t.Fatal(err)
	}
	legacy := event("queued-legacy", "legacy", "state", "bolt", 5)
	legacy.Partial = true
	if err := store.Append(ctx, event("queued", "rs", "state", "bolt", 5), legacy, event("new", "rs", "state", "bolt", 15)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events) != 1 || snapshot.Events[0].ID != "new" || snapshot.Settings.Celebrations || !snapshot.Settings.Enabled || !snapshot.TrackingSince.Equal(at(10)) {
		t.Fatalf("reset/settings not durable: %+v", snapshot)
	}
}

func TestPartialWritesPreservedAndInvalidHeaderNeverRebuilt(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "flight.jsonl")}
	ctx := context.Background()
	if err := store.Initialize(ctx, at(-1)); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(store.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"interrupted":`)
	_ = f.Close()
	if err := store.Append(ctx, event("good", "rs", "state", "bolt", 0)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CorruptLines != 1 || len(snapshot.Events) != 1 {
		t.Fatalf("partial line swallowed record: %+v", snapshot)
	}
	bad := []byte("{unknown-format}\n")
	if err := os.WriteFile(store.Path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, event("lost", "rs", "state", "bolt", 1)); err == nil {
		t.Fatal("corrupt data overwritten")
	}
	data, _ := os.ReadFile(store.Path)
	if string(data) != string(bad) {
		t.Fatal("authoritative data was replaced")
	}
	if err := store.Reset(ctx, at(5)); err != nil {
		t.Fatal(err)
	}
	reset, err := store.Load(ctx)
	if err != nil || len(reset.Events) != 0 || !reset.TrackingSince.Equal(at(5)) {
		t.Fatalf("explicit reset could not recover a corrupt ledger: %+v %v", reset, err)
	}
}

func TestContendedRecorderHonorsBudget(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "flight.jsonl")}
	lock, err := os.OpenFile(store.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := store.Append(ctx, event("blocked", "rs", "state", "bolt", 0)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock ignored caller budget: %v", err)
	}
}

func TestCompactionPreservesCountsDurationsGapsAndFutureObservation(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "flight.jsonl")}
	ctx := context.Background()
	launch := event("launch", "rs", "launch", "", 0)
	launch.LaunchID = "l"
	launch.Fleet = []string{"codex"}
	work := event("work", "rs", "state", "bolt", 1)
	events := []Event{launch, work}
	for i := range 100 {
		if i >= 50 && i <= 52 {
			continue
		} // missed samples must stay unknown after compaction
		events = append(events, event(fmt.Sprintf("sample%d", i), "rs", "sample", "bolt", i*20+20))
	}
	stop := event("stop", "rs", "state", "stop", 2020)
	stop.BeforeIcon = "bolt"
	events = append(events, stop)
	if err := store.Append(ctx, events...); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Load(ctx)
	original := Build(before, Filters{Period: "all"}, at(2100), []string{"codex"})
	for range 2 {
		if err := store.Compact(ctx); err != nil {
			t.Fatal(err)
		}
	}
	after, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	compacted := Build(after, Filters{Period: "all"}, at(2100), []string{"codex"})
	if len(after.Events) > 8 {
		t.Fatalf("samples not compacted: %d", len(after.Events))
	}
	if !reflect.DeepEqual(original.Totals, compacted.Totals) || !reflect.DeepEqual(original.Achievements, compacted.Achievements) || !reflect.DeepEqual(original.Days, compacted.Days) {
		t.Fatalf("compaction changed report: before %+v after %+v", original.Totals, compacted.Totals)
	}
	if !original.Sessions[0].FirstAt.Equal(compacted.Sessions[0].FirstAt) {
		t.Fatal("compaction changed session start")
	}
	more := event("later", "rs", "sample", "stop", 2040)
	if err := store.Append(ctx, more); err != nil {
		t.Fatal(err)
	}
	updated, _ := store.Load(ctx)
	r := Build(updated, Filters{Period: "all"}, at(2100), []string{"codex"})
	if r.Totals.ObservedSeconds != original.Totals.ObservedSeconds+20 || r.Totals.WorkingSeconds != original.Totals.WorkingSeconds {
		t.Fatalf("compaction broke next observation: %+v", r.Totals)
	}
}

func TestHistoricalConversationCountsDoNotUseAFutureConversationAndSurviveCompaction(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "flight.jsonl")}
	ctx := context.Background()
	old := event("old-conversation", "rs", "state", "bolt", 0)
	old.ConversationID = "codex:old"
	sample := event("known", "rs", "sample", "bolt", 30)
	sample.ConversationID = old.ConversationID
	isolated := sample
	isolated.ID = "isolated"
	isolated.At = old.At.AddDate(0, 0, 1)
	newer := event("new-conversation", "rs", "state", "bolt", 0)
	newer.BeforeIcon = "stop"
	newer.At = old.At.AddDate(0, 0, 2)
	newer.ConversationID = "codex:new"
	if err := store.Append(ctx, old, sample, isolated, newer); err != nil {
		t.Fatal(err)
	}
	now := newer.At.Add(time.Hour)
	before, _ := store.Load(ctx)
	original := Build(before, Filters{Period: "all"}, now, []string{"codex"})
	if original.Totals.Conversations != 2 {
		t.Fatalf("lost conversation history: %+v", original.Totals)
	}
	for _, day := range original.Days {
		if day.Date == old.At.Format(time.DateOnly) || day.Date == isolated.At.Format(time.DateOnly) {
			if day.Metrics.Conversations != 1 {
				t.Fatalf("future identity leaked into %s: %+v", day.Date, day.Metrics)
			}
		}
	}
	if err := store.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := store.Load(ctx)
	compacted := Build(after, Filters{Period: "all"}, now, []string{"codex"})
	if !reflect.DeepEqual(original.Days, compacted.Days) || !reflect.DeepEqual(original.Totals, compacted.Totals) || compacted.Sessions[0].ConversationID != "codex:new" {
		t.Fatalf("compaction changed historical identities: before %+v after %+v", original.Totals, compacted.Totals)
	}
}
