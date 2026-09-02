package eventlog

import "github.com/sglavoie/agentmux/internal/daemon/protocol"

// RecordEvent credits one appended row to a window's durable export counters.
// The counters outlive the ring the row will eventually be trimmed from, which
// is why they are maintained at append time rather than derived on export.
// Callers hold the lock that guards the metadata they own.
func RecordEvent(stats *protocol.WindowDebugStats, event protocol.SessionEvent) {
	if stats == nil {
		return
	}
	stats.EventCount++
	if stats.FirstEventAt == 0 {
		stats.FirstEventAt = event.At
	}
	stats.LastEventAt = event.At
	if event.Suppressed && event.SuppressionReason != "" {
		RecordSuppression(stats, event.SuppressionReason)
	}
}

// RecordSuppression credits a suppression reason on its own. It exists because
// a row can be marked suppressed after it was appended — the reducer decides
// the stable reason only once the decision is finalized — and that later
// verdict must reach the same histogram as a suppression known at append time.
func RecordSuppression(stats *protocol.WindowDebugStats, reason string) {
	if stats == nil || reason == "" {
		return
	}
	if stats.SuppressionReasons == nil {
		stats.SuppressionReasons = make(map[string]int)
	}
	stats.SuppressionReasons[reason]++
}
