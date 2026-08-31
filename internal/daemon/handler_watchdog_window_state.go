package daemon

import (
	"time"
)

// pruneClosedWindows drops per-window state for windows that Kitty no longer
// reports as live. Called from the maintenance tick once the live set has
// been materialised from kitty.ListWindows().
//
// The live per-window store must be GC'd here — a session that closes without
// a terminal hook event (e.g. user closes the tab with Cmd+W, daemon misses
// the SessionEnd) would otherwise leak an entry per killed window for the
// daemon's lifetime, which can also let notifiedStale latches survive into a
// future Kitty-ID reuse and silently suppress a notification. Alongside the
// store, the close cancels everything keyed by the window: its pending render
// queue entry, notifier timers/cooldowns, guard entry, remembered Kitty
// socket, and ASL miss streaks.
//
// windowMetadata is preserved behind a bounded tombstone so post-mortem
// `amx daemon export-events` invocations still have identity/context for
// sessions whose tabs have already disappeared; the tombstone (and the
// metadata with it) expires after windowTombstoneRetention or once
// maxWindowTombstones is exceeded.
func (h *Handler) pruneClosedWindows(now time.Time, liveIDs map[int]bool) {
	h.stateMu.Lock()
	closed := make(map[int]struct{})
	for id := range h.store {
		if !liveIDs[id] {
			closed[id] = struct{}{}
		}
	}
	for id := range closed {
		delete(h.store, id)
		delete(h.kittySockets, id)
		h.recordWindowClosedLocked(id, now)
	}
	// ASL miss streaks are per-window leases; a closed window can never
	// corroborate a run again, so its streaks go with it.
	for ref := range h.aslMissStreak {
		if _, ok := closed[ref.windowID]; ok {
			delete(h.aslMissStreak, ref)
		}
	}
	for ref := range h.aslSupervisorTargets {
		if _, ok := closed[ref.windowID]; ok {
			delete(h.aslSupervisorTargets, ref)
			delete(h.aslSupervisorProjections, ref)
			delete(h.aslSupervisorClosureCutoffs, ref)
		}
	}
	// Idle-sample stabilization streaks are per-window too: a closed window's
	// pane can produce no further screen evidence, so its streaks (and the
	// evidence generations they track) go with it.
	for ref := range h.idleSampleStreak {
		if _, ok := closed[ref.windowID]; ok {
			delete(h.idleSampleStreak, ref)
		}
	}
	for ref := range h.idleSampleGen {
		if _, ok := closed[ref.windowID]; ok {
			delete(h.idleSampleGen, ref)
		}
	}
	// Screen-probe decline streaks have the same property for the same reason:
	// a closed pane will never be classified again, so a streak left behind
	// would be a permanently frozen count that no later frame can ever advance
	// or clear. No sweep beyond this one is needed — unlike aslMissStreak, whose
	// leases can vanish while their window lives, a decline ref is keyed by
	// window and a fixed probe source key, so window closure is the only way an
	// entry can become unreachable.
	for ref := range h.screenProbeDeclineStreak {
		if _, ok := closed[ref.windowID]; ok {
			delete(h.screenProbeDeclineStreak, ref)
		}
	}
	for ref := range h.screenProbeDeclineFingerprint {
		if _, ok := closed[ref.windowID]; ok {
			delete(h.screenProbeDeclineFingerprint, ref)
		}
	}
	// A tombstoned ID that Kitty reports live again is a reused window, not a
	// closed one: resurrect it so its diagnostics resume live and it can count
	// as renderer drift again.
	for id := range liveIDs {
		h.reviveTombstoneLocked(id)
	}
	h.expireTombstonesLocked(now)
	h.stateMu.Unlock()

	for id := range closed {
		// The pending render entry for a closed window is cancelled outright;
		// an in-flight render cannot be cancelled, but its completion
		// bookkeeping already tolerates the deleted state. Persist the pending
		// queued-stage trace only after both stateMu and the queue mutex are free.
		if h.renderQueue != nil {
			if trace, purged := h.renderQueue.purge(id); purged {
				h.recordRenderGenerationBounded(trace, id)
			}
		}
		// Notifier cooldown/pending entries are keyed by window ID too. Without
		// cleanup the notifier's per-(window, action) cooldown map grows
		// linearly with closed windows, and a stale pending permission timer
		// for a dead window would fire well after the window itself is gone.
		if h.notifier != nil {
			h.notifier.PurgeWindow(id)
		}
		// State-guard entries are keyed by window ID and otherwise only removed
		// by idle/clear/user_clear events. A window closed without firing a
		// terminal hook would leak its guardEntry for the daemon's lifetime.
		if h.guard != nil {
			h.guard.Clear(id)
		}
	}
}

// recordWindowClosedLocked stamps the durable diagnostics for a closed window
// and starts (or refreshes) its bounded tombstone. Callers hold stateMu.
func (h *Handler) recordWindowClosedLocked(windowID int, now time.Time) {
	meta := h.windowMetadata[windowID]
	meta.Watchdog.LastEvent = eventWindowClosed
	meta.Watchdog.LastReason = "window no longer reported by kitty; live state garbage-collected"
	meta.Watchdog.LastAt = now.Unix()
	meta.Watchdog.ClosedAt = now.Unix()
	h.windowMetadata[windowID] = meta
	if h.windowTombstones == nil {
		h.windowTombstones = make(map[int]time.Time)
	}
	if _, ok := h.windowTombstones[windowID]; !ok {
		h.tombstoneOrder = append(h.tombstoneOrder, windowID)
	}
	h.windowTombstones[windowID] = now
}

// reviveTombstoneLocked clears a window's tombstone when the ID is live again
// (Kitty ID reuse, or an event racing the close re-created state). Callers
// hold stateMu.
func (h *Handler) reviveTombstoneLocked(windowID int) {
	if _, ok := h.windowTombstones[windowID]; !ok {
		return
	}
	delete(h.windowTombstones, windowID)
	for i, id := range h.tombstoneOrder {
		if id == windowID {
			h.tombstoneOrder = append(h.tombstoneOrder[:i:i], h.tombstoneOrder[i+1:]...)
			break
		}
	}
	meta := h.windowMetadata[windowID]
	meta.Watchdog.ClosedAt = 0
	h.windowMetadata[windowID] = meta
}

// expireTombstonesLocked enforces the tombstone bound: entries older than
// windowTombstoneRetention, or beyond maxWindowTombstones (oldest first), are
// dropped together with their durable metadata. Callers hold stateMu.
func (h *Handler) expireTombstonesLocked(now time.Time) {
	keep := h.tombstoneOrder[:0]
	for _, id := range h.tombstoneOrder {
		closedAt, ok := h.windowTombstones[id]
		if !ok {
			continue
		}
		if now.Sub(closedAt) >= windowTombstoneRetention {
			delete(h.windowTombstones, id)
			delete(h.windowMetadata, id)
			continue
		}
		keep = append(keep, id)
	}
	h.tombstoneOrder = keep
	for len(h.tombstoneOrder) > maxWindowTombstones {
		id := h.tombstoneOrder[0]
		h.tombstoneOrder = h.tombstoneOrder[1:]
		delete(h.windowTombstones, id)
		delete(h.windowMetadata, id)
	}
}
