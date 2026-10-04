package notification

import (
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// The global notification snooze.
//
// A snooze silences every notification for a while, or until it is cleared,
// without touching config.toml: it is an operator's momentary request, so it
// lives in memory only and a daemon restart forgets it. That is the difference
// from quiet hours, which are configuration and are shown read-only. The gate
// runs after quiet_hours, so a notification inside both is reported under the
// configured reason rather than the transient one.

// SetSnooze sets or clears the snooze and returns the snooze now in force.
// Mode is one of protocol.SnoozeModeUntil, SnoozeModeIndefinite, and
// SnoozeModeClear; until is read only for SnoozeModeUntil and must lie in the
// future, because a snooze that has already expired would silently change
// nothing.
func (n *Notifier) SetSnooze(mode string, until time.Time) (protocol.SnoozeState, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch mode {
	case protocol.SnoozeModeUntil:
		if !until.After(n.now()) {
			return protocol.SnoozeState{}, fmt.Errorf("snooze end %s is not in the future", until.Format(time.RFC3339))
		}
		n.snoozeUntil = until
		n.snoozeIndefinite = false
	case protocol.SnoozeModeIndefinite:
		n.snoozeUntil = time.Time{}
		n.snoozeIndefinite = true
	case protocol.SnoozeModeClear:
		n.snoozeUntil = time.Time{}
		n.snoozeIndefinite = false
	default:
		return protocol.SnoozeState{}, fmt.Errorf("unknown snooze mode %q", mode)
	}
	return n.snoozeLocked(n.now()), nil
}

// Snooze returns the snooze in force now. A timed snooze whose end has passed
// reads as no snooze at all.
func (n *Notifier) Snooze() protocol.SnoozeState {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.snoozeLocked(n.now())
}

// snoozeLocked projects the stored snooze at now. The caller holds n.mu.
func (n *Notifier) snoozeLocked(now time.Time) protocol.SnoozeState {
	if n.snoozeIndefinite {
		return protocol.SnoozeState{Indefinite: true}
	}
	if !n.snoozeUntil.IsZero() && n.snoozeUntil.After(now) {
		return protocol.SnoozeState{SnoozedUntil: n.snoozeUntil.Unix()}
	}
	return protocol.SnoozeState{}
}

// snoozed reports whether a snooze covers now.
func (n *Notifier) snoozed(now time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.snoozedLocked(now)
}

// snoozedLocked is snoozed for a caller that already holds n.mu.
func (n *Notifier) snoozedLocked(now time.Time) bool {
	state := n.snoozeLocked(now)
	return state.Indefinite || state.SnoozedUntil != 0
}
