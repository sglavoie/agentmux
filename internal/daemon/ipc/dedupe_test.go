package ipc

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// TestDedupeCache_ConcurrentDuplicatesRunOnce is the core exactly-once
// invariant: many concurrent callers sharing one request ID run the underlying
// work a single time and every caller observes the same response.
func TestDedupeCache_ConcurrentDuplicatesRunOnce(t *testing.T) {
	var m requestMetrics
	c := newDedupeCache(&m)

	var computeCount atomic.Int64
	const callers = 64
	responses := make([]protocol.Response, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			responses[idx] = c.do("req-1", func() protocol.Response {
				computeCount.Add(1)
				// Hold long enough that later callers arrive while in-flight.
				time.Sleep(20 * time.Millisecond)
				return protocol.Response{OK: true, IconState: "bolt"}
			})
		}(i)
	}
	close(start)
	wg.Wait()

	if got := computeCount.Load(); got != 1 {
		t.Fatalf("compute ran %d times; want exactly 1", got)
	}
	for i, r := range responses {
		if !r.OK || r.IconState != "bolt" {
			t.Fatalf("caller %d got %+v; want the single OK/bolt response", i, r)
		}
	}
	snap := m.Snapshot()
	if snap.Accepted != 1 {
		t.Errorf("Accepted = %d; want 1", snap.Accepted)
	}
	if snap.Duplicate != callers-1 {
		t.Errorf("Duplicate = %d; want %d", snap.Duplicate, callers-1)
	}
}

// TestDedupeCache_CompletedReplayWithinTTL proves a sequential retry (the
// ambiguous-timeout resend) replays the cached response without re-running the
// work.
func TestDedupeCache_CompletedReplayWithinTTL(t *testing.T) {
	var m requestMetrics
	c := newDedupeCache(&m)

	var computeCount atomic.Int64
	run := func() protocol.Response {
		n := computeCount.Add(1)
		return protocol.Response{OK: true, IconState: fmt.Sprintf("run-%d", n)}
	}

	first := c.do("req-1", run)
	second := c.do("req-1", run)

	if computeCount.Load() != 1 {
		t.Fatalf("compute ran %d times; want 1 (retry must replay)", computeCount.Load())
	}
	if first.IconState != "run-1" || second.IconState != "run-1" {
		t.Fatalf("responses = %q / %q; want both run-1", first.IconState, second.IconState)
	}
	snap := m.Snapshot()
	if snap.Accepted != 1 || snap.Duplicate != 1 {
		t.Errorf("metrics = %+v; want Accepted 1, Duplicate 1", snap)
	}
}

// TestDedupeCache_LegacyNotDeduplicated verifies an empty request ID always
// runs and is never deduplicated.
func TestDedupeCache_LegacyNotDeduplicated(t *testing.T) {
	var m requestMetrics
	c := newDedupeCache(&m)

	var computeCount atomic.Int64
	run := func() protocol.Response { computeCount.Add(1); return protocol.Response{OK: true} }

	c.do("", run)
	c.do("", run)

	if computeCount.Load() != 2 {
		t.Fatalf("legacy compute ran %d times; want 2", computeCount.Load())
	}
	snap := m.Snapshot()
	if snap.Accepted != 2 || snap.Duplicate != 0 {
		t.Errorf("metrics = %+v; want Accepted 2, Duplicate 0", snap)
	}
}

// TestDedupeCache_ExpiredEntryReadmits proves an entry older than the TTL is
// reclaimed and its request ID becomes eligible for reprocessing.
func TestDedupeCache_ExpiredEntryReadmits(t *testing.T) {
	var m requestMetrics
	c := newDedupeCache(&m)
	now := time.Unix(1_000_000, 0)
	c.now = func() time.Time { return now }

	var computeCount atomic.Int64
	run := func() protocol.Response { computeCount.Add(1); return protocol.Response{OK: true} }

	c.do("req-1", run)
	// Advance past the TTL so the completed entry expires.
	now = now.Add(dedupeTTL + time.Second)
	c.do("req-1", run)

	if computeCount.Load() != 2 {
		t.Fatalf("compute ran %d times; want 2 (expired ID re-admitted)", computeCount.Load())
	}
	snap := m.Snapshot()
	if snap.Accepted != 2 {
		t.Errorf("Accepted = %d; want 2", snap.Accepted)
	}
	if snap.ExpiredDedupe == 0 {
		t.Errorf("ExpiredDedupe = 0; want at least 1")
	}
}

// TestDedupeCache_QueueFullDegradesButRuns proves that at capacity a new
// request is still processed (never dropped) but is admitted without a cache
// entry, surfaced through the queue-full metric.
func TestDedupeCache_QueueFullDegradesButRuns(t *testing.T) {
	var m requestMetrics
	c := newDedupeCache(&m)
	c.max = 2

	run := func() protocol.Response { return protocol.Response{OK: true} }
	c.do("a", run)
	c.do("b", run)
	// Cache now holds two non-expired completed entries.
	observedSaturation := false
	c.doObserved("c", func(saturated bool) protocol.Response {
		observedSaturation = saturated
		return run()
	})

	snap := m.Snapshot()
	if snap.QueueFull != 1 {
		t.Errorf("QueueFull = %d; want 1", snap.QueueFull)
	}
	if !observedSaturation {
		t.Error("dedupe saturation was not attributed to the request callback")
	}
	if snap.Accepted != 2 {
		t.Errorf("Accepted = %d; want 2 (the two cached admits only)", snap.Accepted)
	}
	// A saturated request is not cached, so a retry of "c" runs again rather
	// than replaying — confirming the degradation is real.
	var cRuns atomic.Int64
	c.do("c", func() protocol.Response { cRuns.Add(1); return protocol.Response{OK: true} })
	c.do("c", func() protocol.Response { cRuns.Add(1); return protocol.Response{OK: true} })
	if cRuns.Load() != 2 {
		t.Errorf("uncached 'c' ran %d times; want 2 (no replay when queue-full)", cRuns.Load())
	}
}

// TestDedupeCache_InFlightWaitTimesOut proves a duplicate that waits on a stuck
// in-flight original is bounded by waitTimeout instead of hanging forever.
func TestDedupeCache_InFlightWaitTimesOut(t *testing.T) {
	var m requestMetrics
	c := newDedupeCache(&m)
	c.waitTimeout = 30 * time.Millisecond

	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		c.do("req-1", func() protocol.Response {
			close(started)
			<-release // block well past waitTimeout
			return protocol.Response{OK: true, IconState: "bolt"}
		})
	}()
	<-started

	got := c.do("req-1", func() protocol.Response {
		t.Error("duplicate must not run its own compute")
		return protocol.Response{}
	})
	if got.OK {
		t.Fatalf("timed-out duplicate = %+v; want OK=false", got)
	}
	close(release)
}

func TestDedupeReplayHasExplicitDuplicateAcknowledgement(t *testing.T) {
	var metrics requestMetrics
	cache := newDedupeCache(&metrics)
	compute := func() protocol.Response {
		return protocol.Response{OK: true, Delivery: &protocol.DeliveryAcknowledgement{
			SchemaVersion: protocol.DeliveryAcknowledgementSchemaVersion,
			Status:        protocol.DeliveryAcknowledgementAccepted,
			Reason:        "accepted",
		}}
	}

	first := cache.do("request-1", compute)
	duplicate := cache.do("request-1", compute)
	if first.Delivery == nil || first.Delivery.Status != protocol.DeliveryAcknowledgementAccepted {
		t.Fatalf("first acknowledgement = %+v", first.Delivery)
	}
	if duplicate.Delivery == nil || duplicate.Delivery.Status != protocol.DeliveryAcknowledgementDuplicate ||
		duplicate.Delivery.Reason != "request_replayed" {
		t.Fatalf("duplicate acknowledgement = %+v", duplicate.Delivery)
	}
}
