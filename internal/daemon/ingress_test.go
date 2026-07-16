package daemon

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestIngressQueue_SerializesApplication proves the single consumer never runs
// two reductions concurrently: many goroutines submit at once, but the handle
// callback observes an in-flight count that never exceeds one.
func TestIngressQueue_SerializesApplication(t *testing.T) {
	var inFlight atomic.Int32
	var maxObserved atomic.Int32
	var applied atomic.Int32

	handle := func(_ Request) Response {
		n := inFlight.Add(1)
		for {
			old := maxObserved.Load()
			if n <= old || maxObserved.CompareAndSwap(old, n) {
				break
			}
		}
		// Hold the "reduction" briefly so any concurrency would be observed.
		time.Sleep(time.Millisecond)
		applied.Add(1)
		inFlight.Add(-1)
		return Response{OK: true}
	}

	q := newIngressQueue(handle, nil)
	t.Cleanup(q.stop)

	const submitters = 50
	var wg sync.WaitGroup
	wg.Add(submitters)
	for i := 0; i < submitters; i++ {
		go func() {
			defer wg.Done()
			if resp := q.submit(Request{Type: TypeStateUpdate}); !resp.OK {
				t.Errorf("submit response = %+v; want ok", resp)
			}
		}()
	}
	wg.Wait()

	if got := maxObserved.Load(); got != 1 {
		t.Fatalf("max concurrent reductions = %d; want 1 (serialized)", got)
	}
	if got := applied.Load(); got != submitters {
		t.Fatalf("applied = %d; want %d", got, submitters)
	}
}

// TestIngressQueue_AckWaitsForReduction asserts submit returns the handler's
// actual response — proving acknowledgement waits until the reducer has applied
// the event, not merely until it was enqueued.
func TestIngressQueue_AckWaitsForReduction(t *testing.T) {
	handle := func(req Request) Response {
		return Response{OK: true, IconState: req.Action}
	}
	q := newIngressQueue(handle, nil)
	t.Cleanup(q.stop)

	resp := q.submit(Request{Type: TypeStateUpdate, Action: "bolt"})
	if !resp.OK || resp.IconState != "bolt" {
		t.Fatalf("submit response = %+v; want the reduced result", resp)
	}
}

// TestIngressQueue_FullDegradesInlineAndCountsQueueFull saturates a tiny queue
// while the consumer is parked, then submits one more. The overflow request must
// still be processed (never dropped) and must bump the shared queueFull metric.
func TestIngressQueue_FullDegradesInlineAndCountsQueueFull(t *testing.T) {
	entered := make(chan struct{}, 8)
	saturation := make(chan string, 8)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }

	handle := func(req Request) Response {
		saturation <- req.saturationReason
		entered <- struct{}{}
		<-release
		return Response{OK: true}
	}

	var metrics Metrics
	q := newIngressQueueCap(handle, &metrics, 1)
	t.Cleanup(func() {
		releaseAll()
		q.stop()
	})

	// item1: the consumer picks it up and parks inside handle. Once it signals
	// entry, the buffer is empty and the consumer is not receiving.
	go q.submit(Request{Type: TypeStateUpdate})
	<-entered
	<-saturation

	// Fill the single buffer slot directly and deterministically.
	q.ch <- queueItem{req: Request{Type: TypeStateUpdate}, result: make(chan Response, 1)}

	// item3: the buffer is now full and the consumer is parked, so this submit
	// cannot enqueue. It must process inline and count queueFull (which is bumped
	// before handle runs), then park on release itself.
	overflow := make(chan Response, 1)
	go func() { overflow <- q.submit(Request{Type: TypeStateUpdate}) }()

	deadline := time.Now().Add(2 * time.Second)
	for metrics.queueFull.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("queueFull metric = 0; want the overflow submit to count queue-full")
		}
		time.Sleep(time.Millisecond)
	}
	if got := metrics.queueFull.Load(); got != 1 {
		t.Fatalf("queueFull = %d; want exactly 1", got)
	}
	select {
	case got := <-saturation:
		if got != "ingress_queue" {
			t.Fatalf("overflow saturation reason = %q; want ingress_queue", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("overflow request did not expose request-scoped saturation evidence")
	}

	// The overflow request is processed, not dropped: release everything and the
	// inline submit returns its response.
	releaseAll()
	select {
	case resp := <-overflow:
		if !resp.OK {
			t.Fatalf("overflow submit response = %+v; want ok (processed inline)", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("overflow submit never returned; queue-full request was dropped")
	}
}
