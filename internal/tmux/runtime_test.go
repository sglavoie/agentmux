package tmux

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTmuxUnexpectedServerExitIsReportedAsAbsent(t *testing.T) {
	err := &CommandError{Stderr: "server exited unexpectedly"}
	if !errors.Is(err, ErrServerNotRunning) {
		t.Fatalf("tmux exited while accepting a client: %v, want ErrServerNotRunning", err)
	}
}

// fakeBundle writes a bundled runtime: an executable, its VERSION, and
// checksums over both the executable and a license file.
func fakeBundle(t *testing.T, version, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tmux")
	write := func(rel, content string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("bin/tmux", body, 0o755)
	write("licenses/tmux", "ISC", 0o644)
	write("VERSION", "tmux="+version+"\nlibevent=2.1.13-stable\n", 0o644)
	sum := func(content string) string {
		digest := sha256.Sum256([]byte(content))
		return hex.EncodeToString(digest[:])
	}
	write("SHA256SUMS", sum(body)+"  bin/tmux\n"+sum("ISC")+"  licenses/tmux\n", 0o644)
	return dir
}

func fakeExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// isolated is a resolution that reads nothing of the machine running the
// test: no environment, no PATH, no executable-relative bundle.
func isolated(t *testing.T) RuntimeOptions {
	t.Helper()
	return RuntimeOptions{
		Getenv:     func(string) string { return "" },
		Executable: filepath.Join(t.TempDir(), "bin", "amx"),
		Root:       filepath.Join(t.TempDir(), "runtime"),
		LookPath:   func(string) (string, error) { return "", errors.New("not found") },
		Version:    func(string) (string, error) { return "3.7c", nil },
	}
}

func candidate(t *testing.T, candidates []Candidate, source RuntimeSource) Candidate {
	t.Helper()
	for _, c := range candidates {
		if c.Source == source {
			return c
		}
	}
	t.Fatalf("no %s candidate in %+v", source, candidates)
	return Candidate{}
}

func TestSupportedVersion(t *testing.T) {
	for version, want := range map[string]bool{
		"3.5": true, "3.5a": true, "3.7c": true, "4.0": true, "next-3.8": true, "3.10": true,
		"3.4": false, "3.3a": false, "2.9": false, "": false, "master": false, "openbsd-7.6": false, "3": false,
	} {
		if got := SupportedVersion(version); got != want {
			t.Errorf("SupportedVersion(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestBundledRuntimeIsVerifiedAndInstalledOutsideTheBundle(t *testing.T) {
	opts := isolated(t)
	bundle := fakeBundle(t, "3.7c", "pinned tmux")
	opts.Getenv = func(key string) string {
		if key == RuntimeEnv {
			return bundle
		}
		return ""
	}

	rt, _, err := ResolveRuntime(opts)
	if err != nil {
		t.Fatalf("ResolveRuntime: %v", err)
	}
	if rt.Source != SourceBundled || !rt.Verified || rt.Version != "3.7c" {
		t.Fatalf("runtime = %+v, want the verified bundled 3.7c", rt)
	}
	if strings.HasPrefix(rt.Binary, bundle) || !strings.HasPrefix(rt.Binary, opts.Root+string(filepath.Separator)) {
		t.Fatalf("the runtime runs from %s; it must be the copy under %s, not the bundle, so replacing the app never touches a running server's executable", rt.Binary, opts.Root)
	}
	if body, err := os.ReadFile(rt.Binary); err != nil || string(body) != "pinned tmux" {
		t.Fatalf("installed executable = %q, %v", body, err)
	}
	if info, err := os.Stat(rt.Binary); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("installed executable is not executable: %v, %v", info, err)
	}

	// Removing the whole bundle, as an uninstall or an update does, leaves
	// the installed copy usable by an amx started without the app.
	if err := os.RemoveAll(bundle); err != nil {
		t.Fatal(err)
	}
	opts.Getenv = func(string) string { return "" }
	again, candidates, err := ResolveRuntime(opts)
	if err != nil {
		t.Fatalf("ResolveRuntime without the bundle: %v\n%+v", err, candidates)
	}
	if again.Binary != rt.Binary || again.Source != SourceInstalled || !again.Verified {
		t.Fatalf("runtime without the bundle = %+v, want the installed copy %s", again, rt.Binary)
	}
}

func TestBundledRuntimeBesideTheExecutableNeedsNoEnvironment(t *testing.T) {
	opts := isolated(t)
	bundle := fakeBundle(t, "3.7c", "pinned tmux")
	resources := filepath.Dir(bundle)
	if err := os.MkdirAll(filepath.Join(resources, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts.Executable = filepath.Join(resources, "bin", "amx")

	rt, _, err := ResolveRuntime(opts)
	if err != nil {
		t.Fatalf("ResolveRuntime: %v", err)
	}
	if rt.Source != SourceBundled {
		t.Fatalf("runtime = %+v, want the bundle beside the executable", rt)
	}
}

// The app's bundles list VERSION among the checksums; it is installed once.
func TestABundleThatChecksumsItsVersionInstalls(t *testing.T) {
	opts := isolated(t)
	bundle := fakeBundle(t, "3.7c", "pinned tmux")
	version, err := os.ReadFile(filepath.Join(bundle, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(version)
	sums, err := os.OpenFile(filepath.Join(bundle, "SHA256SUMS"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sums.WriteString(hex.EncodeToString(digest[:]) + "  VERSION\n"); err != nil {
		t.Fatal(err)
	}
	_ = sums.Close()
	opts.Getenv = func(key string) string {
		if key == RuntimeEnv {
			return bundle
		}
		return ""
	}

	rt, candidates, err := ResolveRuntime(opts)
	if err != nil {
		t.Fatalf("ResolveRuntime: %v\n%+v", err, candidates)
	}
	if got, _ := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(rt.Binary)), "VERSION")); string(got) != string(version) {
		t.Fatalf("installed VERSION = %q", got)
	}

	// A VERSION altered after signing no longer names what was verified.
	if err := os.WriteFile(filepath.Join(bundle, "VERSION"), []byte("tmux=9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts.Root = filepath.Join(t.TempDir(), "runtime")
	if _, _, err := ResolveRuntime(opts); !IsNoRuntime(err) {
		t.Fatalf("a bundle with an altered VERSION resolved: %v", err)
	}
}

func TestTamperedBundleIsRefused(t *testing.T) {
	opts := isolated(t)
	bundle := fakeBundle(t, "3.7c", "pinned tmux")
	if err := os.WriteFile(filepath.Join(bundle, "bin", "tmux"), []byte("something else"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts.Getenv = func(key string) string {
		if key == RuntimeEnv {
			return bundle
		}
		return ""
	}

	_, candidates, err := ResolveRuntime(opts)
	if !IsNoRuntime(err) {
		t.Fatalf("ResolveRuntime = %v, want a NoRuntimeError", err)
	}
	if problem := candidate(t, candidates, SourceBundled).Problem; !strings.Contains(problem, "bin/tmux does not match its checksum") {
		t.Fatalf("bundled candidate problem = %q", problem)
	}
	if entries, _ := os.ReadDir(opts.Root); len(entries) != 0 {
		t.Fatalf("a refused bundle left files under the runtime root: %v", entries)
	}
}

func TestTamperedInstalledCopyIsReplacedFromTheBundle(t *testing.T) {
	opts := isolated(t)
	bundle := fakeBundle(t, "3.7c", "pinned tmux")
	opts.Getenv = func(key string) string {
		if key == RuntimeEnv {
			return bundle
		}
		return ""
	}
	rt, _, err := ResolveRuntime(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(rt.Binary, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.Binary, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}

	healed, _, err := ResolveRuntime(opts)
	if err != nil {
		t.Fatalf("ResolveRuntime over a tampered copy: %v", err)
	}
	if body, _ := os.ReadFile(healed.Binary); string(body) != "pinned tmux" || !healed.Verified {
		t.Fatalf("the tampered copy was used as it was: %q, %+v", body, healed)
	}
	// The altered directory was renamed aside rather than written over, so a
	// server still running from it keeps its executable.
	retired, _ := filepath.Glob(filepath.Dir(filepath.Dir(healed.Binary)) + ".retired-*")
	if len(retired) != 1 {
		t.Fatalf("retired copies = %v, want the one that was replaced", retired)
	}
}

func TestChecksumsMayNotLeaveTheRuntime(t *testing.T) {
	sum := strings.Repeat("a", 64)
	for _, data := range []string{
		sum + "  ../bin/tmux\n" + sum + "  bin/tmux\n",
		sum + "  /bin/tmux\n" + sum + "  bin/tmux\n",
		sum + "  licenses/tmux\n",
		"short  bin/tmux\n",
		sum + " bin/tmux\n",
	} {
		if _, err := parseChecksums([]byte(data)); err == nil {
			t.Errorf("parseChecksums accepted %q", data)
		}
	}
}

func TestPathTmuxServesAStandaloneCommandLine(t *testing.T) {
	opts := isolated(t)
	installed := fakeExecutable(t)
	opts.LookPath = func(string) (string, error) { return installed, nil }

	rt, _, err := ResolveRuntime(opts)
	if err != nil {
		t.Fatalf("ResolveRuntime: %v", err)
	}
	if rt.Binary != installed || rt.Source != SourcePath || rt.Verified {
		t.Fatalf("runtime = %+v, want the unverified tmux on PATH", rt)
	}
}

func TestOldPathTmuxIsRefusedWithWhatToDo(t *testing.T) {
	opts := isolated(t)
	installed := fakeExecutable(t)
	opts.LookPath = func(string) (string, error) { return installed, nil }
	opts.Version = func(string) (string, error) { return "3.3a", nil }

	_, _, err := ResolveRuntime(opts)
	if !IsNoRuntime(err) {
		t.Fatalf("ResolveRuntime = %v, want a NoRuntimeError", err)
	}
	for _, want := range []string{"tmux 3.3a is older than 3.5", "bundled runtime: not present", "brew install tmux", "[defaults.tmux] path", "Agent Mux.app"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say %q:\n%v", want, err)
		}
	}
}

func TestMissingTmuxNamesEveryPlaceTried(t *testing.T) {
	_, candidates, err := ResolveRuntime(isolated(t))
	if !IsNoRuntime(err) {
		t.Fatalf("ResolveRuntime = %v, want a NoRuntimeError", err)
	}
	if len(candidates) != 2 || candidates[0].Source != SourceBundled || candidates[1].Source != SourcePath {
		t.Fatalf("candidates = %+v", candidates)
	}
	if !strings.Contains(err.Error(), "PATH: tmux not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestAnExplicitChoiceIsNeverSilentlyReplaced(t *testing.T) {
	onPath := fakeExecutable(t)
	for name, configure := range map[string]func(*RuntimeOptions){
		"configuration": func(o *RuntimeOptions) { o.Configured = "/nonexistent/tmux" },
		"environment": func(o *RuntimeOptions) {
			o.Getenv = func(key string) string {
				if key == BinaryEnv {
					return "/nonexistent/tmux"
				}
				return ""
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			opts := isolated(t)
			opts.LookPath = func(string) (string, error) { return onPath, nil }
			configure(&opts)
			_, candidates, err := ResolveRuntime(opts)
			if !IsNoRuntime(err) {
				t.Fatalf("ResolveRuntime = %v: a mistyped explicit tmux must fail, not run the one on PATH", err)
			}
			if len(candidates) != 1 || candidates[0].Problem != "not an executable file" {
				t.Fatalf("candidates = %+v", candidates)
			}
		})
	}
}

func TestConfiguredTmuxWinsOverTheBundle(t *testing.T) {
	opts := isolated(t)
	bundle := fakeBundle(t, "3.7c", "pinned tmux")
	opts.Getenv = func(key string) string {
		if key == RuntimeEnv {
			return bundle
		}
		return ""
	}
	opts.Configured = fakeExecutable(t)

	rt, _, err := ResolveRuntime(opts)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Source != SourceConfigured || rt.Binary != opts.Configured {
		t.Fatalf("runtime = %+v, want the configured one", rt)
	}
}

// shortDir is a private directory with a path short enough for a socket.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "amx-rt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestARunningServerKeepsTheTmuxItWasStartedWith(t *testing.T) {
	dir := shortDir(t)
	socket := filepath.Join(dir, "tmux.sock")
	started := fakeExecutable(t)
	if err := os.WriteFile(filepath.Join(dir, pinName), []byte(started+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// What a fresh resolution would pick now: another tmux, as after an app
	// update that ships a new runtime.
	updated := fakeExecutable(t)
	t.Setenv(BinaryEnv, updated)
	t.Cleanup(func() { binaryVersionForTest = nil })
	binaryVersionForTest = func(string) (string, error) { return "3.7c", nil }

	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	got, err := serverBinary(dir, socket)
	if err != nil {
		t.Fatalf("serverBinary: %v", err)
	}
	if got != started {
		t.Fatalf("a live server was handed %s; it must keep %s", got, started)
	}
	if PinnedBinary(dir) != started {
		t.Fatalf("the record of a live server was rewritten to %s", PinnedBinary(dir))
	}

	// Once the server is gone the next one starts with the current runtime.
	_ = listener.Close()
	_ = os.Remove(socket)
	got, err = serverBinary(dir, socket)
	if err != nil {
		t.Fatalf("serverBinary after the server ended: %v", err)
	}
	if got != updated || PinnedBinary(dir) != updated {
		t.Fatalf("after the server ended: binary %s, record %s; want %s", got, PinnedBinary(dir), updated)
	}
}

func TestAStaleSocketIsNotARunningServer(t *testing.T) {
	dir := shortDir(t)
	socket := filepath.Join(dir, "tmux.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if !Listening(socket) {
		t.Fatal("a listening socket is not reported as running")
	}
	// Closing a Go listener removes the socket file; a killed tmux leaves it.
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = listener.Close()
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("the stale socket is gone: %v", err)
	}
	if Listening(socket) {
		t.Fatal("a stale socket is reported as a running server")
	}
}

func TestARecordThatNamesNothingIsIgnored(t *testing.T) {
	dir := shortDir(t)
	for _, content := range []string{"", "tmux\n", "/nonexistent/tmux\n", dir + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, pinName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := PinnedBinary(dir); got != "" {
			t.Errorf("PinnedBinary with record %q = %q, want empty", content, got)
		}
	}
}
