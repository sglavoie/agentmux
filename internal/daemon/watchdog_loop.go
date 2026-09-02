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

// accountIdle is the hook-silence sweep's per-account idle accumulation entry,
// anchored to the account's lastSeenEventAt value.
type accountIdle struct {
	anchor time.Time
	idle   time.Duration
}

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
		return
	}
	h.sweepStale(now, windows, credit, gapTick)
	h.sweepHookSilence(now, windows, credit, gapTick)
}

// refreshAfterGap re-establishes the daemon's external evidence after a long
// scheduling gap: the cached Kitty snapshot and the last ASL reconciliation
// both predate the gap. Activity timestamps are deliberately not touched —
// the gap simply does not count toward idleness.
func (h *Handler) refreshAfterGap(now time.Time) {
	client := h.kittyClientForList()
	if client == nil {
		return
	}
	reducerReceipts := h.captureSnapshotReducerReceipts()
	windows, err := client.ListWindows()
	if err != nil {
		slog.Debug("watchdog: post-gap kitty refresh failed", "err", err)
		return
	}
	h.publishSnapshotWithReceipts(windows, reducerReceipts, now)
	h.ReconcileASLLeases(now, windows)
}
