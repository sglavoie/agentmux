package daemon

// This file serializes state-changing ingress through one bounded single-
// consumer queue. Without it, N concurrent connection goroutines contend on the
// handler's state mutex in scheduler-dependent order, so two events that arrive
// close together can be applied out of arrival order. Funnelling every mutating
// request through one consumer makes application order deterministic (arrival
// order at the queue) and decouples it from connection scheduling.
//
// The queue admits AFTER dedupe (a retried resend is collapsed before it ever
// reaches a queue slot) and returns as soon as the consumer finishes reducing
// the event — acknowledgement waits for reduction, never for rendering (which
// the renderer loop performs asynchronously). Read-only requests bypass the
// queue so a dashboard poll is never serialized behind a burst of hook events.

import "time"

// ingressQueueCapacity bounds the number of in-flight state-changing requests
// waiting for the single reducer consumer. 4,096 is far above any realistic
// hook burst; past it the queue degrades to inline processing rather than
// dropping an event.
const ingressQueueCapacity = 4096

// queueItem is one request awaiting serialized reduction. result is buffered so
// the consumer never blocks delivering the response even if the submitter has
// already given up. enqueuedAt feeds the queue-latency metric.
type queueItem struct {
	req        Request
	result     chan Response
	enqueuedAt time.Time
}

// ingressQueue is a bounded FIFO with a single consumer goroutine that applies
// each request through handle. It reuses the server's shared queueFull metric
// for its own saturation signal so the plan keeps one "queue-full" counter.
type ingressQueue struct {
	ch      chan queueItem
	handle  func(Request) Response
	metrics *Metrics
	done    chan struct{}
}

// newIngressQueue creates the queue at the production capacity and starts its
// single consumer. Call stop to drain and shut it down.
func newIngressQueue(handle func(Request) Response, metrics *Metrics) *ingressQueue {
	return newIngressQueueCap(handle, metrics, ingressQueueCapacity)
}

// newIngressQueueCap is newIngressQueue with an explicit capacity, so tests can
// force saturation without enqueuing thousands of items.
func newIngressQueueCap(handle func(Request) Response, metrics *Metrics, capacity int) *ingressQueue {
	q := &ingressQueue{
		ch:      make(chan queueItem, capacity),
		handle:  handle,
		metrics: metrics,
		done:    make(chan struct{}),
	}
	go q.run()
	return q
}

func (q *ingressQueue) run() {
	defer close(q.done)
	for item := range q.ch {
		if q.metrics != nil && !item.enqueuedAt.IsZero() {
			q.metrics.observeQueueWait(time.Since(item.enqueuedAt))
		}
		item.result <- q.handle(item.req)
	}
}

// submit enqueues req for serialized reduction and blocks until the consumer has
// applied it, returning the handler's response. When the queue is saturated the
// request is processed inline (never dropped) and the shared queueFull metric is
// incremented; inline processing is still state-mutex-safe, it just forgoes the
// FIFO ordering guarantee under overload.
func (q *ingressQueue) submit(req Request) Response {
	item := queueItem{req: req, result: make(chan Response, 1), enqueuedAt: time.Now()}
	select {
	case q.ch <- item:
		return <-item.result
	default:
		if q.metrics != nil {
			q.metrics.queueFull.Add(1)
		}
		req.saturationReason = appendSaturationReason(req.saturationReason, "ingress_queue")
		return q.handle(req)
	}
}

func appendSaturationReason(existing, reason string) string {
	if existing == "" {
		return reason
	}
	if existing == reason {
		return existing
	}
	return existing + "," + reason
}

// stop closes the queue and waits for the consumer to drain any buffered items.
// It must be called only after all submitters have finished so no submit blocks
// on a closed queue.
func (q *ingressQueue) stop() {
	close(q.ch)
	<-q.done
}

// serializedRequest reports whether a request type mutates daemon state and must
// therefore flow through the ordered reducer queue. Read-only and control
// requests bypass it.
func serializedRequest(t MessageType) bool {
	switch t {
	case TypeHookEvent, TypeStateUpdate:
		return true
	default:
		return false
	}
}
