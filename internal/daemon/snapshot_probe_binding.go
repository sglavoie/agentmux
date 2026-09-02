package daemon

import (
	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// The binding value and its snapshot-independent validity rules live in
// internal/daemon/probes. What stays here is the half that needs the reducer:
// deriving routes from the facts attached to managed panes, and revalidating a
// binding against the reducer under stateMu before an observation is applied.

// screenProbeBindings derives producer/source/target routes from the reducers
// attached to managed panes in the supplied Kitty snapshot. A routed producer
// remains eligible after focus moves because its reducer fact is the route
// lease; direct managed producers retain the historical title-owner-only
// behavior. The legacy fallback preserves direct managed probes that have title
// metadata but no in-process session fact yet.
func (h *Handler) screenProbeBindings(snapshot probes.Snapshot, producerHarness string) []probes.Binding {
	producerHarness = harness.NormalizeKnown(producerHarness)
	if producerHarness == "" {
		return nil
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	windows := snapshot.Windows()
	var bindings []probes.Binding
	for i := range windows {
		for j := range windows[i].Tabs {
			tab := &windows[i].Tabs[j]
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
				binding, found := probes.NewBinding(snapshot, identity, sourceWindowID, target.ID)
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
				binding, found := probes.NewBinding(
					snapshot,
					conversation.Identity{Harness: producerHarness},
					owner.ID,
					owner.ID,
				)
				if !found {
					continue
				}
				binding.LegacyDirectManaged = true
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
		if !binding.LegacyDirectManaged {
			counts[routeKey{producer: binding.Producer, source: binding.SourceWindowID}]++
		}
	}
	filtered := bindings[:0]
	for _, binding := range bindings {
		if binding.LegacyDirectManaged || counts[routeKey{producer: binding.Producer, source: binding.SourceWindowID}] == 1 {
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
func (h *Handler) screenProbeTerminalBindings(snapshot probes.Snapshot, producerHarness string, now int64) []probes.Binding {
	producerHarness = harness.NormalizeKnown(producerHarness)
	if producerHarness == "" {
		return nil
	}

	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	windows := snapshot.Windows()
	var bindings []probes.Binding
	for i := range windows {
		for j := range windows[i].Tabs {
			tab := &windows[i].Tabs[j]
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
				binding, found := probes.NewBinding(snapshot, identity, sourceWindowID, target.ID)
				if !found {
					continue
				}
				binding.TerminalProducer = true
				binding.TerminalAt = now
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
func (h *Handler) screenProbeBindingCurrentLocked(binding probes.Binding) bool {
	if !binding.Valid() || !h.snapshots.BindingFresh(binding) ||
		!h.windowReceiptCurrentLocked(binding.SourceWindowID, binding.SourceReceipt) ||
		!h.windowReceiptCurrentLocked(binding.TargetWindowID, binding.TargetReceipt) {
		return false
	}
	if binding.LegacyDirectManaged {
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
	if binding.RecoverySource.Kind != "" {
		identity, sourceWindowID, ok := st.Reducer.ProbeStopRecoveryRoute(
			binding.RecoverySource, binding.Producer.Harness, binding.TargetWindowID, binding.RecoveryAt,
		)
		return ok && identity == binding.Producer && sourceWindowID == binding.SourceWindowID
	}
	if binding.TerminalProducer {
		return st.Reducer.HasTerminalProducerRoute(
			binding.Producer, binding.SourceWindowID, binding.TargetWindowID, binding.TerminalAt,
		)
	}
	return st.Reducer.HasLiveProducerRoute(binding.Producer, binding.SourceWindowID, binding.TargetWindowID)
}

// screenProbeBindingEligibilityCurrentLocked adds the target's current reduced
// icon to exact binding validation. fallbackIcon is used only for the legacy
// direct-managed case where no in-process state exists yet.
func (h *Handler) screenProbeBindingEligibilityCurrentLocked(
	binding probes.Binding,
	fallbackIcon string,
	eligible func(string) bool,
) bool {
	if !h.screenProbeBindingCurrentLocked(binding) {
		return false
	}
	st, ok := h.windowStateLocked(binding.TargetWindowID)
	if !ok || st == nil {
		return binding.LegacyDirectManaged && eligible(fallbackIcon)
	}
	return eligible(st.Effective.Icon)
}
