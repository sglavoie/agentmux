// Package timeutil contains time calculations shared by retention paths.
package timeutil

import "time"

const day = 24 * time.Hour

// DaysAgo returns the cutoff for a positive number of 24-hour days. An
// unrepresentably large window has no age cutoff: overflowing a duration
// would otherwise turn retention into a cutoff in the future.
func DaysAgo(now time.Time, days int) time.Time {
	if days <= 0 || days > int(time.Duration(1<<63-1)/day) {
		return time.Time{}
	}
	return now.Add(-time.Duration(days) * day)
}
