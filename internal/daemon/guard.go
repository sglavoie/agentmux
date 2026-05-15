package daemon

import (
	"sync"
	"time"
)

// guardEntry holds the active guard state for one Kitty window.
type guardEntry struct {
	action    string
	priority  int
	expiresAt time.Time
}

// StateGuard enforces priority-based state transition blocking per window.
// A higher-priority state blocks lower-priority updates until the guard expires.
// Priority order:
//   failure / stop_failure (4) > permission (3) > stop / subagent (2) >
//   active / compact (1) > idle (0) > clear / user_clear (-1).
//
// Notes on equal-priority pairs:
//   - subagent (2) outranks active (1), so the 🤖 icon survives interleaved
//     parent `active` events; it loses to stop, permission, failure, and
//     stop_failure.
//   - compact (1) is informational — equal to active, so any other active-tier
//     state overwrites it via the same-priority fall-through.
//   - stop_failure (4) is terminal: equal in priority to failure but, unlike
//     failure, never cleared by a follow-up active (the only path that clears
//     failure is the string-exact `entry.action == "failure"` branch below).
type StateGuard struct {
	mu       sync.Mutex
	entries  map[int]*guardEntry // keyed by window_id
	duration time.Duration
}

// NewStateGuard creates a guard with the given hold duration.
func NewStateGuard(duration time.Duration) *StateGuard {
	return &StateGuard{
		entries:  make(map[int]*guardEntry),
		duration: duration,
	}
}

// TryUpdate attempts to record action for windowID.
// Returns (true, effectiveAction) if the transition is allowed.
// Returns (false, "") if a higher-priority guard is still active.
//
// Special rules:
//   - idle and clear ALWAYS succeed and clear the guard (cleanup states).
//   - If the current guard is "permission" and the incoming action is "stop",
//     stop is suppressed as "clear" to avoid a spurious stop icon after the user
//     approves a permission request.
//   - If the current guard is "permission" and the incoming action is "active",
//     the guard is cleared so the active state breaks through immediately.
//     This handles the common flow where a tool executes right after permission
//     approval — without it the key icon would persist through long thinking phases.
//   - If the current guard is "failure" and the incoming action is "active",
//     the guard is cleared so the active state breaks through immediately.
//     Failure is transient: the next active event means Claude is back to work,
//     so the ❗ icon should not strand across long tool calls. The check is
//     string-exact on "failure" so it does NOT apply to "stop_failure", which
//     is terminal and must persist until the user explicitly clears it.
//   - If the current guard is "stop" and isPrompt is true, the guard is cleared
//     so the prompt's active state breaks through immediately. The check is
//     string-exact on "stop" so a prompt after a "stop_failure" still falls
//     through to the priority check below, which blocks it (the user has not
//     yet acknowledged the failure).
//   - All other actions are blocked if Priority(new) < Priority(current) and
//     the guard has not yet expired. This is what makes "subagent" (priority 2)
//     survive interleaved "active" (priority 1) events; no special-case branch
//     is needed because the priority check at the end of the chain handles it.
func (g *StateGuard) TryUpdate(windowID int, action string, isPrompt bool) (allowed bool, effectiveAction string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now()

	// idle, clear, and user_clear always remove the guard regardless of what's active.
	if action == "idle" || action == "clear" || action == "user_clear" {
		delete(g.entries, windowID)
		return true, action
	}

	entry, exists := g.entries[windowID]
	if exists && !now.After(entry.expiresAt) {
		// Guard is still active.
		if entry.action == "permission" && action == "stop" {
			// Post-permission stop suppression.
			delete(g.entries, windowID)
			return true, "clear"
		}
		if entry.action == "permission" && action == "active" {
			// Post-permission active: permission resolved, allow active through.
			delete(g.entries, windowID)
		} else if entry.action == "failure" && action == "active" {
			// Failure is transient: the next active event means Claude is back to
			// work, so let it overwrite the ✗ immediately.
			delete(g.entries, windowID)
		} else if entry.action == "stop" && isPrompt {
			// Prompt bypasses stop guard — fall through to set new guard.
			delete(g.entries, windowID)
		} else if Priority(action) < entry.priority {
			return false, ""
		}
	}

	g.entries[windowID] = &guardEntry{
		action:    action,
		priority:  Priority(action),
		expiresAt: now.Add(g.duration),
	}
	return true, action
}

// Current returns the active guard action for windowID, or "" if none / expired.
func (g *StateGuard) Current(windowID int) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.entries[windowID]
	if !ok || time.Now().After(entry.expiresAt) {
		return ""
	}
	return entry.action
}

// Clear removes any guard for windowID.
func (g *StateGuard) Clear(windowID int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.entries, windowID)
}
