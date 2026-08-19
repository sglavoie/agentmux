package daemon

import (
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// screenProbeBinding is the immutable routing and freshness evidence shared by
// the periodic screen/transcript adapters. Producer identifies the harness
// session whose external evidence is being read; SourceWindowID is that
// producer pane, while TargetWindowID owns the reducer state that observations
// mutate. Direct managed sessions use the same source and target. Both receipts
// are captured before the Kitty snapshot that supplied the route. SnapshotTabID
// and SnapshotTabEpoch invalidate the binding when the tab's focus, topology,
// title, or pane ownership changes, while SnapshotSourceEpoch and
// SnapshotTargetEpoch invalidate it when either of its own two panes changes —
// so an unrelated pane in the same tab cannot starve it. Most bindings require
// a live producer route; terminalProducer and recoverySource narrowly authorize
// Codex clear and Copilot self-recovery after the ordinary live route has
// closed.
type screenProbeBinding struct {
	Producer            conversation.Identity
	SourceWindowID      int
	TargetWindowID      int
	SourceReceipt       uint64
	TargetReceipt       uint64
	SnapshotTabID       int
	SnapshotTabEpoch    uint64
	SnapshotSourceEpoch uint64
	SnapshotTargetEpoch uint64
	legacyDirectManaged bool
	terminalProducer    bool
	terminalAt          int64
	recoverySource      SourceID
	recoveryAt          int64
}

func (b screenProbeBinding) valid() bool {
	if b.SourceWindowID <= 0 || b.TargetWindowID <= 0 ||
		(!b.Producer.Valid() && (!b.legacyDirectManaged || harness.NormalizeKnown(b.Producer.Harness) == "")) {
		return false
	}
	// A hand-built binding that recorded a tab epoch but no pane epochs is
	// partially populated; fail it closed rather than skipping the pane checks.
	if b.SnapshotTabEpoch > 0 &&
		(b.SnapshotTabID <= 0 || b.SnapshotSourceEpoch == 0 || b.SnapshotTargetEpoch == 0) {
		return false
	}
	if b.terminalProducer && b.recoverySource.Kind != "" {
		return false
	}
	return b.recoverySource.Kind == "" || b.recoverySource.Kind == SourceProbe
}

func newScreenProbeBinding(
	snapshot screenProbeSnapshot,
	producer conversation.Identity,
	sourceWindowID int,
	targetWindowID int,
) (screenProbeBinding, bool) {
	sourceTabID, sourceTabEpoch, sourceEpoch, ok := snapshot.probeEpochsForWindow(sourceWindowID)
	if !ok {
		return screenProbeBinding{}, false
	}
	targetTabID, targetTabEpoch, targetEpoch, ok := snapshot.probeEpochsForWindow(targetWindowID)
	if !ok || sourceTabID != targetTabID || sourceTabEpoch != targetTabEpoch {
		return screenProbeBinding{}, false
	}
	return screenProbeBinding{
		Producer:            producer,
		SourceWindowID:      sourceWindowID,
		TargetWindowID:      targetWindowID,
		SourceReceipt:       snapshot.receipt(sourceWindowID),
		TargetReceipt:       snapshot.receipt(targetWindowID),
		SnapshotTabID:       targetTabID,
		SnapshotTabEpoch:    targetTabEpoch,
		SnapshotSourceEpoch: sourceEpoch,
		SnapshotTargetEpoch: targetEpoch,
	}, true
}

// screenProbeBindings derives producer/source/target routes from the reducers
// attached to managed panes in the supplied Kitty snapshot. A routed producer
// remains eligible after focus moves because its reducer fact is the route
// lease; direct managed producers retain the historical title-owner-only
// behavior. The legacy fallback preserves direct managed probes that have title
// metadata but no in-process session fact yet.
func (h *Handler) screenProbeBindings(snapshot screenProbeSnapshot, producerHarness string) []screenProbeBinding {
	producerHarness = harness.NormalizeKnown(producerHarness)
	if producerHarness == "" {
		return nil
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	var bindings []screenProbeBinding
	for i := range snapshot.windows {
		for j := range snapshot.windows[i].Tabs {
			tab := &snapshot.windows[i].Tabs[j]
			owner, _ := kitty.FindTabTitleOwner(tab)
			ownerHasBinding := false
			for k := range tab.Windows {
				target := &tab.Windows[k]
				if target.UserVars.EffectiveProfile() == "" && target.UserVars.EffectiveHarness() == "" {
					continue
				}
				st, ok := h.windowStateLocked(target.ID)
				if !ok || st == nil || st.Reducer == nil {
					continue
				}
				identity, sourceWindowID, ok := st.Reducer.CurrentLiveProducerRoute(producerHarness, target.ID)
				if !ok {
					continue
				}
				// Direct managed sessions keep the historical behavior: only the
				// current tab-title owner is probed. Borrowed sessions are retained
				// by their live reducer binding even when focus moves elsewhere.
				if sourceWindowID == target.ID {
					if owner == nil || owner.ID != target.ID || target.UserVars.EffectiveHarness() != producerHarness {
						continue
					}
				}
				binding, found := newScreenProbeBinding(snapshot, identity, sourceWindowID, target.ID)
				if !found {
					continue
				}
				if h.screenProbeBindingCurrentLocked(binding) {
					bindings = append(bindings, binding)
					if owner != nil && owner.ID == target.ID {
						ownerHasBinding = true
					}
				}
			}

			// Preserve direct title-owner probing for a freshly restored or
			// otherwise legacy managed producer pane that has not yet established
			// an in-process session fact. Routed bindings never use this fallback.
			if owner != nil && !ownerHasBinding && owner.UserVars.EffectiveHarness() == producerHarness {
				if st, ok := h.windowStateLocked(owner.ID); ok && st != nil && st.LastHarness != "" &&
					harness.NormalizeKnown(st.LastHarness) != producerHarness {
					continue
				}
				binding, found := newScreenProbeBinding(
					snapshot,
					conversation.Identity{Harness: producerHarness},
					owner.ID,
					owner.ID,
				)
				if !found {
					continue
				}
				binding.legacyDirectManaged = true
				if h.screenProbeBindingCurrentLocked(binding) {
					bindings = append(bindings, binding)
				}
			}
		}
	}
	// A producer/source tuple must resolve to exactly one managed target. This
	// mirrors ordinary hook routing's fail-closed ambiguity rule and prevents a
	// stale duplicate fact from fanning one screen observation into two panes.
	type routeKey struct {
		producer conversation.Identity
		source   int
	}
	counts := make(map[routeKey]int, len(bindings))
	for _, binding := range bindings {
		if !binding.legacyDirectManaged {
			counts[routeKey{producer: binding.Producer, source: binding.SourceWindowID}]++
		}
	}
	filtered := bindings[:0]
	for _, binding := range bindings {
		if binding.legacyDirectManaged || counts[routeKey{producer: binding.Producer, source: binding.SourceWindowID}] == 1 {
			filtered = append(filtered, binding)
		}
	}
	return filtered
}

// screenProbeTerminalBindings derives exact routed terminal bindings for
// adapters that need one final piece of post-terminal evidence. It deliberately
// excludes direct sessions (the legacy direct-managed fallback already covers
// their title-owner behavior) and does not make a terminal fact a live route for
// ordinary hook traffic.
func (h *Handler) screenProbeTerminalBindings(snapshot screenProbeSnapshot, producerHarness string, now int64) []screenProbeBinding {
	producerHarness = harness.NormalizeKnown(producerHarness)
	if producerHarness == "" {
		return nil
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	var bindings []screenProbeBinding
	for i := range snapshot.windows {
		for j := range snapshot.windows[i].Tabs {
			tab := &snapshot.windows[i].Tabs[j]
			for k := range tab.Windows {
				target := &tab.Windows[k]
				if target.UserVars.EffectiveProfile() == "" && target.UserVars.EffectiveHarness() == "" {
					continue
				}
				st, ok := h.windowStateLocked(target.ID)
				if !ok || st == nil || st.Reducer == nil {
					continue
				}
				identity, sourceWindowID, ok := st.Reducer.CurrentTerminalProducerRoute(producerHarness, target.ID, now)
				if !ok || sourceWindowID == target.ID {
					continue
				}
				binding, found := newScreenProbeBinding(snapshot, identity, sourceWindowID, target.ID)
				if !found {
					continue
				}
				binding.terminalProducer = true
				binding.terminalAt = now
				if h.screenProbeBindingCurrentLocked(binding) {
					bindings = append(bindings, binding)
				}
			}
		}
	}

	type routeKey struct {
		producer conversation.Identity
		source   int
	}
	counts := make(map[routeKey]int, len(bindings))
	for _, binding := range bindings {
		counts[routeKey{producer: binding.Producer, source: binding.SourceWindowID}]++
	}
	filtered := bindings[:0]
	for _, binding := range bindings {
		if counts[routeKey{producer: binding.Producer, source: binding.SourceWindowID}] == 1 {
			filtered = append(filtered, binding)
		}
	}
	return filtered
}

// screenProbeBindingCurrentLocked revalidates every causal component of a
// binding. Callers hold stateMu and keep it held through any ensuing mutation.
func (h *Handler) screenProbeBindingCurrentLocked(binding screenProbeBinding) bool {
	if !binding.valid() || !h.screenProbeFreshnessCurrentLocked(binding) ||
		!h.windowReceiptCurrentLocked(binding.SourceWindowID, binding.SourceReceipt) ||
		!h.windowReceiptCurrentLocked(binding.TargetWindowID, binding.TargetReceipt) {
		return false
	}
	if binding.legacyDirectManaged {
		if binding.SourceWindowID != binding.TargetWindowID {
			return false
		}
		st, ok := h.windowStateLocked(binding.TargetWindowID)
		return !ok || st == nil || st.LastHarness == "" ||
			harness.NormalizeKnown(st.LastHarness) == harness.NormalizeKnown(binding.Producer.Harness)
	}
	st, ok := h.windowStateLocked(binding.TargetWindowID)
	if !ok || st == nil || st.Reducer == nil {
		return false
	}
	if binding.recoverySource.Kind != "" {
		identity, sourceWindowID, ok := st.Reducer.ProbeStopRecoveryRoute(
			binding.recoverySource, binding.Producer.Harness, binding.TargetWindowID, binding.recoveryAt,
		)
		return ok && identity == binding.Producer && sourceWindowID == binding.SourceWindowID
	}
	if binding.terminalProducer {
		return st.Reducer.HasTerminalProducerRoute(
			binding.Producer, binding.SourceWindowID, binding.TargetWindowID, binding.terminalAt,
		)
	}
	return st.Reducer.HasLiveProducerRoute(binding.Producer, binding.SourceWindowID, binding.TargetWindowID)
}

// screenProbeFreshnessCurrentLocked revalidates the tab epoch plus the epoch of
// each of the binding's own two panes. Presence in the tab's pane map is proof
// of current residency, so no window-tree scan is needed.
func (h *Handler) screenProbeFreshnessCurrentLocked(binding screenProbeBinding) bool {
	if binding.SnapshotTabEpoch == 0 {
		return true
	}
	h.snapshots.mu.RLock()
	defer h.snapshots.mu.RUnlock()
	if !h.snapshots.snap.ready {
		return false
	}
	state, ok := h.snapshots.snap.probeTabs[binding.SnapshotTabID]
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

// screenProbeBindingEligibilityCurrentLocked adds the target's current reduced
// icon to exact binding validation. fallbackIcon is used only for the legacy
// direct-managed case where no in-process state exists yet.
func (h *Handler) screenProbeBindingEligibilityCurrentLocked(
	binding screenProbeBinding,
	fallbackIcon string,
	eligible func(string) bool,
) bool {
	if !h.screenProbeBindingCurrentLocked(binding) {
		return false
	}
	st, ok := h.windowStateLocked(binding.TargetWindowID)
	if !ok || st == nil {
		return binding.legacyDirectManaged && eligible(fallbackIcon)
	}
	return eligible(st.Effective.Icon)
}
