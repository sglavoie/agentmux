package daemon

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// This file turns the list coordinator's health edges into durable journal
// evidence. The snapshot block on the wire answers "how is Kitty right now",
// which a live operator can read; the journal answers "has this happened
// before", which is the only question a stall that has already cleared can
// still be asked.

// kittySnapshotStage is the journal stage carrying Kitty list-health edges.
const kittySnapshotStage = "daemon.kitty_snapshot"

// kittySnapshotObserver builds the list-health observer the daemon installs on
// its one Kitty client at startup.
//
// The record is bounded by the coordinator rather than by a counter here: it
// reports only the first failure of a streak and the first success that ends
// one, so a Kitty wedged for an hour writes one record and its recovery writes
// the second. A nil recorder (journal disabled) installs no observer at all
// rather than an observer that discards, so a disabled journal costs nothing on
// the list path.
func (r *lifecycleRecorder) kittySnapshotObserver() kitty.ListObserver {
	if r == nil {
		return nil
	}
	return func(transition kitty.ListTransition) {
		outcome, reason := "success", "list_recovered"
		if transition.Kind == kitty.ListTransitionFailed {
			outcome, reason = "error", "list_failed"
		}
		r.record(kittySnapshotStage, outcome, reason, kittySnapshotEvidence(transition.Status))
	}
}

// kittySnapshotEvidence projects one coordinator status onto the journal
// evidence map.
//
// What is here is what makes a recurrence actionable and stays stable across
// releases: the error class from the fixed timeout/command/decode vocabulary,
// the completed-attempt counters, and the latency the reads that did complete
// took. What is deliberately absent is everything that identifies the machine
// or could carry harness content — the socket value, the kitten argv, the
// command payload, its stderr, and the raw error. The transition carries the
// error itself precisely so a consumer can choose; this one classifies it and
// drops it.
//
// error_class is populated on both edges: the coordinator retains it across a
// recovery, so the recovery record says which failure class it ended.
func kittySnapshotEvidence(status kitty.ListStatus) map[string]string {
	return map[string]string{
		"error_class":          string(status.LastErrorClass),
		"attempts":             fmt.Sprint(status.Attempts),
		"failures":             fmt.Sprint(status.Failures),
		"timeouts":             fmt.Sprint(status.Timeouts),
		"coalesced_waiters":    fmt.Sprint(status.CoalescedWaiters),
		"consecutive_failures": fmt.Sprint(status.ConsecutiveFailures),
		"last_latency_ms":      fmt.Sprint(protocol.DurationMillis(status.LastLatency)),
		"max_latency_ms":       fmt.Sprint(protocol.DurationMillis(status.MaxLatency)),
	}
}
