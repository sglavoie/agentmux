package dashboard

import (
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

// CursorIdentity is everything the dashboard remembers about the row under the
// cursor so it can find that row again after the session list is rebuilt. The
// fields are ordered by how exactly they identify a live instance: the Kitty
// tab and window IDs are exact, the session key is exact for a keyed session,
// and the base name is a last resort that is disambiguated by the launch
// identity fields below it.
type CursorIdentity struct {
	TabID     int
	WindowID  int
	Key       string
	Base      string
	Profile   string
	Project   string
	Harness   string
	Directory string
}

// CaptureCursorIdentity snapshots the row under the cursor before a refresh
// replaces the row set.
func CaptureCursorIdentity(s session.RunningSession) CursorIdentity {
	return CursorIdentity{
		TabID:     s.TabID,
		WindowID:  s.WindowID,
		Key:       s.Key,
		Base:      s.Base,
		Profile:   s.EffectiveProfile(),
		Project:   s.EffectiveProject(),
		Harness:   s.Harness,
		Directory: s.Directory,
	}
}

// RestoreCursor picks the cursor row after the session list is rebuilt.
// Preference order: (1) the row whose Base matches priorBase, (2) the first
// attention-state row (orange band), (3) row 0. Returns 0 for an empty list so
// callers can use the result directly without clamping.
func RestoreCursor(rows []session.RunningSession, priorBase string, lastActivity map[int]time.Time, idle time.Duration) int {
	return RestoreCursorByIdentity(rows, CursorIdentity{Base: priorBase}, lastActivity, idle)
}

// RestoreCursorByIdentity resolves a captured identity against the rebuilt row
// set, walking the identity fields from exact to approximate and falling back
// to the first attention row, then row 0.
func RestoreCursorByIdentity(rows []session.RunningSession, prior CursorIdentity, lastActivity map[int]time.Time, idle time.Duration) int {
	if len(rows) == 0 {
		return 0
	}
	if prior.TabID != 0 {
		for i, s := range rows {
			if s.TabID == prior.TabID {
				return i
			}
		}
	}
	if prior.WindowID != 0 {
		for i, s := range rows {
			if s.WindowID == prior.WindowID {
				return i
			}
		}
	}
	if prior.Key != "" {
		for i, s := range rows {
			if s.Key == prior.Key {
				return i
			}
		}
	}
	if prior.Base != "" {
		for i, s := range rows {
			if s.Base == prior.Base &&
				s.EffectiveProfile() == prior.Profile &&
				s.EffectiveProject() == prior.Project &&
				s.Harness == prior.Harness &&
				s.Directory == prior.Directory {
				return i
			}
		}
		for i, s := range rows {
			if s.Base == prior.Base {
				return i
			}
		}
	}
	for i, s := range rows {
		if StateGroup(s, lastActivity, idle) == 0 {
			return i
		}
	}
	return 0
}
