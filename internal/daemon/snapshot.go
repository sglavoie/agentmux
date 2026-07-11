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
	s.mu.Lock()
	s.snap.windows = windows
	s.snap.gen++
	s.snap.at = now
	s.snap.ready = true
	s.mu.Unlock()
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
	h.snapshots.publish(windows, now)
}

// cachedSnapshot returns the last published Kitty snapshot without any Kitty
// I/O. ready is false until the initial restore has published once.
func (h *Handler) cachedSnapshot() (windows []kitty.OSWindow, gen uint64, ready bool) {
	return h.snapshots.load()
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
	windows, err := client.ListWindows()
	if err != nil {
		return
	}
	h.publishSnapshot(windows, now)
}
