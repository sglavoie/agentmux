package aslbridge

// Where the daemon looks for `asl`, and what it does when the answer is
// "nowhere yet". Both halves exist because the daemon does not inherit the
// operator's shell: a lookup that only consults PATH is wrong on a stock macOS
// login agent, and a launch failure that is permanent in required mode must not
// be permanent in auto mode, where an install can fix it without a restart.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withExecutableDir points the sibling lookup at dir for one test.
func withExecutableDir(t *testing.T, dir string, err error) {
	t.Helper()
	previous := executableDir
	executableDir = func() (string, error) { return dir, err }
	t.Cleanup(func() { executableDir = previous })
}

// withLookPath replaces PATH resolution for one test.
func withLookPath(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	previous := lookPath
	lookPath = fn
	t.Cleanup(func() { lookPath = previous })
}

// writeExecutable creates a runnable file and returns its path.
func writeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// The whole reason this package does its own resolution: the binary beside amx
// wins over anything the daemon happened to inherit, because that is the one
// location it can name without consulting an environment it did not choose.
func TestABareNameResolvesBesideTheAmxBinaryBeforePath(t *testing.T) {
	sibling := t.TempDir()
	onPath := t.TempDir()
	want := writeExecutable(t, sibling, "asl")
	writeExecutable(t, onPath, "asl")

	withExecutableDir(t, sibling, nil)
	withLookPath(t, func(string) (string, error) { return filepath.Join(onPath, "asl"), nil })

	resolution, err := ResolveCommand("asl")
	if err != nil {
		t.Fatalf("ResolveCommand() = %v", err)
	}
	if resolution.Executable != want {
		t.Errorf("executable = %s, want the sibling %s", resolution.Executable, want)
	}
	if resolution.Source != SourceSibling {
		t.Errorf("source = %q, want %q", resolution.Source, SourceSibling)
	}
}

// A split install is legitimate, so PATH is a fallback rather than an error.
func TestABareNameFallsBackToPathWhenNoSiblingExists(t *testing.T) {
	onPath := t.TempDir()
	want := writeExecutable(t, onPath, "asl")

	withExecutableDir(t, t.TempDir(), nil)
	withLookPath(t, func(string) (string, error) { return want, nil })

	resolution, err := ResolveCommand("asl")
	if err != nil {
		t.Fatalf("ResolveCommand() = %v", err)
	}
	if resolution.Executable != want || resolution.Source != SourcePath {
		t.Errorf("resolution = %+v, want %s from %q", resolution, want, SourcePath)
	}
}

// A file beside amx that cannot be executed is not a bridge. Falling through to
// PATH rather than failing is what keeps a stray non-executable `asl` in a
// stow target from masking a working install.
func TestANonExecutableSiblingFallsThroughToPath(t *testing.T) {
	sibling := t.TempDir()
	if err := os.WriteFile(filepath.Join(sibling, "asl"), []byte("not runnable"), 0o644); err != nil {
		t.Fatalf("writing sibling: %v", err)
	}
	want := writeExecutable(t, t.TempDir(), "asl")

	withExecutableDir(t, sibling, nil)
	withLookPath(t, func(string) (string, error) { return want, nil })

	resolution, err := ResolveCommand("asl")
	if err != nil {
		t.Fatalf("ResolveCommand() = %v", err)
	}
	if resolution.Source != SourcePath {
		t.Errorf("source = %q, want the non-executable sibling skipped", resolution.Source)
	}
}

// A directory named `asl` is the same non-answer as an absent one.
func TestADirectorySiblingIsNotAnExecutable(t *testing.T) {
	sibling := t.TempDir()
	if err := os.Mkdir(filepath.Join(sibling, "asl"), 0o755); err != nil {
		t.Fatalf("creating directory: %v", err)
	}
	withExecutableDir(t, sibling, nil)
	withLookPath(t, func(string) (string, error) { return "", exec.ErrNotFound })

	if _, err := ResolveCommand("asl"); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("ResolveCommand() = %v, want exec.ErrNotFound", err)
	}
}

// A configured path is taken at its word: no directory is consulted, so the
// pairing `amx config asl-sync` persists cannot be changed by an install
// landing somewhere else later.
func TestAConfiguredPathSkipsEveryLookup(t *testing.T) {
	configured := writeExecutable(t, t.TempDir(), "asl")
	withExecutableDir(t, "", errors.New("the running binary must not be consulted"))
	withLookPath(t, func(string) (string, error) {
		t.Error("PATH was consulted for a path-bearing command")
		return "", exec.ErrNotFound
	})

	resolution, err := ResolveCommand(configured)
	if err != nil {
		t.Fatalf("ResolveCommand() = %v", err)
	}
	if resolution.Executable != configured || resolution.Source != SourceConfigured {
		t.Errorf("resolution = %+v, want %s from %q", resolution, configured, SourceConfigured)
	}
}

// A relative path is still a path: it is made absolute so the value persisted
// in config and reported in status does not depend on a working directory.
func TestARelativePathIsMadeAbsolute(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "asl")
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	resolution, err := ResolveCommand("./asl")
	if err != nil {
		t.Fatalf("ResolveCommand() = %v", err)
	}
	if !filepath.IsAbs(resolution.Executable) {
		t.Errorf("executable = %s, want an absolute path", resolution.Executable)
	}
}

// Every not-found spelling reaches the manager as the same condition, because
// the manager's permanent-failure rule keys on exec.ErrNotFound alone.
func TestEveryNotFoundCaseWrapsErrNotFound(t *testing.T) {
	withExecutableDir(t, t.TempDir(), nil)
	withLookPath(t, func(string) (string, error) { return "", exec.ErrNotFound })

	cases := map[string]string{
		"empty":            "",
		"blank":            "   ",
		"absent bare name": "asl",
		"absent path":      filepath.Join(t.TempDir(), "nowhere", "asl"),
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ResolveCommand(command); !errors.Is(err, exec.ErrNotFound) {
				t.Errorf("ResolveCommand(%q) = %v, want exec.ErrNotFound", command, err)
			}
		})
	}
}

// The diagnostic has to say where the daemon looked. "not found in $PATH" sends
// an operator to their shell profile for a problem that is not there.
func TestTheNotFoundMessageNamesTheSiblingDirectory(t *testing.T) {
	sibling := t.TempDir()
	withExecutableDir(t, sibling, nil)
	withLookPath(t, func(string) (string, error) { return "", exec.ErrNotFound })

	_, err := ResolveCommand("asl")
	if err == nil {
		t.Fatal("ResolveCommand() succeeded for an absent command")
	}
	if !strings.Contains(err.Error(), sibling) {
		t.Errorf("error %q does not name the sibling directory %s", err, sibling)
	}
}

// An install that lands after the daemon started must be picked up without the
// operator knowing they were supposed to restart it, so auto mode keeps
// probing on a slow schedule instead of retiring the integration.
func TestAutoModeReprobesAMissingExecutable(t *testing.T) {
	var (
		mu      sync.Mutex
		delays  []time.Duration
		probes  atomic.Int32
		present atomic.Bool
	)
	done := make(chan struct{})
	launcher := func(ctx context.Context) (*Child, error) {
		if !present.Load() {
			probes.Add(1)
			return nil, exec.ErrNotFound
		}
		fake := newFakeBridge(t, serveSnapshots)
		return fake.launcher()(ctx)
	}
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl", Launcher: launcher,
		Sleep: func(_ context.Context, d time.Duration) bool {
			mu.Lock()
			delays = append(delays, d)
			count := len(delays)
			mu.Unlock()
			if count == 3 {
				// The install lands between two probes, exactly as it would
				// for an operator running an installer while amx is up.
				present.Store(true)
				close(done)
			}
			return true
		},
	})
	<-done
	waitFor(t, "the bridge to come up once installed", func() bool { return m.Status().Available })

	mu.Lock()
	got := append([]time.Duration(nil), delays...)
	mu.Unlock()
	want := []time.Duration{5 * time.Second, 15 * time.Second, time.Minute}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("probe delays = %v, want the missing-executable schedule %v", got, want)
		}
	}
	if m.Status().RestartCount != 0 {
		t.Errorf("restart count = %d; a missing executable is not a crash", m.Status().RestartCount)
	}
}

// The probe schedule is bounded: an uninstalled ASL costs one wakeup every five
// minutes forever, not a tightening loop and not an unbounded one.
func TestMissingProbeBackoffSaturatesAtTheCeiling(t *testing.T) {
	if got := missingProbeBackoffFor(-1); got != 5*time.Second {
		t.Errorf("missingProbeBackoffFor(-1) = %s, want the first step", got)
	}
	if got := missingProbeBackoffFor(len(missingProbeBackoff) + 10); got != MissingProbeCeiling {
		t.Errorf("saturated probe delay = %s, want %s", got, MissingProbeCeiling)
	}
	if MissingProbeCeiling != 5*time.Minute {
		t.Errorf("MissingProbeCeiling = %s, want 5m", MissingProbeCeiling)
	}
}

// Required mode is the opposite promise: the operator declared the integration
// mandatory, so they are owed one stable failure that `amx doctor --strict`
// reports rather than a retry loop that buries it.
func TestRequiredModeStopsProbingAMissingExecutable(t *testing.T) {
	var probes atomic.Int32
	m := startManager(t, Options{
		Mode: ModeRequired, Command: "asl",
		Launcher: func(context.Context) (*Child, error) {
			probes.Add(1)
			return nil, exec.ErrNotFound
		},
		Sleep: func(context.Context, time.Duration) bool {
			t.Error("required mode waited to re-probe a missing executable")
			return false
		},
	})
	waitFor(t, "the integration to be reported unavailable", func() bool {
		return !m.Status().Available && m.Status().Reason != ""
	})
	time.Sleep(100 * time.Millisecond)
	if got := probes.Load(); got != 1 {
		t.Errorf("required mode made %d launch attempts, want exactly 1", got)
	}
}

// Status reports how the executable was found, because "on the daemon's PATH"
// and "configured absolutely" name the same file today and different files
// after the next reboot.
func TestStatusReportsHowTheExecutableWasFound(t *testing.T) {
	fake := newFakeBridge(t, serveSnapshots)
	m := startManager(t, Options{
		Mode: ModeAuto, Command: "asl",
		Launcher: func(ctx context.Context) (*Child, error) {
			child, err := fake.launcher()(ctx)
			if err != nil {
				return nil, err
			}
			child.Path = "/opt/asl/bin/asl"
			child.Source = SourceSibling
			return child, nil
		},
	})
	waitFor(t, "connection", func() bool { return m.Status().Available })

	status := m.Status()
	if status.Executable != "/opt/asl/bin/asl" || status.ExecutableSource != SourceSibling {
		t.Errorf("status executable = %q from %q, want the launcher's resolution",
			status.Executable, status.ExecutableSource)
	}
}
