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
// The snapshot is treated as immutable: Publish takes one Publication — the
// window slice a single ListWindows call produced, plus the receipts, socket,
// and completion time of that same read — and stores the slice and receipt map
// by reference rather than copying them (the real client and the test fake both
// return freshly built, unshared data, and the daemon hands ownership over at
// publication). Consumers only ever read them, and nothing mutates a published
// snapshot in place, so the read path needs no defensive deep copy and never
// blocks on Kitty.

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
	// socket is the Kitty remote-control endpoint the ListWindows behind these
	// windows was read through. Window IDs are unique only inside one Kitty
	// instance, so the tree alone does not say which world it describes: the
	// daemon's effective socket can change while a published snapshot stays put,
	// and a consumer handed the tree without the socket cannot tell that the two
	// no longer belong together. Empty means the read went through no reusable
	// endpoint and the snapshot binds to no identifiable instance.
	socket string
	// at is the wall-clock instant the successful ListWindows that produced
	// these windows COMPLETED, which is the origin every freshness measurement
	// counts from. It is deliberately not the tick or start time of the loop
	// that asked for the read: a list that blocked for seconds describes the
	// world as of its answer, not as of its request.
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

// publish replaces the cached snapshot with windows captured at capturedAt. The
// slice is stored by reference and must not be mutated by the caller afterward.
// The daemon always has receipts and a socket to publish with, so this wrapper
// exists only for the epoch tests below.
func (s *SnapshotStore) publish(windows []kitty.OSWindow, capturedAt time.Time) {
	s.Publish(Publication{Windows: windows, CapturedAt: capturedAt})
}

// Publication is one successful Kitty list as the store records it. The four
// facts are a single value rather than four parameters because they are only
// meaningful together: the receipts predate this exact read, the socket names
// the Kitty instance whose IDs the windows carry, and the capture time is when
// this read — not some other one — answered. Publishing any of them under
// another read's provenance is exactly the incoherence the cache exists to
// prevent.
type Publication struct {
	// Windows is the tree the read returned. It is stored by reference and must
	// not be mutated after publication.
	Windows []kitty.OSWindow
	// ReducerReceipts are the per-window receipts sampled before the read
	// started. Nil is a legitimate receipt-free publication.
	ReducerReceipts map[int]uint64
	// Socket is the remote-control endpoint the read ran against, which is what
	// binds the window IDs to one Kitty instance. Empty when the read went
	// through no reusable endpoint.
	Socket string
	// CapturedAt is the instant the read completed successfully.
	CapturedAt time.Time
}

// Publish replaces the cached snapshot with p. Publication is the only thing
// that advances the generation, the capture time, and the recorded socket, so a
// failed read leaves all three — and the windows they describe — exactly as the
// last success left them.
func (s *SnapshotStore) Publish(p Publication) {
	windows, reducerReceipts, capturedAt := p.Windows, p.ReducerReceipts, p.CapturedAt
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
	s.snap.socket = p.Socket
	s.snap.at = capturedAt
	s.snap.ready = true
	s.mu.Unlock()
}

// Load returns the current snapshot's windows, its generation, and whether any
// snapshot has been published yet. The returned slice is read-only; callers must
// not mutate it.
func (s *SnapshotStore) Load() (windows []kitty.OSWindow, gen uint64, ready bool) {
	view := s.View()
	return view.windows, view.gen, view.ready
}

// View returns the whole published snapshot as one immutable value read under a
// single lock acquisition. Windows, generation, socket, capture time, and
// readiness are five facts about ONE publication; handing them back separately
// would let a caller pair a generation with the capture time of the publication
// that replaced it, or a window tree with the socket the daemon has since moved
// to.
func (s *SnapshotStore) View() SnapshotView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SnapshotView{
		windows:    s.snap.windows,
		gen:        s.snap.gen,
		socket:     s.snap.socket,
		capturedAt: s.snap.at,
		ready:      s.snap.ready,
	}
}

// SnapshotView is one coherent read of the published Kitty snapshot: the window
// tree, the publication generation that owns it, the socket the tree was read
// through, the instant the successful ListWindows behind it completed, and
// whether anything has been published at all. The zero value is the cold view a
// daemon serves before its first successful list, which is the only state
// readiness distinguishes — an old capture time makes a view stale, never
// un-ready.
type SnapshotView struct {
	windows    []kitty.OSWindow
	gen        uint64
	socket     string
	capturedAt time.Time
	ready      bool
}

// Windows returns the captured window tree. It is read-only after publication
// and callers must not mutate it.
func (v SnapshotView) Windows() []kitty.OSWindow { return v.windows }

// Generation returns the publication counter, which advances once per
// successful publication and never on a failed read.
func (v SnapshotView) Generation() uint64 { return v.gen }

// Socket returns the Kitty remote-control endpoint this view's windows were
// read through — the one thing that says which Kitty instance their IDs belong
// to. It is empty for a cold view and for a publication whose read went through
// no reusable endpoint; empty therefore means "unattributed", never "the local
// instance", so a consumer must not read it as agreement with its own socket.
func (v SnapshotView) Socket() string { return v.socket }

// CapturedAt returns the completion time of the ListWindows that produced this
// view. It is the zero time for a cold view.
func (v SnapshotView) CapturedAt() time.Time { return v.capturedAt }

// Ready reports whether any snapshot has been published. False is cold: the
// daemon has never seen the window tree.
func (v SnapshotView) Ready() bool { return v.ready }

// Age reports how long ago this view's list completed, clamped at zero so a
// backwards wall-clock adjustment reports a fresh snapshot rather than a
// negative age. A cold view has no capture instant and ages zero.
func (v SnapshotView) Age(now time.Time) time.Duration {
	if !v.ready || v.capturedAt.IsZero() {
		return 0
	}
	age := now.Sub(v.capturedAt)
	if age < 0 {
		return 0
	}
	return age
}

// loadForScreenProbes returns the immutable window tree together with the
// reducer receipts captured before the ListWindows call that supplied it and
// the publication generation that owns both. Both the slice and map are
// read-only after publication.
func (s *SnapshotStore) loadForScreenProbesWithSocket() (
	windows []kitty.OSWindow,
	reducerReceipts map[int]uint64,
	gen uint64,
	probeTabs map[int]tabState,
	socket string,
	ready bool,
) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap.windows, s.snap.reducerReceipts, s.snap.gen, s.snap.probeTabs, s.snap.socket, s.snap.ready
}

func (s *SnapshotStore) loadForScreenProbes() (
	windows []kitty.OSWindow,
	reducerReceipts map[int]uint64,
	gen uint64,
	probeTabs map[int]tabState,
	ready bool,
) {
	windows, reducerReceipts, gen, probeTabs, _, ready = s.loadForScreenProbesWithSocket()
	return
}

// ScreenProbeSnapshot returns the last published external evidence and its
// causally earlier reducer receipts without any Kitty I/O. ready is false until
// the initial restore has published once.
func (s *SnapshotStore) ScreenProbeSnapshot() (Snapshot, bool) {
	windows, receipts, gen, probeTabs, socket, ready := s.loadForScreenProbesWithSocket()
	return Snapshot{windows: windows, reducerReceipts: receipts, probeTabs: probeTabs, gen: gen, socket: socket}, ready
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
	gen    uint64
	socket string
}

func NewSnapshotWithSocket(windows []kitty.OSWindow, reducerReceipts map[int]uint64, socket string) Snapshot {
	return Snapshot{windows: windows, reducerReceipts: reducerReceipts, socket: socket}
}

func (s Snapshot) Socket() string { return s.socket }

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
