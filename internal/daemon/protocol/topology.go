package protocol

import "time"

// TopologyStaleAfter is the age at which a cached session list needs a
// refresh warning. The dashboard uses the same threshold for every backend.
const TopologyStaleAfter = 6 * time.Second

// TopologyStale reports whether a published session list has aged past the
// refresh threshold. A list that has never been published is cold, not stale.
func TopologyStale(ready bool, age time.Duration) bool {
	return ready && age >= TopologyStaleAfter
}
