package activity

import (
	"strings"
	"time"
)

// The stale sweep's policy: how long a window may hold an active-tier icon with
// no fresh evidence before the daemon expires the fact behind it. This is the
// active→idle transition the daemon makes on its own, so the policy that
// decides it belongs beside the evidence trackers rather than in the watchdog
// loop that applies it.

// StaleThresholds resolves the sweep duration for an (account, action) pair.
// Per-account overrides win over the global default; a zero value at either
// level means "no threshold configured for this action".
//
// The zero StaleThresholds is usable and disables the sweep, which is what a
// Handler built before its setter runs must behave like.
type StaleThresholds struct {
	global     map[string]time.Duration
	perAccount map[string]map[string]time.Duration
}

// NewStaleThresholds copies the configured per-action sweep thresholds. The
// global map keys are action names ("active", "subagent", "compact"); the
// perAccount map is keyed first by account name (lowercased here, so lookups
// are case-insensitive) and then by action name. Empty per-account entries are
// dropped rather than stored, so AnySet cannot be fooled by an account that
// configured nothing.
func NewStaleThresholds(global map[string]time.Duration, perAccount map[string]map[string]time.Duration) StaleThresholds {
	gs := make(map[string]time.Duration, len(global))
	for action, d := range global {
		gs[action] = d
	}
	pa := make(map[string]map[string]time.Duration, len(perAccount))
	for account, m := range perAccount {
		if len(m) == 0 {
			continue
		}
		inner := make(map[string]time.Duration, len(m))
		for action, d := range m {
			inner[action] = d
		}
		pa[strings.ToLower(account)] = inner
	}
	return StaleThresholds{global: gs, perAccount: pa}
}

// Resolve returns the sweep duration for the given account and action.
func (t StaleThresholds) Resolve(account, action string) time.Duration {
	if action == "" {
		return 0
	}
	if account != "" {
		if inner, ok := t.perAccount[strings.ToLower(account)]; ok {
			if d, ok := inner[action]; ok && d > 0 {
				return d
			}
		}
	}
	return t.global[action]
}

// AnySet reports whether at least one global or per-account threshold is
// positive. The sweep early-exits when this returns false.
func (t StaleThresholds) AnySet() bool {
	for _, d := range t.global {
		if d > 0 {
			return true
		}
	}
	for _, inner := range t.perAccount {
		for _, d := range inner {
			if d > 0 {
				return true
			}
		}
	}
	return false
}

// StaleActionForIcon names the action whose threshold governs a visible icon,
// and "" for an icon the sweep does not judge stale at all.
func StaleActionForIcon(icon string) string {
	switch icon {
	case "bolt":
		return "active"
	case "subagent":
		return "subagent"
	case "compact":
		return "compact"
	}
	return ""
}
