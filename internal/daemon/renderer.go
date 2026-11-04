package daemon

// The daemon's periodic work is split into independent loops so no single
// slow dependency can stall the others:
//
//   - runMaintenance (this file): reconciles registered tmux sessions and ASL
//     leases, then persists conversation bindings.
//   - runWatchdog (watchdog_loop.go): the 1-second staleness evaluator. It
//     measures idleness by accumulating in-process elapsed time (never by
//     rewriting activity timestamps), so delayed rendering and sleep-like
//     scheduling gaps cannot distort expiry.
//   - runScreenProbes (this file): Codex prompt, Claude context-limit, Claude
//     idle stop screen probes, and the Copilot transcript stop probe, reading
//     tmux panes and transcripts outside the other loops.
//   - Event reduction is already independent: the serialized ingress consumer
//     (ingress.go) acknowledges after reduction and never waits on rendering.

import (
	"context"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/rendering"
)

func (h *Handler) runMaintenance(ctx context.Context) {
	ticker := time.NewTicker(rendering.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.maintenanceTick(now)
		}
	}
}

// runScreenProbes drives the Codex prompt, Claude context-limit, Claude idle
// stop, and Copilot transcript stop probes on their own loop. A slow screen or
// transcript read stalls only this probe loop.
func (h *Handler) runScreenProbes(ctx context.Context) {
	ticker := time.NewTicker(rendering.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.runScreenProbeTick(now)
		}
	}
}

// runScreenProbeTick reads the screens of registered tmux sessions.
func (h *Handler) runScreenProbeTick(now time.Time) {
	h.probeRuntimeScreens(now, h.screenTextCacheFor())
}

// maintenanceTick keeps registered tmux sessions and their ASL state current.
func (h *Handler) maintenanceTick(now time.Time) {
	defer h.persistConversationBindings()
	h.maintainCaptureStore(now)
	h.maintainRuntimeSessions(now)
	h.maintainASLWithoutSnapshot(now)
}

// maintainASLWithoutSnapshot reconciles ASL leases for registered tmux
// sessions and refreshes supervisor questions from lease records. The work
// continues even when no viewer is running.
func (h *Handler) maintainASLWithoutSnapshot(now time.Time) {
	h.stateMu.Lock()
	runtimeState := h.hasRuntimeASLStateLocked()
	h.stateMu.Unlock()
	if runtimeState {
		// A skipped sweep would restart the interval and delay the next real one.
		h.maybeReconcileASLLeasesRuntime(now)
	}
	h.refreshASLSupervisorQuestions(now)
}
