package daemon

import (
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// A tmux agent runs under a server that outlives the app and the daemon, and
// nothing tells either when it stops: an agent exits on its own, a server
// dies, a viewer is killed before it can let go of the session. The daemon is
// the one process always there to notice, so on its maintenance schedule it
// has the registry reconciled with what the private servers hold. Everything
// else follows from the registry: a session no longer live there is retired
// from the slot table by retireEndedRuntimeSessions, dropped from the next
// snapshot, and refused by `amx attach` with whether it exited or was lost.
//
// Reconciling only records. It starts no server and launches no agent: an
// agent that is absent stays absent until the operator starts one.

// runtimeReconcileInterval bounds how often the private servers are read.
const runtimeReconcileInterval = 2 * time.Second

// runtimeReconcileSlack is how much sooner than its interval a pass may run.
// The pass is asked for by the maintenance tick, which has the same period:
// measured against the full interval, a tick that arrives a microsecond early
// is refused and the servers are read every four seconds instead of two.
const runtimeReconcileSlack = 200 * time.Millisecond

// runtimeReconcileMaxBackoff is the longest wait between passes while they
// keep failing. A pass against a server that does not answer holds the
// maintenance loop for the whole of a client timeout, so asking again every
// two seconds would leave that loop little time for anything else. The wait
// doubles with each consecutive failure up to this, and one good pass
// restores the interval.
const runtimeReconcileMaxBackoff = 30 * time.Second

// runtimeReconcileWait is how long after a pass the next one is due, given
// how many passes in a row have failed.
func runtimeReconcileWait(consecutiveFailures uint64) time.Duration {
	wait := runtimeReconcileInterval
	for ; consecutiveFailures > 0 && wait < runtimeReconcileMaxBackoff; consecutiveFailures-- {
		wait *= 2
	}
	return min(wait, runtimeReconcileMaxBackoff)
}

// RuntimeSessionReconciler brings the runtime-session registry in line with
// the backends. It runs off stateMu and may start subprocesses.
type RuntimeSessionReconciler func(now time.Time) (tmux.Report, error)

// SetRuntimeSessionReconciler installs the reconciliation pass. It is called
// once at startup, before any pass runs; without one the registry changes
// only when a launcher or an attach changes it.
func (h *Handler) SetRuntimeSessionReconciler(reconcile RuntimeSessionReconciler) {
	h.runtimeReconciler = reconcile
}

// NewTmuxRuntimeReconciler is the production reconciler over store, sharing
// reader with the hook path so both see the same registry version.
func NewTmuxRuntimeReconciler(store *runtimesession.Store, reader *runtimesession.Reader) RuntimeSessionReconciler {
	return tmux.NewReconciler(store, reader).Reconcile
}

// reconcileRuntimeSessions runs one pass when one is due. A failed pass
// changes nothing it could not prove and is tried again at the next interval.
func (h *Handler) reconcileRuntimeSessions(now time.Time) {
	if h.runtimeReconciler == nil {
		return
	}
	wait := runtimeReconcileWait(h.runtimeReconcileFailures)
	if !h.lastRuntimeReconcile.IsZero() && now.Sub(h.lastRuntimeReconcile) < wait-runtimeReconcileSlack && !now.Before(h.lastRuntimeReconcile) {
		return
	}
	h.lastRuntimeReconcile = now
	started := time.Now()
	report, err := h.runtimeReconciler(now)
	finished := time.Now()
	h.runtimeReconcileFailures = h.recordRuntimeReconcile(finished, finished.Sub(started), report, err)
	if err != nil {
		slog.Debug("maintenance: runtime session reconciliation incomplete", "err", err)
	}
	h.observeRuntimeViewers(report.Viewed, finished)
	h.observeRuntimeMutes(report.Mutes)
	h.observeRuntimeColors(report.Colors)
	h.observeRuntimeNames(report.Names)
	if !report.Empty() {
		// The directory answers a known session from the version it loaded;
		// what was just recorded has to be the version the restore and the
		// hook path read next.
		if refresher, ok := h.runtimeDirectory.(runtimeDirectoryRefresher); ok {
			if err := refresher.Refresh(); err != nil {
				slog.Debug("maintenance: runtime registry refresh failed", "err", err)
			}
		}
	}
	for _, ended := range report.Ended {
		slog.Info("maintenance: runtime session is no longer running", "runtime_session", ended.ID, "outcome", ended.Outcome, "reason", ended.Reason)
	}
	for _, id := range report.Released {
		slog.Info("maintenance: released the ownership of a runtime session whose terminal is gone", "runtime_session", id)
	}
	for _, client := range report.Detached {
		slog.Info("maintenance: detached a terminal competing with a session's owner", "client", client.Name, "session", client.Group.ID)
	}
}

// observeRuntimeMutes is the mute observation of a session with no Kitty
// window. A Kitty pane's mute is read from every Kitty listing
// (setMutedFromSnapshot); a tmux session's is the same variable on its pane,
// which the reconciliation pass has just read. It is the cache notification
// suppression consults and the companion snapshot publishes, and like the
// Kitty one it is never durable: the next pass reads it again.
func (h *Handler) observeRuntimeMutes(mutes []tmux.Muted) {
	if len(mutes) == 0 {
		return
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for _, observed := range mutes {
		slot, ok := h.runtimeRoutes.byRuntime[string(observed.ID)]
		if !ok || !isRuntimeSyntheticSlot(slot) {
			continue
		}
		st, ok := h.windowStateLocked(slot)
		if !ok || st.Muted == observed.Muted {
			continue
		}
		st.Muted = observed.Muted
		h.companionDirty()
	}
}

// observeRuntimeColors is the color observation of a session with no Kitty
// window. Kitty paints a tab in the color picked for it; a tmux session has
// no tab, so the pick stays on its pane and the companion snapshot publishes
// it for whoever lists the session. It is kept beside the session's label
// and, like the mute, read again by every pass.
func (h *Handler) observeRuntimeColors(colors []tmux.Colored) {
	if len(colors) == 0 {
		return
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for _, observed := range colors {
		slot, ok := h.runtimeRoutes.byRuntime[string(observed.ID)]
		if !ok || !isRuntimeSyntheticSlot(slot) {
			continue
		}
		label, ok := h.runtimeLabels[string(observed.ID)]
		if !ok || label.color == observed.Color {
			continue
		}
		label.color = observed.Color
		h.runtimeLabels[string(observed.ID)] = label
		h.companionDirty()
	}
}

// observeRuntimeNames is the name observation of a session with no Kitty
// window: the name the operator gave it stays on its pane, and the companion
// snapshot publishes it for whoever lists the session. Like the color it is
// kept beside the session's label and read again by every pass.
func (h *Handler) observeRuntimeNames(names []tmux.Named) {
	if len(names) == 0 {
		return
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for _, observed := range names {
		slot, ok := h.runtimeRoutes.byRuntime[string(observed.ID)]
		if !ok || !isRuntimeSyntheticSlot(slot) {
			continue
		}
		label, ok := h.runtimeLabels[string(observed.ID)]
		if !ok || label.name == observed.Name {
			continue
		}
		label.name = observed.Name
		h.runtimeLabels[string(observed.ID)] = label
		h.companionDirty()
	}
}

// observeRuntimeViewers is the focus observation of a session with no Kitty
// window. A Kitty pane is seen when a snapshot shows it focused
// (refreshFocusFromSnapshotLocked); a tmux session has no focus to read, but
// its server knows when a terminal attached to it and when that terminal was
// last used. Either one, at or after the moment the session came to rest, is
// the operator having looked at it, and clears the unseen bit.
//
// A terminal that is merely still attached proves nothing: the desktop
// workspace keeps one attached to every session for as long as its window is
// open. Only an attach or a use that is not older than the rest counts. tmux
// reports both in whole seconds, so the rest is compared at the same grain.
//
// Like the Kitty observation this only ever clears the bit, and the clear is
// a durable mutation: it marks the slot dirty so the runtime checkpoint
// records it. Kitty slots are left to the Kitty observation.
func (h *Handler) observeRuntimeViewers(viewed []tmux.Viewed, observedAt time.Time) {
	if len(viewed) == 0 {
		return
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	for _, viewing := range viewed {
		slot, ok := h.runtimeRoutes.byRuntime[string(viewing.ID)]
		if !ok || !isRuntimeSyntheticSlot(slot) {
			continue
		}
		st, ok := h.windowStateLocked(slot)
		if !ok || !st.Unseen || viewing.At.Before(st.PresentationChangedAt.Truncate(time.Second)) {
			continue
		}
		st.Unseen = false
		st.PresentationChangedAt = observedAt
		h.markWindowDirtyLocked(slot)
		h.companionDirty()
	}
}
