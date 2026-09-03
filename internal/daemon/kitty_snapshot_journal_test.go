package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// The journal half of the recurrence evidence: a Kitty that stops answering has
// to leave a durable trace behind, and that trace has to stay bounded no matter
// how long the outage lasts. These tests drive a real *kitty.SubprocessClient
// through the observer the daemon installs, because the edges-only contract that
// makes the record bounded belongs to the coordinator rather than to the daemon.

// The secrets a `kitten @ ls` invocation has access to and that must never reach
// a durable record: the remote-control socket, the argv, the payload, and the
// stderr or error text kitten produced.
const (
	journalSecretSocket = "unix:/tmp/kitty-amx-journal-secret-socket"
	journalSecretStderr = "kitten-said-this-and-it-must-not-be-journalled"
)

// faultyListRunner answers `kitten @ ls` with a failure until it is healed.
// Latency comes from the coordinator's own clock around the runner, so a test
// never has to sleep to produce one.
type faultyListRunner struct {
	mu      sync.Mutex
	broken  bool
	payload []byte
}

func newFaultyListRunner(payload []byte) *faultyListRunner {
	return &faultyListRunner{broken: true, payload: payload}
}

func (r *faultyListRunner) heal() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.broken = false
}

func (r *faultyListRunner) Run(_ string, args ...string) ([]byte, []byte, error) {
	if _, isList := parseKittenList(args); !isList {
		return []byte("{}"), nil, nil
	}
	r.mu.Lock()
	broken, payload := r.broken, r.payload
	r.mu.Unlock()
	if broken {
		return nil, []byte(journalSecretStderr), errors.New(journalSecretStderr)
	}
	return payload, nil, nil
}

// journalRecorder builds the production lifecycle recorder over a temporary
// state root, so a test reads back exactly what a daemon would have written.
func journalRecorder(t *testing.T) *lifecycleRecorder {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	cfg := &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
		JournalEnabled: boolPointer(true), JournalKeepDays: 14, JournalMaxMiB: 32,
	}}}
	recorder, err := newLifecycleRecorder(cfg)
	if err != nil {
		t.Fatalf("newLifecycleRecorder() = %v", err)
	}
	if recorder == nil {
		t.Fatal("newLifecycleRecorder() = nil with the journal enabled")
	}
	return recorder
}

// renderJournalRecord is the whole envelope as it would be persisted, so a leak
// test scans every field rather than only the ones it thought to name.
func renderJournalRecord(t *testing.T, record diagnostics.TraceEnvelope) string {
	t.Helper()
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshalling journal record: %v", err)
	}
	return string(encoded)
}

func readKittySnapshotRecords(t *testing.T) []diagnostics.TraceEnvelope {
	t.Helper()
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		t.Fatalf("diagnostics.DefaultPaths() = %v", err)
	}
	// The writer is this process and it is idle by the time a test reads, so the
	// generous lock budget is about repository-wide package concurrency rather
	// than about the production wait.
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{LockTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("diagnostics.NewJournal() = %v", err)
	}
	records, err := journal.Read(context.Background(), diagnostics.Selector{Stages: []string{kittySnapshotStage}})
	if err != nil {
		t.Fatalf("journal.Read() = %v", err)
	}
	return records
}

// waitForKittySnapshotRecords blocks until the journal shows want records for
// the stage. The observer runs on the listing goroutine but the journal append
// is a separate file write, so the count is polled rather than assumed.
func waitForKittySnapshotRecords(t *testing.T, want int) []diagnostics.TraceEnvelope {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		records := readKittySnapshotRecords(t)
		if len(records) >= want {
			return records
		}
		if time.Now().After(deadline) {
			t.Fatalf("journal holds %d %s records; want %d", len(records), kittySnapshotStage, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A failure streak of any length is one record, and the recovery that ends it is
// one more. This is what keeps a Kitty that has been down for an hour from
// filling the journal with the same fact.
func TestKittySnapshotJournalRecordsOnlyHealthEdges(t *testing.T) {
	recorder := journalRecorder(t)
	runner := newFaultyListRunner(mustMarshalWindows(t, fakeOSWindowsWithTab(42, "W:api")))
	client := kitty.NewClientWithSocket(runner, "")
	client.SetListObserver(recorder.kittySnapshotObserver())
	scoped := client.WithSocket(journalSecretSocket)

	const attempts = 6
	for i := 0; i < attempts; i++ {
		if _, err := scoped.ListWindows(); err == nil {
			t.Fatalf("list %d succeeded against a broken runner", i)
		}
	}
	failures := waitForKittySnapshotRecords(t, 1)
	if len(failures) != 1 {
		t.Fatalf("%d records for a %d-attempt failure streak; want the single opening edge: %+v",
			len(failures), attempts, failures)
	}
	if failures[0].Outcome != "error" || failures[0].Reason != "list_failed" {
		t.Errorf("failure record = (%q, %q); want (error, list_failed)", failures[0].Outcome, failures[0].Reason)
	}
	if got := failures[0].Evidence["error_class"]; got != string(kitty.ListErrorCommand) {
		t.Errorf("error_class = %q; want %q", got, kitty.ListErrorCommand)
	}
	if got := failures[0].Evidence["consecutive_failures"]; got != "1" {
		t.Errorf("consecutive_failures = %q; want the streak's opening edge, 1", got)
	}

	runner.heal()
	for i := 0; i < attempts; i++ {
		if _, err := scoped.ListWindows(); err != nil {
			t.Fatalf("list %d failed after the runner healed: %v", i, err)
		}
	}
	records := waitForKittySnapshotRecords(t, 2)
	if len(records) != 2 {
		t.Fatalf("%d records for one outage and one recovery; want 2: %+v", len(records), records)
	}
	recovery := records[1]
	if recovery.Outcome != "success" || recovery.Reason != "list_recovered" {
		t.Errorf("recovery record = (%q, %q); want (success, list_recovered)", recovery.Outcome, recovery.Reason)
	}
	if got := recovery.Evidence["consecutive_failures"]; got != "0" {
		t.Errorf("consecutive_failures = %q on recovery; want 0", got)
	}
	// The coordinator retains LastErrorClass across a recovery on purpose: the
	// recovery record names the outage it ended rather than reporting "none".
	if got := recovery.Evidence["error_class"]; got != string(kitty.ListErrorCommand) {
		t.Errorf("error_class = %q on recovery; want the class the outage ended, %q", got, kitty.ListErrorCommand)
	}
	if got := recovery.Evidence["failures"]; got != "6" {
		t.Errorf("failures = %q; want the %d attempts that failed", got, attempts)
	}
	// The recovery edge is the FIRST success, so the counters it carries are the
	// outage plus that one read — not the whole healthy run that followed.
	if got := recovery.Evidence["attempts"]; got != "7" {
		t.Errorf("attempts = %q; want the %d failures plus the read that recovered", got, attempts)
	}
}

// The record is evidence, not a capture. It carries the stable class, the
// counters, and the latencies, and it must never carry the socket, the kitten
// argv, the payload, the stderr, or the raw error text.
func TestKittySnapshotJournalEvidenceCarriesNoSecrets(t *testing.T) {
	recorder := journalRecorder(t)
	runner := newFaultyListRunner(mustMarshalWindows(t, fakeOSWindowsWithTab(42, "W:payload-secret")))
	client := kitty.NewClientWithSocket(runner, "")
	client.SetListObserver(recorder.kittySnapshotObserver())
	if _, err := client.WithSocket(journalSecretSocket).ListWindows(); err == nil {
		t.Fatal("list succeeded against a broken runner")
	}
	records := waitForKittySnapshotRecords(t, 1)
	if len(records) != 1 {
		t.Fatalf("%d records; want 1: %+v", len(records), records)
	}
	record := records[0]

	// The evidence keys are the whole contract: a new one is a deliberate
	// decision, so the set is pinned exactly rather than merely spot-checked.
	want := map[string]bool{
		"error_class": true, "attempts": true, "failures": true, "timeouts": true,
		"coalesced_waiters": true, "consecutive_failures": true,
		"last_latency_ms": true, "max_latency_ms": true,
	}
	for key := range record.Evidence {
		if !want[key] {
			t.Errorf("unexpected evidence key %q = %q", key, record.Evidence[key])
		}
	}
	for key := range want {
		if _, ok := record.Evidence[key]; !ok {
			t.Errorf("evidence is missing %q: %+v", key, record.Evidence)
		}
	}

	// A record is one line of JSON; scanning the whole envelope catches a leak
	// that arrived through any field, not only through Evidence.
	rendered := renderJournalRecord(t, record)
	for _, secret := range []string{journalSecretSocket, journalSecretStderr, "W:payload-secret", "kitten", "--to"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("the record leaks %q:\n%s", secret, rendered)
		}
	}
}

// A daemon whose journal is disabled installs no observer at all, so the list
// path costs nothing rather than calling a recorder that discards.
func TestDisabledJournalInstallsNoKittyListObserver(t *testing.T) {
	var recorder *lifecycleRecorder
	if observer := recorder.kittySnapshotObserver(); observer != nil {
		t.Error("a nil recorder produced an observer; the list path would pay for a discarded record")
	}
}

// The wiring itself: a real daemon start against a Kitty that will not answer
// has to leave the record behind, which is only true if the observer is
// installed before the bounded startup restore performs the first list.
func TestRunInstallsTheKittyListObserverBeforeTheFirstList(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "amx-kitty-journal-run-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	cfg := &config.Config{Defaults: config.Defaults{
		GuardDuration: 1,
		Diagnostics: config.DiagnosticsConfig{
			JournalEnabled: boolPointer(true), JournalKeepDays: 14, JournalMaxMiB: 32,
		},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := newFaultyListRunner(mustMarshalWindows(t, fakeOSWindowsWithTab(42, "W:api")))
	published := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- run(ctx, cfg, runDependencies{
			kittyRunner:             runner,
			afterStartupPublication: func() { close(published) },
		})
	}()
	select {
	case <-published:
	case err := <-runErr:
		t.Fatalf("daemon exited before publication: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not publish")
	}

	records := waitForKittySnapshotRecords(t, 1)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := ipc.NewClientWithSocket(ipc.SocketPath()).ShutdownAndWait(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down")
	}

	// A daemon lists from four subsystems on their own schedules, so the exact
	// number of failed reads before shutdown is not fixed. What is fixed is that
	// every one of them is an outage record for the same first edge per socket
	// key, never one per attempt.
	for _, record := range records {
		if record.Outcome != "error" || record.Reason != "list_failed" {
			t.Errorf("record = (%q, %q); want (error, list_failed)", record.Outcome, record.Reason)
		}
		if _, ok := record.Evidence["error_class"]; !ok {
			t.Errorf("startup record carries no error_class: %+v", record.Evidence)
		}
		if strings.Contains(renderJournalRecord(t, record), journalSecretStderr) {
			t.Errorf("the startup record leaks kitten stderr: %+v", record)
		}
	}
}
