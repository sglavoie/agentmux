package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	runewidth "github.com/mattn/go-runewidth"
)

// toastSeverity classifies a toast for styling and ordering.
type toastSeverity int

const (
	toastInfo toastSeverity = iota
	toastSuccess
	toastError
)

// maxToasts caps the number of toasts shown at once. Older toasts are
// dropped when a push would exceed this — newest-on-top means the user
// always sees the most recent action.
const maxToasts = 3

// defaultToastTTL is the lifetime of a transient toast. Progress toasts
// reuse this so a stalled launch chain still clears itself eventually.
const defaultToastTTL = 4 * time.Second

// progressToastTTL is the lifetime of in-flight progress toasts — short
// enough that an abandoned launch doesn't linger, long enough that the
// per-launch tick (kitten @ launch is sub-second) won't TTL mid-flight.
const progressToastTTL = 10 * time.Second

// toastTickInterval drives the TTL sweeper. Coarse enough to avoid waking
// the runloop too often, fine enough that visible expiry is snappy.
const toastTickInterval = 250 * time.Millisecond

// Toast is a transient status message shown above the footer.
type Toast struct {
	Severity toastSeverity
	Text     string
	Expires  time.Time
	// id ties a toast to a logical channel (e.g., a launch batch) so a
	// follow-up push can replace the existing toast instead of stacking
	// alongside it. Zero means "not addressable" — the toast is purely
	// transient.
	id int
}

// toastTickMsg fires on toastTickInterval while at least one toast is
// alive. Handler sweeps expired toasts and re-arms the ticker if any
// remain.
type toastTickMsg struct{}

// launchProgressMsg reports the result of a single picker-launched
// session. The picker dispatches one of these per selected session via
// tea.Batch so the toast subsystem can show 1/N, 2/N progress.
type launchProgressMsg struct {
	batchID int
	total   int
	title   string
	err     error
}

// toastTickCmd schedules the next toast TTL sweep.
func toastTickCmd() tea.Cmd {
	return tea.Tick(toastTickInterval, func(time.Time) tea.Msg {
		return toastTickMsg{}
	})
}

// pushToast appends t to m.toasts, capping at maxToasts (newest on top).
// Returns a cmd that arms the toast tick when the list transitions from
// empty to non-empty; otherwise nil (the existing tick handles TTL).
func (m *Model) pushToast(t Toast) tea.Cmd {
	if t.Expires.IsZero() {
		t.Expires = time.Now().Add(defaultToastTTL)
	}
	wasEmpty := len(m.toasts) == 0
	// Newest on top: prepend and trim.
	m.toasts = append([]Toast{t}, m.toasts...)
	if len(m.toasts) > maxToasts {
		m.toasts = m.toasts[:maxToasts]
	}
	if wasEmpty {
		return toastTickCmd()
	}
	return nil
}

// replaceToastByID replaces (or pushes) a toast carrying the given id.
// Used for progress toasts so successive "Launched 1/N · …" updates
// occupy the same slot rather than stacking. id == 0 means "not addressable"
// so the toast is appended as a fresh transient entry instead of merging
// with whatever happens to sit in the slot — a stray zero would otherwise
// clobber a recent unrelated info toast (whose id field defaults to 0).
func (m *Model) replaceToastByID(id int, t Toast) tea.Cmd {
	if t.Expires.IsZero() {
		t.Expires = time.Now().Add(defaultToastTTL)
	}
	t.id = id
	if id != 0 {
		for i := range m.toasts {
			if m.toasts[i].id == id {
				m.toasts[i] = t
				return nil
			}
		}
	}
	return m.pushToast(t)
}

// sweepToasts drops every toast whose TTL has elapsed. Returns a cmd
// re-arming the tick when any toast is still alive; nil otherwise so the
// ticker quiesces when nothing is on screen.
func (m *Model) sweepToasts() tea.Cmd {
	now := time.Now()
	kept := m.toasts[:0]
	for _, t := range m.toasts {
		if now.Before(t.Expires) {
			kept = append(kept, t)
		}
	}
	m.toasts = kept
	if len(m.toasts) == 0 {
		return nil
	}
	return toastTickCmd()
}

// renderToasts renders the toast strip (one line per toast, newest on
// top). Returns "" when no toasts are alive so the footer layout collapses.
func renderToasts(m Model) string {
	if len(m.toasts) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, t := range m.toasts {
		text := "  " + t.Text
		if m.width > 0 {
			text = runewidth.Truncate(text, m.width, "…")
		}
		switch t.Severity {
		case toastSuccess:
			sb.WriteString(styleToastSuccess.Render(text))
		case toastError:
			sb.WriteString(styleToastError.Render(text))
		default:
			sb.WriteString(styleToastInfo.Render(text))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
