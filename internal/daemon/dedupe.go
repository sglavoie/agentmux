package daemon

import (
	"sync"
	"sync/atomic"
	"time"
)

// dedupeMaxEntries bounds how many distinct request IDs the daemon tracks at
// once. Past this ceiling a new request is still processed but is admitted
// without a dedupe entry (a queue-full degradation), so a runaway client can
// never make the cache grow without bound.
const dedupeMaxEntries = 10000

// dedupeTTL is how long a completed response is retained for replay to a later
// retry carrying the same request ID. Retries after an ambiguous timeout arrive
// within seconds; fifteen minutes is a generous grace before an entry expires
// and its request ID becomes eligible for reprocessing.
const dedupeTTL = 15 * time.Minute

// Metrics counts dedupe-layer outcomes. Every request lands in exactly one of
// the accepted/duplicate/rejected/queueFull buckets; expiredDedupe is a
// supplementary counter of stale entries reclaimed by the TTL. All fields are
// updated atomically and read as a consistent-enough snapshot via Snapshot.
type Metrics struct {
	accepted      atomic.Uint64
	duplicate     atomic.Uint64
	rejected      atomic.Uint64
	queueFull     atomic.Uint64
	expiredDedupe atomic.Uint64
	// Queue latency (ingress admission → consumer pickup) in nanoseconds:
	// the most recent observation and the process-lifetime maximum.
	lastQueueWaitNs atomic.Int64
	maxQueueWaitNs  atomic.Int64
}

// observeQueueWait records one ingress-queue wait (enqueue → consumer pickup).
func (m *Metrics) observeQueueWait(wait time.Duration) {
	ns := wait.Nanoseconds()
	m.lastQueueWaitNs.Store(ns)
	for {
		prev := m.maxQueueWaitNs.Load()
		if ns <= prev || m.maxQueueWaitNs.CompareAndSwap(prev, ns) {
			return
		}
	}
}

// MetricsSnapshot is a plain-value copy of Metrics for reporting.
type MetricsSnapshot struct {
	Accepted      uint64 `json:"accepted"`
	Duplicate     uint64 `json:"duplicate"`
	Rejected      uint64 `json:"rejected"`
	QueueFull     uint64 `json:"queue_full"`
	ExpiredDedupe uint64 `json:"expired_dedupe"`
	// Ingress queue latency in nanoseconds (last observation / lifetime max).
	QueueLastWaitNs int64 `json:"queue_last_wait_ns,omitempty"`
	QueueMaxWaitNs  int64 `json:"queue_max_wait_ns,omitempty"`
}

// Snapshot returns the current counter values.
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Accepted:        m.accepted.Load(),
		Duplicate:       m.duplicate.Load(),
		Rejected:        m.rejected.Load(),
		QueueFull:       m.queueFull.Load(),
		ExpiredDedupe:   m.expiredDedupe.Load(),
		QueueLastWaitNs: m.lastQueueWaitNs.Load(),
		QueueMaxWaitNs:  m.maxQueueWaitNs.Load(),
	}
}

// dedupeEntry tracks one in-flight or completed request. done is closed once
// resp is populated; completedAt is zero while the request is still in flight.
type dedupeEntry struct {
	done        chan struct{}
	resp        Response
	completedAt time.Time
}

// expired reports whether a completed entry has outlived the cache TTL. An
// in-flight entry (zero completedAt) is never expired.
func (e *dedupeEntry) expired(now time.Time, ttl time.Duration) bool {
	return !e.completedAt.IsZero() && now.Sub(e.completedAt) >= ttl
}

// dedupeCache makes state-changing IPC idempotent under retries. A request
// carrying a stable ID is applied once: the first caller runs the handler,
// concurrent duplicates block until it finishes and reuse its response, and a
// later retry within the TTL replays the cached response without re-applying.
type dedupeCache struct {
	metrics     *Metrics
	ttl         time.Duration
	max         int
	waitTimeout time.Duration
	now         func() time.Time

	mu      sync.Mutex
	entries map[string]*dedupeEntry
}

// newDedupeCache returns a cache with the production bounds, recording outcomes
// into metrics.
func newDedupeCache(metrics *Metrics) *dedupeCache {
	return &dedupeCache{
		metrics:     metrics,
		ttl:         dedupeTTL,
		max:         dedupeMaxEntries,
		waitTimeout: connDeadline,
		now:         time.Now,
		entries:     make(map[string]*dedupeEntry),
	}
}

// do runs compute for the logical request identified by id, guaranteeing it
// executes at most once per live/cached entry. An empty id marks a legacy
// (non-deduplicated) request that always runs. compute is expected to be fast;
// it holds no cache lock while running so unrelated requests proceed in
// parallel.
func (c *dedupeCache) do(id string, compute func() Response) Response {
	if id == "" {
		c.metrics.accepted.Add(1)
		return compute()
	}

	c.mu.Lock()
	if e, ok := c.entries[id]; ok {
		if !e.expired(c.now(), c.ttl) {
			// A duplicate: an in-flight original to await or a completed
			// response to replay. Either way we return the original's result.
			c.mu.Unlock()
			c.metrics.duplicate.Add(1)
			select {
			case <-e.done:
				return e.resp
			case <-time.After(c.waitTimeout):
				return Response{OK: false, Error: "duplicate request wait timed out"}
			}
		}
		// The prior entry has expired; drop it and re-admit as a fresh request.
		delete(c.entries, id)
		c.metrics.expiredDedupe.Add(1)
	}

	c.sweepLocked(c.now())
	if len(c.entries) >= c.max {
		// Cache saturated: process the request but do not cache it. A retry
		// would re-apply, so this is a deliberate degradation surfaced via the
		// queue-full metric rather than a hard rejection that drops the event.
		c.mu.Unlock()
		c.metrics.queueFull.Add(1)
		return compute()
	}

	e := &dedupeEntry{done: make(chan struct{})}
	c.entries[id] = e
	c.mu.Unlock()

	c.metrics.accepted.Add(1)
	resp := compute()

	c.mu.Lock()
	e.resp = resp
	e.completedAt = c.now()
	close(e.done)
	c.mu.Unlock()
	return resp
}

// sweepLocked removes completed entries older than the TTL. The caller must
// hold c.mu.
func (c *dedupeCache) sweepLocked(now time.Time) {
	for id, e := range c.entries {
		if e.expired(now, c.ttl) {
			delete(c.entries, id)
			c.metrics.expiredDedupe.Add(1)
		}
	}
}
