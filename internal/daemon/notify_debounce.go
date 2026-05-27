package daemon

// Permission debouncing state and logic for the Notifier.

import "time"

type pendingNotification struct {
	timer *time.Timer
	seq   uint64
}

func (n *Notifier) schedulePermissionLocked(windowID int, account, title, subtitle string) {
	n.cancelPendingLocked(windowID)
	n.nextPendingSeq++
	seq := n.nextPendingSeq
	entry := pendingNotification{seq: seq}
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
			return
		}
		n.last[key] = now
		n.mu.Unlock()

		n.runDispatch(windowID, account, title, subtitle)
	})
	n.pending[windowID] = entry
}

func (n *Notifier) cancelPendingLocked(windowID int) {
	if pending, ok := n.pending[windowID]; ok {
		pending.timer.Stop()
		delete(n.pending, windowID)
	}
}

// CancelPending cancels any pending permission notification for windowID.
func (n *Notifier) CancelPending(windowID int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cancelPendingLocked(windowID)
}
