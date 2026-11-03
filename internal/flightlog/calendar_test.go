package flightlog

import (
	"testing"
	"time"
)

func TestTodayStartsAtTheFirstInstantAfterASkippedMidnight(t *testing.T) {
	t.Parallel()
	zone, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, zone)
	want := time.Date(2026, 9, 6, 1, 0, 0, 0, zone)
	if got := periodStart("today", now); !got.Equal(want) {
		t.Fatalf("today starts at %s, want %s", got, want)
	}
	week := time.Date(2026, 8, 31, 0, 0, 0, 0, zone)
	if got := periodStart("week", now); !got.Equal(week) {
		t.Fatalf("week starts at %s, want %s", got, week)
	}
}

func TestCalendarAdvancesAcrossASkippedMidnight(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"America/Santiago", "Africa/Cairo"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			zone, err := time.LoadLocation(name)
			if err != nil {
				t.Fatal(err)
			}
			month, day := time.September, 6
			if name == "Africa/Cairo" {
				month, day = time.April, 24
			}
			start := time.Date(2026, month, day, 23, 59, 45, 0, zone)
			end := start.Add(30 * time.Second)
			now := end.Add(time.Hour)
			first := event("work", "rs", "state", "bolt", 0)
			first.At = start
			last := event("sample", "rs", "sample", "bolt", 0)
			last.At = end
			report := Build(testSnapshot(first, last), Filters{Period: "all"}, now, nil)
			if report.Totals.WorkingSeconds != 30 {
				t.Fatalf("working time = %v, want 30 seconds", report.Totals.WorkingSeconds)
			}
			seen := map[string]bool{}
			for _, date := range report.Days {
				if seen[date.Date] {
					t.Fatalf("calendar repeats date %s", date.Date)
				}
				seen[date.Date] = true
				want := 0.0
				if date.Date == start.Format(time.DateOnly) || date.Date == end.Format(time.DateOnly) {
					want = 15
				}
				if date.Metrics.WorkingSeconds != want {
					t.Errorf("%s working time = %v, want %v", date.Date, date.Metrics.WorkingSeconds, want)
				}
			}
			for _, date := range []time.Time{start, end} {
				if !seen[date.Format(time.DateOnly)] {
					t.Errorf("calendar omits %s", date.Format(time.DateOnly))
				}
			}
		})
	}
}
