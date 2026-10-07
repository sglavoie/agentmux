package debugger

import (
	"testing"
	"time"
)

func TestHookDeliveryFailuresSummaryPlacesFailuresAgainstDaemonStart(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	latest := now.Add(-5 * time.Minute)
	failures := &HookDeliveryFailures{Count: 2, LatestAt: &latest}

	for _, tc := range []struct {
		name      string
		startedAt time.Time
		predate   bool
		want      string
	}{
		{"restarted after the drops", now.Add(-2 * time.Minute), true, "2 hook deliveries dropped before the daemon started 2m ago; none since"},
		{"running through the drops", now.Add(-time.Hour), false, "2 hook deliveries failed in the trailing hour, the latest 5m ago, while the daemon was running"},
		{"no daemon", time.Time{}, false, "2 hook deliveries failed in the trailing hour, the latest 5m ago; deliveries that cannot reach a daemon are dropped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := failures.PredateDaemonStart(tc.startedAt); got != tc.predate {
				t.Fatalf("PredateDaemonStart = %v, want %v", got, tc.predate)
			}
			if got := failures.Summary(now, tc.startedAt); got != tc.want {
				t.Fatalf("Summary =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}

	// A count that cannot be placed in time is never explained away by a restart.
	unplaced := &HookDeliveryFailures{Count: 1}
	if unplaced.PredateDaemonStart(now.Add(-time.Minute)) {
		t.Fatal("a failure without latest_at was attributed to the restart gap")
	}
	if got, want := unplaced.Summary(now, now.Add(-time.Minute)), "1 hook delivery failed in the trailing hour, while the daemon was running"; got != want {
		t.Fatalf("Summary = %q, want %q", got, want)
	}
}
