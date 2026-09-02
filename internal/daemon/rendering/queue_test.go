package rendering

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func drainWake(q *Queue) int {
	woken := 0
	for {
		select {
		case <-q.Wake():
			woken++
		default:
			return woken
		}
	}
}

func TestQueueHoldsOneLatestGenerationEntryPerWindow(t *testing.T) {
	q := NewQueue()
	q.Enqueue(42, protocol.RenderGenerationTrace{Generation: 1})
	q.Enqueue(43, protocol.RenderGenerationTrace{Generation: 7})
	q.Enqueue(42, protocol.RenderGenerationTrace{Generation: 2})

	if !q.Pending(42) || !q.Pending(43) {
		t.Fatalf("both windows should be pending")
	}

	// FIFO across windows, latest generation within a window.
	id, trace, ok := q.Acquire()
	if !ok || id != 42 || trace.Generation != 2 {
		t.Fatalf("first Acquire = (%d, gen %d, %v), want (42, gen 2, true)", id, trace.Generation, ok)
	}
	if q.Pending(42) {
		t.Fatalf("42 is still pending after Acquire")
	}
	id, trace, ok = q.Acquire()
	if !ok || id != 43 || trace.Generation != 7 {
		t.Fatalf("second Acquire = (%d, gen %d, %v), want (43, gen 7, true)", id, trace.Generation, ok)
	}
	if _, _, ok := q.Acquire(); ok {
		t.Fatalf("third Acquire returned an entry from a drained queue")
	}
}

func TestQueueExcludesAnInFlightWindowUntilReleased(t *testing.T) {
	q := NewQueue()
	q.Enqueue(42, protocol.RenderGenerationTrace{Generation: 1})
	if _, _, ok := q.Acquire(); !ok {
		t.Fatalf("Acquire found nothing to render")
	}

	// A newer generation lands while the window is still being written.
	q.Enqueue(42, protocol.RenderGenerationTrace{Generation: 2})
	if _, _, ok := q.Acquire(); ok {
		t.Fatalf("Acquire handed out a window that is already in flight")
	}
	if !q.Pending(42) {
		t.Fatalf("the superseding generation was dropped instead of held pending")
	}

	drainWake(q)
	q.Release(42)
	if woken := drainWake(q); woken != 1 {
		t.Fatalf("releasing a requeued window signalled %d times, want 1", woken)
	}
	id, trace, ok := q.Acquire()
	if !ok || id != 42 || trace.Generation != 2 {
		t.Fatalf("post-release Acquire = (%d, gen %d, %v), want (42, gen 2, true)", id, trace.Generation, ok)
	}
}

func TestQueueReleaseWithoutRequeueDoesNotWake(t *testing.T) {
	q := NewQueue()
	q.Enqueue(42, protocol.RenderGenerationTrace{Generation: 1})
	if _, _, ok := q.Acquire(); !ok {
		t.Fatalf("Acquire found nothing to render")
	}
	drainWake(q)
	q.Release(42)
	if woken := drainWake(q); woken != 0 {
		t.Fatalf("releasing an unqueued window signalled %d times, want 0", woken)
	}
}

func TestQueueWakeIsCoalesced(t *testing.T) {
	q := NewQueue()
	q.Enqueue(42, protocol.RenderGenerationTrace{Generation: 1})
	q.Enqueue(43, protocol.RenderGenerationTrace{Generation: 1})
	q.Enqueue(44, protocol.RenderGenerationTrace{Generation: 1})
	if woken := drainWake(q); woken != 1 {
		t.Fatalf("three enqueues signalled %d times, want 1 coalesced wake", woken)
	}
}

func TestQueuePurgeReturnsTheQueuedTraceAndRemovesTheWindow(t *testing.T) {
	q := NewQueue()
	q.Enqueue(42, protocol.RenderGenerationTrace{Generation: 3, TraceIDs: []string{"t-1"}})
	q.Enqueue(43, protocol.RenderGenerationTrace{Generation: 4})

	trace, purged := q.Purge(42)
	if !purged || trace.Generation != 3 || len(trace.TraceIDs) != 1 || trace.TraceIDs[0] != "t-1" {
		t.Fatalf("Purge = (%+v, %v), want the queued generation-3 trace", trace, purged)
	}
	if q.Pending(42) {
		t.Fatalf("42 is still pending after Purge")
	}
	if _, purged := q.Purge(42); purged {
		t.Fatalf("Purge reported a second entry for an already-purged window")
	}

	// The surviving window keeps its place in the order.
	id, _, ok := q.Acquire()
	if !ok || id != 43 {
		t.Fatalf("Acquire after Purge = (%d, %v), want 43", id, ok)
	}
}

func TestQueuePurgeDoesNotCancelAnInFlightRender(t *testing.T) {
	q := NewQueue()
	q.Enqueue(42, protocol.RenderGenerationTrace{Generation: 1})
	if _, _, ok := q.Acquire(); !ok {
		t.Fatalf("Acquire found nothing to render")
	}
	if trace, purged := q.Purge(42); purged {
		t.Fatalf("Purge = (%+v, %v), want no pending entry for an in-flight window", trace, purged)
	}
}

func TestQueueDiscardPendingHandsBackEveryQueuedTrace(t *testing.T) {
	q := NewQueue()
	q.Enqueue(42, protocol.RenderGenerationTrace{Generation: 1})
	q.Enqueue(43, protocol.RenderGenerationTrace{Generation: 2})
	if _, _, ok := q.Acquire(); !ok {
		t.Fatalf("Acquire found nothing to render")
	}

	// The in-flight window already had its queued stage persisted by Acquire.
	discarded := q.DiscardPending()
	if len(discarded) != 1 || discarded[0].WindowID != 43 || discarded[0].Trace.Generation != 2 {
		t.Fatalf("DiscardPending = %+v, want only window 43 at generation 2", discarded)
	}
	if !q.Empty() {
		t.Fatalf("the queue is not empty after DiscardPending")
	}
}

func TestQueueCopiesTheCallersTrace(t *testing.T) {
	q := NewQueue()
	ids := []string{"t-1"}
	q.Enqueue(42, protocol.RenderGenerationTrace{Generation: 1, TraceIDs: ids})
	ids[0] = "mutated"

	_, trace, ok := q.Acquire()
	if !ok || len(trace.TraceIDs) != 1 || trace.TraceIDs[0] != "t-1" {
		t.Fatalf("Acquire = (%+v, %v), want the trace as it was enqueued", trace, ok)
	}
}
