package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/gitstate"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// gitStateWindows builds a snapshot placing each windowID in the directory it
// maps to, using the same cc_cwd user var a real managed pane carries.
func gitStateWindows(directories map[int]string) []kitty.OSWindow {
	tab := kitty.Tab{ID: 1, IsActive: true}
	for windowID, directory := range directories {
		tab.Windows = append(tab.Windows, kitty.Window{
			ID:       windowID,
			IsActive: true,
			UserVars: kitty.UserVars{CWD: directory},
		})
	}
	return []kitty.OSWindow{{ID: 1, IsFocused: true, Tabs: []kitty.Tab{tab}}}
}

// scriptedGitState answers rev-parse per directory and reports dirt for the
// directories named dirty. before runs inside the subprocess seam, which is
// where the lock-discipline assertions live.
type scriptedGitState struct {
	branches map[string]string
	dirty    map[string]bool
	before   func(directory string)

	mu    sync.Mutex
	calls []string
}

func (s *scriptedGitState) run(_ context.Context, directory string, _ time.Duration, args ...string) ([]byte, error) {
	if s.before != nil {
		s.before(directory)
	}
	s.mu.Lock()
	s.calls = append(s.calls, directory+" "+args[0])
	s.mu.Unlock()
	if args[0] == "rev-parse" {
		return []byte(s.branches[directory] + "\n"), nil
	}
	if s.dirty[directory] {
		return []byte(" M file.go\n"), nil
	}
	return nil, nil
}

func (s *scriptedGitState) calledDirectories() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// seedGitStateHandler tracks each window in the store and publishes a snapshot
// that places it, which is the state one watchdog tick sees.
func seedGitStateHandler(t *testing.T, git *scriptedGitState, directories map[int]string) *Handler {
	t.Helper()
	h := newTestHandler()
	h.setGitStateCache(gitstate.New(gitstate.Options{Git: git.run}))
	for windowID := range directories {
		h.store[windowID] = &state.WindowState{}
	}
	h.publishSnapshot(gitStateWindows(directories), time.Unix(1_700_000_000, 0))
	return h
}

func windowGitState(t *testing.T, h *Handler, windowID int) state.WindowState {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[windowID]
	if !ok {
		t.Fatalf("window %d is not tracked", windowID)
	}
	return *st
}

// THE lock-discipline test. The refresh must never hold stateMu across a git
// subprocess: stateMu guards every reducer mutation and every query answer, so
// a probe that waits out its timeout under it would stall hook acknowledgement
// behind a slow filesystem. TryLock inside the executor is the direct evidence
// — it can only succeed if no lock is held at the moment git would have run.
func TestGitStateRefreshTakesNoLockAcrossASubprocess(t *testing.T) {
	git := &scriptedGitState{branches: map[string]string{"/repo/one": "main", "/repo/two": "amx/feature"}}
	h := seedGitStateHandler(t, git, map[int]string{42: "/repo/one", 43: "/repo/two"})
	git.before = func(directory string) {
		if !h.stateMu.TryLock() {
			t.Errorf("stateMu was held across the git subprocess for %s", directory)
			return
		}
		h.stateMu.Unlock()
	}

	h.runGitStateRefresh(gitStateWindows(map[int]string{42: "/repo/one", 43: "/repo/two"}))

	if len(git.calledDirectories()) == 0 {
		t.Fatal("no git command ran, so the lock assertion never executed")
	}
}

func TestGitStateRefreshStampsTrackedWindows(t *testing.T) {
	git := &scriptedGitState{
		branches: map[string]string{"/repo/one": "main", "/repo/two": "amx/feature"},
		dirty:    map[string]bool{"/repo/two": true},
	}
	directories := map[int]string{42: "/repo/one", 43: "/repo/two"}
	h := seedGitStateHandler(t, git, directories)

	h.runGitStateRefresh(gitStateWindows(directories))

	clean := windowGitState(t, h, 42)
	if clean.GitBranch != "main" || clean.GitDirty || clean.GitDirectory != "/repo/one" {
		t.Fatalf("window 42 = %+v, want main/clean in /repo/one", clean)
	}
	dirty := windowGitState(t, h, 43)
	if dirty.GitBranch != "amx/feature" || !dirty.GitDirty || dirty.GitDirectory != "/repo/two" {
		t.Fatalf("window 43 = %+v, want amx/feature/dirty in /repo/two", dirty)
	}
}

// An untracked pane is not a session, so nothing about it is worth a
// subprocess. Only windows in the store are probed.
func TestGitStateRefreshProbesOnlyTrackedWindows(t *testing.T) {
	git := &scriptedGitState{branches: map[string]string{"/repo/one": "main", "/repo/two": "amx/feature"}}
	h := seedGitStateHandler(t, git, map[int]string{42: "/repo/one"})
	windows := gitStateWindows(map[int]string{42: "/repo/one", 43: "/repo/two"})
	h.publishSnapshot(windows, time.Unix(1_700_000_000, 0))

	h.runGitStateRefresh(windows)

	for _, call := range git.calledDirectories() {
		if call == "/repo/two rev-parse" || call == "/repo/two status" {
			t.Fatalf("probed an untracked window's directory: %v", git.calledDirectories())
		}
	}
}

// The probe runs off-lock, so a pane can move — or a window ID be reused by a
// new pane — while git is running. The write-back must not stamp the answer
// onto a directory it does not describe, and must not leave the previous
// answer standing either, because the daemon now knows it is wrong.
func TestGitStateRefreshDropsAResultForAPaneThatMoved(t *testing.T) {
	git := &scriptedGitState{branches: map[string]string{"/repo/one": "main", "/repo/two": "amx/feature"}}
	collected := gitStateWindows(map[int]string{42: "/repo/one"})
	h := seedGitStateHandler(t, git, map[int]string{42: "/repo/one"})
	h.runGitStateRefresh(collected)
	if got := windowGitState(t, h, 42); got.GitBranch != "main" {
		t.Fatalf("precondition: window 42 = %+v, want main", got)
	}

	// The pane is now somewhere else, and only the write-back can notice.
	git.before = func(string) {
		h.publishSnapshot(gitStateWindows(map[int]string{42: "/repo/two"}), time.Unix(1_700_000_100, 0))
	}
	h.setGitStateCache(gitstate.New(gitstate.Options{Git: git.run}))
	h.runGitStateRefresh(collected)

	got := windowGitState(t, h, 42)
	if got.GitBranch != "" || got.GitDirty || got.GitDirectory != "" {
		t.Fatalf("window 42 = %+v, want the stale facts cleared after the pane moved", got)
	}
}

// A window closed while the probes ran has no state to write back to, and the
// refresh must not resurrect an entry for it.
func TestGitStateRefreshSkipsAWindowThatClosed(t *testing.T) {
	git := &scriptedGitState{branches: map[string]string{"/repo/one": "main"}}
	directories := map[int]string{42: "/repo/one"}
	h := seedGitStateHandler(t, git, directories)
	git.before = func(string) {
		h.stateMu.Lock()
		delete(h.store, 42)
		h.stateMu.Unlock()
	}

	h.runGitStateRefresh(gitStateWindows(directories))

	h.stateMu.Lock()
	_, resurrected := h.store[42]
	h.stateMu.Unlock()
	if resurrected {
		t.Fatal("the refresh recreated a closed window's state")
	}
}

// The toggle must cost nothing when it is off: no cache, no goroutine, and
// above all no git.
func TestGitStateDisabledRunsNoProbe(t *testing.T) {
	git := &scriptedGitState{branches: map[string]string{"/repo/one": "main"}}
	directories := map[int]string{42: "/repo/one"}
	h := seedGitStateHandler(t, git, directories)
	h.SetGitProbe(false)
	if h.gitState != nil {
		t.Fatal("SetGitProbe(false) left a cache installed")
	}

	h.refreshGitState(gitStateWindows(directories))

	if calls := git.calledDirectories(); len(calls) != 0 {
		t.Fatalf("a disabled probe ran git: %v", calls)
	}
	if got := windowGitState(t, h, 42); got.GitBranch != "" {
		t.Fatalf("a disabled probe stamped %+v", got)
	}
}

// A round already in flight wins: ticks arrive every second and a round that
// hits several probe timeouts can outlast several of them.
func TestGitStateRefreshSingleFlightsRounds(t *testing.T) {
	git := &scriptedGitState{branches: map[string]string{"/repo/one": "main"}}
	directories := map[int]string{42: "/repo/one"}
	h := seedGitStateHandler(t, git, directories)
	windows := gitStateWindows(directories)
	// A synchronous probe runner keeps the assertion deterministic; the reentrant
	// call below stands in for the next watchdog tick arriving mid-round.
	h.probeRunner = func(fn func()) { fn() }
	reentered := false
	git.before = func(string) {
		if reentered {
			return
		}
		reentered = true
		h.refreshGitState(windows)
	}

	h.refreshGitState(windows)

	// Two commands: the branch and the dirty bit of one round. A second round
	// would have doubled them.
	if calls := git.calledDirectories(); len(calls) != 2 {
		t.Fatalf("git calls = %v, want exactly one round's worth", calls)
	}
	if h.gitStateRefreshing.Load() {
		t.Fatal("the single-flight latch was not released")
	}
}

// watchdogTick is the integration point named by the design: the refresh runs
// from the watchdog loop, never from the render path or the TUI.
func TestWatchdogTickStartsTheGitStateRefresh(t *testing.T) {
	git := &scriptedGitState{branches: map[string]string{"/repo/one": "main"}}
	directories := map[int]string{42: "/repo/one"}
	h := seedGitStateHandler(t, git, directories)
	h.probeRunner = func(fn func()) { fn() }

	h.watchdogTick(time.Unix(1_700_000_001, 0), time.Second)

	if got := windowGitState(t, h, 42); got.GitBranch != "main" {
		t.Fatalf("window 42 = %+v after a watchdog tick, want main", got)
	}
}
