package daemon

import (
	"reflect"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/conversation"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// This file introduces the daemon's immutable Kitty snapshot cache. Hook
// processing must never call ListWindows (a slow or wedged Kitty remote-control
// subprocess would otherwise delay hook acknowledgement and reorder state
// application). Instead the maintenance/render loop is the single lister: each
// tick it publishes the freshly listed windows here, and hook routing reads the
// last published snapshot without any Kitty I/O.
//
// The snapshot is treated as immutable: publishSnapshot stores the slice
// produced by a single ListWindows call (the real client and the test fake both
// return freshly built, unshared data) and consumers only ever read it. Nothing
// mutates a published snapshot in place, so the read path needs no defensive
// deep copy and never blocks on Kitty.

// kittySnapshot is one published view of the Kitty window tree plus the
// bookkeeping the read path needs to reason about freshness and readiness.
type kittySnapshot struct {
	windows []kitty.OSWindow
	// reducerReceipts binds the external window-tree evidence to the daemon's
	// reducer generation at the start of the ListWindows capture that produced
	// it. Screen/transcript probes compare the per-window receipt before doing
	// any external read, so a reducer event accepted after this snapshot began
	// cannot be treated as part of the older eligibility evidence.
	reducerReceipts map[int]uint64
	// gen increments on every publish so a consumer can detect that the world
	// changed between two reads without diffing the whole tree.
	gen uint64
	// probeTabs carries a separate freshness epoch for each Kitty tab. Repeated
	// maintenance/render publications preserve an unchanged tab's epoch, so
	// activity in one tab cannot starve screen probes in another. A tab-local
	// focus, topology, mute, title, ownership, or foreground-command change
	// advances only that tab's epoch.
	probeTabs map[int]screenProbeTabState
	// at is the wall-clock time the snapshot was captured, used only for
	// diagnostics/staleness reporting.
	at time.Time
	// ready is false until the first successful publish. A cold (never
	// published) cache means the daemon has not yet completed its initial
	// restore; the hook path treats that as "cannot see the tab" so a wipe
	// falls back to the source pane rather than silently dropping.
	ready bool
}

// snapshotStore holds the current immutable Kitty snapshot behind a
// reader/writer lock. Reads dominate (every hook resolves against it) while
// writes happen once per render interval, so an RWMutex keeps concurrent hook
// routing off the writer's back.
type snapshotStore struct {
	mu             sync.RWMutex
	snap           kittySnapshot
	nextProbeEpoch uint64
}

// screenProbeTabState is the immutable, probe-relevant projection of one
// Kitty tab plus its monotonic freshness epoch.
type screenProbeTabState struct {
	epoch     uint64
	signature screenProbeTabSignature
}

type screenProbeTabSignature struct {
	osWindowID       int
	osWindowActive   bool
	osWindowFocused  bool
	osWindowLastUsed bool
	tabActive        bool
	title            string
	windows          []screenProbeWindowSignature
}

type screenProbeWindowSignature struct {
	id                    int
	active                bool
	effectiveProfile      string
	effectiveHarness      string
	mute                  string
	cmdline               []string
	foregroundProcessArgs [][]string
}

// publish replaces the cached snapshot with windows captured at now. The slice
// is stored by reference and must not be mutated by the caller afterward.
func (s *snapshotStore) publish(windows []kitty.OSWindow, now time.Time) {
	s.publishWithReceipts(windows, nil, now)
}

func (s *snapshotStore) publishWithReceipts(windows []kitty.OSWindow, reducerReceipts map[int]uint64, now time.Time) {
	s.mu.Lock()
	probeTabs := make(map[int]screenProbeTabState)
	for i := range windows {
		osWindow := &windows[i]
		for j := range osWindow.Tabs {
			tab := &osWindow.Tabs[j]
			state := screenProbeTabState{
				signature: screenProbeSignature(osWindow, tab),
			}
			if previous, ok := s.snap.probeTabs[tab.ID]; ok &&
				reflect.DeepEqual(previous.signature, state.signature) {
				state.epoch = previous.epoch
			} else {
				s.nextProbeEpoch++
				state.epoch = s.nextProbeEpoch
			}
			probeTabs[tab.ID] = state
		}
	}
	s.snap.windows = windows
	s.snap.reducerReceipts = reducerReceipts
	s.snap.gen++
	s.snap.probeTabs = probeTabs
	s.snap.at = now
	s.snap.ready = true
	s.mu.Unlock()
}

func screenProbeSignature(osWindow *kitty.OSWindow, tab *kitty.Tab) screenProbeTabSignature {
	signature := screenProbeTabSignature{
		osWindowID:       osWindow.ID,
		osWindowActive:   osWindow.IsActive,
		osWindowFocused:  osWindow.IsFocused,
		osWindowLastUsed: osWindow.LastFocused,
		tabActive:        tab.IsActive,
		title:            tab.Title,
		windows:          make([]screenProbeWindowSignature, 0, len(tab.Windows)),
	}
	for i := range tab.Windows {
		win := &tab.Windows[i]
		windowSignature := screenProbeWindowSignature{
			id:               win.ID,
			active:           win.IsActive,
			effectiveProfile: win.UserVars.EffectiveProfile(),
			effectiveHarness: win.UserVars.EffectiveHarness(),
			mute:             win.UserVars.Mute,
			cmdline:          win.Cmdline,
		}
		for j := range win.ForegroundProcesses {
			windowSignature.foregroundProcessArgs = append(
				windowSignature.foregroundProcessArgs,
				win.ForegroundProcesses[j].Cmdline,
			)
		}
		signature.windows = append(signature.windows, windowSignature)
	}
	return signature
}

// loadForScreenProbes returns the immutable window tree together with the
// reducer receipts captured before the ListWindows call that produced it and
// the publication generation that owns both. Both the slice and map are
// read-only after publication.
func (s *snapshotStore) loadForScreenProbes() (
	windows []kitty.OSWindow,
	reducerReceipts map[int]uint64,
	gen uint64,
	probeTabs map[int]screenProbeTabState,
	ready bool,
) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap.windows, s.snap.reducerReceipts, s.snap.gen, s.snap.probeTabs, s.snap.ready
}

// screenProbeSnapshot is the causally bound evidence consumed by every
// periodic screen/transcript adapter. receipt returns zero for a window that
// had no in-process reducer state when capture began; creating state in the
// meantime necessarily advances the current receipt past zero.
type screenProbeSnapshot struct {
	windows         []kitty.OSWindow
	reducerReceipts map[int]uint64
	probeTabs       map[int]screenProbeTabState
	// gen binds every cached observation to the Kitty snapshot that supplied
	// it. Probe bindings use the narrower per-tab epochs above; gen remains the
	// publication counter exposed by the general snapshot cache. A zero
	// generation denotes a direct caller-supplied snapshot.
	gen uint64
}

func (s screenProbeSnapshot) receipt(windowID int) uint64 {
	return s.reducerReceipts[windowID]
}

func (s screenProbeSnapshot) tabEpochForWindow(windowID int) (tabID int, epoch uint64, ok bool) {
	_, tab, _, found := kitty.FindWindowLocationByID(s.windows, windowID)
	if !found {
		return 0, 0, false
	}
	if s.gen == 0 {
		return tab.ID, 0, true
	}
	state, found := s.probeTabs[tab.ID]
	if !found || state.epoch == 0 {
		return 0, 0, false
	}
	return tab.ID, state.epoch, true
}

// screenProbeBinding is the immutable routing and freshness evidence shared by
// the periodic screen/transcript adapters. Producer identifies the harness
// session whose external evidence is being read; SourceWindowID is that
// producer pane, while TargetWindowID owns the reducer state that observations
// mutate. Direct managed sessions use the same source and target. Both receipts
// are captured before the Kitty snapshot that supplied the route. SnapshotTabID
// and SnapshotTabEpoch invalidate the complete binding when that tab's focus,
// topology, mute, ownership, or foreground command changes without allowing
// unrelated tab publications to starve it. Most bindings require a live
// producer route; terminalProducer and recoverySource narrowly authorize Codex
// clear and Copilot self-recovery after the ordinary live route has closed.
type screenProbeBinding struct {
	Producer            conversation.Identity
	SourceWindowID      int
	TargetWindowID      int
	SourceReceipt       uint64
	TargetReceipt       uint64
	SnapshotTabID       int
	SnapshotTabEpoch    uint64
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
	if b.SnapshotTabEpoch > 0 && b.SnapshotTabID <= 0 {
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
	sourceTabID, sourceEpoch, ok := snapshot.tabEpochForWindow(sourceWindowID)
	if !ok {
		return screenProbeBinding{}, false
	}
	targetTabID, targetEpoch, ok := snapshot.tabEpochForWindow(targetWindowID)
	if !ok || sourceTabID != targetTabID || sourceEpoch != targetEpoch {
		return screenProbeBinding{}, false
	}
	return screenProbeBinding{
		Producer:         producer,
		SourceWindowID:   sourceWindowID,
		TargetWindowID:   targetWindowID,
		SourceReceipt:    snapshot.receipt(sourceWindowID),
		TargetReceipt:    snapshot.receipt(targetWindowID),
		SnapshotTabID:    targetTabID,
		SnapshotTabEpoch: targetEpoch,
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
	if !binding.valid() || !h.screenProbeTabCurrentLocked(binding) ||
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

func (h *Handler) screenProbeTabCurrentLocked(binding screenProbeBinding) bool {
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
	_, sourceTab, _, sourceFound := kitty.FindWindowLocationByID(
		h.snapshots.snap.windows,
		binding.SourceWindowID,
	)
	_, targetTab, _, targetFound := kitty.FindWindowLocationByID(
		h.snapshots.snap.windows,
		binding.TargetWindowID,
	)
	return sourceFound && targetFound &&
		sourceTab.ID == binding.SnapshotTabID &&
		targetTab.ID == binding.SnapshotTabID
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

// load returns the current snapshot's windows, its generation, and whether any
// snapshot has been published yet. The returned slice is read-only; callers must
// not mutate it.
func (s *snapshotStore) load() (windows []kitty.OSWindow, gen uint64, ready bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap.windows, s.snap.gen, s.snap.ready
}

// publishSnapshot records windows as the current immutable snapshot. It is the
// single seam the render/restore loop uses; the hook path only ever reads via
// cachedSnapshot.
func (h *Handler) publishSnapshot(windows []kitty.OSWindow, now time.Time) {
	h.publishSnapshotWithReceipts(windows, h.captureSnapshotReducerReceipts(), now)
}

// publishSnapshotWithReceipts publishes windows with reducer receipts captured
// before the ListWindows call that returned them. Production listers use this
// seam; publishSnapshot remains a convenient atomic-now wrapper for tests and
// callers that already hold an in-memory tree.
func (h *Handler) publishSnapshotWithReceipts(windows []kitty.OSWindow, reducerReceipts map[int]uint64, now time.Time) {
	// Publish the new generation and its mute cache in one state critical
	// section. Probe apply paths hold the same lock while checking gen, so an
	// older observation cannot pass its generation check and then overwrite a
	// mute from a snapshot published in between the check and mutation.
	h.stateMu.Lock()
	h.refreshMutedFromSnapshotLocked(windows)
	h.snapshots.publishWithReceipts(windows, reducerReceipts, now)
	h.stateMu.Unlock()
}

// cachedSnapshot returns the last published Kitty snapshot without any Kitty
// I/O. ready is false until the initial restore has published once.
func (h *Handler) cachedSnapshot() (windows []kitty.OSWindow, gen uint64, ready bool) {
	return h.snapshots.load()
}

// cachedScreenProbeSnapshot returns the last published external evidence and
// its causally earlier reducer receipts without any Kitty I/O.
func (h *Handler) cachedScreenProbeSnapshot() (screenProbeSnapshot, bool) {
	windows, receipts, gen, probeTabs, ready := h.snapshots.loadForScreenProbes()
	return screenProbeSnapshot{
		windows:         windows,
		reducerReceipts: receipts,
		probeTabs:       probeTabs,
		gen:             gen,
	}, ready
}

// screenProbeSnapshotForWindows binds an already-available window tree to the
// current reducer receipts. Direct probe wrappers and tests use it when they
// supply their own ListWindows result instead of consuming the shared cache.
func (h *Handler) screenProbeSnapshotForWindows(windows []kitty.OSWindow) screenProbeSnapshot {
	return screenProbeSnapshot{windows: windows, reducerReceipts: h.captureSnapshotReducerReceipts()}
}

// primeSnapshot performs one best-effort ListWindows through the injected client
// and publishes the result. It is used at client injection time (and by tests)
// so the hook path has an immediate cache to route against; a list error leaves
// the cache cold and the maintenance loop reconciles on its next tick. It never
// returns an error because a cold cache is a supported degraded state, not a
// failure.
func (h *Handler) primeSnapshot(now time.Time) {
	client := h.kittyClientForList()
	if client == nil {
		return
	}
	reducerReceipts := h.captureSnapshotReducerReceipts()
	windows, err := client.ListWindows()
	if err != nil {
		return
	}
	h.publishSnapshotWithReceipts(windows, reducerReceipts, now)
}
