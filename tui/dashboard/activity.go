package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// ageWarningGlyph is appended to the activity text once the time since the
// last hook event has crossed the 80% warning band. Rendered in
// styleAgeWarning so it reads as "approaching stale" without competing with
// the row's icon column.
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

// activityColumnWidth returns a stable width for every row in the current
// filtered result set: the widest rendered activity text plus one trailing
// space. Mirrors tabNumberColumnWidth — one scan per render pass, so the
// column cannot jitter between rows of the same frame.
func activityColumnWidth(rows []session.RunningSession, watchdog map[int]protocol.WatchdogState, now time.Time) int {
	widest := layout.Width("—")
	for _, s := range rows {
		text, _ := activityCellText(s, watchdog[s.WindowID], now)
		if w := layout.Width(text); w > widest {
			widest = w
		}
	}
	return widest + 1
}

// renderActivityCell builds one activity cell padded to width. The trailing
// space is part of the cell so callers can concatenate it before the
// directory without an extra separator. Warning rows get the warning glyph
// appended and the whole cell styled in styleAgeWarning.
func renderActivityCell(s session.RunningSession, ws protocol.WatchdogState, now time.Time, width int) string {
	text, warning := activityCellText(s, ws, now)
	pad := width - 1 - layout.Width(text)
	if pad < 0 {
		pad = 0
	}
	cell := text + strings.Repeat(" ", pad)
	if warning {
		cell = styleAgeWarning.Render(cell)
	}
	return cell + " "
}
