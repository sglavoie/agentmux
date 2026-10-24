package runtimesession

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "runtime-sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestNewStoreRequiresACleanAbsolutePath(t *testing.T) {
	for _, path := range []string{"", "relative.json", "/tmp/../tmp/x.json"} {
		if _, err := NewStore(path); err == nil {
			t.Errorf("NewStore(%q) accepted an unsafe path", path)
		}
	}
}

func TestStoreRoundTripsAPrivateRegistry(t *testing.T) {
	store := testStore(t)
	registry, err := store.Load()
	if err != nil || len(registry.List()) != 0 {
		t.Fatalf("Load(missing) = %d records, %v; want empty", len(registry.List()), err)
	}
	var want Record
	if err := store.Update(func(registry *Registry) error {
		pending, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-1", Relation: RelationFresh, Conversation: testConversation(t, "conv-1")}, testEpoch)
		if err != nil {
			return err
		}
		live, err := registry.Commit(pending.ID, testTarget("gen-1", "%1"), testProcess(100, "a"), testEpoch.Add(time.Second))
		if err != nil {
			return err
		}
		want, err = registry.Attach(live.ID, Attachment{ID: "view-1", Client: "desktop"}, testEpoch.Add(2*time.Second))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != fileMode {
		t.Fatalf("registry mode = %o, want %o", info.Mode().Perm(), fileMode)
	}
	reloaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Get(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reloaded record = %+v, want %+v", got, want)
	}
}

// A failed update, whether the callback refuses or the result is invalid,
// leaves the published file byte-for-byte as it was and no temporaries behind.
func TestFailedUpdatesPublishNothing(t *testing.T) {
	store := testStore(t)
	if err := store.Update(func(registry *Registry) error {
		_, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-1", Relation: RelationFresh}, testEpoch)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("launch failed")
	if err := store.Update(func(registry *Registry) error {
		if _, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-2", Relation: RelationFresh}, testEpoch); err != nil {
			return err
		}
		return refusal
	}); !errors.Is(err, refusal) {
		t.Fatalf("Update = %v, want the callback's error", err)
	}
	if err := store.Update(func(registry *Registry) error {
		// Bypassing the registry API to corrupt state must still be caught
		// before anything is written.
		for id, record := range registry.sessions {
			record.Project = ""
			registry.sessions[id] = record
		}
		return nil
	}); err == nil {
		t.Fatal("Update published an invalid registry")
	}
	after, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("a failed update changed the registry:\n%s\nwant:\n%s", after, before)
	}
	entries, err := os.ReadDir(filepath.Dir(store.Path()))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("a temporary file survived: %s", entry.Name())
		}
	}
}

// An update whose rename landed is an update made, even when the directory
// cannot be synced afterwards: a caller told otherwise would undo a commit
// every reader already sees. The directory here can be written and searched
// but not opened, which is exactly what an fsync of it needs.
func TestAnUpdateThatLandedSucceedsWithoutADirectorySync(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens a directory whatever its mode")
	}
	store := testStore(t)
	var pending Record
	if err := store.Update(func(registry *Registry) error {
		var err error
		pending, err = registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-1", Relation: RelationFresh}, testEpoch)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(store.Path())
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := os.Open(dir); err == nil {
		t.Skip("this filesystem opens a directory without read permission")
	}
	if err := store.Update(func(registry *Registry) error {
		_, err := registry.Commit(pending.ID, testTarget("gen-1", "%1"), testProcess(100, "a"), testEpoch.Add(time.Second))
		return err
	}); err != nil {
		t.Fatalf("Update = %v; want the landed commit reported as made", err)
	}
	reloaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reloaded.Get(pending.ID); err != nil || got.State != StateLive {
		t.Fatalf("reloaded = %+v, %v; want the commit", got, err)
	}
}

// A writer killed mid-replacement leaves its temporary file beside the
// registry. Readers ignore it, and the next writer neither adopts nor trips
// over it.
func TestAnInterruptedWriteLeavesThePreviousVersion(t *testing.T) {
	store := testStore(t)
	if err := store.Update(func(registry *Registry) error {
		_, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-1", Relation: RelationFresh}, testEpoch)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	crashed := filepath.Join(filepath.Dir(store.Path()), "."+filepath.Base(store.Path())+".12345.tmp")
	if err := os.WriteFile(crashed, []byte(`{"schema_version":1,"sessions":[{"id":`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := store.Load()
	if err != nil {
		t.Fatalf("Load beside a crashed temp = %v", err)
	}
	if got := len(registry.List()); got != 1 {
		t.Fatalf("records = %d, want the 1 published before the crash", got)
	}
	if err := store.Update(func(registry *Registry) error {
		_, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-2", Relation: RelationFresh}, testEpoch)
		return err
	}); err != nil {
		t.Fatalf("Update after a crashed writer = %v", err)
	}
	registry, err = store.Load()
	if err != nil || len(registry.List()) != 2 {
		t.Fatalf("after recovery: %d records, %v", len(registry.List()), err)
	}
}

// Begin and Commit are separate durable steps. A crash between them is
// visible after a restart as a pending record that recovery can abandon.
func TestInterruptedRegistrationSurvivesARestart(t *testing.T) {
	store := testStore(t)
	var pending Record
	if err := store.Update(func(registry *Registry) error {
		var err error
		pending, err = registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-1", Relation: RelationFresh}, testEpoch)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// The launcher dies here. A restarted launcher retrying the same launch
	// is told about the reservation instead of registering a second agent.
	err := store.Update(func(registry *Registry) error {
		_, err := registry.Begin(BeginRequest{Project: "API", LaunchID: "launch-1", Relation: RelationFresh}, testEpoch.Add(time.Minute))
		return err
	})
	if !errors.Is(err, ErrDuplicateLaunch) {
		t.Fatalf("retried Begin = %v, want ErrDuplicateLaunch", err)
	}
	var abandoned []ID
	if err := store.Update(func(registry *Registry) error {
		abandoned = registry.AbandonInterrupted(testEpoch.Add(10*time.Minute), testEpoch.Add(time.Hour))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(abandoned) != 1 || abandoned[0] != pending.ID {
		t.Fatalf("abandoned = %v, want %s", abandoned, pending.ID)
	}
	registry, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	record, err := registry.Get(pending.ID)
	if err != nil || record.State != StateAbandoned {
		t.Fatalf("persisted state = %s, %v; want abandoned", record.State, err)
	}
}

func TestConcurrentUpdatesSerialize(t *testing.T) {
	store := testStore(t)
	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.Update(func(registry *Registry) error {
				_, err := registry.Begin(BeginRequest{Project: "API", LaunchID: fmt.Sprintf("launch-%d", i), Relation: RelationFresh}, testEpoch)
				return err
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	registry, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(registry.List()); got != writers {
		t.Fatalf("records = %d, want %d; a concurrent update was lost", got, writers)
	}
}

func TestLoadRejectsUntrustworthyFiles(t *testing.T) {
	id1 := "rs-" + strings.Repeat("1", 32)
	id2 := "rs-" + strings.Repeat("2", 32)
	stamp := `"created_at":"2026-10-09T12:00:00Z","updated_at":"2026-10-09T12:00:00Z"`
	target := `"target":{"instance":{"kind":"tmux","address":"/tmp/s","generation":"g1"},"ref":"%1"}`
	live := func(id, pid string) string {
		return `{"id":"` + id + `","project":"API","launch_id":"launch-` + id + `","relation":"fresh",` + target +
			`,"process":{"pid":` + pid + `,"start_token":"a"},"state":"live",` + stamp + `}`
	}
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "truncated", body: `{"schema_version":1,"sessions":[`, want: "unexpected EOF"},
		{name: "future version", body: `{"schema_version":2,"sessions":[]}`, want: "unsupported schema version 2"},
		{name: "unknown field", body: `{"schema_version":1,"sessions":[],"future":true}`, want: "unknown field"},
		{name: "trailing data", body: `{"schema_version":1,"sessions":[]}{}`, want: "trailing data"},
		{name: "duplicate ID", body: `{"schema_version":1,"sessions":[` + live(id1, "100") + `,` + live(id1, "101") + `]}`, want: "appears twice"},
		{name: "shared live target", body: `{"schema_version":1,"sessions":[` + live(id1, "100") + `,` + live(id2, "101") + `]}`, want: "already registered to a live session"},
		{name: "live without process", body: `{"schema_version":1,"sessions":[{"id":"` + id1 + `","project":"API","launch_id":"l","relation":"fresh",` + target + `,"state":"live",` + stamp + `}]}`, want: "needs a target and process"},
		{name: "missing parent", body: `{"schema_version":1,"sessions":[{"id":"` + id1 + `","project":"API","launch_id":"l","relation":"resume","parent":"` + id2 + `","state":"pending",` + stamp + `}]}`, want: "is not registered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := testStore(t)
			if err := os.WriteFile(store.Path(), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := store.Load()
			if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load = %v, want ErrCorrupt mentioning %q", err, tc.want)
			}
			// An untrusted registry is never overwritten by an update.
			called := false
			if err := store.Update(func(*Registry) error { called = true; return nil }); !errors.Is(err, ErrCorrupt) || called {
				t.Fatalf("Update over a corrupt file = %v (callback ran: %v)", err, called)
			}
			after, err := os.ReadFile(store.Path())
			if err != nil || string(after) != tc.body {
				t.Fatal("a corrupt registry was modified")
			}
		})
	}
}
