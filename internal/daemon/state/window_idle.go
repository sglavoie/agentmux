package state

// This file holds the watchdog's in-process idle accounting for one window. The
// accumulators are deliberately not persisted and not exported as data: the
// watchdog owns the policy (thresholds, gap detection) while WindowState owns
// the bookkeeping, so the seam is behavioral. Staleness is measured by
// ACCUMULATING observed in-process elapsed time rather than by comparing
// wall-clock timestamps, which is why a system sleep cannot expire a session
// that produced no in-process idle time.

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// factIdle is the watchdog's per-fact idle accumulation entry. seq/at pin the
// fact revision the accumulation belongs to; any advance resets it.
type factIdle struct {
	seq  uint64
	at   int64
	idle time.Duration
}

// IdleDuration reports the window's currently accumulated scalar idle time. It
// is the value AdvanceIdle last returned, exposed so the watchdog can quote it
// in the event it appends when a window expires.
func (s *WindowState) IdleDuration() time.Duration { return s.watchdogIdle }

// AdvanceIdle advances the window's scalar idle accumulator and returns the
// accumulated idle duration. The accumulation is anchored to the window's
// Activity timestamp: fresh activity re-anchors and reseeds. A first
// observation seeds from the entry's wall-clock age — a one-shot sweep over
// pre-aged state (tests, a restart restoring an old bolt) expires it
// immediately — except on a gap tick, where wall age is exactly the evidence
// that cannot be trusted. Callers hold the daemon's state lock.
func (s *WindowState) AdvanceIdle(now time.Time, credit time.Duration, gapTick bool) time.Duration {
	if !s.watchdogSeeded || !s.Activity.Equal(s.watchdogAnchor) {
		s.watchdogSeeded = true
		s.watchdogAnchor = s.Activity
		s.watchdogIdle = 0
		if !gapTick {
			if age := now.Sub(s.Activity); age > 0 {
				s.watchdogIdle = age
			}
		}
		return s.watchdogIdle
	}
	s.watchdogIdle += credit
	return s.watchdogIdle
}

// ResetIdle drops the scalar accumulation so the next observation reseeds. The
// watchdog uses it when a window stops carrying an expirable action.
func (s *WindowState) ResetIdle() {
	s.watchdogSeeded = false
	s.watchdogIdle = 0
}

// ResetIdleAccounting drops the scalar accumulation and every per-fact one. It
// is the wholesale reset used when a window's facts were rewritten underneath
// the watchdog (process-lifecycle reconciliation), where no surviving
// accumulation describes the new state.
func (s *WindowState) ResetIdleAccounting() {
	s.ResetIdle()
	s.watchdogFactIdle = nil
}

// AdvanceFactIdle advances one source fact's idle accumulator, with the same
// seeding rules as AdvanceIdle. Any fact advance (new sequence or emitted
// timestamp — e.g. an ASL heartbeat) resets the accumulation. Callers hold the
// daemon's state lock.
func (s *WindowState) AdvanceFactIdle(
	fact protocol.SourceFactSnapshot,
	now time.Time,
	credit time.Duration,
	gapTick bool,
) time.Duration {
	if s.watchdogFactIdle == nil {
		s.watchdogFactIdle = make(map[protocol.SourceID]*factIdle)
	}
	src := fact.Source()
	entry := s.watchdogFactIdle[src]
	if entry == nil || entry.seq != fact.Seq || entry.at != fact.At {
		entry = &factIdle{seq: fact.Seq, at: fact.At}
		if !gapTick {
			if age := now.Unix() - fact.At; age > 0 {
				entry.idle = time.Duration(age) * time.Second
			}
		}
		s.watchdogFactIdle[src] = entry
		return entry.idle
	}
	entry.idle += credit
	return entry.idle
}

// ForgetFactIdle drops one fact's accumulation, used when the watchdog has just
// expired that fact.
func (s *WindowState) ForgetFactIdle(src protocol.SourceID) {
	delete(s.watchdogFactIdle, src)
}

// RetainFactIdle drops the accumulation of every fact the caller did not
// observe in the current sweep, so accumulators for facts that no longer
// contribute an active-class icon (retired, acked, or advanced to another
// state) cannot grow the map with dead sources.
func (s *WindowState) RetainFactIdle(observed map[protocol.SourceID]bool) {
	for src := range s.watchdogFactIdle {
		if !observed[src] {
			delete(s.watchdogFactIdle, src)
		}
	}
}

// maxNotifiedSourceEvents bounds the per-window ring of upstream source event
// IDs that already produced a notification decision.
const maxNotifiedSourceEvents = 64

// MarkSourceEventNotified reports whether a notification decision for this
// upstream source event ID has not been made yet, and records it. An empty ID
// is never deduplicated (the notifier's own per-action cooldown still applies).
// The ring is unexported and never persisted: a daemon restart re-arms at most
// one duplicate notification per retried upstream event. Callers hold the
// daemon's state lock.
func (s *WindowState) MarkSourceEventNotified(sourceEventID string) bool {
	if sourceEventID == "" {
		return true
	}
	if _, seen := s.notifiedSourceEvents[sourceEventID]; seen {
		return false
	}
	if s.notifiedSourceEvents == nil {
		s.notifiedSourceEvents = make(map[string]struct{}, maxNotifiedSourceEvents)
	}
	s.notifiedSourceEvents[sourceEventID] = struct{}{}
	s.notifiedSourceOrder = append(s.notifiedSourceOrder, sourceEventID)
	if len(s.notifiedSourceOrder) > maxNotifiedSourceEvents {
		oldest := s.notifiedSourceOrder[0]
		s.notifiedSourceOrder = s.notifiedSourceOrder[1:]
		delete(s.notifiedSourceEvents, oldest)
	}
	return true
}

// PromotedBusyBody returns the marker body of the Claude pane title the
// busy-title probe last promoted this window to ⚡ from.
func (s *WindowState) PromotedBusyBody() string { return s.promotedBusyBody }

// SetPromotedBusyBody records the marker body a busy-title promotion fired on,
// arming the one-shot guard against re-promoting from an unchanged stale title.
func (s *WindowState) SetPromotedBusyBody(body string) { s.promotedBusyBody = body }

// ClearPromotedBusyBody re-arms one-shot busy-body promotion and releases any
// operator-clear hold. Only a real hook/ASL event calls it: the probe applies
// its own fact directly and must not re-arm itself.
func (s *WindowState) ClearPromotedBusyBody() {
	s.promotedBusyBody = ""
	s.busyPromotionHeld = false
}

// HoldBusyPromotion disarms busy-title promotion until the next real hook/ASL
// event. An operator clear of a settled icon calls it; an operator clear never
// calls ClearPromotedBusyBody.
func (s *WindowState) HoldBusyPromotion() { s.busyPromotionHeld = true }

// BusyPromotionHeld reports whether an operator clear is holding busy-title
// promotion.
func (s *WindowState) BusyPromotionHeld() bool { return s.busyPromotionHeld }
