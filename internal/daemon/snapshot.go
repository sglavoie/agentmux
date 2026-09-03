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
// it returned, the reducer receipts sampled before it started, the socket it
// was read through, and the instant it completed. The four belong together —
// publishing any one of them under a different read's provenance is exactly the
// incoherence this type exists to prevent.
type kittySnapshotCapture struct {
	windows         []kitty.OSWindow
	reducerReceipts map[int]uint64
	// socket is the remote-control endpoint the read actually ran against,
	// taken from the capture rather than from the daemon's current effective
	// socket. The two can differ: h.lastKittyListenOn moves the moment a hook
	// arrives from another Kitty instance, while this read stays bound to the
	// instance whose window IDs it returned.
	socket string
	// at is the completion instant of the underlying read — stamped after
	// ListWindows returns, never before it is called, and shared unchanged with
	// every caller that coalesced onto that read rather than restamped. The
	// loop that asks for a read holds a tick or start time that can be
	// arbitrarily older than the answer — a list wedged behind a slow Kitty
	// would otherwise publish a snapshot that claims to have been captured when
	// it was merely requested, and every freshness measurement downstream would
	// inherit that lie.
	at time.Time
}

// kittyCaptureLister is the coordinated-capture seam a Kitty client offers when
// its reads can coalesce. It is deliberately separate from kittyTitleClient:
// only the real *kitty.SubprocessClient collapses concurrent reads, and a fake
// that answers a list from memory has no other read to join and therefore
// nothing to share provenance with.
type kittyCaptureLister interface {
	ListWindowsCapture(sample func() any) (kitty.ListCapture, error)
}

// captureKittySnapshot performs one coordinated ListWindows through client,
// which the caller has already scoped to socket, and returns its windows, the
// reducer receipts sampled before it started, the endpoint it was read through,
// and its completion time. Every daemon publication path goes through it so the
// stamping rule lives in one place rather than in five loops.
//
// socket is the caller's belief about where the read is going; a coordinated
// client reports where it actually went, and that answer wins. The two agree in
// production, but only the client's answer is derived from the same value the
// `kitten @ --to` argument was built from — and when the daemon has no recorded
// listen address at all, the client's own constructor socket is the read's true
// endpoint while the caller's belief is the empty string.
//
// A failure returns the error and nothing else: the caller publishes nothing,
// which is what leaves the last successful windows, generation, and capture
// time standing. Session 1's per-socket coordinator sits underneath, so a call
// arriving while another subsystem's read of the same socket is in flight joins
// that read instead of starting a second `kitten @ ls`.
//
// Joining is exactly why the receipts are sampled through the coordinator
// rather than here. A waiter arrives after the shared read began, so receipts
// it sampled for itself would be younger than the windows it is handed: a
// reducer event accepted in between would be inside the receipt and outside the
// topology, and screen or transcript evidence gathered against that older tree
// would then pass the current-receipt guard and mutate newer state. The
// coordinator hands every caller the executor's sample instead, so all three
// facts describe one read.
func (h *Handler) captureKittySnapshot(client kittyTitleClient, socket string) (kittySnapshotCapture, error) {
	lister, ok := client.(kittyCaptureLister)
	if !ok {
		// No coalescing underneath, so this caller is always its own executor,
		// and the socket it scoped the client to is the only account of the
		// read's endpoint available.
		reducerReceipts := h.captureSnapshotReducerReceipts()
		windows, err := client.ListWindows()
		if err != nil {
			return kittySnapshotCapture{}, err
		}
		return kittySnapshotCapture{
			windows:         windows,
			reducerReceipts: reducerReceipts,
			socket:          socket,
			at:              time.Now(),
		}, nil
	}
	capture, err := lister.ListWindowsCapture(func() any { return h.captureSnapshotReducerReceipts() })
	if err != nil {
		return kittySnapshotCapture{}, err
	}
	// A nil assertion result is the cold, receipt-free capture a publication
	// already tolerates; it can only arise if the sample never ran, which means
	// there were no receipts to inherit either.
	reducerReceipts, _ := capture.Provenance.(map[int]uint64)
	return kittySnapshotCapture{
		windows:         capture.Windows,
		reducerReceipts: reducerReceipts,
		socket:          capture.Socket,
		at:              capture.CompletedAt,
	}, nil
}

// publishCapture publishes a successful capture under its own completion time
// and its own socket.
func (h *Handler) publishCapture(capture kittySnapshotCapture) {
	h.publishSnapshotFrom(probes.Publication{
		Windows:         capture.windows,
		ReducerReceipts: capture.reducerReceipts,
		Socket:          capture.socket,
		CapturedAt:      capture.at,
	})
}

// publishSnapshot records windows as the current immutable snapshot, captured
// at capturedAt. Production listers publish a kittySnapshotCapture instead; this
// remains the seam for tests and callers that already hold an in-memory tree,
// so the tree is attributed to the daemon's current effective socket — the best
// available answer when no read produced one.
func (h *Handler) publishSnapshot(windows []kitty.OSWindow, capturedAt time.Time) {
	h.publishSnapshotFrom(probes.Publication{
		Windows:         windows,
		ReducerReceipts: h.captureSnapshotReducerReceipts(),
		Socket:          h.currentKittyListenOn(),
		CapturedAt:      capturedAt,
	})
}

// publishSnapshotFrom publishes one completed read: its windows, the reducer
// receipts captured before the ListWindows call that returned them, the socket
// that call ran against, and the instant it completed.
func (h *Handler) publishSnapshotFrom(publication probes.Publication) {
	// Publish the new generation and its mute cache in one state critical
	// section. Probe apply paths hold the same lock while checking gen, so an
	// older observation cannot pass its generation check and then overwrite a
	// mute from a snapshot published in between the check and mutation.
	// The focus observation joins them for the same reason: it is the only path
	// that clears the unseen bit, and clearing it in the same critical section as
	// the publication means a publish path cannot raise the bit against a tree
	// this read has already superseded.
	h.stateMu.Lock()
	h.refreshMutedFromSnapshotLocked(publication.Windows)
	h.refreshFocusFromSnapshotLocked(publication.Windows)
	h.snapshots.Publish(publication)
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
	client, socket := h.kittyListTarget()
	if client == nil {
		return
	}
	capture, err := h.captureKittySnapshot(client, socket)
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
