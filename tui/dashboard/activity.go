package dashboard

import (
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
)

// ageWarningGlyph is appended to the activity text once the time since the
// last hook event has crossed the 80% warning band. A trailing glyph rather
// than a color: the text it qualifies is rendered inside the detail strip's
// single identity span, and a second color inside that span would break it in
// two for a fact the glyph already carries.
const ageWarningGlyph = "⏳"

// FormatAge returns the detail pane's age label for a single watchdog entry:
// `—` for an entry with no LastAction, otherwise the coarsely rounded time
// since the last hook event (`<n>m` under one hour, `<n>h` beyond).
func FormatAge(ws protocol.WatchdogState, now time.Time) string {
	if ws.LastAction == "" {
		return "—"
	}
	elapsed := now.Sub(time.Unix(ws.LastEventAt, 0))
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed < time.Hour {
		return fmt.Sprintf("%dm", int(elapsed/time.Minute))
	}
	return fmt.Sprintf("%dh", int(elapsed/time.Hour))
}

// Run-timer display contract. Every band rolls over before its leading count
// reaches three digits and the last one saturates, which is what bounds any
// elapsed duration — however absurd — to runTimerMaxCells.
const (
	runTimerMaxCells      = 5
	runTimerHourBandLimit = 100 * time.Hour
	runTimerDayBandLimit  = 100 * 24 * time.Hour
	runTimerOverflowText  = "99d+"
)

// formatRunElapsed renders how long a row's current busy stretch has been
// running, in at most runTimerMaxCells cells: `—` for a row with no anchor,
// `M:SS` below one hour, `<n>h<MM>` below 100 hours, `<n>d<HH>` below 100 days,
// and the saturating `99d+` beyond that. Each band rolls over before its
// leading count reaches three digits, so the activity column can never outgrow
// its reserved width. Negative durations clamp to zero so clock skew between
// the daemon and the TUI cannot surface a count running backwards.
func formatRunElapsed(busySince, now time.Time) string {
	if busySince.IsZero() {
		return "—"
	}
	elapsed := now.Sub(busySince)
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed < time.Hour:
		return fmt.Sprintf("%d:%02d", int(elapsed/time.Minute), int(elapsed/time.Second)%60)
	case elapsed < runTimerHourBandLimit:
		return fmt.Sprintf("%dh%02d", int(elapsed/time.Hour), int(elapsed/time.Minute)%60)
	case elapsed < runTimerDayBandLimit:
		return fmt.Sprintf("%dd%02d", int(elapsed/(24*time.Hour)), int(elapsed/time.Hour)%24)
	default:
		return runTimerOverflowText
	}
}

// activityCellText returns a row's rendered activity text and whether the
// staleness warning fires. The warning keeps the watchdog's 80%-of-threshold
// rule (ThresholdSec ≤ 0 disables it) but applies only to a row that has a
// timer: the reducer is authoritative for what the row displays, so a row
// whose legacy LastAction scalar has diverged from the reduced icon must not
// render a bare `— ⏳`.
func activityCellText(s session.RunningSession, ws protocol.WatchdogState, now time.Time) (text string, warning bool) {
	text = formatRunElapsed(s.BusySince, now)
	if s.BusySince.IsZero() || ws.LastAction == "" || ws.ThresholdSec <= 0 {
		return text, false
	}
	elapsed := now.Sub(time.Unix(ws.LastEventAt, 0))
	threshold := time.Duration(ws.ThresholdSec) * time.Second
	if elapsed*5 < threshold*4 {
		return text, false
	}
	return text + " " + ageWarningGlyph, true
}
