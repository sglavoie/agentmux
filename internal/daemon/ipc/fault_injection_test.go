package ipc

// Transport-level fault injection for the ingress queue. Queue pressure is the
// failure where the bounded FIFO is full, so submits degrade to inline
// processing and bump queue_full, but degradation must never turn into a
// dropped event. The companion response-loss fault lives with the daemon root,
// whose handler is the thing that must apply a replayed request only once.

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// TestFaultQueuePressureNeverDropsEvents saturates a two-slot ingress queue
// while the consumer is parked. Overflow requests degrade to inline processing
// and bump queue_full, but every submitted event still applies exactly once,
// and the queue-latency counters record the pressure.
func TestFaultQueuePressureNeverDropsEvents(t *testing.T) {
	var applied atomic.Int32
	gate := make(chan struct{})
	var gateOnce sync.Once
	handle := func(_ protocol.Request) protocol.Response {
		<-gate
		applied.Add(1)
		return protocol.Response{OK: true}
	}
	var metrics requestMetrics
	q := newIngressQueueCap(handle, &metrics, 2)
	t.Cleanup(func() {
		gateOnce.Do(func() { close(gate) })
		q.stop()
	})

	const submitters = 12
	var wg sync.WaitGroup
	wg.Add(submitters)
	for i := 0; i < submitters; i++ {
		go func() {
			defer wg.Done()
			if resp := q.submit(protocol.Request{Type: protocol.TypeStateUpdate}); !resp.OK {
				t.Errorf("submit under pressure = %+v; want ok", resp)
			}
		}()
	}

	// Wait until the queue is saturated (some submits degraded inline and are
	// parked in handle), then release everything.
	deadline := time.Now().Add(3 * time.Second)
	for metrics.queueFull.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("queue never saturated")
		}
		time.Sleep(time.Millisecond)
	}
	gateOnce.Do(func() { close(gate) })
	wg.Wait()

	if got := applied.Load(); got != submitters {
		t.Fatalf("applied = %d; want %d (queue pressure must not drop events)", got, submitters)
	}
	snap := metrics.Snapshot()
	if snap.QueueFull == 0 {
		t.Fatal("queue_full = 0; want the overflow degradation counted")
	}
	if snap.QueueMaxWaitNs == 0 {
		t.Fatal("queue latency was not recorded under pressure")
	}
}
