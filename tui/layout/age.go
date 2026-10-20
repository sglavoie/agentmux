package layout

import (
	"fmt"
	"time"
)

// Age labels. Every surface that says how long ago something happened builds
// its label from the primitives below, so the units, the clamping of a future
// timestamp, and the tokens for "just happened" and "unknown" read the same in
// every list and every sentence. Each helper takes the reference instant
// explicitly; none reads the wall clock, which is the caller's to supply.

const (
	// AgeNow is the compact token for an age under a minute.
	AgeNow = "now"
	// AgeUnknown is the compact token for a missing (zero) timestamp, which
	// would otherwise read as an absurd age measured from the Go epoch.
	AgeUnknown = "?"
)

// clampAge treats a timestamp in the future (clock skew between the daemon,
// the cache, and the TUI) as having happened right now.
func clampAge(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

// AgeUnits is the coarse single-unit age the prose labels are made of: whole
// seconds under a minute, then minutes, hours, and days ("12s", "3m", "1h",
// "2d"). Ages are read to judge whether something is current, so a single
// unit is enough. A negative duration clamps to zero.
func AgeUnits(d time.Duration) string {
	d = clampAge(d)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

// Ago is the prose label for an event at at, seen from now: AgeUnits plus
// " ago" ("12s ago", "3m ago").
func Ago(at, now time.Time) string {
	return AgeUnits(now.Sub(at)) + " ago"
}

// CompactAge is the column label for how long ago at was: AgeUnknown for a
// zero time, AgeNow under a minute, then the same minute, hour, and day units
// as AgeUnits. It fits in at most five cells for any age under 274 years.
func CompactAge(at, now time.Time) string {
	if at.IsZero() {
		return AgeUnknown
	}
	d := clampAge(now.Sub(at))
	if d < time.Minute {
		return AgeNow
	}
	return AgeUnits(d)
}

// When is the timestamp column of the Past Sessions and plan lists:
// "NNm ago" right-aligned within the hour (at least one minute once any time has passed),
// "NNh ago" within the day, "NNd ago" within the week, and the calendar
// date after that. A zero time is ten blank cells. The relative forms are nine
// cells and the date ten; the lists have always rendered that shape.
func When(at, now time.Time) string {
	if at.IsZero() {
		return "          "
	}
	delta := clampAge(now.Sub(at))
	switch {
	case delta < time.Hour:
		mins := int(delta.Minutes())
		if mins < 1 && delta > 0 {
			mins = 1
		}
		return fmt.Sprintf("%9s", fmt.Sprintf("%dm ago", mins))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%9s", fmt.Sprintf("%dh ago", int(delta.Hours())))
	case delta < 7*24*time.Hour:
		return fmt.Sprintf("%9s", fmt.Sprintf("%dd ago", int(delta.Hours()/24)))
	default:
		return at.Format("2006-01-02")
	}
}
