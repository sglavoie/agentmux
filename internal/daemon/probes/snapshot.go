package probes

import (
	"reflect"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// This file holds the daemon's immutable Kitty snapshot cache. Hook processing
// must never call ListWindows (a slow or wedged Kitty remote-control subprocess
// would otherwise delay hook acknowledgement and reorder state application).
// Instead the maintenance/render loop is the single lister: each tick it
// publishes the freshly listed windows here, and hook routing reads the last
// published snapshot without any Kitty I/O.
//
// The snapshot is treated as immutable: PublishWithReceipts stores the slice
// produced by a single ListWindows call (the real client and the test fake both return
// freshly built, unshared data) and consumers only ever read it. Nothing mutates
// a published snapshot in place, so the read path needs no defensive deep copy
// and never blocks on Kitty.

// snapshot is one published view of the Kitty window tree plus the bookkeeping
// the read path needs to reason about freshness and readiness.
type snapshot struct {
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
	// probeTabs carries a separate freshness epoch for each Kitty tab plus one
	// for each pane inside it. Repeated maintenance/render publications preserve
	// an unchanged epoch, so activity in one tab cannot starve screen probes in
	// another, and a busy plain split cannot starve the managed pane beside it.
	probeTabs map[int]tabState
	// at is the wall-clock time the snapshot was captured, used only for
	// diagnostics/staleness reporting.
	at time.Time
	// ready is false until the first successful publish. A cold (never
	// published) cache means the daemon has not yet completed its initial
	// restore; the hook path treats that as "cannot see the tab" so a wipe
	// falls back to the source pane rather than silently dropping.
	ready bool
}

// SnapshotStore holds the current immutable Kitty snapshot behind a
// reader/writer lock. Reads dominate (every hook resolves against it) while
// writes happen once per render interval, so an RWMutex keeps concurrent hook
// routing off the writer's back. The zero value is a cold cache.
type SnapshotStore struct {
	mu             sync.RWMutex
	snap           snapshot
	nextProbeEpoch uint64
}

// publish replaces the cached snapshot with windows captured at now. The slice
// is stored by reference and must not be mutated by the caller afterward. The
// daemon always has receipts to publish with, so this atomic-now wrapper exists
// only for the epoch tests below.
func (s *SnapshotStore) publish(windows []kitty.OSWindow, now time.Time) {
	s.PublishWithReceipts(windows, nil, now)
}

// PublishWithReceipts publishes windows together with the reducer receipts
// sampled before the ListWindows call that returned them.
func (s *SnapshotStore) PublishWithReceipts(windows []kitty.OSWindow, reducerReceipts map[int]uint64, now time.Time) {
	s.mu.Lock()
	probeTabs := make(map[int]tabState)
	for i := range windows {
		osWindow := &windows[i]
		for j := range osWindow.Tabs {
			tab := &osWindow.Tabs[j]
			state := tabState{
				signature: tabSignatureFor(osWindow, tab),
				windows:   make(map[int]windowState, len(tab.Windows)),
			}
			previous, hadPrevious := s.snap.probeTabs[tab.ID]
			if hadPrevious && reflect.DeepEqual(previous.signature, state.signature) {
				state.epoch = previous.epoch
			} else {
				s.nextProbeEpoch++
				state.epoch = s.nextProbeEpoch
			}
			for k := range tab.Windows {
				win := &tab.Windows[k]
				winState := windowState{signature: windowSignatureFor(win)}
				if prior, ok := previous.windows[win.ID]; hadPrevious && ok &&
					prior.signature == winState.signature {
					winState.epoch = prior.epoch
				} else {
					s.nextProbeEpoch++
					winState.epoch = s.nextProbeEpoch
				}
				state.windows[win.ID] = winState
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

// Load returns the current snapshot's windows, its generation, and whether any
// snapshot has been published yet. The returned slice is read-only; callers must
// not mutate it.
func (s *SnapshotStore) Load() (windows []kitty.OSWindow, gen uint64, ready bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap.windows, s.snap.gen, s.snap.ready
}

// loadForScreenProbes returns the immutable window tree together with the
// reducer receipts captured before the ListWindows call that supplied it and
// the publication generation that owns both. Both the slice and map are
// read-only after publication.
func (s *SnapshotStore) loadForScreenProbes() (
	windows []kitty.OSWindow,
	reducerReceipts map[int]uint64,
	gen uint64,
	probeTabs map[int]tabState,
	ready bool,
) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap.windows, s.snap.reducerReceipts, s.snap.gen, s.snap.probeTabs, s.snap.ready
}

// ScreenProbeSnapshot returns the last published external evidence and its
// causally earlier reducer receipts without any Kitty I/O. ready is false until
// the initial restore has published once.
func (s *SnapshotStore) ScreenProbeSnapshot() (Snapshot, bool) {
	windows, receipts, gen, probeTabs, ready := s.loadForScreenProbes()
	return Snapshot{windows: windows, reducerReceipts: receipts, probeTabs: probeTabs, gen: gen}, ready
}

// Snapshot is the causally bound evidence consumed by every periodic
// screen/transcript adapter. Receipt returns zero for a window that had no
// in-process reducer state when capture began; creating state in the meantime
// necessarily advances the current receipt past zero.
type Snapshot struct {
	windows         []kitty.OSWindow
	reducerReceipts map[int]uint64
	probeTabs       map[int]tabState
	// gen binds every cached observation to the Kitty snapshot that supplied
	// it. Probe bindings use the narrower per-tab epochs above; gen remains the
	// publication counter exposed by the general snapshot cache. A zero
	// generation denotes a direct caller-supplied snapshot.
	gen uint64
}

// NewSnapshot binds an already-available window tree to reducer receipts
// sampled by the caller. It carries no freshness epochs, so bindings derived
// from it skip epoch validation entirely; the shared cache is the only source
// of epoch-bearing snapshots.
func NewSnapshot(windows []kitty.OSWindow, reducerReceipts map[int]uint64) Snapshot {
	return Snapshot{windows: windows, reducerReceipts: reducerReceipts}
}

// Windows returns the captured window tree. It is read-only after publication
// and callers must not mutate it.
func (s Snapshot) Windows() []kitty.OSWindow { return s.windows }

// Receipt returns the reducer receipt captured for windowID before the
// ListWindows call that produced this snapshot.
func (s Snapshot) Receipt(windowID int) uint64 {
	return s.reducerReceipts[windowID]
}

// probeEpochsForWindow returns the tab-scoped and pane-scoped freshness epochs
// covering windowID. A zero generation denotes a direct caller-supplied
// snapshot that carries no epochs at all; those bindings skip epoch validation
// entirely.
func (s Snapshot) probeEpochsForWindow(windowID int) (
	tabID int, tabEpoch, windowEpoch uint64, ok bool,
) {
	_, tab, _, found := kitty.FindWindowLocationByID(s.windows, windowID)
	if !found {
		return 0, 0, 0, false
	}
	if s.gen == 0 {
		return tab.ID, 0, 0, true
	}
	state, found := s.probeTabs[tab.ID]
	if !found || state.epoch == 0 {
		return 0, 0, 0, false
	}
	winState, found := state.windows[windowID]
	if !found || winState.epoch == 0 {
		return 0, 0, 0, false
	}
	return tab.ID, state.epoch, winState.epoch, true
}
