package daemon

// The independent watchdog loop. Staleness used to be evaluated on the
// two-second render tick against wall-clock timestamp differences, with a
// tick-gap heuristic that rewrote activity timestamps after long gaps to keep
// sleep time from counting as idleness. Both are gone: the watchdog now runs
// on its own one-second loop and measures idleness by ACCUMULATING the
// in-process elapsed time it actually observes between ticks. Activity
// timestamps are never rewritten, delayed rendering cannot postpone expiry
// (the loop never writes to tmux), and a sleep-like scheduling gap credits at
// most one tick interval toward idleness instead of the whole gap.

import (
	"context"
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

// watchdogTick evaluates tmux runtime sessions on the one-second cadence.
func (h *Handler) watchdogTick(now time.Time, elapsed time.Duration) {
	credit := elapsed
	if credit < 0 {
		credit = 0
	}
	gapTick := elapsed > watchdogGapThreshold
	if gapTick {
		credit = watchdogInterval
	}
	h.sweepStale(now, credit, gapTick, false)
	h.sweepHookSilence(now, nil, credit, gapTick)
	h.refreshGitState()
}
