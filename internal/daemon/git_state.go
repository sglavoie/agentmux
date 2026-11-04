package daemon

// The daemon's per-session Git probe. A refresh collects tmux session
// directories under stateMu, runs Git without that lock, and applies results
// only while each slot still names the directory that was probed.

import (
	"context"

	"github.com/sglavoie/agentmux/internal/gitstate"
)

// refreshGitState starts one asynchronous round. A slow filesystem cannot
// stack rounds or delay the watchdog tick.
func (h *Handler) refreshGitState() {
	if h.gitState == nil || !h.gitStateRefreshing.CompareAndSwap(false, true) {
		return
	}
	h.runProbeAsync(func() {
		defer h.gitStateRefreshing.Store(false)
		h.runGitStateRefresh()
	})
}

// runGitStateRefresh is the synchronous round used by runtime tests.
func (h *Handler) runGitStateRefresh() {
	targets := h.gitStateTargets()
	directories := make([]string, 0, len(targets))
	for _, directory := range targets {
		directories = append(directories, directory)
	}
	states := h.gitState.Refresh(context.Background(), directories)
	h.applyGitState(targets, states)
}

// gitStateTargets copies the directory of each tracked tmux slot under
// stateMu. Unlabeled and unbound slots are not probed.
func (h *Handler) gitStateTargets() map[int]string {
	targets := make(map[int]string)
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for slot := range h.store {
		if !isRuntimeSyntheticSlot(slot) {
			continue
		}
		if key, ok := h.runtimeSlotDirectoryLocked(slot); ok {
			targets[slot] = key
		}
	}
	return targets
}

// applyGitState admits a probe result only while the same slot still carries
// the same directory. A moved session loses stale Git facts immediately.
func (h *Handler) applyGitState(targets map[int]string, states map[string]gitstate.State) {
	if len(targets) == 0 {
		return
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for slot, directory := range targets {
		st, ok := h.store[slot]
		if !ok {
			continue
		}
		observed, ok := h.runtimeSlotDirectoryLocked(slot)
		if !ok || observed != directory {
			st.GitDirectory, st.GitBranch, st.GitDirty = "", "", false
			continue
		}
		state := states[directory]
		st.GitDirectory, st.GitBranch, st.GitDirty = directory, state.Branch, state.Dirty
	}
}

// runtimeSlotDirectoryLocked resolves the directory recorded on the tmux
// session's label. Callers hold stateMu.
func (h *Handler) runtimeSlotDirectoryLocked(slot int) (string, bool) {
	b, ok := h.runtimeRoutes.binding(slot)
	if !ok || b.target.backend != "tmux" {
		return "", false
	}
	return gitstate.Key(h.runtimeLabels[b.runtime].directory)
}
