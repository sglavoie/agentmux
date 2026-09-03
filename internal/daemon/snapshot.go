package daemon

import (
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// This file holds the Handler's seams onto the immutable Kitty snapshot cache
// in internal/daemon/probes. The cache itself, its freshness epochs, and the
// probe-facing projection live there; what stays here is everything that needs
// the Handler: sampling reducer receipts under stateMu, republishing the mute
// cache in the same critical section as a publication, and the one best-effort
// ListWindows the daemon performs at client-injection time.

// captureSnapshotReducerReceipts samples every in-process window receipt
// before a potentially slow ListWindows call. The returned map is immutable
// once published with the resulting Kitty evidence.
func (h *Handler) captureSnapshotReducerReceipts() map[int]uint64 {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	receipts := make(map[int]uint64, len(h.store))
	for windowID, st := range h.store {
		if st != nil {
			receipts[windowID] = st.ShadowSeq
		}
	}
	return receipts
}

// kittySnapshotCapture is one successful coordinated ListWindows: the windows
// it returned, the reducer receipts sampled before it started, and the instant
// it completed. The three belong together — publishing any one of them under a
// different read's provenance is exactly the incoherence this type exists to
// prevent.
type kittySnapshotCapture struct {
	windows         []kitty.OSWindow
	reducerReceipts map[int]uint64
	// at is stamped after ListWindows returns, never before it is called. The
	// loop that asks for a read holds a tick or start time that can be
	// arbitrarily older than the answer — a list wedged behind a slow Kitty
	// would otherwise publish a snapshot that claims to have been captured when
	// it was merely requested, and every freshness measurement downstream would
	// inherit that lie.
	at time.Time
}

// captureKittySnapshot performs one coordinated ListWindows and stamps its
// completion time. Every daemon publication path goes through it so the
// stamping rule lives in one place rather than in five loops.
//
// A failure returns the error and nothing else: the caller publishes nothing,
// which is what leaves the last successful windows, generation, and capture
// time standing. Session 1's per-socket coordinator sits underneath, so a call
// arriving while another subsystem's read of the same socket is in flight joins
// that read instead of starting a second `kitten @ ls`.
func (h *Handler) captureKittySnapshot(client kittyTitleClient) (kittySnapshotCapture, error) {
	reducerReceipts := h.captureSnapshotReducerReceipts()
	windows, err := client.ListWindows()
	if err != nil {
		return kittySnapshotCapture{}, err
	}
	return kittySnapshotCapture{windows: windows, reducerReceipts: reducerReceipts, at: time.Now()}, nil
}

// publishCapture publishes a successful capture under its own completion time.
func (h *Handler) publishCapture(capture kittySnapshotCapture) {
	h.publishSnapshotWithReceipts(capture.windows, capture.reducerReceipts, capture.at)
}

// publishSnapshot records windows as the current immutable snapshot, captured
// at capturedAt. Production listers publish a kittySnapshotCapture instead; this
// remains the seam for tests and callers that already hold an in-memory tree.
func (h *Handler) publishSnapshot(windows []kitty.OSWindow, capturedAt time.Time) {
	h.publishSnapshotWithReceipts(windows, h.captureSnapshotReducerReceipts(), capturedAt)
}

// publishSnapshotWithReceipts publishes windows with reducer receipts captured
// before the ListWindows call that returned them, stamped with the instant that
// call completed.
func (h *Handler) publishSnapshotWithReceipts(windows []kitty.OSWindow, reducerReceipts map[int]uint64, capturedAt time.Time) {
	// Publish the new generation and its mute cache in one state critical
	// section. Probe apply paths hold the same lock while checking gen, so an
	// older observation cannot pass its generation check and then overwrite a
	// mute from a snapshot published in between the check and mutation.
	h.stateMu.Lock()
	h.refreshMutedFromSnapshotLocked(windows)
	h.snapshots.PublishWithReceipts(windows, reducerReceipts, capturedAt)
	h.stateMu.Unlock()
}

// cachedSnapshot returns the last published Kitty snapshot without any Kitty
// I/O. ready is false until the initial restore has published once.
func (h *Handler) cachedSnapshot() (windows []kitty.OSWindow, gen uint64, ready bool) {
	return h.snapshots.Load()
}

// cachedSnapshotView returns the last published snapshot together with the
// generation, capture time, and readiness that describe it, read as one value
// so a consumer cannot pair facts from two different publications.
func (h *Handler) cachedSnapshotView() probes.SnapshotView {
	return h.snapshots.View()
}

// cachedScreenProbeSnapshot returns the last published external evidence and
// its causally earlier reducer receipts without any Kitty I/O.
func (h *Handler) cachedScreenProbeSnapshot() (probes.Snapshot, bool) {
	return h.snapshots.ScreenProbeSnapshot()
}

// primeSnapshot performs one best-effort ListWindows through the injected client
// and publishes the result under its completion time. It is used at client
// injection time (and by tests) so the hook path has an immediate cache to route
// against; a list error leaves the cache cold and the maintenance loop
// reconciles on its next tick. It never returns an error because a cold cache is
// a supported degraded state, not a failure.
func (h *Handler) primeSnapshot() {
	client := h.kittyClientForList()
	if client == nil {
		return
	}
	capture, err := h.captureKittySnapshot(client)
	if err != nil {
		return
	}
	h.publishCapture(capture)
}

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
