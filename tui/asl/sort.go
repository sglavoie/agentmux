package asl

// This file owns the order the Runs list is presented in. The ordering is
// total: every comparison ends in a deterministic tie-break, so an unchanged
// snapshot always renders identically and a poll can never appear to shuffle
// the list under the cursor.

import (
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

// SortRows orders needs-action ahead of in-progress and everything else, then
// by ASL's own urgency rank, then by most recent activity. Label and plan
// directory break the final tie so an unchanged snapshot always renders in the
// same order.
func SortRows(rows []aslbridge.Row) []aslbridge.Row {
	ordered := append([]aslbridge.Row(nil), rows...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if ga, gb := groupRank(a.UrgencyGroup), groupRank(b.UrgencyGroup); ga != gb {
			return ga < gb
		}
		if a.UrgencyRank != b.UrgencyRank {
			return a.UrgencyRank < b.UrgencyRank
		}
		at, bt := activityTime(a), activityTime(b)
		if !at.Equal(bt) {
			return at.After(bt)
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.PlanDir < b.PlanDir
	})
	return ordered
}

// groupRank mirrors ASL's urgency grouping. An unknown group sorts after every
// known one rather than silently joining needs-action.
func groupRank(group string) int {
	switch group {
	case "needs-action":
		return 0
	case "in-progress":
		return 1
	case "ready":
		return 2
	case "complete":
		return 3
	default:
		return 4
	}
}

// activityTime is when a run was last seen doing something, falling back to
// when it started and finally to the zero time for a run that reports neither.
func activityTime(row aslbridge.Row) time.Time {
	for _, stamp := range []string{row.LastActivityAt, row.RunStartedAt} {
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(stamp)); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
