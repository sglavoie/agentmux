package daemon

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
		if last, ok := n.last[key]; ok && now.Sub(last) < cooldownDuration {
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
