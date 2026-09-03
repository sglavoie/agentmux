package daemon

// The dashboard's per-directory Git probe, seen from the daemon side.
//
// The probe itself lives in internal/gitstate and knows nothing about windows;
// what lives here is the only part that needs the daemon: which directories are
// worth probing, and how a result gets back onto a window without ever holding
// stateMu across a subprocess.
//
// THE LOCK DISCIPLINE IS THE WHOLE POINT OF THIS FILE. A git command can take
// its full timeout, and stateMu is the daemon's single guard for every reducer
// mutation, every render decision and every query answer. Holding it across a
// probe would stall hook acknowledgement behind a slow filesystem — the exact
// hazard the ShadowSeq discussion in shadow.go describes for a slow off-lock
// read, and the reason applyAuthoritativeIdle has the shape it does. So the
// refresh is strictly three steps: collect the directories under the lock,
// probe with no lock held, write the results back under the lock behind a
// staleness check. Every one of those three is asserted by a test.

import (
	"context"

	"github.com/sglavoie/agentmux/internal/gitstate"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// refreshGitState starts one probe round for the windows in a published
// snapshot. It is called from watchdogTick and returns immediately: the round
// runs off the watchdog goroutine, because the watchdog measures idleness by
// the elapsed time it observes between ticks and a tick that waited on git
// would credit that wait to every session on the machine.
//
// A round already in flight wins. Single-flighting is not an optimization here
// but a bound: ticks arrive every second and a round that hits several probe
// timeouts can outlast several of them, so without it a slow filesystem would
// stack one goroutine and one process fork per tick.
func (h *Handler) refreshGitState(windows []kitty.OSWindow) {
	if h.gitState == nil {
		return
	}
	if !h.gitStateRefreshing.CompareAndSwap(false, true) {
		return
	}
	h.runProbeAsync(func() {
		defer h.gitStateRefreshing.Store(false)
		h.runGitStateRefresh(windows)
	})
}

// runGitStateRefresh is the three-step round. It is separated from
// refreshGitState so tests can drive one round synchronously without reasoning
// about the single-flight latch.
func (h *Handler) runGitStateRefresh(windows []kitty.OSWindow) {
	targets := h.gitStateTargets(windows)
	directories := make([]string, 0, len(targets))
	for _, directory := range targets {
		directories = append(directories, directory)
	}
	// No lock is held here, and the context is Background rather than the
	// daemon's: each command carries its own deadline, so shutdown costs at
	// most one probe timeout instead of a cancellation path that would have to
	// be threaded through the watchdog signature.
	states := h.gitState.Refresh(context.Background(), directories)
	h.applyGitState(targets, states)
}

// gitStateTargets collects, under stateMu, the directory of every tracked
// window that the snapshot can place. It is the only step that reads the store,
// and it runs no git.
//
// Directories are normalized to gitstate's key spelling here so the write-back
// can index the result map directly; a window whose directory is unusable
// (blank, or relative, which would resolve against the daemon's working
// directory rather than the user's) is simply not a target.
func (h *Handler) gitStateTargets(windows []kitty.OSWindow) map[int]string {
	if len(windows) == 0 {
		return nil
	}
	targets := make(map[int]string)
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for windowID := range h.store {
		if key, ok := gitStateDirectory(windows, windowID); ok {
			targets[windowID] = key
		}
	}
	return targets
}

// applyGitState writes the probe results back under stateMu.
//
// The staleness check is the reason this is not a plain loop. The probes ran
// with no lock held, so between collection and write-back a pane can have been
// closed, moved to another directory, or — worst — had its window ID reused by
// a new pane. Each result is therefore admitted only if the window is still
// tracked AND the directory it now reports is still the one that was probed.
// A window that moved has its stale facts cleared rather than left in place:
// the daemon knows the old branch is wrong, and saying nothing for one sweep is
// better than saying something known to be false.
func (h *Handler) applyGitState(targets map[int]string, states map[string]gitstate.State) {
	if len(targets) == 0 {
		return
	}
	current, _, ready := h.cachedSnapshot()
	if !ready {
		// The snapshot cache went cold while the probes ran, so there is no
		// evidence left to check the results against.
		return
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for windowID, directory := range targets {
		st, ok := h.store[windowID]
		if !ok {
			continue
		}
		observed, ok := gitStateDirectory(current, windowID)
		if !ok || observed != directory {
			st.GitDirectory, st.GitBranch, st.GitDirty = "", "", false
			continue
		}
		state := states[directory]
		st.GitDirectory, st.GitBranch, st.GitDirty = directory, state.Branch, state.Dirty
	}
}

// gitStateDirectory resolves a window's working directory from a snapshot and
// normalizes it into the cache key it will be stored under. It reuses
// windowDirectoryHint, which already encodes where a managed pane's directory
// really comes from (the amx user var first, then the foreground process, then
// Kitty's own cwd), so the probe cannot disagree with the loop-lifecycle path
// about which directory a window is in.
func gitStateDirectory(windows []kitty.OSWindow, windowID int) (string, bool) {
	_, _, win, ok := kitty.FindWindowLocationByID(windows, windowID)
	if !ok || win == nil {
		return "", false
	}
	return gitstate.Key(windowDirectoryHint(*win))
}
