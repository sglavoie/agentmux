package layout

import (
	"testing"
	"time"
)

func TestAgeLabels(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name               string
		at                 time.Time
		ago, compact, when string
	}{
		{"zero", time.Time{}, "", AgeUnknown, "          "},
		{"future", now.Add(5 * time.Second), "0s ago", AgeNow, " 0m   ago"},
		{"seconds", now.Add(-12 * time.Second), "12s ago", AgeNow, " 1m   ago"},
		{"minutes", now.Add(-3 * time.Minute), "3m ago", "3m", " 3m   ago"},
		{"hours", now.Add(-23 * time.Hour), "23h ago", "23h", "23h   ago"},
		{"days", now.Add(-49 * time.Hour), "2d ago", "2d", " 2d   ago"},
		{"weeks", now.Add(-8 * 24 * time.Hour), "8d ago", "8d", "2026-08-25"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ago != "" {
				if got := Ago(tc.at, now); got != tc.ago {
					t.Errorf("Ago = %q, want %q", got, tc.ago)
				}
			}
			if got := CompactAge(tc.at, now); got != tc.compact {
				t.Errorf("CompactAge = %q, want %q", got, tc.compact)
			}
			got := When(tc.at, now)
			if got != tc.when {
				t.Errorf("When = %q, want %q", got, tc.when)
			}
		})
	}
}
