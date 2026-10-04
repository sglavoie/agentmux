package notification

// Permission debouncing state and logic for the Notifier.

import "time"

type pendingNotification struct {
	timer  *time.Timer
	seq    uint64
	trace  NotificationTrace
	target NotificationTarget
}

func (n *Notifier) schedulePermissionLocked(target NotificationTarget, title, subtitle string, trace NotificationTrace) (NotificationDisposition, bool) {
	windowID := target.WindowID
	cancelled, hadPending := n.cancelPendingLocked(windowID, "debounce_refreshed")
	n.nextPendingSeq++
	seq := n.nextPendingSeq
	entry := pendingNotification{seq: seq, trace: trace, target: target}
	entry.timer = time.AfterFunc(n.debounce, func() {
		n.mu.Lock()
		pending, ok := n.pending[windowID]
		if !ok || pending.seq != seq {
			n.mu.Unlock()
			return
		}
		delete(n.pending, windowID)

		key := notifyKey{windowID: windowID, action: "permission"}
		now := n.now()
		// A snooze set while the permission was debouncing still holds it:
		// the operator asked for silence before anything was delivered.
		if n.snoozedLocked(now) {
			n.mu.Unlock()
			n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: "permission", Stage: "decision", Outcome: "suppressed", Reason: "snoozed"})
			return
		}
		if last, ok := n.last[key]; ok && now.Sub(last) < n.cooldown {
			n.mu.Unlock()
			n.emitDisposition(NotificationDisposition{Trace: trace, WindowID: windowID, Action: "permission", Stage: "decision", Outcome: "suppressed", Reason: "cooldown"})
			return
		}
		n.last[key] = now
		n.mu.Unlock()

		n.runDispatch(pending.target, title, subtitle, "permission", trace)
	})
	n.pending[windowID] = entry
	return cancelled, hadPending
}

func (n *Notifier) cancelPendingLocked(windowID int, reason string) (NotificationDisposition, bool) {
	if pending, ok := n.pending[windowID]; ok {
		pending.timer.Stop()
		delete(n.pending, windowID)
		return NotificationDisposition{Trace: pending.trace, WindowID: windowID, Action: "permission", Stage: "decision", Outcome: "cancelled", Reason: reason}, true
	}
	return NotificationDisposition{}, false
}

// CancelPendingDisposition removes any pending permission notification for
// windowID and returns its disposition without invoking the observer. The
// daemon's process-exit and codex clear paths hold their own locks while they
// decide to cancel, so they publish the disposition later via EmitDisposition.
func (n *Notifier) CancelPendingDisposition(windowID int) (NotificationDisposition, bool) {
	return n.cancelPending(windowID)
}

// HasPending reports whether a debounced permission notification is still
// scheduled for windowID. It is the read half of the bookkeeping PurgeWindow
// releases when Kitty stops reporting a window.
func (n *Notifier) HasPending(windowID int) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.pending[windowID]
	return ok
}

// cancelPending removes any pending permission notification for windowID and
// returns its disposition without invoking the observer. Callers that hold
// broader locks can therefore defer observer work until those locks are free.
func (n *Notifier) cancelPending(windowID int) (NotificationDisposition, bool) {
	n.mu.Lock()
	disposition, cancelled := n.cancelPendingLocked(windowID, "superseded")
	n.mu.Unlock()
	return disposition, cancelled
}

// CancelPending cancels any pending permission notification for windowID.
func (n *Notifier) CancelPending(windowID int) {
	disposition, cancelled := n.cancelPending(windowID)
	if cancelled {
		n.emitDisposition(disposition)
	}
}
