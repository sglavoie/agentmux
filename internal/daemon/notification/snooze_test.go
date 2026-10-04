package notification

// The in-memory snooze: its three modes, its expiry, its place in the gate
// order right after quiet hours, and its reach into a permission that was
// already debouncing when the snooze was set. A snooze must never record a
// cooldown, or clearing it would still leave the next real notification
// silenced.

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// dispositionLog collects decision reasons; the debounce timer reports from
// its own goroutine, so reads and writes share a mutex.
type dispositionLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *dispositionLog) observe(d NotificationDisposition) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, d.Stage+":"+d.Outcome+":"+d.Reason)
}

func (l *dispositionLog) has(entry string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.entries {
		if e == entry {
			return true
		}
	}
	return false
}

func (l *dispositionLog) first() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) == 0 {
		return ""
	}
	return l.entries[0]
}

func TestSnoozeModes(t *testing.T) {
	n, _ := newTestNotifier(&config.NotificationConfig{Stop: true})
	t0 := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	n.now = func() time.Time { return t0 }

	if got := n.Snooze(); got != (protocol.SnoozeState{}) {
		t.Fatalf("fresh notifier snooze = %+v, want none", got)
	}
	until := t0.Add(time.Hour)
	got, err := n.SetSnooze(protocol.SnoozeModeUntil, until)
	if err != nil || got != (protocol.SnoozeState{SnoozedUntil: until.Unix()}) {
		t.Fatalf("SetSnooze(until) = %+v, %v", got, err)
	}
	got, err = n.SetSnooze(protocol.SnoozeModeIndefinite, time.Time{})
	if err != nil || got != (protocol.SnoozeState{Indefinite: true}) {
		t.Fatalf("SetSnooze(indefinite) = %+v, %v", got, err)
	}
	if capability := n.Capability(); !capability.SnoozeIndefinite || capability.SnoozedUntil != 0 {
		t.Fatalf("capability = %+v, want the indefinite snooze stamped on it", capability)
	}
	got, err = n.SetSnooze(protocol.SnoozeModeClear, time.Time{})
	if err != nil || got != (protocol.SnoozeState{}) {
		t.Fatalf("SetSnooze(clear) = %+v, %v", got, err)
	}
	if capability := n.Capability(); capability.SnoozeIndefinite || capability.SnoozedUntil != 0 {
		t.Fatalf("capability = %+v, want no snooze after clear", capability)
	}
}

func TestSnoozeRefusesPastEndAndUnknownMode(t *testing.T) {
	n, _ := newTestNotifier(&config.NotificationConfig{Stop: true})
	t0 := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	n.now = func() time.Time { return t0 }
	if _, err := n.SetSnooze(protocol.SnoozeModeIndefinite, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.SetSnooze(protocol.SnoozeModeUntil, t0); err == nil {
		t.Fatal("a snooze ending now was accepted")
	}
	if _, err := n.SetSnooze("forever", time.Time{}); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
	if got := n.Snooze(); !got.Indefinite {
		t.Fatalf("a refused request changed the snooze to %+v", got)
	}
}

func TestSnoozeExpires(t *testing.T) {
	n, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	n.now = func() time.Time { return now }
	if _, err := n.SetSnooze(protocol.SnoozeModeUntil, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if got := n.Snooze(); got != (protocol.SnoozeState{}) {
		t.Fatalf("snooze at its end = %+v, want none", got)
	}
	if capability := n.Capability(); capability.SnoozedUntil != 0 {
		t.Fatalf("capability snoozed_until = %d after expiry", capability.SnoozedUntil)
	}
	synctest.Test(t, func(t *testing.T) {
		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()
	})
	if rec.len() != 1 {
		t.Fatalf("dispatches after expiry = %d, want 1", rec.len())
	}
}

func TestSnoozeSuppressesWithAReasonAndNoCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, rec := newTestNotifier(&config.NotificationConfig{Stop: true})
		var log dispositionLog
		n.SetObserver(log.observe)
		if _, err := n.SetSnooze(protocol.SnoozeModeIndefinite, time.Time{}); err != nil {
			t.Fatal(err)
		}

		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()
		if rec.len() != 0 {
			t.Fatal("a snoozed notification was dispatched")
		}
		if got := log.first(); got != "decision:suppressed:snoozed" {
			t.Fatalf("first disposition = %q, want decision:suppressed:snoozed", got)
		}

		if _, err := n.SetSnooze(protocol.SnoozeModeClear, time.Time{}); err != nil {
			t.Fatal(err)
		}
		n.MaybeNotify(1, "W:api", "work", "stop", false)
		synctest.Wait()
		if rec.len() != 1 {
			t.Fatalf("dispatches after clear = %d, want 1 (the snooze must not leave a cooldown)", rec.len())
		}
	})
}

// TestSnoozeRunsAfterQuietHours pins the gate order: inside both, the
// configured reason wins over the transient one.
func TestSnoozeRunsAfterQuietHours(t *testing.T) {
	n, rec := newTestNotifier(&config.NotificationConfig{Stop: true, QuietHours: "22:00-08:00"})
	var log dispositionLog
	n.SetObserver(log.observe)
	n.now = func() time.Time { return time.Date(2026, 10, 2, 23, 0, 0, 0, time.Local) }
	if _, err := n.SetSnooze(protocol.SnoozeModeIndefinite, time.Time{}); err != nil {
		t.Fatal(err)
	}
	n.MaybeNotifyTraced(1, "W:api", "work", "stop", false, NotificationTrace{})
	if rec.len() != 0 {
		t.Fatal("a notification inside quiet hours and a snooze was dispatched")
	}
	if got := log.first(); got != "decision:suppressed:quiet_hours" {
		t.Fatalf("first disposition = %q, want quiet_hours to outrank snoozed", got)
	}
	if log.has("decision:suppressed:snoozed") {
		t.Fatal("both gates reported for one notification")
	}
}

// TestSnoozeLeavesEarlierGatesFirst pins that the mute gate still reports
// first: a muted window is muted whether or not a snooze is set.
func TestSnoozeLeavesEarlierGatesFirst(t *testing.T) {
	n, _ := newTestNotifier(&config.NotificationConfig{Stop: true})
	var log dispositionLog
	n.SetObserver(log.observe)
	n.SetIsMuted(func(int) bool { return true })
	if _, err := n.SetSnooze(protocol.SnoozeModeIndefinite, time.Time{}); err != nil {
		t.Fatal(err)
	}
	n.MaybeNotify(1, "W:api", "work", "stop", false)
	if got := log.first(); got != "decision:suppressed:muted" {
		t.Fatalf("first disposition = %q, want muted", got)
	}
}

func TestSnoozeHoldsADebouncingPermission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, rec := newTestNotifier(&config.NotificationConfig{Permission: true, PermissionDebounceMs: 200})
		var log dispositionLog
		n.SetObserver(log.observe)

		n.MaybeNotify(1, "W:api", "work", "permission", false)
		synctest.Wait()
		if !log.has("decision:scheduled:debounce") {
			t.Fatal("the permission was not scheduled")
		}
		if _, err := n.SetSnooze(protocol.SnoozeModeIndefinite, time.Time{}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if rec.len() != 0 {
			t.Fatal("a permission snoozed during its debounce was dispatched")
		}
		if !log.has("decision:suppressed:snoozed") {
			t.Fatal("the held permission reported no snoozed disposition")
		}
	})
}
