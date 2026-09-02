package eventlog

import (
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

// Detail deep-copies a window's rows for a reader, applying enrich to each copy
// so live-only correlation (the notification outcome of an asynchronous
// dispatch, say) can be stamped on without being retained in the ring.
//
// The result is always non-nil: an unknown window yields an empty slice rather
// than nil so the TUI can distinguish "no daemon error" from "no events yet"
// without a nil check. The copies are deep, so a caller that mutates them
// cannot corrupt the daemon's ring.
func Detail(st *state.WindowState, enrich func(protocol.SessionEvent) protocol.SessionEvent) []protocol.SessionEvent {
	var src []protocol.SessionEvent
	if st != nil {
		src = st.EventLog
	}
	out := make([]protocol.SessionEvent, len(src))
	for i := range src {
		row := events.CloneSessionEvent(src[i])
		if enrich != nil {
			row = enrich(row)
		}
		out[i] = row
	}
	return out
}

// TrimLast keeps at most the last rows of an exported window, which is the
// tail an operator asks for with `amx daemon export-events --last N`. A
// non-positive last keeps everything. The result is always a fresh slice so
// the tail never aliases the exported snapshot it was cut from.
func TrimLast(rows []protocol.SessionEvent, last int) []protocol.SessionEvent {
	if last > 0 && len(rows) > last {
		rows = rows[len(rows)-last:]
	}
	out := make([]protocol.SessionEvent, len(rows))
	copy(out, rows)
	return out
}

// CloneWindowMetadata deep-copies the durable per-window export snapshot that
// travels alongside the rows, so an exported reader owns every map and slice
// it can reach.
func CloneWindowMetadata(in protocol.WindowDebugMetadata) protocol.WindowDebugMetadata {
	out := in
	if in.Stats.SuppressionReasons != nil {
		out.Stats.SuppressionReasons = make(map[string]int, len(in.Stats.SuppressionReasons))
		for reason, count := range in.Stats.SuppressionReasons {
			out.Stats.SuppressionReasons[reason] = count
		}
	}
	if in.Shadow.Mismatches != nil {
		out.Shadow.Mismatches = make([]protocol.ShadowMismatch, len(in.Shadow.Mismatches))
		copy(out.Shadow.Mismatches, in.Shadow.Mismatches)
	}
	if in.Watchdog.ScreenDeclines != nil {
		out.Watchdog.ScreenDeclines = make([]protocol.ScreenDeclineSnapshot, len(in.Watchdog.ScreenDeclines))
		copy(out.Watchdog.ScreenDeclines, in.Watchdog.ScreenDeclines)
	}
	out.Renderer.Generations = rendering.CloneTraces(in.Renderer.Generations)
	return out
}
