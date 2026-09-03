package kitty

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// maxRetainedListSockets bounds how many idle socket keys the coordinator
// keeps accounting for. A long-lived daemon scopes its client to whatever
// socket a window reports, so the key space is bounded by the OS windows that
// have ever existed rather than by the ones that still do. Sixteen is far
// above the number of Kitty instances a real session runs and small enough
// that stale keys cannot accumulate; in-flight keys are never evicted, so the
// bound trims history and never interrupts a read in progress.
const maxRetainedListSockets = 16

// ListErrorClass is the stable classification of a failed coordinated
// `kitten @ ls`. It is a diagnostic vocabulary, not a control-flow signal:
// callers still receive the original error.
type ListErrorClass string

const (
	// ListErrorNone means the socket has never failed.
	ListErrorNone ListErrorClass = ""
	// ListErrorTimeout means the kitten subprocess was killed by its deadline.
	ListErrorTimeout ListErrorClass = "timeout"
	// ListErrorCommand means kitten ran and failed for any other reason.
	ListErrorCommand ListErrorClass = "command"
	// ListErrorDecode means kitten succeeded but its payload did not decode.
	ListErrorDecode ListErrorClass = "decode"
)

// ListStatus is the accounting a coordinator retains for one socket key.
//
// Attempts, Failures, Timeouts, and the latency fields describe underlying
// executions only: a caller that joins an in-flight read increments
// CoalescedWaiters and nothing else, so latency stays a measure of how slow
// Kitty is rather than of how long a waiter happened to arrive before the
// answer. LastFailure and LastErrorClass are retained after a recovery and
// describe the most recent failure; ConsecutiveFailures is the field that says
// whether the socket is failing right now.
type ListStatus struct {
	Socket              string
	Attempts            uint64
	Failures            uint64
	Timeouts            uint64
	CoalescedWaiters    uint64
	InFlight            bool
	ConsecutiveFailures uint64
	LastLatency         time.Duration
	MaxLatency          time.Duration
	LastFailure         time.Time
	LastErrorClass      ListErrorClass
}

// ListTransitionKind names an edge in a socket's list health.
type ListTransitionKind string

const (
	// ListTransitionFailed is the first failure of a streak.
	ListTransitionFailed ListTransitionKind = "failed"
	// ListTransitionRecovered is the first success after a failure streak.
	ListTransitionRecovered ListTransitionKind = "recovered"
)

// ListTransition is what an observer is told about. Only edges are reported:
// a socket that keeps failing produces one transition, not one per attempt.
type ListTransition struct {
	Kind   ListTransitionKind
	Status ListStatus
	// Err is the failure that opened the streak, and nil for a recovery.
	Err error
}

// ListObserver receives list-health transitions. It is invoked outside every
// coordinator lock, so an observer may call back into the client without
// deadlocking, and a slow observer delays only the caller that ran the
// underlying attempt.
type ListObserver func(ListTransition)

// listDecodeError marks a `kitten @ ls` invocation that ran successfully but
// whose payload did not decode. It exists so the coordinator can tell a
// malformed answer apart from a failed command without matching on message
// text; its Error string is the message ListWindows has always returned.
type listDecodeError struct{ err error }

func (e *listDecodeError) Error() string {
	return "parsing kitten @ ls output: " + e.err.Error()
}

func (e *listDecodeError) Unwrap() error { return e.err }

func classifyListError(err error) ListErrorClass {
	switch {
	case err == nil:
		return ListErrorNone
	case errors.Is(err, context.DeadlineExceeded):
		return ListErrorTimeout
	case errors.As(err, new(*listDecodeError)):
		return ListErrorDecode
	default:
		return ListErrorCommand
	}
}

// listCoordinator collapses concurrent `kitten @ ls` reads that target the
// same socket into a single underlying execution, and retains bounded
// accounting per socket key. Different socket keys never interact.
type listCoordinator struct {
	mu       sync.Mutex
	entries  map[string]*listEntry
	observer ListObserver
	idleSeq  uint64

	// now is the coordinator's clock, replaced by tests that need latency and
	// failure timestamps to be exact rather than merely plausible.
	now func() time.Time
}

type listEntry struct {
	call   *listCall
	status ListStatus
	// idleAt is the coordinator sequence number at which this entry last
	// became idle. It orders eviction without needing a wall clock.
	idleAt uint64
}

// listCall is one underlying execution. Its result fields are written by the
// executing caller before done is closed and read by waiters only after, so
// the close is what publishes them.
type listCall struct {
	done    chan struct{}
	windows []OSWindow
	err     error
	// provenance is whatever the executing caller sampled immediately before
	// this read started, and completedAt is when the read answered. Both belong
	// to the execution rather than to any one caller, which is what lets a
	// waiter describe the shared topology with facts that truly predate it.
	provenance  any
	completedAt time.Time
}

// ListCapture is one underlying coordinated `kitten @ ls` as every caller that
// shared it sees it: the decoded topology, the provenance the executing caller
// sampled before the read started, and the instant the read completed.
//
// The three travel together on purpose. A caller that joins an in-flight read
// arrives after that read began, so anything it samples for itself is younger
// than the windows it is about to receive. Pairing the two would let evidence
// gathered against a newer world be published as if it described the older one.
type ListCapture struct {
	// Windows is this caller's own deep copy of the shared topology.
	Windows []OSWindow
	// Provenance is the value the executor's sample callback returned, or nil
	// when the caller supplied no callback. The coordinator never inspects it:
	// it exists so a caller can bind its own causally earlier facts to this
	// exact read. It is shared by reference with every coalesced caller and
	// must be treated as immutable.
	Provenance any
	// CompletedAt is the coordinator-clock instant the underlying read
	// returned. It is identical for the executor and every waiter, and is never
	// the instant a waiter happened to join.
	CompletedAt time.Time
}

func newListCoordinator() *listCoordinator {
	return &listCoordinator{
		entries: make(map[string]*listEntry),
		now:     time.Now,
	}
}

// capture runs run exactly once per socket key per in-flight window. The first
// caller executes; callers that arrive while it is running wait for its
// result. Every caller — the executor included — receives its own deep copy of
// the topology, so the ownership semantics of a per-caller JSON decode survive
// coalescing.
//
// sample is invoked by the executing caller only, after the entry is marked in
// flight and before the read begins, and its value reaches every caller that
// shares the read. That ordering is the whole point: marking in flight first is
// what makes a caller arriving in between join this read instead of starting
// one, and running sample before the read is what makes the returned provenance
// causally earlier than the topology it is published with.
func (co *listCoordinator) capture(
	socket string,
	sample func() any,
	run func() ([]OSWindow, error),
) (ListCapture, error) {
	co.mu.Lock()
	entry := co.entries[socket]
	if entry == nil {
		entry = &listEntry{status: ListStatus{Socket: socket}}
		co.entries[socket] = entry
	}
	if call := entry.call; call != nil {
		entry.status.CoalescedWaiters++
		co.mu.Unlock()
		<-call.done
		return ListCapture{
			Windows:     cloneOSWindows(call.windows),
			Provenance:  call.provenance,
			CompletedAt: call.completedAt,
		}, call.err
	}
	call := &listCall{done: make(chan struct{})}
	entry.call = call
	entry.status.InFlight = true
	co.mu.Unlock()

	var provenance any
	if sample != nil {
		provenance = sample()
	}

	// Sampling is the caller's own bookkeeping, not Kitty's latency, so the
	// attempt clock starts after it.
	started := co.now()
	windows, err := run()
	finished := co.now()

	call.windows, call.err = windows, err
	call.provenance, call.completedAt = provenance, finished
	transition, observer := co.settle(entry, started, finished, err)
	close(call.done)

	if observer != nil && transition != nil {
		observer(*transition)
	}
	return ListCapture{
		Windows:     cloneOSWindows(windows),
		Provenance:  provenance,
		CompletedAt: finished,
	}, err
}

// settle records one underlying attempt and reports the transition, if any,
// that the caller must emit after releasing the lock.
func (co *listCoordinator) settle(entry *listEntry, started, finished time.Time, err error) (*ListTransition, ListObserver) {
	// A clock that went backwards must not produce a negative latency or a
	// MaxLatency that can never be beaten again.
	latency := finished.Sub(started)
	if latency < 0 {
		latency = 0
	}

	co.mu.Lock()
	defer co.mu.Unlock()

	entry.call = nil
	entry.status.InFlight = false
	entry.status.Attempts++
	entry.status.LastLatency = latency
	if latency > entry.status.MaxLatency {
		entry.status.MaxLatency = latency
	}

	var kind ListTransitionKind
	if err != nil {
		class := classifyListError(err)
		entry.status.Failures++
		if class == ListErrorTimeout {
			entry.status.Timeouts++
		}
		entry.status.ConsecutiveFailures++
		entry.status.LastFailure = finished
		entry.status.LastErrorClass = class
		if entry.status.ConsecutiveFailures == 1 {
			kind = ListTransitionFailed
		}
	} else if entry.status.ConsecutiveFailures > 0 {
		entry.status.ConsecutiveFailures = 0
		kind = ListTransitionRecovered
	}

	co.idleSeq++
	entry.idleAt = co.idleSeq
	co.evictIdleLocked()

	if kind == "" {
		return nil, nil
	}
	return &ListTransition{Kind: kind, Status: entry.status, Err: err}, co.observer
}

// evictIdleLocked trims the retained key space to maxRetainedListSockets idle
// entries, oldest idle first. In-flight entries are neither counted nor
// evicted: dropping one would orphan the accounting of a read that is still
// running.
func (co *listCoordinator) evictIdleLocked() {
	idle := 0
	for _, e := range co.entries {
		if e.call == nil {
			idle++
		}
	}
	for idle > maxRetainedListSockets {
		oldestKey, oldest := "", (*listEntry)(nil)
		for key, e := range co.entries {
			if e.call != nil {
				continue
			}
			if oldest == nil || e.idleAt < oldest.idleAt {
				oldestKey, oldest = key, e
			}
		}
		if oldest == nil {
			return
		}
		delete(co.entries, oldestKey)
		idle--
	}
}

func (co *listCoordinator) setObserver(observer ListObserver) {
	co.mu.Lock()
	defer co.mu.Unlock()
	co.observer = observer
}

func (co *listCoordinator) status(socket string) (ListStatus, bool) {
	co.mu.Lock()
	defer co.mu.Unlock()
	entry, ok := co.entries[socket]
	if !ok {
		return ListStatus{}, false
	}
	return entry.status, true
}

func (co *listCoordinator) statuses() []ListStatus {
	co.mu.Lock()
	defer co.mu.Unlock()
	out := make([]ListStatus, 0, len(co.entries))
	for _, entry := range co.entries {
		out = append(out, entry.status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Socket < out[j].Socket })
	return out
}

// cloneOSWindows deep-copies a decoded topology, down to the string slices on
// each window and foreground process. Callers of ListWindows have always owned
// a freshly decoded tree they may sort, filter, or mutate in place; coalescing
// hands several callers one decode, so the copy is what keeps that ownership
// true and keeps two callers from racing on the same backing arrays.
func cloneOSWindows(windows []OSWindow) []OSWindow {
	if windows == nil {
		return nil
	}
	out := make([]OSWindow, len(windows))
	copy(out, windows)
	for i := range out {
		if out[i].Tabs == nil {
			continue
		}
		tabs := make([]Tab, len(out[i].Tabs))
		copy(tabs, out[i].Tabs)
		for j := range tabs {
			if tabs[j].Windows == nil {
				continue
			}
			wins := make([]Window, len(tabs[j].Windows))
			copy(wins, tabs[j].Windows)
			for k := range wins {
				wins[k].Cmdline = cloneStrings(wins[k].Cmdline)
				wins[k].ForegroundProcesses = cloneForegroundProcesses(wins[k].ForegroundProcesses)
			}
			tabs[j].Windows = wins
		}
		out[i].Tabs = tabs
	}
	return out
}

func cloneForegroundProcesses(procs []ForegroundProcess) []ForegroundProcess {
	if procs == nil {
		return nil
	}
	out := make([]ForegroundProcess, len(procs))
	copy(out, procs)
	for i := range out {
		out[i].Cmdline = cloneStrings(out[i].Cmdline)
	}
	return out
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
