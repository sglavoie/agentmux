package atomicfile

// The ways callers legitimately differ — fault injection part-way, symlink
// targets, parent creation, directory durability, temp naming, and error
// voice — each held to what the migrated caller relied on before.

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// halfWriter writes half of what it is given and then fails, the way a crash
// part-way through the write would leave a non-atomic replacement truncated.
type halfWriter struct {
	file *os.File
	err  error
}

func (h halfWriter) Write(p []byte) (int, error) {
	n, err := h.file.Write(p[:len(p)/2])
	if err != nil {
		return n, err
	}
	return n, h.err
}

func TestAFaultMidWriteLeavesTheOriginalIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	fault := errors.New("injected write fault")
	prev := writerFor
	t.Cleanup(func() { writerFor = prev })
	writerFor = func(f *os.File) io.Writer { return halfWriter{file: f, err: fault} }

	if err := Write(path, []byte("replacement"), 0o644); !errors.Is(err, fault) {
		t.Fatalf("Write() = %v, want the injected fault", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "original" {
		t.Fatalf("original = %q, %v; want it untouched", got, err)
	}
	assertOnlyEntries(t, filepath.Dir(path), 1)
}

func TestAChmodFaultCreatesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	fault := errors.New("injected chmod fault")
	prev := chmodFile
	t.Cleanup(func() { chmodFile = prev })
	chmodFile = func(*os.File, os.FileMode) error { return fault }

	if err := Write(path, []byte("x"), 0o644); !errors.Is(err, fault) {
		t.Fatalf("Write() = %v, want the injected fault", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("destination exists after a failed write: %v", err)
	}
	assertOnlyEntries(t, filepath.Dir(path), 0)
}

// The requested mode is exact: Chmod is not narrowed by the umask, unlike the
// mode argument of an open, so a 0644 settings file stays readable however
// restrictive the writer's umask is.
func TestTheModeIsExactWhateverTheUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })
	for _, perm := range []os.FileMode{0o600, 0o640, 0o644, 0o755} {
		path := filepath.Join(t.TempDir(), "file")
		if err := Write(path, []byte("x"), perm); err != nil {
			t.Fatalf("Write(%v) = %v", perm, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != perm {
			t.Errorf("mode = %v, want %v", info.Mode().Perm(), perm)
		}
	}
}

// Without RequireRegularTarget a symlink at the target is replaced by a file,
// never written through, so whatever it pointed at is untouched either way.
func TestASymlinkTargetIsReplacedNotFollowed(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "link")
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if err := Write(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("target mode = %v, want a regular file", info.Mode())
	}
	if got, _ := os.ReadFile(outside); string(got) != "outside" {
		t.Errorf("the symlink was written through: outside = %q", got)
	}
}

func TestRequireRegularTargetRefusesASymlinkAndLeavesItAlone(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "link")
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink: %v", err)
	}
	err := Write(path, []byte("new"), 0o600, RequireRegularTarget(), ErrorPrefix("pkg"))
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Write() = %v, want ErrNotRegular", err)
	}
	var notRegular *NotRegularError
	if !errors.As(err, &notRegular) || notRegular.Path != path {
		t.Fatalf("Write() = %v, want a NotRegularError naming %s", err, path)
	}
	if want := "pkg: " + path + " is not a regular file"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced: %v, %v", info, err)
	}
	if got, _ := os.ReadFile(outside); string(got) != "outside" {
		t.Errorf("outside = %q, want it untouched", got)
	}
	assertOnlyEntries(t, dir, 1)
}

func TestRequireRegularTargetRefusesADirectoryAndAcceptsAFileOrNothing(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Write(sub, []byte("x"), 0o600, RequireRegularTarget()); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Write(directory) = %v, want ErrNotRegular", err)
	}
	fresh := filepath.Join(dir, "fresh")
	if err := Write(fresh, []byte("1"), 0o600, RequireRegularTarget()); err != nil {
		t.Fatalf("Write(missing) = %v", err)
	}
	if err := Write(fresh, []byte("2"), 0o600, RequireRegularTarget()); err != nil {
		t.Fatalf("Write(regular) = %v", err)
	}
	if got, _ := os.ReadFile(fresh); string(got) != "2" {
		t.Errorf("contents = %q", got)
	}
}

// A temp a crashed writer left behind is not ours to adopt or to delete: it
// could as well be a concurrent writer's in-flight temp. The write succeeds
// beside it, and its own temp is the only one it ever removes.
func TestACrashLeftoverTempIsNeitherAdoptedNorRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	leftover := filepath.Join(dir, ".settings.json.123456.tmp")
	if err := os.WriteFile(leftover, []byte("half a write"), 0o666); err != nil {
		t.Fatal(err)
	}
	legacy := path + ".tmp"
	if err := os.WriteFile(legacy, []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	for file, want := range map[string]string{leftover: "half a write", legacy: "unrelated", path: "new"} {
		if got, err := os.ReadFile(file); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", filepath.Base(file), got, err, want)
		}
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v; a leftover's mode leaked into the replacement", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Errorf("directory = %v, want the leftover, the legacy sibling, and the file", entries)
	}
}

func TestWriteStreamAbandonsTheReplacementWhenFillFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	fault := errors.New("encoder failed")
	err := WriteStream(path, 0o600, func(w io.Writer) error {
		if _, err := w.Write([]byte("partial")); err != nil {
			return err
		}
		return fault
	})
	if !errors.Is(err, fault) {
		t.Fatalf("WriteStream() = %v, want the fill error", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "original" {
		t.Fatalf("original = %q, %v; want it untouched", got, err)
	}
	assertOnlyEntries(t, filepath.Dir(path), 1)
}

func TestWriteStreamWritesWhatFillProduces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out")
	err := WriteStream(path, 0o640, func(w io.Writer) error {
		_, err := io.WriteString(w, "streamed")
		return err
	})
	if err != nil {
		t.Fatalf("WriteStream() = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "streamed" {
		t.Errorf("contents = %q", got)
	}
}

func TestNoCreateParentsFailsOnAMissingParent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "missing")
	err := Write(filepath.Join(parent, "file"), []byte("x"), 0o600, NoCreateParents())
	if err == nil {
		t.Fatal("Write() = nil, want a failure for the missing parent")
	}
	if _, statErr := os.Stat(parent); !os.IsNotExist(statErr) {
		t.Errorf("the parent was created: %v", statErr)
	}
}

func TestParentModeAppliesToTheDirectoriesItCreates(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })
	parent := filepath.Join(t.TempDir(), "private")
	if err := Write(filepath.Join(parent, "file"), []byte("x"), 0o600, ParentMode(0o700)); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("parent mode = %v, want 0700", info.Mode().Perm())
	}
}

func TestTempPatternNamesTheStagingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	var seen []string
	prev := writerFor
	t.Cleanup(func() { writerFor = prev })
	writerFor = func(f *os.File) io.Writer {
		seen = append(seen, filepath.Base(f.Name()))
		return f
	}
	if err := Write(path, []byte("x"), 0o600, TempPattern("staging-*.part")); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	if err := Write(path, []byte("y"), 0o600); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("temps = %v", seen)
	}
	if !strings.HasPrefix(seen[0], "staging-") || !strings.HasSuffix(seen[0], ".part") {
		t.Errorf("custom temp = %q, want staging-*.part", seen[0])
	}
	if !strings.HasPrefix(seen[1], ".file.") || !strings.HasSuffix(seen[1], ".tmp") {
		t.Errorf("default temp = %q, want .file.*.tmp", seen[1])
	}
	assertOnlyEntries(t, dir, 1)
}

// The directory fsync runs after the rename, on the parent. Strict surfaces
// its failure, best effort swallows it, none never attempts it.
func TestTheDirectorySyncModes(t *testing.T) {
	fault := errors.New("injected dir sync fault")
	for _, tc := range []struct {
		name     string
		opts     []Option
		wantCall bool
		wantErr  bool
	}{
		{name: "default is strict", wantCall: true, wantErr: true},
		{name: "strict", opts: []Option{WithDirSync(SyncDirStrict)}, wantCall: true, wantErr: true},
		{name: "best effort", opts: []Option{WithDirSync(SyncDirBestEffort)}, wantCall: true},
		{name: "none", opts: []Option{WithDirSync(SyncDirNone)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "file")
			var synced []string
			prev := syncDir
			t.Cleanup(func() { syncDir = prev })
			syncDir = func(d string) error {
				synced = append(synced, d)
				if _, err := os.Stat(path); err != nil {
					t.Errorf("dir synced before the rename landed: %v", err)
				}
				return fault
			}
			err := Write(path, []byte("x"), 0o600, tc.opts...)
			if called := len(synced) > 0; called != tc.wantCall {
				t.Errorf("dir sync called = %v, want %v", called, tc.wantCall)
			}
			if tc.wantCall && len(synced) > 0 && synced[0] != dir {
				t.Errorf("synced %q, want the parent %q", synced[0], dir)
			}
			if gotErr := errors.Is(err, fault); gotErr != tc.wantErr {
				t.Errorf("Write() = %v, want fault surfaced = %v", err, tc.wantErr)
			}
			// The rename has happened either way: the new contents are live.
			if got, _ := os.ReadFile(path); string(got) != "x" {
				t.Errorf("contents = %q", got)
			}
		})
	}
}

func TestTheRealDirectorySyncSucceeds(t *testing.T) {
	if err := syncDir(t.TempDir()); err != nil {
		t.Fatalf("syncDir() = %v", err)
	}
	if err := syncDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("syncDir(missing) = nil, want an error")
	}
}

func TestErrorPrefixWrapsEveryFailureAndKeepsTheCause(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	fault := errors.New("injected chmod fault")
	prev := chmodFile
	t.Cleanup(func() { chmodFile = prev })
	chmodFile = func(*os.File, os.FileMode) error { return fault }
	err := Write(path, []byte("x"), 0o600, ErrorPrefix("handoff: writing artifact"))
	if !errors.Is(err, fault) {
		t.Fatalf("Write() = %v, want the cause kept", err)
	}
	if !strings.HasPrefix(err.Error(), "handoff: writing artifact: ") {
		t.Errorf("error = %q, want the prefix", err)
	}
	chmodFile = prev
	if err := Write(path, []byte("x"), 0o600, ErrorPrefix("unused")); err != nil {
		t.Errorf("a successful write returned %v", err)
	}
}

func TestReplaceAcceptsOptions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Replace(path, []byte("x"), 0o644, RequireRegularTarget()); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Replace() = %v, want ErrNotRegular", err)
	}
}

// assertOnlyEntries fails when dir holds a staging temp or a different number
// of entries than want.
func assertOnlyEntries(t *testing.T, dir string, want int) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("a temporary file survived: %s", entry.Name())
		}
	}
	if len(entries) != want {
		t.Errorf("directory has %d entries, want %d", len(entries), want)
	}
}
