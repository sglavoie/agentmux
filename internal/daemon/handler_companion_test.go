package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func snoozeRequest(mode string, until int64) protocol.Request {
	return protocol.Request{Type: protocol.TypeNotificationSnooze, Snooze: &protocol.SnoozeRequest{Mode: mode, Until: until}}
}

// drainCompanionDirty empties the doorbell so a test can tell whether the
// request under test rang it.
func drainCompanionDirty(h *Handler) {
	select {
	case <-h.companion.dirty:
	default:
	}
}

func TestNotificationSnoozeSetsReadsAndClears(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	until := time.Now().Add(time.Hour).Unix()

	drainCompanionDirty(h)
	resp := h.Handle(snoozeRequest(protocol.SnoozeModeUntil, until))
	if !resp.OK || resp.Snooze == nil || resp.Snooze.SnoozedUntil != until {
		t.Fatalf("until response = %+v", resp)
	}
	select {
	case <-h.companion.dirty:
	default:
		t.Fatal("a snooze change did not ring the companion hub")
	}

	read := h.Handle(protocol.Request{Type: protocol.TypeNotificationSnooze})
	if !read.OK || read.Snooze == nil || read.Snooze.SnoozedUntil != until {
		t.Fatalf("read response = %+v", read)
	}
	if health := h.Handle(protocol.Request{Type: protocol.TypeHealth}); health.Notification == nil || health.Notification.SnoozedUntil != until {
		t.Fatalf("health notification = %+v, want snoozed_until %d", health.Notification, until)
	}
	if snap := h.buildCompanionSnapshot(time.Now()); snap.Notification.SnoozedUntil != until {
		t.Fatalf("companion notification = %+v, want snoozed_until %d", snap.Notification, until)
	}

	resp = h.Handle(snoozeRequest(protocol.SnoozeModeIndefinite, 0))
	if !resp.OK || resp.Snooze == nil || !resp.Snooze.Indefinite {
		t.Fatalf("indefinite response = %+v", resp)
	}
	resp = h.Handle(snoozeRequest(protocol.SnoozeModeClear, 0))
	if !resp.OK || resp.Snooze == nil || *resp.Snooze != (protocol.SnoozeState{}) {
		t.Fatalf("clear response = %+v", resp)
	}
}

func TestNotificationSnoozeRefusalChangesNothing(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	drainCompanionDirty(h)
	for name, req := range map[string]protocol.Request{
		"past end":     snoozeRequest(protocol.SnoozeModeUntil, time.Now().Add(-time.Minute).Unix()),
		"unknown mode": snoozeRequest("forever", 0),
	} {
		if resp := h.Handle(req); resp.OK || !strings.HasPrefix(resp.Error, "notification_snooze: ") {
			t.Fatalf("%s: response = %+v, want a notification_snooze error", name, resp)
		}
	}
	select {
	case <-h.companion.dirty:
		t.Fatal("a refused snooze rang the companion hub")
	default:
	}
	if got := h.notifier.Snooze(); got != (protocol.SnoozeState{}) {
		t.Fatalf("snooze after refusals = %+v", got)
	}
}
