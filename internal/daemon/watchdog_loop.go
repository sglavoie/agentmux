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

// factIdle is the watchdog's per-fact idle accumulation entry. seq/at pin the
// fact revision the accumulation belongs to; any advance resets it.
type factIdle struct {
	seq  uint64
	at   int64
	idle time.Duration
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
	windows, err := client.ListWindows()
	if err != nil {
		slog.Debug("watchdog: post-gap kitty refresh failed", "err", err)
		return
	}
	h.publishSnapshot(windows, now)
	h.ReconcileASLLeases(now, windows)
}

// advanceWindowIdle advances a window's scalar idle accumulator and returns
// the accumulated idle duration. The accumulation is anchored to the window's
// Activity timestamp: fresh activity re-anchors and reseeds. A first
// observation seeds from the entry's wall-clock age — a one-shot sweep over
// pre-aged state (tests, a restart restoring an old bolt) expires it
// immediately — except on a gap tick, where wall age is exactly the evidence
// that cannot be trusted. Callers hold stateMu.
func advanceWindowIdle(st *WindowState, now time.Time, credit time.Duration, gapTick bool) time.Duration {
	if !st.watchdogSeeded || !st.Activity.Equal(st.watchdogAnchor) {
		st.watchdogSeeded = true
		st.watchdogAnchor = st.Activity
		st.watchdogIdle = 0
		if !gapTick {
			if age := now.Sub(st.Activity); age > 0 {
				st.watchdogIdle = age
			}
		}
		return st.watchdogIdle
	}
	st.watchdogIdle += credit
	return st.watchdogIdle
}

// advanceFactIdle advances one source fact's idle accumulator, with the same
// seeding rules as advanceWindowIdle. Any fact advance (new sequence or
// emitted timestamp — e.g. an ASL heartbeat) resets the accumulation. Callers
// hold stateMu.
func advanceFactIdle(st *WindowState, fact SourceFactSnapshot, now time.Time, credit time.Duration, gapTick bool) time.Duration {
	if st.watchdogFactIdle == nil {
		st.watchdogFactIdle = make(map[SourceID]*factIdle)
	}
	src := fact.Source()
	entry := st.watchdogFactIdle[src]
	if entry == nil || entry.seq != fact.Seq || entry.at != fact.At {
		entry = &factIdle{seq: fact.Seq, at: fact.At}
		if !gapTick {
			if age := now.Unix() - fact.At; age > 0 {
				entry.idle = time.Duration(age) * time.Second
			}
		}
		st.watchdogFactIdle[src] = entry
		return entry.idle
	}
	entry.idle += credit
	return entry.idle
}
