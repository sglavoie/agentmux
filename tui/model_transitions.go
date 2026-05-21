package tui

import "time"

// pulseDecay is how long a recorded state transition keeps pulsing in the
// list before it's swept off.
const pulseDecay = 5 * time.Second

// recordTransitions diffs the current m.sessions against m.lastSeenStates
// and records any state change in m.recentTransitions. On the very first
// refresh (lastSeenStates is nil) the snapshot is seeded silently so the
// initial list does not pulse every row.
func (m *Model) recordTransitions() {
	if m.lastSeenStates == nil {
		m.lastSeenStates = make(map[int]string, len(m.sessions))
		for _, s := range m.sessions {
			m.lastSeenStates[s.WindowID] = s.State
		}
		return
	}
	for _, s := range m.sessions {
		prev, ok := m.lastSeenStates[s.WindowID]
		if !ok {
			// New session since last snapshot — treat as no-op so a freshly
			// launched tab does not pulse on its first appearance.
			m.lastSeenStates[s.WindowID] = s.State
			continue
		}
		if prev == s.State {
			continue
		}
		if m.recentTransitions == nil {
			m.recentTransitions = make(map[int]time.Time)
		}
		if _, already := m.recentTransitions[s.WindowID]; !already {
			m.recentTransitions[s.WindowID] = time.Now()
		}
	}
}

// sweepRecentTransitions drops entries older than pulseDecay so the maps
// stay bounded and stale pulses do not linger after their visual window.
func (m *Model) sweepRecentTransitions() {
	if len(m.recentTransitions) == 0 {
		return
	}
	cutoff := time.Now().Add(-pulseDecay)
	for id, t := range m.recentTransitions {
		if t.Before(cutoff) {
			delete(m.recentTransitions, id)
		}
	}
}

// snapshotSeenStates refreshes the per-session state snapshot to match the
// current view and clears any in-flight pulses. Called on every ModeList
// key press so the pulse layer surfaces only changes the user has not yet
// seen.
func (m *Model) snapshotSeenStates() {
	if m.lastSeenStates == nil {
		m.lastSeenStates = make(map[int]string, len(m.sessions))
	}
	for _, s := range m.sessions {
		m.lastSeenStates[s.WindowID] = s.State
	}
	if len(m.recentTransitions) > 0 {
		m.recentTransitions = nil
	}
}

// hasActivePulse reports whether windowID has a recent-transition record
// that has not yet decayed past pulseDecay.
func (m Model) hasActivePulse(windowID int) bool {
	if len(m.recentTransitions) == 0 {
		return false
	}
	t, ok := m.recentTransitions[windowID]
	if !ok {
		return false
	}
	return time.Since(t) < pulseDecay
}
