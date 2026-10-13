package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/backend/backendtest"
)

// Every test here runs a real tmux server on a private socket. HOME and
// XDG_CONFIG_HOME point at a personal configuration that would visibly change
// the server if tmux ever read it, and TMUX_TMPDIR at a directory that must
// stay empty, so the whole suite doubles as an isolation check.

const personalMarker = "amx-personal-config-leaked"

func requireTmux(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed; the private tmux backend needs it")
	}
	return path
}

type fixture struct {
	server  *Server
	backend *Backend
	dir     string
	home    string
	tmpdir  string
}

// newFixture starts nothing; the first launch starts the server.
func newFixture(t *testing.T, opts Options) *fixture {
	t.Helper()
	requireTmux(t)
	home := t.TempDir()
	personal := strings.Join([]string{
		"set -g @amx_personal " + personalMarker,
		"set -g prefix C-a",
		"set -g remain-on-exit off",
		"set -g history-limit 7",
		"set-environment -g AMX_PERSONAL_ENV " + personalMarker,
	}, "\n") + "\n"
	writeFile(t, filepath.Join(home, ".tmux.conf"), personal)
	writeFile(t, filepath.Join(home, ".config", "tmux", "tmux.conf"), personal)
	tmuxTmp := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("TMUX_TMPDIR", tmuxTmp)

	// Socket paths are length-limited, so the server lives under a short
	// directory rather than t.TempDir().
	dir, err := os.MkdirTemp("", "amxtmux")
	if err != nil {
		t.Fatal(err)
	}
	opts.Dir = filepath.Join(dir, "srv")
	server, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Kill(); err != nil {
			t.Errorf("kill server: %v", err)
		}
		_ = os.RemoveAll(dir)
	})
	return &fixture{server: server, backend: NewBackend(server), dir: dir, home: home, tmpdir: tmuxTmp}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func echoArgv(marker string) []string {
	return []string{"/bin/sh", "-c", `printf '%s\n' "$1"; exec sleep 600`, "amx-echo", marker}
}

func TestBackendContract(t *testing.T) {
	backendtest.Run(t, func(t *testing.T) backendtest.Harness {
		f := newFixture(t, Options{})
		return backendtest.Harness{
			Backend:   f.backend,
			Directory: t.TempDir(),
			EchoArgv:  echoArgv,
			Timeout:   5 * time.Second,
		}
	})
}

// waitFor polls Discover until the pane tagged id satisfies ok.
func (f *fixture) waitFor(t *testing.T, id string, ok func(backend.Pane) bool) backend.Pane {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		panes, err := f.backend.Discover()
		if err == nil {
			for _, p := range panes {
				if p.Vars["amx_launch_id"] == id && ok(p) {
					return p
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s never reached the expected state; err=%v panes=%+v", id, err, panes)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *fixture) launch(t *testing.T, id string, argv []string, env map[string]string) Launched {
	t.Helper()
	l, err := f.backend.LaunchTarget(backend.LaunchSpec{
		Title:     "title " + id,
		Directory: t.TempDir(),
		Env:       env,
		Vars:      map[string]string{"amx_launch_id": id},
		Argv:      argv,
	})
	if err != nil {
		t.Fatalf("LaunchTarget(%s): %v", id, err)
	}
	return l
}

func (f *fixture) tmux(t *testing.T, args ...string) string {
	t.Helper()
	out, err := f.server.run(args)
	if err != nil {
		t.Fatalf("tmux %v: %v", args, err)
	}
	return strings.TrimSuffix(out, "\n")
}

func waitFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && strings.HasSuffix(string(data), "\x00END\x00") {
			return strings.TrimSuffix(string(data), "\x00END\x00")
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never completed (err=%v data=%q)", path, err, data)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLaunchTargetMatchesDiscovery(t *testing.T) {
	f := newFixture(t, Options{})
	l := f.launch(t, "target", echoArgv("target-marker"), nil)
	p := f.waitFor(t, "target", func(backend.Pane) bool { return true })
	if p.Ref != l.Pane || p.Group != l.Group {
		t.Fatalf("launch reported %v/%v, discovery %v/%v", l.Group, l.Pane, p.Group, p.Ref)
	}
	if len(p.Foreground) != 1 || p.Foreground[0].PID != l.PID || p.Exit != nil {
		t.Fatalf("foreground = %+v exit=%+v, want pid %d running", p.Foreground, p.Exit, l.PID)
	}
	inst, err := f.server.Instance()
	if err != nil {
		t.Fatal(err)
	}
	if inst != l.Instance || inst.Validate() != nil || inst.Address != f.server.Socket() {
		t.Fatalf("Instance() = %+v, launch reported %+v", inst, l.Instance)
	}
	// One session holds exactly one pane.
	if got := f.tmux(t, "list-panes", "-s", "-t", l.Group.ID, "-F", "#{pane_id}"); got != l.Pane.ID {
		t.Fatalf("session %s panes = %q, want only %s", l.Group.ID, got, l.Pane.ID)
	}
	t.Logf("observed: launch %s/%s pid %d on instance %s matches discovery", l.Group.ID, l.Pane.ID, l.PID, inst.Generation)
}

func TestPersonalConfigIsolation(t *testing.T) {
	f := newFixture(t, Options{})
	t.Setenv("TMUX", "/tmp/parent-tmux,1,0")
	t.Setenv("TMUX_PANE", "%99")
	t.Setenv("KITTY_WINDOW_ID", "42")
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty-parent")
	t.Setenv("AMX_RUNTIME_SESSION_ID", "rs-00000000000000000000000000000000")
	out := filepath.Join(f.dir, "env.out")
	f.launch(t, "isolated", []string{"/bin/sh", "-c", `env > "$1"; printf '\0END\0' >> "$1"; exec sleep 600`, "sh", out}, nil)
	env := waitFile(t, out)

	if got := f.tmux(t, "show-options", "-gqv", "@amx_personal"); got != "" {
		t.Errorf("personal option leaked: @amx_personal=%q", got)
	}
	want := map[string]string{"prefix": "None", "remain-on-exit": "on", "history-limit": "50000"}
	for opt, v := range want {
		if got := f.tmux(t, "show-options", "-gv", opt); got != v {
			t.Errorf("option %s = %q, want AgentMux's %q", opt, got, v)
		}
	}
	if got := f.tmux(t, "show-environment", "-g"); strings.Contains(got, personalMarker) {
		t.Errorf("personal environment leaked into the server: %q", got)
	}
	for _, leaked := range []string{"TMUX=/tmp/parent-tmux", "TMUX_PANE=%99", "KITTY_WINDOW_ID=", "KITTY_LISTEN_ON=", "AMX_RUNTIME_SESSION_ID=", personalMarker} {
		if strings.Contains(env, leaked) {
			t.Errorf("pane environment contains %q", leaked)
		}
	}
	if !strings.Contains(env, "TMUX="+f.server.Socket()) {
		t.Errorf("pane TMUX does not name the private socket %s", f.server.Socket())
	}
	if entries, err := os.ReadDir(f.tmpdir); err != nil || len(entries) != 0 {
		t.Errorf("default tmux socket directory touched: %v %v", entries, err)
	}
	info, err := os.Stat(f.server.dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("server directory mode = %v (%v), want 0700", info.Mode().Perm(), err)
	}
	if data, err := os.ReadFile(f.server.ConfigPath()); err != nil || string(data) != Config {
		t.Errorf("server configuration is not AgentMux's: %v", err)
	}
	t.Logf("observed: with personal ~/.tmux.conf and XDG tmux.conf setting @amx_personal/prefix C-a/remain-on-exit off/history-limit 7, the private server reports @amx_personal unset, prefix None, remain-on-exit on, history-limit 50000; pane env has TMUX=%s and no parent TMUX/KITTY_*/AMX_RUNTIME_SESSION_ID; TMUX_TMPDIR stayed empty", f.server.Socket())
}

func TestPersonalConfigIsolationRejectsSharedDirectory(t *testing.T) {
	requireTmux(t)
	dir, err := os.MkdirTemp("", "amxtmux")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(Options{Dir: dir}); err == nil {
		t.Fatal("NewServer accepted a directory other users can read")
	}
	if _, err := NewServer(Options{Dir: "relative"}); err == nil {
		t.Fatal("NewServer accepted a relative directory")
	}
}

func TestProfileEnvironmentsArePreservedPerSession(t *testing.T) {
	f := newFixture(t, Options{})
	script := `for k in AMX_PROFILE_HOME AMX_PROFILE_ODD AMX_OTHER_ONLY; do printf '%s=[%s]\n' "$k" "$(printenv "$k" || printf '<unset>')"; done > "$1"
shift; for a in "$@"; do printf 'arg=[%s]\n' "$a"; done >> "$OUT"; printf '\0END\0' >> "$OUT"; exec sleep 600`
	odd := `a b 'q' "d" $HOME #{session_name} x;`
	args := []string{"tail;", `tail\;`, "#{pane_id}", "-", "spaced arg"}
	launch := func(name, home string, extra map[string]string) string {
		out := filepath.Join(f.dir, name+".out")
		env := map[string]string{"AMX_PROFILE_HOME": home, "AMX_PROFILE_ODD": odd, "OUT": out}
		for k, v := range extra {
			env[k] = v
		}
		argv := append([]string{"/bin/sh", "-c", script, "sh", out}, args...)
		f.launch(t, name, argv, env)
		return out
	}
	first := launch("first", "/profiles/work dir", map[string]string{"AMX_OTHER_ONLY": "only-first"})
	second := launch("second", "/profiles/personal", nil)

	wantArgs := ""
	for _, a := range args {
		wantArgs += "arg=[" + a + "]\n"
	}
	checks := map[string]string{
		first:  "AMX_PROFILE_HOME=[/profiles/work dir]\nAMX_PROFILE_ODD=[" + odd + "]\nAMX_OTHER_ONLY=[only-first]\n" + wantArgs,
		second: "AMX_PROFILE_HOME=[/profiles/personal]\nAMX_PROFILE_ODD=[" + odd + "]\nAMX_OTHER_ONLY=[<unset>]\n" + wantArgs,
	}
	for path, want := range checks {
		if got := waitFile(t, path); got != want {
			t.Errorf("%s:\n got %q\nwant %q", filepath.Base(path), got, want)
		}
	}
	if got := f.tmux(t, "show-environment", "-g"); strings.Contains(got, "AMX_PROFILE_HOME") || strings.Contains(got, "AMX_OTHER_ONLY") {
		t.Errorf("a profile variable reached the global environment: %q", got)
	}
	t.Logf("observed: two sessions with different AMX_PROFILE_HOME (one with a space) and an AMX_OTHER_ONLY only in the first each saw exactly their own values; a value with quotes, $HOME, #{session_name} and a trailing ';' and argv entries ending in ';' and '\\;' arrived byte-for-byte; the global environment holds neither")
}

func TestRetainedExitUntilExplicitClose(t *testing.T) {
	f := newFixture(t, Options{})
	f.launch(t, "status", []string{"/bin/sh", "-c", `echo exit-marker; exit 7`}, nil)
	f.launch(t, "signal", []string{"/bin/sh", "-c", `echo signal-marker; kill -TERM $$`}, nil)
	f.launch(t, "missing", []string{"/nonexistent/amx-agent", "--flag"}, nil)
	live := f.launch(t, "live", echoArgv("live-marker"), nil)

	dead := func(p backend.Pane) bool { return p.Exit != nil && (p.Exit.Status >= 0 || p.Exit.Signal != "") }
	status := f.waitFor(t, "status", dead)
	if status.Exit.Status != 7 || status.Exit.Signal != "" || len(status.Foreground) != 0 {
		t.Errorf("status exit = %+v foreground=%+v, want status 7", status.Exit, status.Foreground)
	}
	signal := f.waitFor(t, "signal", dead)
	if signal.Exit.Signal == "" || signal.Exit.Status != -1 {
		t.Errorf("signal exit = %+v, want a signal", signal.Exit)
	}
	missing := f.waitFor(t, "missing", dead)
	if missing.Exit.Status <= 0 || missing.Exit.Signal != "" {
		t.Errorf("missing program exit = %+v, want a nonzero status", missing.Exit)
	}

	// Still retained after a while, with its screen.
	time.Sleep(300 * time.Millisecond)
	status = f.waitFor(t, "status", dead)
	text, err := f.backend.Capture(status.Ref, backend.ExtentAll)
	if err != nil || !strings.Contains(text, "exit-marker") {
		t.Errorf("Capture(exited pane) = %q, %v; want the retained output", text, err)
	}
	if err := f.backend.SetMetadata(status.Ref, map[string]string{"amx_project": "kept"}); err != nil {
		t.Errorf("SetMetadata(exited pane): %v", err)
	}

	// Explicit cleanup is what removes it, and only it.
	if err := f.backend.Close(status.Group); err != nil {
		t.Fatalf("Close(exited): %v", err)
	}
	panes, err := f.backend.Discover()
	if err != nil {
		t.Fatal(err)
	}
	if backend.GroupPresent(panes, status.Group) {
		t.Error("closed exited group still discovered")
	}
	if !backend.GroupPresent(panes, live.Group) || !backend.GroupPresent(panes, signal.Group) {
		t.Error("closing one exited group removed another")
	}
	t.Logf("observed: exit 7 -> Exit{Status:7}; kill -TERM -> Exit{Signal:%q}; missing program -> Exit{Status:%d}; the exited pane stayed discoverable 300ms later with its output captured and metadata writable; Close removed only it", signal.Exit.Signal, missing.Exit.Status)
}

func TestServerDisappearance(t *testing.T) {
	f := newFixture(t, Options{})
	l := f.launch(t, "before", echoArgv("before-marker"), nil)
	f.waitFor(t, "before", func(backend.Pane) bool { return true })
	before, err := f.server.Instance()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(f.tmux(t, "display-message", "-p", "#{pid}"))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("server %d survived SIGKILL", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(f.server.Socket()); err != nil {
		t.Fatalf("expected a stale socket after SIGKILL: %v", err)
	}

	panes, err := f.backend.Discover()
	if err != nil || len(panes) != 0 {
		t.Fatalf("Discover after server death = %+v, %v; want no panes and no error", panes, err)
	}
	if _, err := f.server.Instance(); !errors.Is(err, ErrServerNotRunning) {
		t.Errorf("Instance err = %v, want ErrServerNotRunning", err)
	}
	ops := map[string]error{
		"Close":       f.backend.Close(l.Group),
		"SetMetadata": f.backend.SetMetadata(l.Pane, map[string]string{"amx_project": "x"}),
		"SetTitle":    f.backend.SetTitle(l.Group, "x"),
	}
	_, ops["Capture"] = f.backend.Capture(l.Pane, backend.ExtentScreen)
	for name, err := range ops {
		if !errors.Is(err, ErrServerNotRunning) {
			t.Errorf("%s after server death err = %v, want ErrServerNotRunning", name, err)
		}
	}

	// The next launch replaces the stale socket with a new generation.
	after := f.launch(t, "after", echoArgv("after-marker"), nil)
	f.waitFor(t, "after", func(backend.Pane) bool { return true })
	if after.Instance.Generation == before.Generation || after.Instance.Address != before.Address {
		t.Errorf("restarted server instance %+v, before %+v; want same address, new generation", after.Instance, before)
	}
	panes, _ = f.backend.Discover()
	for _, p := range panes {
		if p.Vars["amx_launch_id"] == "before" {
			t.Errorf("a pane of the dead server is discovered again: %+v", p)
		}
	}
	t.Logf("observed: SIGKILL of server pid %d left a stale socket; Discover returned no panes and nil error; Instance/Close/SetMetadata/SetTitle/Capture returned ErrServerNotRunning; the next launch started generation %s (was %s) at the same socket; old group ref %s new group ref %s", pid, after.Instance.Generation, before.Generation, l.Group.ID, after.Group.ID)

	// A graceful exit (last session closed) is the same absence.
	if err := f.backend.Close(after.Group); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		if _, err := f.server.Instance(); errors.Is(err, ErrServerNotRunning) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server kept running after its last session closed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if panes, err := f.backend.Discover(); err != nil || len(panes) != 0 {
		t.Errorf("Discover after the last session closed = %+v, %v", panes, err)
	}
}

// faultyTmux writes a tmux wrapper that runs the real tmux and then fails
// the invocation launching the "doomed" marker the way action describes.
func faultyTmux(t *testing.T, action string) string {
	t.Helper()
	real := requireTmux(t)
	path := filepath.Join(t.TempDir(), "tmux")
	script := fmt.Sprintf(`#!/bin/sh
%q "$@"
rc=$?
case " $* " in
*" new-session "*" doomed "*) %s ;;
esac
exit $rc
`, real, action)
	writeFile(t, path, script)
	return path
}

func TestPartialLaunchFailureLeavesNothing(t *testing.T) {
	cases := []struct {
		name    string
		action  string
		timeout time.Duration
	}{
		// The server created the session but the client reported failure.
		{"client_fails_after_create", "echo injected failure >&2; exit 1", 0},
		// The server created the session but the client never answered.
		{"client_hangs_after_create", "exec sleep 30", 700 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapper := faultyTmux(t, tc.action)
			f := newFixture(t, Options{Binary: wrapper, Timeout: tc.timeout})
			survivor := f.launch(t, "survivor", echoArgv("survivor"), nil)
			before := f.tmux(t, "list-sessions", "-F", "#{session_id}")
			if before != survivor.Group.ID {
				t.Fatalf("sessions before = %q", before)
			}
			start := time.Now()
			err := f.backend.Launch(backend.LaunchSpec{
				Title: "doomed", Directory: t.TempDir(),
				Vars: map[string]string{"amx_launch_id": "doomed"}, Argv: echoArgv("doomed"),
			})
			if err == nil {
				t.Fatal("Launch reported success")
			}
			if !strings.Contains(err.Error(), "partial session") || !strings.Contains(err.Error(), "removed") {
				t.Errorf("Launch err = %v, want it to report the removed partial session", err)
			}
			after := f.tmux(t, "list-sessions", "-F", "#{session_id}")
			if after != survivor.Group.ID {
				t.Errorf("sessions after failed launch = %q, want only the survivor %s", after, survivor.Group.ID)
			}
			panes, derr := f.backend.Discover()
			if derr != nil {
				t.Fatal(derr)
			}
			for _, p := range panes {
				if p.Vars["amx_launch_id"] == "doomed" {
					t.Errorf("partial launch still discovered: %+v", p)
				}
			}
			t.Logf("observed: %s -> Launch error %q after %s; sessions before %q, after %q", tc.name, err, time.Since(start).Round(time.Millisecond), before, after)
		})
	}
	t.Run("server_dies_during_launch", func(t *testing.T) {
		real := requireTmux(t)
		wrapper := faultyTmux(t, fmt.Sprintf(`%q -S "$2" kill-server; exit 1`, real))
		f := newFixture(t, Options{Binary: wrapper})
		err := f.backend.Launch(backend.LaunchSpec{
			Title: "doomed", Directory: t.TempDir(),
			Vars: map[string]string{"amx_launch_id": "doomed"}, Argv: echoArgv("doomed"),
		})
		if err == nil || !strings.Contains(err.Error(), "removed") {
			t.Fatalf("Launch err = %v, want a failed launch with nothing left", err)
		}
		if panes, err := f.backend.Discover(); err != nil || len(panes) != 0 {
			t.Errorf("Discover after the server died mid-launch = %+v, %v", panes, err)
		}
		t.Logf("observed: server killed between session creation and the client's answer -> Launch error %q; Discover empty", err)
	})
	t.Run("refused_before_tmux_runs", func(t *testing.T) {
		f := newFixture(t, Options{})
		specs := map[string]backend.LaunchSpec{
			"missing directory": {Directory: filepath.Join(f.dir, "absent"), Argv: echoArgv("x")},
			"relative dir":      {Directory: "relative", Argv: echoArgv("x")},
			"no program":        {Directory: f.dir},
			"bad env":           {Directory: f.dir, Env: map[string]string{"A=B": "x"}, Argv: echoArgv("x")},
			"bad placement":     {Directory: f.dir, Placement: "sideways", Argv: echoArgv("x")},
		}
		for name, spec := range specs {
			if err := f.backend.Launch(spec); err == nil {
				t.Errorf("%s: Launch succeeded", name)
			}
		}
		if _, err := os.Stat(f.server.Socket()); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a refused launch started the server (socket stat err=%v)", err)
		}
	})
}

func TestSetMetadataMergesUnderConcurrency(t *testing.T) {
	f := newFixture(t, Options{})
	l := f.launch(t, "meta", echoArgv("meta"), nil)
	const writers = 8
	errs := make(chan error, writers)
	for i := range writers {
		go func() {
			errs <- f.backend.SetMetadata(l.Pane, map[string]string{fmt.Sprintf("amx_key_%d", i): strconv.Itoa(i)})
		}()
	}
	for range writers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	p := f.waitFor(t, "meta", func(backend.Pane) bool { return true })
	for i := range writers {
		if p.Vars[fmt.Sprintf("amx_key_%d", i)] != strconv.Itoa(i) {
			t.Errorf("lost concurrent metadata write %d: %+v", i, p.Vars)
		}
	}
	if err := f.backend.Launch(backend.LaunchSpec{Directory: f.dir, Env: map[string]string{"AMX_SECRET": "hunter2"},
		Argv: []string{"/bin/sh", "-c", "exit 0"}, Placement: backend.PlacementAfter}); err != nil {
		t.Fatal(err)
	}
	if err := f.backend.SetMetadata(backend.PaneID("%999"), map[string]string{"amx_project": "x"}); err == nil {
		t.Error("SetMetadata on a missing pane succeeded")
	}
}

func TestRefsRejectForeignTargetSyntax(t *testing.T) {
	refs := []backend.Ref{
		backend.GroupID("amx-name"), backend.GroupID("$1:0"), backend.GroupID("$01"), backend.GroupID("%1"),
		backend.PaneID("$1"), backend.PaneID("%-1"), backend.PaneID("%1;kill-server"), backend.PaneID("=x:"),
	}
	for _, ref := range refs {
		if err := validRef(ref); !errors.Is(err, backend.ErrInvalidRef) {
			t.Errorf("validRef(%v) = %v, want ErrInvalidRef", ref, err)
		}
	}
	for _, ref := range []backend.Ref{backend.GroupID("$0"), backend.GroupID("$12"), backend.PaneID("%0"), backend.PaneID("%7")} {
		if err := validRef(ref); err != nil {
			t.Errorf("validRef(%v) = %v", ref, err)
		}
	}
}

func TestClientEnvStripsTerminalIdentity(t *testing.T) {
	got := clientEnv([]string{"PATH=/bin", "TMUX=x", "TMUX_PANE=%1", "KITTY_WINDOW_ID=3", "KITTY_PID=9",
		"WINDOWID=5", "CLAUDECODE=1", "AMX_RUNTIME_SESSION_ID=rs-x", "AMX_WORKSPACE_HOSTED=1", "CLAUDE_CONFIG_DIR=/p", "TMUX_TMPDIR=/t"})
	want := []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/p", "TMUX_TMPDIR=/t"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("clientEnv = %q, want %q", got, want)
	}
}

func TestCommandErrorRedactsLaunchEnvironment(t *testing.T) {
	f := newFixture(t, Options{})
	_, err := f.server.runChain([][]string{{"new-session", "-d", "-e", "AMX_SECRET=hunter2", "-x", "not-a-width"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "hunter2") || !strings.Contains(err.Error(), "AMX_SECRET=<redacted>") {
		t.Errorf("error = %v, want the value redacted", err)
	}
}

func TestEscapeArgKeepsTrailingSemicolon(t *testing.T) {
	for in, want := range map[string]string{"a": "a", "a;": `a\;`, `a\;`: `a\\;`, ";": `\;`, "a;b": "a;b"} {
		if got := escapeArg(in); got != want {
			t.Errorf("escapeArg(%q) = %q, want %q", in, got, want)
		}
	}
}
