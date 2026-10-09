package runtimesession

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestReaderSeesCommitsAndEndsWithoutRestart(t *testing.T) {
	store := testStore(t)
	reader := NewReader(store)

	missing, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := reader.Lookup(missing); ok || err != nil {
		t.Fatalf("Lookup on a missing registry = ok %v err %v; want a clean miss", ok, err)
	}

	var pending Record
	if err := store.Update(func(r *Registry) error {
		pending, err = r.Begin(BeginRequest{Project: "API", LaunchID: "launch-1", Relation: RelationFresh}, testEpoch)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if record, ok, err := reader.Lookup(pending.ID); !ok || err != nil || record.State != StatePending {
		t.Fatalf("Lookup(pending) = %+v ok %v err %v", record, ok, err)
	}

	// A pending record is re-read on every lookup, so the commit is visible
	// to the very next hook.
	if err := store.Update(func(r *Registry) error {
		_, err := r.Commit(pending.ID, testTarget("gen-1", "%1"), testProcess(10, "start-a"), testEpoch.Add(time.Second))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if record, ok, _ := reader.Lookup(pending.ID); !ok || record.State != StateLive || record.Target == nil {
		t.Fatalf("Lookup after commit = %+v ok %v; want live with target", record, ok)
	}

	// A live record is answered from cache until a periodic Refresh.
	if err := store.Update(func(r *Registry) error {
		_, err := r.End(pending.ID, testEpoch.Add(2*time.Second))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if record, _, _ := reader.Lookup(pending.ID); record.State != StateLive {
		t.Fatalf("cached Lookup = %s; want the cached live record until refresh", record.State)
	}
	if err := reader.Refresh(); err != nil {
		t.Fatal(err)
	}
	if record, _, _ := reader.Lookup(pending.ID); record.State != StateEnded {
		t.Fatalf("Lookup after Refresh = %s; want ended", record.State)
	}
}

func TestReaderKeepsTheLastGoodVersionWhenTheFileBreaks(t *testing.T) {
	store := testStore(t)
	reader := NewReader(store)
	var live Record
	if err := store.Update(func(r *Registry) error {
		live = beginLive(t, r, "launch-1", testTarget("gen-1", "%1"), testProcess(10, "start-a"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := reader.Lookup(live.ID); !ok || err != nil {
		t.Fatalf("Lookup = ok %v err %v", ok, err)
	}
	if err := os.WriteFile(store.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reader.Refresh(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Refresh over a corrupt file = %v; want ErrCorrupt", err)
	}
	if record, ok, _ := reader.Lookup(live.ID); !ok || record.State != StateLive {
		t.Fatalf("Lookup after corruption = %+v ok %v; want the last good record", record, ok)
	}
}

func TestReaderReturnsCopies(t *testing.T) {
	store := testStore(t)
	reader := NewReader(store)
	var live Record
	if err := store.Update(func(r *Registry) error {
		live = beginLive(t, r, "launch-1", testTarget("gen-1", "%1"), testProcess(10, "start-a"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first, _, _ := reader.Lookup(live.ID)
	first.Target.Ref = "%99"
	second, _, _ := reader.Lookup(live.ID)
	if second.Target.Ref != "%1" {
		t.Fatalf("mutating a returned record changed the cache: %q", second.Target.Ref)
	}
}
