package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/gitstate"
)

// scriptedGitState records every command and can change daemon state at the
// subprocess boundary, where a refresh must hold no daemon lock.
type scriptedGitState struct {
	branches map[string]string
	dirty    map[string]bool
	before   func(directory string)
	mu       sync.Mutex
	calls    []string
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

func windowGitState(t *testing.T, h *Handler, slot int) state.WindowState {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st, ok := h.store[slot]
	if !ok {
		t.Fatalf("slot %d is not tracked", slot)
	}
	return *st
}

func TestGitRefreshReadsTmuxDirectoryOffLock(t *testing.T) {
	w := newRuntimeWatchdogWorld(t, RuntimeSessionLabel{Project: "API", Profile: "work", Harness: "claude", Directory: "/repo/api"})
	git := &scriptedGitState{branches: map[string]string{"/repo/api": "main"}, dirty: map[string]bool{"/repo/api": true}}
	h := w.h
	h.setGitStateCache(gitstate.New(gitstate.Options{Git: git.run}))
	git.before = func(directory string) {
		if !h.stateMu.TryLock() {
			t.Errorf("stateMu held across Git for %s", directory)
			return
		}
		h.stateMu.Unlock()
	}
	h.runGitStateRefresh()
	got := windowGitState(t, h, w.slot)
	if got.GitDirectory != "/repo/api" || got.GitBranch != "main" || !got.GitDirty || len(git.calledDirectories()) != 2 {
		t.Fatalf("state = %+v, calls = %v", got, git.calledDirectories())
	}
}

func TestGitRefreshSkipsEndedSlot(t *testing.T) {
	w := newRuntimeWatchdogWorld(t, RuntimeSessionLabel{Project: "API", Profile: "work", Harness: "claude", Directory: "/repo/api"})
	git := &scriptedGitState{branches: map[string]string{"/repo/api": "main"}}
	h := w.h
	h.setGitStateCache(gitstate.New(gitstate.Options{Git: git.run}))
	git.before = func(string) {
		h.stateMu.Lock()
		delete(h.store, w.slot)
		h.stateMu.Unlock()
	}
	h.runGitStateRefresh()
	h.stateMu.Lock()
	_, resurrected := h.store[w.slot]
	h.stateMu.Unlock()
	if resurrected {
		t.Fatal("Git refresh recreated a closed runtime slot")
	}
}

func TestGitRefreshSingleFlightsRounds(t *testing.T) {
	w := newRuntimeWatchdogWorld(t, RuntimeSessionLabel{Project: "API", Profile: "work", Harness: "claude", Directory: "/repo/api"})
	git := &scriptedGitState{branches: map[string]string{"/repo/api": "main"}}
	h := w.h
	h.setGitStateCache(gitstate.New(gitstate.Options{Git: git.run}))
	h.probeRunner = func(fn func()) { fn() }
	reentered := false
	git.before = func(string) {
		if !reentered {
			reentered = true
			h.refreshGitState()
		}
	}
	h.refreshGitState()
	if calls := git.calledDirectories(); len(calls) != 2 {
		t.Fatalf("Git calls = %v, want one round", calls)
	}
	if h.gitStateRefreshing.Load() {
		t.Fatal("single-flight latch was not released")
	}
}

func TestGitDisabledRunsNoProbe(t *testing.T) {
	w := newRuntimeWatchdogWorld(t, RuntimeSessionLabel{Project: "API", Profile: "work", Harness: "claude", Directory: "/repo/api"})
	git := &scriptedGitState{branches: map[string]string{"/repo/api": "main"}}
	h := w.h
	h.setGitStateCache(gitstate.New(gitstate.Options{Git: git.run}))
	h.SetGitProbe(false, 0, 0)
	h.refreshGitState()
	if len(git.calledDirectories()) != 0 || h.gitState != nil {
		t.Fatal("disabled Git probe ran")
	}
}
