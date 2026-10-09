package probes

import (
	"strconv"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// Binding is the immutable routing and freshness evidence shared by the periodic
// screen/transcript adapters. Producer identifies the harness session whose
// external evidence is being read; SourceWindowID is that producer pane, while
// TargetWindowID owns the reducer state that observations mutate. Direct managed
// sessions use the same source and target. Both receipts are captured before the
// Kitty snapshot that supplied the route. SnapshotTabID and SnapshotTabEpoch
// invalidate the binding when the tab's focus, topology, title, or pane
// ownership changes, while SnapshotSourceEpoch and SnapshotTargetEpoch
// invalidate it when either of its own two panes changes — so an unrelated pane
// in the same tab cannot starve it. Most bindings require a live producer route;
// TerminalProducer and RecoverySource narrowly authorize Codex clear and Copilot
// self-recovery after the ordinary live route has closed.
//
// The authorization fields are exported because the daemon root's harness
// adapters are the only things that may grant them: the root decides that a
// route is a legacy direct-managed one, that a terminal fact still buys one last
// read, or that a stop recovery applies. Binding itself only records and
// validates that decision.
type Binding struct {
	Producer            conversation.Identity
	SourceWindowID      int
	TargetWindowID      int
	SourceReceipt       uint64
	TargetReceipt       uint64
	SnapshotTabID       int
	SnapshotTabEpoch    uint64
	SnapshotSourceEpoch uint64
	SnapshotTargetEpoch uint64
	LegacyDirectManaged bool
	TerminalProducer    bool
	TerminalAt          int64
	RecoverySource      protocol.SourceID
	RecoveryAt          int64
}

// SourcePane names the producer pane as a backend reference, which is how the
// screen readers address it. A Binding is built from the Kitty window tree, so
// the reference is that window's ID.
func (b Binding) SourcePane() backend.Ref {
	return backend.PaneID(strconv.Itoa(b.SourceWindowID))
}

// ScreenExpectation is what a screen read for this binding must describe: the
// producer pane, on Kitty. The binding's epochs already tie it to one Kitty
// window tree, so no address or generation is named here.
func (b Binding) ScreenExpectation() ScreenExpectation {
	return ScreenExpectation{
		Instance: runtimesession.BackendInstance{Kind: runtimesession.BackendKitty},
		Pane:     b.SourcePane(),
	}
}

// Valid reports whether the binding is internally coherent. It is the
// snapshot-independent half of validation; the caller still has to revalidate
// freshness and the reducer route before acting on an observation.
func (b Binding) Valid() bool {
	if b.SourceWindowID <= 0 || b.TargetWindowID <= 0 ||
		(!b.Producer.Valid() && (!b.LegacyDirectManaged || harness.NormalizeKnown(b.Producer.Harness) == "")) {
		return false
	}
	// A hand-built binding that recorded a tab epoch but no pane epochs is
	// partially populated; fail it closed rather than skipping the pane checks.
	if b.SnapshotTabEpoch > 0 &&
		(b.SnapshotTabID <= 0 || b.SnapshotSourceEpoch == 0 || b.SnapshotTargetEpoch == 0) {
		return false
	}
	if b.TerminalProducer && b.RecoverySource.Kind != "" {
		return false
	}
	return b.RecoverySource.Kind == "" || b.RecoverySource.Kind == protocol.SourceProbe
}

// NewBinding derives one binding from a snapshot, failing closed when either
// pane is absent from the captured tree or when the two panes do not share a
// single tab at a single epoch.
func NewBinding(
	snapshot Snapshot,
	producer conversation.Identity,
	sourceWindowID int,
	targetWindowID int,
) (Binding, bool) {
	sourceTabID, sourceTabEpoch, sourceEpoch, ok := snapshot.probeEpochsForWindow(sourceWindowID)
	if !ok {
		return Binding{}, false
	}
	targetTabID, targetTabEpoch, targetEpoch, ok := snapshot.probeEpochsForWindow(targetWindowID)
	if !ok || sourceTabID != targetTabID || sourceTabEpoch != targetTabEpoch {
		return Binding{}, false
	}
	return Binding{
		Producer:            producer,
		SourceWindowID:      sourceWindowID,
		TargetWindowID:      targetWindowID,
		SourceReceipt:       snapshot.Receipt(sourceWindowID),
		TargetReceipt:       snapshot.Receipt(targetWindowID),
		SnapshotTabID:       targetTabID,
		SnapshotTabEpoch:    targetTabEpoch,
		SnapshotSourceEpoch: sourceEpoch,
		SnapshotTargetEpoch: targetEpoch,
	}, true
}

// BindingFresh revalidates the tab epoch plus the epoch of each of the
// binding's own two panes against the currently published snapshot. Presence in
// the tab's pane map is proof of current residency, so no window-tree scan is
// needed. A binding built from a caller-supplied snapshot carries no epochs and
// is always fresh; a cold cache never is.
func (s *SnapshotStore) BindingFresh(binding Binding) bool {
	if binding.SnapshotTabEpoch == 0 {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.snap.ready {
		return false
	}
	state, ok := s.snap.probeTabs[binding.SnapshotTabID]
	if !ok || state.epoch != binding.SnapshotTabEpoch {
		return false
	}
	source, ok := state.windows[binding.SourceWindowID]
	if !ok || source.epoch != binding.SnapshotSourceEpoch {
		return false
	}
	target, ok := state.windows[binding.TargetWindowID]
	return ok && target.epoch == binding.SnapshotTargetEpoch
}
