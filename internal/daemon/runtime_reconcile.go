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
	if !h.lastRuntimeReconcile.IsZero() && now.Sub(h.lastRuntimeReconcile) < runtimeReconcileInterval && !now.Before(h.lastRuntimeReconcile) {
		return
	}
	h.lastRuntimeReconcile = now
	report, err := h.runtimeReconciler(now)
	if err != nil {
		slog.Debug("maintenance: runtime session reconciliation incomplete", "err", err)
	}
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
