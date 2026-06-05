package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// toastSeverity classifies a toast for styling and ordering.
type toastSeverity int

const (
	toastInfo toastSeverity = iota
	toastSuccess
	toastError
)

// maxToasts caps the number of transient status messages retained. Older
// toasts are dropped when a push would exceed this — newest-on-top means the
// sticky status bar always sees the most recent active action first.
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

// Toast is a transient message shown through the sticky status bar.
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
// share one logical channel rather than stacking. Replacements move to the
// top so the sticky status bar treats the update as the latest transient.
// id == 0 means "not addressable" so the toast is appended as a fresh
// transient entry instead of merging with whatever happens to sit in the slot.
func (m *Model) replaceToastByID(id int, t Toast) tea.Cmd {
	if t.Expires.IsZero() {
		t.Expires = time.Now().Add(defaultToastTTL)
	}
	t.id = id
	if id != 0 {
		for i := range m.toasts {
			if m.toasts[i].id == id {
				if i == 0 {
					m.toasts[0] = t
					return nil
				}
				copy(m.toasts[1:i+1], m.toasts[0:i])
				m.toasts[0] = t
				return nil
			}
		}
	}
	return m.pushToast(t)
}

func (m *Model) clearToastByID(id int) {
	if id == 0 || len(m.toasts) == 0 {
		return
	}
	kept := m.toasts[:0]
	for _, t := range m.toasts {
		if t.id != id {
			kept = append(kept, t)
		}
	}
	m.toasts = kept
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
