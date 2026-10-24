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

// PendingCancellation is what CancelPendingDisposition took from the
// notifier: the cancelled permission notification's disposition, if any, and
// the companion notices still to retract. FinishCancellation carries both out.
type PendingCancellation struct {
	Disposition NotificationDisposition
	Cancelled   bool
	app         AppBackend
	retract     []string
}

// CancelPendingDisposition removes any pending permission notification for
// windowID and takes the companion notices still showing for it, without
// invoking the observer or the companion. The daemon's process-exit and codex
// clear paths hold their own locks while they decide to cancel, so they pass
// the result to FinishCancellation once those locks are released.
func (n *Notifier) CancelPendingDisposition(windowID int) PendingCancellation {
	n.mu.Lock()
	disposition, cancelled := n.cancelPendingLocked(windowID, "superseded")
	app, superseded := n.takeAppNoticesLocked(windowID)
	n.mu.Unlock()
	return PendingCancellation{Disposition: disposition, Cancelled: cancelled, app: app, retract: superseded}
}

// FinishCancellation retracts the notices a CancelPendingDisposition took and
// emits its disposition. Callers run it with no lock of their own held, since
// a retraction reaches the companion and the observer may re-enter them.
func (n *Notifier) FinishCancellation(c PendingCancellation) {
	retractAll(c.app, c.retract, "superseded")
	if c.Cancelled {
		n.emitDisposition(c.Disposition)
	}
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

// CancelPending cancels any pending permission notification for windowID.
//
// The window has moved on, so any companion notice still showing for it is
// retracted as well: a banner announcing a stop the operator already answered
// would only send them to a pane that no longer needs them.
func (n *Notifier) CancelPending(windowID int) {
	n.FinishCancellation(n.CancelPendingDisposition(windowID))
}
