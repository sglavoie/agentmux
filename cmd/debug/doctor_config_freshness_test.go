package debug

import (
	"fmt"
	"testing"
	"time"
)

func TestDoctorConfigFreshness(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	uptime := time.Hour
	cases := []struct {
		name    string
		modTime time.Time
		uptime  time.Duration
		want    bool
	}{
		{"edited before the daemon started", now.Add(-2 * time.Hour), uptime, false},
		{"written during the restart itself", now.Add(-uptime).Add(time.Second), uptime, false},
		{"edited after the daemon started", now.Add(-10 * time.Minute), uptime, true},
		{"no uptime reported", now.Add(-time.Minute), 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetDoctorTestState(t)
			doctorNow = func() time.Time { return now }
			modTime := tc.modTime
			doctorConfigModTime = func() (time.Time, error) { return modTime, nil }
			var checks []string
			checkDoctorConfigFreshness(tc.uptime, func(severity, check, format string, args ...any) {
				if severity != severityWarning {
					t.Fatalf("severity = %q, want warning", severity)
				}
				checks = append(checks, check+": "+fmt.Sprintf(format, args...))
			})
			if got := len(checks) == 1; got != tc.want {
				t.Fatalf("findings = %v, want warning=%v", checks, tc.want)
			}
		})
	}
}
