package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

const maxRenderGenerationTraces = 16

const (
	renderStageDirty     = "dirty"
	renderStageQueued    = "queued"
	renderStageAttempted = "attempted"
	renderStagePersisted = "persisted"
	renderStageRendered  = "rendered"
	renderStageFailed    = "failed"
)

func cloneRenderGenerationTrace(in RenderGenerationTrace) RenderGenerationTrace {
	out := in
	out.SourceGenerations = append([]uint64(nil), in.SourceGenerations...)
	out.TraceIDs = append([]string(nil), in.TraceIDs...)
	out.RequestIDs = append([]string(nil), in.RequestIDs...)
	out.SourceEventIDs = append([]string(nil), in.SourceEventIDs...)
	return out
}

func cloneRenderGenerationTraces(in []RenderGenerationTrace) []RenderGenerationTrace {
	out := make([]RenderGenerationTrace, len(in))
	for i := range in {
		out[i] = cloneRenderGenerationTrace(in[i])
	}
	return out
}

func appendBoundedUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	values = append(values, value)
	if len(values) > MaxRenderCausalIDs {
		values = append([]string(nil), values[len(values)-MaxRenderCausalIDs:]...)
	}
	return values
}

func appendBoundedUniqueGeneration(values []uint64, value uint64) []uint64 {
	if value == 0 {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	values = append(values, value)
	if len(values) > MaxRenderCausalIDs {
		values = append([]uint64(nil), values[len(values)-MaxRenderCausalIDs:]...)
	}
	return values
}

func renderGenerationTerminal(trace RenderGenerationTrace) bool {
	// Failed generations remain retryable. Their durable failure row stays in
	// the journal, while the in-memory generation can advance through a later
	// queued/attempted/successful retry without creating a duplicate generation.
	return trace.Stage == renderStageRendered
}

func renderGenerationLocked(st *WindowState, generation uint64) *RenderGenerationTrace {
	for i := range st.RenderGenerations {
		if st.RenderGenerations[i].Generation == generation {
			return &st.RenderGenerations[i]
		}
	}
	st.RenderGenerations = append(st.RenderGenerations, RenderGenerationTrace{
		Generation:        generation,
		SourceGenerations: []uint64{generation},
		Stage:             renderStageDirty,
		Outcome:           "pending",
		UpdatedAt:         time.Now().UnixNano(),
	})
	if len(st.RenderGenerations) > maxRenderGenerationTraces {
		st.RenderGenerations = append([]RenderGenerationTrace(nil), st.RenderGenerations[len(st.RenderGenerations)-maxRenderGenerationTraces:]...)
	}
	return &st.RenderGenerations[len(st.RenderGenerations)-1]
}

// linkRequestRenderGenerationLocked associates the exact event row for req
// with the next state generation before markWindow*DirtyLocked increments Seq.
// Callers hold stateMu.
func (h *Handler) linkRequestRenderGenerationLocked(req Request, generation uint64) {
	if h == nil || req.WindowID <= 0 || generation == 0 {
		return
	}
	st := h.ensureWindowStateLocked(req.WindowID)
	event := eventByReferenceLocked(st, req.eventRef)
	h.linkSessionEventRenderGenerationLocked(req.WindowID, st, event, req.TraceID, req.RequestID, req.SourceEventID, generation)
}

func (h *Handler) linkSessionEventRenderGenerationLocked(
	windowID int,
	st *WindowState,
	event *SessionEvent,
	traceID, requestID, sourceEventID string,
	generation uint64,
) {
	if st == nil || generation == 0 {
		return
	}
	if event != nil {
		event.RenderGeneration = generation
	}
	trace := renderGenerationLocked(st, generation)
	trace.SourceGenerations = appendBoundedUniqueGeneration(trace.SourceGenerations, generation)
	trace.TraceIDs = appendBoundedUnique(trace.TraceIDs, traceID)
	trace.RequestIDs = appendBoundedUnique(trace.RequestIDs, requestID)
	trace.SourceEventIDs = appendBoundedUnique(trace.SourceEventIDs, sourceEventID)
	trace.UpdatedAt = time.Now().UnixNano()
	h.syncRendererMetadataLocked(windowID)
}

func mergeRenderGeneration(dst *RenderGenerationTrace, src RenderGenerationTrace) {
	for _, generation := range src.SourceGenerations {
		dst.SourceGenerations = appendBoundedUniqueGeneration(dst.SourceGenerations, generation)
	}
	dst.SourceGenerations = appendBoundedUniqueGeneration(dst.SourceGenerations, src.Generation)
	for _, id := range src.TraceIDs {
		dst.TraceIDs = appendBoundedUnique(dst.TraceIDs, id)
	}
	for _, id := range src.RequestIDs {
		dst.RequestIDs = appendBoundedUnique(dst.RequestIDs, id)
	}
	for _, id := range src.SourceEventIDs {
		dst.SourceEventIDs = appendBoundedUnique(dst.SourceEventIDs, id)
	}
}

func (h *Handler) markRenderStageLocked(windowID int, generation uint64, stage, outcome, reason string, coalesce bool) RenderGenerationTrace {
	st, ok := h.windowStateLocked(windowID)
	if !ok || generation == 0 {
		return RenderGenerationTrace{}
	}
	if coalesce {
		target := RenderGenerationTrace{
			Generation: generation,
			Stage:      renderStageDirty, Outcome: "pending",
		}
		kept := make([]RenderGenerationTrace, 0, len(st.RenderGenerations)+1)
		for _, existing := range st.RenderGenerations {
			if existing.Generation <= generation && !renderGenerationTerminal(existing) {
				mergeRenderGeneration(&target, existing)
				continue
			}
			kept = append(kept, existing)
		}
		target.SourceGenerations = appendBoundedUniqueGeneration(target.SourceGenerations, generation)
		kept = append(kept, target)
		st.RenderGenerations = kept
	}
	trace := renderGenerationLocked(st, generation)
	trace.Stage = stage
	trace.Outcome = outcome
	trace.Reason = reason
	trace.UpdatedAt = time.Now().UnixNano()
	if len(st.RenderGenerations) > maxRenderGenerationTraces {
		st.RenderGenerations = append([]RenderGenerationTrace(nil), st.RenderGenerations[len(st.RenderGenerations)-maxRenderGenerationTraces:]...)
		trace = &st.RenderGenerations[len(st.RenderGenerations)-1]
	}
	if stage == renderStageRendered || stage == renderStageFailed {
		h.finishRenderEventsLocked(st, *trace)
	}
	h.syncRendererMetadataLocked(windowID)
	return cloneRenderGenerationTrace(*trace)
}

func (h *Handler) finishRenderEventsLocked(st *WindowState, trace RenderGenerationTrace) {
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
		if isDuplicateReplay(event) {
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

func isDuplicateReplay(event *SessionEvent) bool {
	return event != nil && event.ReducerDecision == DecisionDuplicate && event.DecisionReason == DecisionReasonRequestReplayed
}

func (h *Handler) recordRenderGeneration(trace RenderGenerationTrace, windowID int) {
	if h == nil || h.decisionJournal == nil || trace.Generation == 0 {
		return
	}
	// Exactly one record per (generation, stage): coalesced causal IDs travel
	// in the trace_ids/request_ids evidence lists, and Selector matching
	// resolves TraceID/RequestID queries against those lists, so per-ID
	// duplicate records are unnecessary volume.
	envelope := diagnostics.NewEnvelope("daemon.renderer." + trace.Stage)
	if len(trace.TraceIDs) > 0 {
		envelope.TraceID = trace.TraceIDs[0]
	}
	if len(trace.RequestIDs) > 0 {
		envelope.RequestID = trace.RequestIDs[0]
	}
	envelope.WindowID = windowID
	envelope.Outcome = trace.Outcome
	envelope.Reason = trace.Reason
	envelope.Evidence = map[string]string{
		"generation":         fmt.Sprint(trace.Generation),
		"source_generations": formatUint64List(trace.SourceGenerations),
		"trace_ids":          strings.Join(trace.TraceIDs, ","),
		"request_ids":        strings.Join(trace.RequestIDs, ","),
		"source_event_ids":   strings.Join(trace.SourceEventIDs, ","),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	h.decisionJournal.TryAppend(ctx, envelope)
}

func (h *Handler) recordRenderGenerationBounded(trace RenderGenerationTrace, windowID int) {
	if h == nil || h.decisionJournal == nil || trace.Generation == 0 {
		return
	}
	h.recordRenderGeneration(cloneRenderGenerationTrace(trace), windowID)
}

func formatUint64List(values []uint64) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprint(value))
	}
	return strings.Join(parts, ",")
}

func notificationTraceFromRequest(req Request) NotificationTrace {
	return NotificationTrace{
		TraceID: req.TraceID, RequestID: req.RequestID,
		SourceEventID: req.SourceEventID, Harness: req.Harness,
		Profile: req.Account,
	}
}

func notificationTraceFromEvent(event SessionEvent) NotificationTrace {
	return NotificationTrace{
		TraceID: event.TraceID, RequestID: event.RequestID,
		SourceEventID: event.SourceEventID, Harness: event.ProducerHarness,
		Profile: event.Profile, SessionID: event.SessionID, RunID: event.RunID,
	}
}

func (h *Handler) recordNotificationDispositionBounded(disposition NotificationDisposition) {
	if h == nil {
		return
	}
	h.recordNotificationDisposition(disposition)
}

func (h *Handler) recordNotificationDisposition(disposition NotificationDisposition) {
	if h == nil {
		return
	}
	if disposition.Stage != "focus" {
		key := notificationEffectKey(disposition.WindowID, disposition.Trace.TraceID)
		h.asyncMu.Lock()
		if _, exists := h.notificationEffects[key]; !exists {
			h.notificationEffectOrder = append(h.notificationEffectOrder, key)
		}
		h.notificationEffects[key] = disposition
		if len(h.notificationEffectOrder) > maxEventsPerWindow {
			oldest := h.notificationEffectOrder[0]
			h.notificationEffectOrder = append([]string(nil), h.notificationEffectOrder[1:]...)
			delete(h.notificationEffects, oldest)
		}
		h.asyncMu.Unlock()
	}
	if h.decisionJournal == nil {
		return
	}
	envelope := diagnostics.NewEnvelope("daemon.notification." + disposition.Stage)
	if disposition.Trace.TraceID != "" {
		envelope.TraceID = disposition.Trace.TraceID
	}
	envelope.RequestID = disposition.Trace.RequestID
	envelope.Harness = disposition.Trace.Harness
	envelope.Profile = disposition.Trace.Profile
	envelope.SessionID = disposition.Trace.SessionID
	envelope.RunID = disposition.Trace.RunID
	envelope.WindowID = disposition.WindowID
	envelope.Outcome = disposition.Outcome
	envelope.Reason = disposition.Reason
	envelope.ObservedAt = disposition.ObservedAt
	envelope.Evidence = map[string]string{
		"action":          disposition.Action,
		"source_event_id": disposition.Trace.SourceEventID,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	h.decisionJournal.TryAppend(ctx, envelope)
}

func notificationEffectKey(windowID int, traceID string) string {
	return fmt.Sprintf("%d:%s", windowID, traceID)
}

func (h *Handler) enrichNotificationEvent(windowID int, event SessionEvent) SessionEvent {
	if h == nil || event.TraceID == "" {
		return event
	}
	// Delivery retries retain the original trace ID for correlation, but the
	// replay row did not cause the original request's notification. Mirror the
	// render-completion exclusion so export-time enrichment cannot manufacture
	// downstream notification causality for that row.
	if isDuplicateReplay(&event) {
		return event
	}
	h.asyncMu.Lock()
	disposition, ok := h.notificationEffects[notificationEffectKey(windowID, event.TraceID)]
	h.asyncMu.Unlock()
	if !ok {
		return event
	}
	event.NotificationTrace = disposition.Trace.TraceID
	event.NotificationOutcome = disposition.Outcome
	event.NotificationReason = disposition.Reason
	return event
}

func (h *Handler) linkProbeChild(parentTraceID, childTraceID string, windowIDs []int) {
	if h == nil || parentTraceID == "" || childTraceID == "" {
		return
	}
	h.stateMu.Lock()
	for _, windowID := range windowIDs {
		st, ok := h.windowStateLocked(windowID)
		if !ok {
			continue
		}
		for i := len(st.EventLog) - 1; i >= 0; i-- {
			if isDuplicateReplay(&st.EventLog[i]) {
				continue
			}
			if st.EventLog[i].TraceID != parentTraceID {
				continue
			}
			st.EventLog[i].ProbeTraceIDs = appendBoundedUnique(st.EventLog[i].ProbeTraceIDs, childTraceID)
			break
		}
	}
	h.stateMu.Unlock()
}

func (h *Handler) recordProbeStage(
	traceID, parentTraceID string,
	req Request,
	windowID int,
	stage, outcome, reason string,
	evidence map[string]string,
) {
	if h == nil || h.decisionJournal == nil || traceID == "" {
		return
	}
	envelope := diagnostics.NewEnvelope("daemon.probe." + stage)
	envelope.TraceID = traceID
	envelope.ParentTraceID = parentTraceID
	envelope.RequestID = req.RequestID
	envelope.Harness = req.Harness
	envelope.Profile = req.Account
	envelope.WindowID = windowID
	envelope.Outcome = outcome
	envelope.Reason = reason
	envelope.PayloadHMAC = req.PayloadHMAC
	envelope.PayloadSize = req.PayloadSize
	envelope.Evidence = evidence
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	h.decisionJournal.TryAppend(ctx, envelope)
}

func (h *Handler) recordProbeStageBounded(
	traceID, parentTraceID string,
	req Request,
	windowID int,
	stage, outcome, reason string,
	evidence map[string]string,
) {
	if h == nil || h.decisionJournal == nil {
		return
	}
	copyEvidence := make(map[string]string, len(evidence))
	for key, value := range evidence {
		copyEvidence[key] = value
	}
	h.recordProbeStage(traceID, parentTraceID, req, windowID, stage, outcome, reason, copyEvidence)
}
