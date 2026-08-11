package diagnostics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func testJournal(t *testing.T, options JournalOptions) (*Journal, Paths) {
	t.Helper()
	paths := PathsForRoot(filepath.Join(t.TempDir(), "state", "agent-mux"))
	journal, err := NewJournal(paths, options)
	if err != nil {
		t.Fatal(err)
	}
	return journal, paths
}

func testEnvelope(stage, request string) TraceEnvelope {
	envelope := NewEnvelope(stage)
	envelope.RequestID = request
	envelope.Outcome = "success"
	return envelope
}

func TestDefaultPathsResolveXDGAndHomeFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "state", "agent-mux")
	if paths.Root != want || paths.JournalFile != filepath.Join(want, "journal", "events.jsonl") || paths.DaemonLogFile != filepath.Join(want, "daemon", "daemon.jsonl") {
		t.Fatalf("home fallback paths = %+v, want root %s", paths, want)
	}
	xdg := filepath.Join(t.TempDir(), "xdg-state")
	t.Setenv("XDG_STATE_HOME", xdg)
	paths, err = DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if paths.Root != filepath.Join(xdg, "agent-mux") {
		t.Fatalf("XDG root = %s", paths.Root)
	}
	t.Setenv("XDG_STATE_HOME", "relative")
	if _, err := DefaultPaths(); err == nil {
		t.Fatal("relative XDG_STATE_HOME was accepted")
	}
	if err := (Paths{}).Ensure(); err == nil {
		t.Fatal("empty paths were accepted")
	}
	unsafe := PathsForRoot(filepath.Join(t.TempDir(), "root"))
	unsafe.JournalDir = t.TempDir()
	if err := unsafe.Ensure(); err == nil {
		t.Fatal("journal directory outside state root was accepted")
	}
}

func TestJournalProcessHelper(t *testing.T) {
	if os.Getenv("AMX_JOURNAL_HELPER") != "1" {
		return
	}
	root := os.Getenv("AMX_JOURNAL_ROOT")
	count, _ := strconv.Atoi(os.Getenv("AMX_JOURNAL_COUNT"))
	journal, err := NewJournal(PathsForRoot(root), JournalOptions{LockTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		envelope := testEnvelope("concurrency.process", fmt.Sprintf("%d-%d", os.Getpid(), i))
		if err := journal.Append(context.Background(), envelope); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJournalConcurrentProcessesDoNotCorruptJSONL(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{LockTimeout: 2 * time.Second})
	const processes = 4
	const recordsPerProcess = 12
	commands := make([]*exec.Cmd, processes)
	outputs := make([]bytes.Buffer, processes)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestJournalProcessHelper$")
		commands[i].Stdout = &outputs[i]
		commands[i].Stderr = &outputs[i]
		commands[i].Env = append(os.Environ(),
			"AMX_JOURNAL_HELPER=1",
			"AMX_JOURNAL_ROOT="+paths.Root,
			"AMX_JOURNAL_COUNT="+strconv.Itoa(recordsPerProcess),
		)
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("helper failed: %v\n%s", err, outputs[i].Bytes())
		}
	}
	records, err := journal.Read(context.Background(), Selector{Stages: []string{"concurrency.process"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != processes*recordsPerProcess {
		t.Fatalf("record count = %d, want %d", len(records), processes*recordsPerProcess)
	}
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		if seen[record.RequestID] {
			t.Fatalf("duplicate request ID %q", record.RequestID)
		}
		seen[record.RequestID] = true
	}
}

func TestJournalConcurrentGoroutinesDoNotCorruptJSONL(t *testing.T) {
	journal, _ := testJournal(t, JournalOptions{})
	const writers = 8
	const each = 10
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := journal.Append(context.Background(), testEnvelope("concurrency.goroutine", fmt.Sprintf("%d-%d", writer, i))); err != nil {
					errs <- err
					return
				}
			}
		}(writer)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	records, err := journal.Read(context.Background(), Selector{Stages: []string{"concurrency.goroutine"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != writers*each {
		t.Fatalf("record count = %d, want %d", len(records), writers*each)
	}
}

func TestJournalRestartAndCrashTailRecovery(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	if err := journal.Append(context.Background(), testEnvelope("restart", "before")); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(paths.JournalDir, "events.jsonl")
	file, err := os.OpenFile(current, os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"schema_version":1,"trace_id":"crash`); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	restarted, err := NewJournal(paths, JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Append(context.Background(), testEnvelope("restart", "after")); err != nil {
		t.Fatal(err)
	}
	records, err := restarted.Read(context.Background(), Selector{Stages: []string{"restart"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].RequestID != "before" || records[1].RequestID != "after" {
		t.Fatalf("recovered records = %+v", records)
	}
	quarantines, err := filepath.Glob(current + ".partial-*")
	if err != nil || len(quarantines) != 1 {
		t.Fatalf("partial quarantines = %v, err = %v", quarantines, err)
	}
	partial, err := os.ReadFile(quarantines[0])
	if err != nil || !bytes.Contains(partial, []byte(`"trace_id":"crash`)) {
		t.Fatalf("quarantined tail = %q, err = %v", partial, err)
	}
}

func TestJournalPrivatePermissionsHMACAndFailOpen(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	payload := []byte("raw secret payload")
	envelope := testEnvelope("privacy", "one")
	journal.ProtectPayload(&envelope, payload)
	if envelope.PayloadHMAC == "" || envelope.PayloadSize != int64(len(payload)) || strings.Contains(envelope.PayloadHMAC, string(payload)) {
		t.Fatalf("payload metadata = HMAC:%q size:%d", envelope.PayloadHMAC, envelope.PayloadSize)
	}
	if err := journal.Append(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Root, paths.JournalDir, paths.DaemonDir, paths.NativeDir, paths.CaptureDir, paths.SpoolDir} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != directoryMode {
			t.Fatalf("directory %s mode = %v, err = %v", path, info.Mode().Perm(), err)
		}
	}
	for _, path := range []string{paths.HMACKey, filepath.Join(paths.JournalDir, "events.jsonl"), filepath.Join(paths.JournalDir, "events.lock"), filepath.Join(paths.JournalDir, "append.lock")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != fileMode {
			t.Fatalf("file %s mode = %v, err = %v", path, info.Mode().Perm(), err)
		}
	}
	data, err := os.ReadFile(filepath.Join(paths.JournalDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, payload) {
		t.Fatal("raw payload was persisted in journal")
	}

	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(paths.JournalDir, "events.jsonl")
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, current); err != nil {
		t.Fatal(err)
	}
	if journal.TryAppend(context.Background(), testEnvelope("failure", "ignored")) {
		t.Fatal("TryAppend reported success for symlink target")
	}
	unchanged, _ := os.ReadFile(target)
	if string(unchanged) != "unchanged" {
		t.Fatalf("fail-open path modified symlink target: %q", unchanged)
	}
}

func TestDaemonLogWriterRotatesStructuredRecordsAndBootstrapIsPrivate(t *testing.T) {
	paths := PathsForRoot(filepath.Join(t.TempDir(), "state"))
	writer, err := NewDaemonLogWriter(paths, DaemonLogOptions{SegmentBytes: 500, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(writer, nil))
	for i := 0; i < 6; i++ {
		logger.Info("daemon test record", "sequence", i, "detail", strings.Repeat("x", 220))
	}
	files, _ := filepath.Glob(filepath.Join(paths.DaemonDir, "*.jsonl"))
	if len(files) < 2 {
		t.Fatalf("daemon log files = %v, want rotation", files)
	}
	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var record map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				t.Fatalf("invalid structured log %s: %v", path, err)
			}
		}
		_ = file.Close()
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
	}
	bootstrap, bootstrapPath, err := OpenBootstrapLog(paths, "launch:test/unsafe")
	if err != nil {
		t.Fatal(err)
	}
	_ = bootstrap.Close()
	if !strings.Contains(filepath.Base(bootstrapPath), "launchtestunsafe") {
		t.Fatalf("bootstrap filename was not sanitized: %s", bootstrapPath)
	}
	info, err := os.Stat(bootstrapPath)
	if err != nil || info.Mode().Perm() != fileMode {
		t.Fatalf("bootstrap mode = %v, err = %v", info.Mode().Perm(), err)
	}
}

func TestAppendDoesNotWaitOnSealedNamespaceLock(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{})
	sealed, err := os.OpenFile(filepath.Join(paths.JournalDir, "events.lock"), os.O_RDWR, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(sealed.Fd()), unix.LOCK_EX); err != nil {
		_ = sealed.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = unix.Flock(int(sealed.Fd()), unix.LOCK_UN)
		_ = sealed.Close()
	})

	if err := journal.Append(context.Background(), testEnvelope("append.split", "sealed-held")); err != nil {
		t.Fatalf("Append should not depend on events.lock: %v", err)
	}
	spools, _ := filepath.Glob(filepath.Join(paths.SpoolDir, "journal-*.jsonl"))
	if len(spools) != 0 {
		t.Fatalf("append fell back to spool while only events.lock was held: %v", spools)
	}
	data, err := os.ReadFile(paths.JournalFile)
	if err != nil || !bytes.Contains(data, []byte("sealed-held")) {
		t.Fatalf("active segment = %q, err = %v", data, err)
	}
}

func TestReadTakesSealedThenAppendLockAndReleasesOnFailure(t *testing.T) {
	journal, paths := testJournal(t, JournalOptions{LockTimeout: 50 * time.Millisecond})
	if err := journal.Append(context.Background(), testEnvelope("read.ordering", "seed")); err != nil {
		t.Fatal(err)
	}
	holdLock := func(name string) *os.File {
		t.Helper()
		file, err := os.OpenFile(filepath.Join(paths.JournalDir, name), os.O_RDWR, fileMode)
		if err != nil {
			t.Fatal(err)
		}
		if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		return file
	}
	release := func(file *os.File) {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}

	appendLock := holdLock("append.lock")
	if _, err := journal.Read(context.Background(), Selector{}); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("Read with append.lock held = %v, want %v", err, ErrLockTimeout)
	}
	// events.lock is acquired first, so the failed append.lock step must have
	// released it again; a fresh exclusive claim proves that.
	probe, err := os.OpenFile(filepath.Join(paths.JournalDir, "events.lock"), os.O_RDWR, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("events.lock still held after failed Read: %v", err)
	}
	release(probe)
	release(appendLock)

	sealedLock := holdLock("events.lock")
	if _, err := journal.Read(context.Background(), Selector{}); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("Read with events.lock held = %v, want %v", err, ErrLockTimeout)
	}
	release(sealedLock)
	records, err := journal.Read(context.Background(), Selector{Stages: []string{"read.ordering"}})
	if err != nil || len(records) != 1 {
		t.Fatalf("Read after releasing locks = %+v, err = %v", records, err)
	}
}

func TestMaintenanceLockTimeoutIsNormalized(t *testing.T) {
	normalized := normalizeJournalOptions(JournalOptions{})
	if normalized.MaintenanceLockTimeout != 2*time.Second {
		t.Fatalf("default MaintenanceLockTimeout = %v, want 2s", normalized.MaintenanceLockTimeout)
	}
	custom := normalizeJournalOptions(JournalOptions{MaintenanceLockTimeout: 250 * time.Millisecond})
	if custom.MaintenanceLockTimeout != 250*time.Millisecond {
		t.Fatalf("custom MaintenanceLockTimeout = %v, want 250ms", custom.MaintenanceLockTimeout)
	}
}
