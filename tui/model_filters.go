package tui

import (
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
)

type cursorIdentity struct {
	tabID     int
	windowID  int
	key       string
	base      string
	profile   string
	project   string
	harness   string
	directory string
}

// applyFilterChange swaps in a new active filter, re-applies it to the
// session list, and resets cursor + chip focus. Used by chip removal and
// the Esc "clear all" path so they share the bookkeeping.
func (m *Model) applyFilterChange(f Filter) {
	m.activeFilter = f
	m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.sortFiltered()
	m.cursor = 0
	m.focusedChipIdx = -1
	scrollListToCursor(m)
}

// dashboardSessionProfiles returns unique effective profiles from the full
// running-session set in case-insensitive alphabetical order. It deliberately
// ignores m.filtered so text, harness, and state filters do not remove profiles
// from the cycle.
func dashboardSessionProfiles(sessions []session.RunningSession) []string {
	profiles := make([]string, 0)
	seen := make(map[string]bool)
	for _, running := range sessions {
		profile := running.EffectiveProfile()
		key := strings.ToLower(profile)
		if profile == "" || seen[key] {
			continue
		}
		seen[key] = true
		profiles = append(profiles, profile)
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		return strings.ToLower(profiles[i]) < strings.ToLower(profiles[j])
	})
	return profiles
}

// nextDashboardProfileFilter advances through the profiles represented by
// running sessions and then wraps to the empty "all profiles" state. A current
// profile that disappeared during refresh restarts the cycle at the first
// running profile.
func nextDashboardProfileFilter(sessions []session.RunningSession, current string) string {
	profiles := dashboardSessionProfiles(sessions)
	if len(profiles) == 0 {
		return ""
	}
	if current == "" {
		return profiles[0]
	}
	for i, profile := range profiles {
		if !strings.EqualFold(profile, current) {
			continue
		}
		if i+1 < len(profiles) {
			return profiles[i+1]
		}
		return ""
	}
	return profiles[0]
}

// cycleDashboardProfile updates the dashboard's existing @profile filter
// component while preserving all other DSL components.
func (m *Model) cycleDashboardProfile() tea.Cmd {
	next := nextDashboardProfileFilter(m.sessions, m.activeFilter.Account)
	filter := m.activeFilter
	filter.Account = next
	filter.Raw = serializeFilter(filter)
	m.applyFilterChange(filter)
	m.listOffset = 0
	return m.refreshPreviewForCursor()
}

// enterFilter switches into ModeSearch with the given pre-filled buffer and
// snapshots the current filter so Esc can restore it. The pre-fill is parsed
// and live-applied immediately so the list reflects the buffer on entry.
func (m *Model) enterFilter(prefill string) {
	m.clearJumpChord()
	m.savedFilter = m.activeFilter
	m.filterText = prefill
	m.activeFilter = Parse(prefill)
	m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.sortFiltered()
	m.cursor = 0
	m.focusedChipIdx = -1
	m.mode = ModeSearch
	m.recomputeCompletion()
	scrollListToCursor(m)
}

// reapplyFilterLive parses filterText and refreshes the list. Used by every
// keystroke handler in ModeSearch to provide live filtering.
func (m *Model) reapplyFilterLive() {
	m.activeFilter = Parse(m.filterText)
	m.filtered = applyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	m.sortFiltered()
	m.cursor = clamp(m.cursor, 0, len(m.filtered)-1)
	m.focusedChipIdx = -1
	scrollListToCursor(m)
}

// restoreCursor picks the cursor row after the session list is rebuilt.
// Preference order: (1) the row whose Base matches priorBase, (2) the
// first attention-state row (orange band), (3) row 0. Returns 0 for an
// empty list so callers can use the result directly without clamping.
func restoreCursor(filtered []session.RunningSession, priorBase string, lastActivity map[int]time.Time, idle time.Duration) int {
	prior := cursorIdentity{base: priorBase}
	return restoreCursorByIdentity(filtered, prior, lastActivity, idle)
}

func captureCursorIdentity(s session.RunningSession) cursorIdentity {
	return cursorIdentity{
		tabID:     s.TabID,
		windowID:  s.WindowID,
		key:       s.Key,
		base:      s.Base,
		profile:   s.EffectiveProfile(),
		project:   s.EffectiveProject(),
		harness:   s.Harness,
		directory: s.Directory,
	}
}

func restoreCursorByIdentity(filtered []session.RunningSession, prior cursorIdentity, lastActivity map[int]time.Time, idle time.Duration) int {
	if len(filtered) == 0 {
		return 0
	}
	if prior.tabID != 0 {
		for i, s := range filtered {
			if s.TabID == prior.tabID {
				return i
			}
		}
	}
	if prior.windowID != 0 {
		for i, s := range filtered {
			if s.WindowID == prior.windowID {
				return i
			}
		}
	}
	if prior.key != "" {
		for i, s := range filtered {
			if s.Key == prior.key {
				return i
			}
		}
	}
	if prior.base != "" {
		for i, s := range filtered {
			if s.Base == prior.base &&
				s.EffectiveProfile() == prior.profile &&
				s.EffectiveProject() == prior.project &&
				s.Harness == prior.harness &&
				s.Directory == prior.directory {
				return i
			}
		}
		for i, s := range filtered {
			if s.Base == prior.base {
				return i
			}
		}
	}
	for i, s := range filtered {
		if stateGroup(s, lastActivity, idle) == 0 {
			return i
		}
	}
	return 0
}

// applyFilter returns sessions that pass the active Filter.
func applyFilter(sessions []session.RunningSession, f Filter, lastActivity map[int]time.Time, idle time.Duration) []session.RunningSession {
	if f.IsEmpty() {
		out := make([]session.RunningSession, len(sessions))
		copy(out, sessions)
		return out
	}
	var out []session.RunningSession
	for _, s := range sessions {
		if f.Match(s, lastActivity, idle) {
			out = append(out, s)
		}
	}
	return out
}

// sortFiltered applies a stable sort to m.filtered so attention items come
// first, then active, then idle — preserving existing order within each
// group. Uses the model's activity map + idle threshold to bucket sessions
// with no state icon.
func (m Model) sortFiltered() {
	sort.SliceStable(m.filtered, func(i, j int) bool {
		gi := stateGroup(m.filtered[i], m.lastActivity, m.idleThreshold)
		gj := stateGroup(m.filtered[j], m.lastActivity, m.idleThreshold)
		if gi != gj {
			return gi < gj
		}
		// Within Active (group 1), sessions with an explicit state icon sort before those without.
		if gi == 1 {
			si := m.filtered[i].State != ""
			sj := m.filtered[j].State != ""
			if si != sj {
				return si
			}
		}
		return false
	})
}

// clamp constrains v to [lo, hi]. Returns lo when hi < lo.
func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
