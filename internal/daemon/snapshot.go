package daemon

import (
	"sync"
	"time"

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
	mu   sync.RWMutex
	snap kittySnapshot
}

// publish replaces the cached snapshot with windows captured at now. The slice
// is stored by reference and must not be mutated by the caller afterward.
func (s *snapshotStore) publish(windows []kitty.OSWindow, now time.Time) {
	s.publishWithReceipts(windows, nil, now)
}

func (s *snapshotStore) publishWithReceipts(windows []kitty.OSWindow, reducerReceipts map[int]uint64, now time.Time) {
	s.mu.Lock()
	s.snap.windows = windows
	s.snap.reducerReceipts = reducerReceipts
	s.snap.gen++
	s.snap.at = now
	s.snap.ready = true
	s.mu.Unlock()
}

// loadForScreenProbes returns the immutable window tree together with the
// reducer receipts captured before the ListWindows call that produced it and
// the publication generation that owns both. Both the slice and map are
// read-only after publication.
func (s *snapshotStore) loadForScreenProbes() (windows []kitty.OSWindow, reducerReceipts map[int]uint64, gen uint64, ready bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap.windows, s.snap.reducerReceipts, s.snap.gen, s.snap.ready
}

// screenProbeSnapshot is the causally bound evidence consumed by every
// periodic screen/transcript adapter. receipt returns zero for a window that
// had no in-process reducer state when capture began; creating state in the
// meantime necessarily advances the current receipt past zero.
type screenProbeSnapshot struct {
	windows         []kitty.OSWindow
	reducerReceipts map[int]uint64
	// gen binds every cached observation to the Kitty snapshot that supplied
	// its routing, title mute, and cc_mute evidence. A zero generation denotes
	// a direct caller-supplied snapshot rather than the shared cache.
	gen uint64
}

func (s screenProbeSnapshot) receipt(windowID int) uint64 {
	return s.reducerReceipts[windowID]
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
	windows, receipts, gen, ready := h.snapshots.loadForScreenProbes()
	return screenProbeSnapshot{windows: windows, reducerReceipts: receipts, gen: gen}, ready
}

// screenProbeSnapshotForWindows binds an already-available window tree to the
// current reducer receipts. Direct probe wrappers and tests use it when they
// supply their own ListWindows result instead of consuming the shared cache.
func (h *Handler) screenProbeSnapshotForWindows(windows []kitty.OSWindow) screenProbeSnapshot {
	return screenProbeSnapshot{windows: windows, reducerReceipts: h.captureSnapshotReducerReceipts()}
}

// screenProbeGenerationCurrentLocked reports whether cached probe evidence is
// still owned by the latest published Kitty snapshot. Callers hold stateMu;
// publishSnapshotWithReceipts holds that same lock while incrementing the
// generation and refreshing mute state, making this check atomic with every
// subsequent state mutation. Generation zero is reserved for direct snapshots
// that did not come from the shared cache.
func (h *Handler) screenProbeGenerationCurrentLocked(capturedGen uint64) bool {
	if capturedGen == 0 {
		return true
	}
	_, currentGen, ready := h.snapshots.load()
	return ready && currentGen == capturedGen
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
