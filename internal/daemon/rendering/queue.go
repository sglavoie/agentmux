package rendering

import (
	"sync"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// Queue is a latest-generation per-window render queue. pending holds at
// most one entry per window (FIFO order across windows); inflight marks
// windows currently being rendered so concurrent drainers (the worker and a
// synchronous snapshot render) never write to the same window at once.
//
// Lock ordering: the daemon's state mutex may be held while taking the queue's
// mutex, never the reverse — queue methods never touch handler state.
type Queue struct {
	mu           sync.Mutex
	pending      map[int]bool
	queuedTraces map[int]protocol.RenderGenerationTrace
	order        []int
	inflight     map[int]bool
	// wake is the coalesced worker signal: capacity one, non-blocking sends.
	wake chan struct{}
}

// Queued is one abandoned queue entry handed back to the caller so its
// queued-stage trace can still be journalled.
type Queued struct {
	WindowID int
	Trace    protocol.RenderGenerationTrace
}

func NewQueue() *Queue {
	return &Queue{
		pending:      make(map[int]bool),
		queuedTraces: make(map[int]protocol.RenderGenerationTrace),
		inflight:     make(map[int]bool),
		wake:         make(chan struct{}, 1),
	}
}

// Wake is the worker's signal channel. A receive means at least one window was
// enqueued or re-signalled since the last drain.
func (q *Queue) Wake() <-chan struct{} {
	return q.wake
}

// Enqueue adds a window to the queue and carries its latest queued-stage trace
// to the worker. Safe to call while holding the daemon's state mutex: it
// performs no diagnostic I/O.
func (q *Queue) Enqueue(windowID int, trace protocol.RenderGenerationTrace) {
	q.mu.Lock()
	if !q.pending[windowID] {
		q.pending[windowID] = true
		q.order = append(q.order, windowID)
	}
	q.queuedTraces[windowID] = CloneTrace(trace)
	q.mu.Unlock()
	q.signal()
}

func (q *Queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Acquire pops the oldest pending window that is not already being rendered
// and marks it in flight. ok is false when nothing is available (empty queue
// or every pending window is in flight elsewhere).
func (q *Queue) Acquire() (windowID int, trace protocol.RenderGenerationTrace, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, id := range q.order {
		if q.inflight[id] {
			continue
		}
		q.order = append(q.order[:i:i], q.order[i+1:]...)
		trace = q.queuedTraces[id]
		delete(q.pending, id)
		delete(q.queuedTraces, id)
		q.inflight[id] = true
		return id, trace, true
	}
	return 0, protocol.RenderGenerationTrace{}, false
}

// Release ends a window's in-flight render. If the window was re-enqueued
// while in flight, the pending entry survived and the worker is re-woken so
// the newer generation renders promptly.
func (q *Queue) Release(windowID int) {
	q.mu.Lock()
	delete(q.inflight, windowID)
	requeued := q.pending[windowID]
	q.mu.Unlock()
	if requeued {
		q.signal()
	}
}

// Purge removes and returns a window's pending entry, if any. Closed-window GC
// uses it so a queued render for a dead window is cancelled instead of failing
// its Kitty write, then persists the returned queued-stage trace after this
// queue lock has been released. An in-flight render cannot be cancelled; its
// completion bookkeeping already tolerates the deleted state.
func (q *Queue) Purge(windowID int) (protocol.RenderGenerationTrace, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending[windowID] {
		trace := CloneTrace(q.queuedTraces[windowID])
		delete(q.pending, windowID)
		delete(q.queuedTraces, windowID)
		for i, id := range q.order {
			if id == windowID {
				q.order = append(q.order[:i:i], q.order[i+1:]...)
				break
			}
		}
		return trace, true
	}
	return protocol.RenderGenerationTrace{}, false
}

// DiscardPending atomically transfers every pending queue entry to the caller
// for off-lock queued-stage persistence during worker shutdown. In-flight
// entries have already had their queued stage persisted by Acquire and are not
// represented in pending.
func (q *Queue) DiscardPending() []Queued {
	q.mu.Lock()
	defer q.mu.Unlock()

	discarded := make([]Queued, 0, len(q.pending))
	for _, windowID := range q.order {
		if !q.pending[windowID] {
			continue
		}
		discarded = append(discarded, Queued{
			WindowID: windowID,
			Trace:    CloneTrace(q.queuedTraces[windowID]),
		})
	}
	q.pending = make(map[int]bool)
	q.queuedTraces = make(map[int]protocol.RenderGenerationTrace)
	q.order = nil
	return discarded
}

// Empty reports whether no window is waiting to be rendered. In-flight windows
// are not pending.
func (q *Queue) Empty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending) == 0
}

// Pending reports whether a specific window is waiting to be rendered.
func (q *Queue) Pending(windowID int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pending[windowID]
}
