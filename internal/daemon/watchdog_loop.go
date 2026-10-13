package daemon

// The independent watchdog loop. Staleness used to be evaluated on the
// two-second render tick against wall-clock timestamp differences, with a
// tick-gap heuristic that rewrote activity timestamps after long gaps to keep
// sleep time from counting as idleness. Both are gone: the watchdog now runs
// on its own one-second loop and measures idleness by ACCUMULATING the
// in-process elapsed time it actually observes between ticks. Activity
// timestamps are never rewritten, delayed rendering cannot postpone expiry
// (the loop never writes to Kitty), and a sleep-like scheduling gap credits at
// most one tick interval toward idleness instead of the whole gap.

import (
	"context"
	"log/slog"
	"time"
)

const (
	// watchdogInterval is the independent staleness-evaluation cadence.
	watchdogInterval = time.Second
	// watchdogGapThreshold flags a scheduling gap: a tick arriving this late
	// means the process was suspended (system sleep) or severely starved. The
	// gap's wall time never counts toward idleness, and the tick refreshes the
	// daemon's external evidence before judging expiry.
	watchdogGapThreshold = 5 * time.Second
)

// runWatchdog drives staleness evaluation every watchdogInterval. Elapsed time
// between ticks comes from the monotonic clock reading carried by the tick
// values, so wall-clock adjustments cannot stretch or shrink it.
func (h *Handler) runWatchdog(ctx context.Context) {
	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			elapsed := now.Sub(last)
			last = now
			h.watchdogTick(now, elapsed)
		}
	}
}

// watchdogTick runs one staleness pass, crediting the observed in-process
// elapsed time toward every tracked idle accumulator. A long scheduling gap
// credits only a single interval — the machine (and every session on it) was
// as suspended as the daemon was — and first refreshes the Kitty snapshot and
// the ASL lease reconciliation so expiry is never judged against evidence
// captured before the gap.
//
// A Kitty window is judged only against a published Kitty snapshot. A session
// on another backend has no window and needs none: its slot, profile, and
// directory are the daemon's own, so its sweeps run on every tick, in a
// daemon with no Kitty at all as in one whose Kitty has not answered yet.
func (h *Handler) watchdogTick(now time.Time, elapsed time.Duration) {
	credit := elapsed
	if credit < 0 {
		credit = 0
	}
	gapTick := elapsed > watchdogGapThreshold
	if gapTick {
		credit = watchdogInterval
		h.refreshAfterGap(now)
	}
	windows, _, ready := h.cachedSnapshot()
	if !ready {
		windows = nil
	}
	h.sweepStale(now, credit, gapTick, ready)
	h.sweepHookSilence(now, windows, credit, gapTick)
	// The gitstate refresh rides this loop rather than the render path or the
	// TUI: it forks processes, so it belongs where nothing is waiting on it.
	// It starts a round and returns — see git_state.go for why it must never
	// run inline here, and for the lock discipline the round itself keeps.
	h.refreshGitState(windows)
}

// refreshAfterGap re-establishes the daemon's external evidence after a long
// scheduling gap: the cached Kitty snapshot and the last ASL reconciliation
// both predate the gap. Activity timestamps are deliberately not touched —
// the gap simply does not count toward idleness.
func (h *Handler) refreshAfterGap(now time.Time) {
	client, socket := h.kittyListTarget()
	if client == nil {
		return
	}
	capture, err := h.captureKittySnapshot(client, socket)
	if err != nil {
		slog.Debug("watchdog: post-gap kitty refresh failed", "err", err)
		return
	}
	// The snapshot is dated by the refresh that produced it; the lease
	// reconciliation keeps the tick time, since expiry is judged against the
	// idleness this tick is crediting.
	h.publishCapture(capture)
	h.ReconcileASLLeases(now, capture.windows)
}
