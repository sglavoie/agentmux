package eventlog

import (
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

// MaxEventsPerWindow caps the per-window ring buffer served by the session
// detail pane. At ~50 windows × ~200 B/event, the upper bound stays around
// ~5 MB worst case.
const MaxEventsPerWindow = 5000

// Sequence is the daemon's global monotonic receipt cursor. Every appended row
// is stamped with its next position so exports can totally order receipts
// across windows regardless of which path appended them. Receipt order is a
// process-lifetime notion: the cursor restarts from zero with the daemon.
//
// Sequence is deliberately CALLER-LOCKED and zero-value ready, following the
// activity.Samples precedent: the daemon advances the cursor in the same
// stateMu critical section that appends the row it stamps, so an internal
// mutex would hide an atomicity requirement rather than satisfy it.
//
// Only Append advances it. Holders observe the cursor through Value, which
// keeps "a position was handed out" and "a row was appended" the same event.
type Sequence struct {
	n uint64
}

// next advances the cursor and returns the position to stamp.
func (s *Sequence) next() uint64 {
	if s == nil {
		return 0
	}
	s.n++
	return s.n
}

// Value reports the last position handed out without advancing the cursor.
func (s *Sequence) Value() uint64 {
	if s == nil {
		return 0
	}
	return s.n
}

// Append records event into st's bounded ring and returns the stamped row.
// Older entries are dropped from the front once the slice would exceed
// MaxEventsPerWindow. Every appended event is stamped with the next receipt
// position from seq and, when the producer did not set one, a nanosecond
// receipt time.
//
// A caller-supplied event reference is resolved rather than retained: the
// producer's reference learns the receipt position it can look the row up by,
// and the row itself drops the pointer so the ring never aliases request
// state. Callers hold the lock that guards st.
func Append(st *state.WindowState, seq *Sequence, event protocol.SessionEvent) protocol.SessionEvent {
	if st == nil {
		return event
	}

	event.DaemonSeq = seq.next()
	if event.EventRef != nil {
		event.EventRef.DaemonSeq = event.DaemonSeq
		event.EventRef = nil
	}
	if event.ReceivedAtNs == 0 {
		event.ReceivedAtNs = time.Now().UnixNano()
	}

	buf := st.EventLog
	buf = append(buf, event)
	if len(buf) > MaxEventsPerWindow {
		buf = buf[len(buf)-MaxEventsPerWindow:]
	}
	st.EventLog = buf
	return event
}

// ByReference returns the live row a producer's event reference points at, or
// nil when the reference is unresolved or its row has already aged out of the
// ring. The returned pointer aliases the ring, so callers hold the lock that
// guards st for as long as they use it.
func ByReference(st *state.WindowState, ref *protocol.SessionEventReference) *protocol.SessionEvent {
	if st == nil || ref == nil || ref.DaemonSeq == 0 {
		return nil
	}
	for i := len(st.EventLog) - 1; i >= 0; i-- {
		if st.EventLog[i].DaemonSeq == ref.DaemonSeq {
			return &st.EventLog[i]
		}
	}
	return nil
}

// ByRequestID returns the newest row carrying requestID, or nil when the
// request never produced a row. An empty request ID never matches: rows
// predating request identity would otherwise all collide on "".
func ByRequestID(st *state.WindowState, requestID string) *protocol.SessionEvent {
	if st == nil || requestID == "" {
		return nil
	}
	for i := len(st.EventLog) - 1; i >= 0; i-- {
		if st.EventLog[i].RequestID == requestID {
			return &st.EventLog[i]
		}
	}
	return nil
}

// Latest returns the most recently appended row, or nil for an empty ring.
func Latest(st *state.WindowState) *protocol.SessionEvent {
	if st == nil || len(st.EventLog) == 0 {
		return nil
	}
	return &st.EventLog[len(st.EventLog)-1]
}

// IsDuplicateReplay reports whether a row is the non-mutating record of an IPC
// retry that the dedupe cache replayed before reducer admission. Such rows
// retain the original request's logical identity for correlation but caused
// none of its downstream effects, so causal linking must skip them.
func IsDuplicateReplay(event *protocol.SessionEvent) bool {
	return event != nil && event.ReducerDecision == protocol.DecisionDuplicate &&
		event.DecisionReason == protocol.DecisionReasonRequestReplayed
}

// FinishRender stamps the outcome of a completed render generation back onto
// every row that caused it, matched by trace or request identity. Callers hold
// the lock that guards st.
func FinishRender(st *state.WindowState, trace protocol.RenderGenerationTrace) {
	if st == nil {
		return
	}
	traceIDs := make(map[string]struct{}, len(trace.TraceIDs))
	requestIDs := make(map[string]struct{}, len(trace.RequestIDs))
	for _, id := range trace.TraceIDs {
		traceIDs[id] = struct{}{}
	}
	for _, id := range trace.RequestIDs {
		requestIDs[id] = struct{}{}
	}
	for i := range st.EventLog {
		event := &st.EventLog[i]
		// Delivery retries retain their logical request ID for correlation, but
		// the replay row did not cause the original request's render. Do not let
		// request-ID matching manufacture downstream causality for that row.
		if IsDuplicateReplay(event) {
			continue
		}
		_, traceMatch := traceIDs[event.TraceID]
		_, requestMatch := requestIDs[event.RequestID]
		if !traceMatch && !requestMatch {
			continue
		}
		event.RenderGeneration = trace.Generation
		event.RenderOutcome = trace.Stage
		event.RenderReason = trace.Reason
	}
}

// LinkProbeTrace attaches a probe's trace identity to the newest row that
// caused it, so a probe launched by an event remains followable from that
// event. Replayed duplicate rows are skipped for the same reason FinishRender
// skips them. Callers hold the lock that guards st.
func LinkProbeTrace(st *state.WindowState, parentTraceID, childTraceID string) {
	if st == nil || parentTraceID == "" || childTraceID == "" {
		return
	}
	for i := len(st.EventLog) - 1; i >= 0; i-- {
		if IsDuplicateReplay(&st.EventLog[i]) {
			continue
		}
		if st.EventLog[i].TraceID != parentTraceID {
			continue
		}
		st.EventLog[i].ProbeTraceIDs = rendering.AppendCausalID(st.EventLog[i].ProbeTraceIDs, childTraceID)
		return
	}
}
