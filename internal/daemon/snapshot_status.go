package daemon

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// This file projects the daemon's cached Kitty snapshot onto the wire block
// that TypeStateQuery and TypeHealth both carry. The cache and its freshness
// arithmetic live in internal/daemon/probes; the shape and the stale threshold
// live in internal/daemon/protocol. What stays here is the part that needs the
// Handler: reading the view under the established lock order, and sampling the
// coordinator for the daemon's effective socket without re-entering that lock.

// kittyListStatusClient is the optional coordination-accounting surface a Kitty
// client may expose. kitty.SubprocessClient satisfies it; the narrow fakes the
// daemon's own tests inject deliberately do not. Every caller therefore goes
// through kittyListStatus rather than asserting inline — a client without the
// surface must degrade to an omitted Coordinator block, never to a panic.
type kittyListStatusClient interface {
	ListStatus() (kitty.ListStatus, bool)
}

// kittyListSourceLocked returns the last published snapshot together with the
// client and socket a coordination sample must be taken through. Callers hold
// stateMu, which is the established stateMu → SnapshotStore order: reading the
// view here is what binds the topology to the very window states the caller
// built in the same critical section.
//
// It deliberately hands back the unscoped client and the socket rather than a
// ready-to-use scoped client, because scoping is kittyClientForList's job and
// that helper takes stateMu itself. The caller scopes and samples after
// unlocking.
func (h *Handler) kittyListSourceLocked() (probes.SnapshotView, kittyTitleClient, string) {
	return h.snapshots.View(), h.kittyClient, h.lastKittyListenOn
}

// kittyListStatus samples the coordination accounting of client scoped to
// socket. Callers must NOT hold stateMu.
//
// ok is false when the client exposes no coordination surface, when the socket
// has never been read, and when its idle key has been evicted. All three mean
// the same thing to a consumer — no information — which is why they collapse
// into one flag rather than into three distinguishable zero blocks that would
// each read as "healthy".
func kittyListStatus(client kittyTitleClient, socket string) (kitty.ListStatus, bool) {
	scoped, ok := scopedKittyClient(client, socket).(kittyListStatusClient)
	if !ok {
		return kitty.ListStatus{}, false
	}
	return scoped.ListStatus()
}

// kittySnapshotBlock projects one snapshot view onto the wire. now is the
// instant the response is being built: age and staleness are computed here
// rather than stored, because a daemon that stopped listing also stopped
// writing, and the useful signal is exactly the one a stalled lister could not
// have recorded.
//
// The returned block is always non-nil, cold cache included. A new daemon that
// has never completed a list still answers "I have no snapshot yet"; nil is
// reserved for a daemon too old to carry the field at all. Collapsing the two
// would leave a client unable to tell "wait for the first publication" from "do
// the Kitty read yourself".
//
// includeWindows separates the two request types: the state query carries the
// tree a dashboard renders from, health carries only the status a probe needs.
func kittySnapshotBlock(
	view probes.SnapshotView,
	status kitty.ListStatus,
	hasStatus bool,
	now time.Time,
	includeWindows bool,
) *protocol.KittySnapshot {
	// SnapshotView.Age already clamps a backwards wall clock to zero and reports
	// zero for a cold view, so the clamping rule is honored by asking it rather
	// than by subtracting capture time from now a second time here.
	age := view.Age(now)
	block := &protocol.KittySnapshot{
		Ready:      view.Ready(),
		Stale:      protocol.KittySnapshotStale(view.Ready(), age),
		Generation: view.Generation(),
		AgeMS:      protocol.DurationMillis(age),
	}
	if capturedAt := view.CapturedAt(); !capturedAt.IsZero() {
		block.CapturedAt = capturedAt.Unix()
	}
	if hasStatus {
		coordinator := protocol.NewKittyListCoordinatorStatus(status)
		block.Coordinator = &coordinator
	}
	if includeWindows {
		block.Windows = view.Windows()
	}
	return block
}

// kittySnapshotStatus builds the status-only block TypeHealth carries: the same
// freshness facts as the state query without the window tree, so a health probe
// stays well inside its acknowledgement budget on a daemon tracking a large
// Kitty tree.
func (h *Handler) kittySnapshotStatus(now time.Time) *protocol.KittySnapshot {
	h.stateMu.Lock()
	view, client, socket := h.kittyListSourceLocked()
	h.stateMu.Unlock()
	status, hasStatus := kittyListStatus(client, socket)
	return kittySnapshotBlock(view, status, hasStatus, now, false)
}
