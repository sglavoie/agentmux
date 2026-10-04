package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/eventlog"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/rendering"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// linkRequestRenderGenerationLocked associates the exact event row for req
// with the next state generation before markWindow*DirtyLocked increments Seq.
// Callers hold stateMu.
func (h *Handler) linkRequestRenderGenerationLocked(req protocol.Request, generation uint64) {
	if h == nil || req.WindowID <= 0 || generation == 0 {
		return
	}
	st := h.ensureWindowStateLocked(req.WindowID)
	event := eventlog.ByReference(st, req.EventRef)
	h.linkSessionEventRenderGenerationLocked(req.WindowID, st, event, req.TraceID, req.RequestID, req.SourceEventID, generation)
}

func (h *Handler) linkSessionEventRenderGenerationLocked(
	windowID int,
	st *state.WindowState,
	event *protocol.SessionEvent,
	traceID, requestID, sourceEventID string,
	generation uint64,
) {
	if st == nil || generation == 0 {
		return
	}
	if event != nil {
		event.RenderGeneration = generation
	}
	rendering.LinkCausalIDs(st, generation, traceID, requestID, sourceEventID)
	h.syncRendererMetadataLocked(windowID)
}

func (h *Handler) markRenderStageLocked(windowID int, generation uint64, stage, outcome, reason string, coalesce bool) protocol.RenderGenerationTrace {
	st, ok := h.windowStateLocked(windowID)
	if !ok || generation == 0 {
		return protocol.RenderGenerationTrace{}
	}
	trace := rendering.MarkStage(st, generation, stage, outcome, reason, coalesce)
	if rendering.StageCompletes(stage) {
		eventlog.FinishRender(st, trace)
	}
	h.syncRendererMetadataLocked(windowID)
	return trace
}

func (h *Handler) recordRenderGeneration(trace protocol.RenderGenerationTrace, windowID int) {
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
		"source_generations": rendering.FormatSourceGenerations(trace.SourceGenerations),
		"trace_ids":          strings.Join(trace.TraceIDs, ","),
		"request_ids":        strings.Join(trace.RequestIDs, ","),
		"source_event_ids":   strings.Join(trace.SourceEventIDs, ","),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	h.decisionJournal.TryAppend(ctx, envelope)
}

func (h *Handler) recordRenderGenerationBounded(trace protocol.RenderGenerationTrace, windowID int) {
	if h == nil || h.decisionJournal == nil || trace.Generation == 0 {
		return
	}
	h.recordRenderGeneration(rendering.CloneTrace(trace), windowID)
}

func notificationTraceFromRequest(req protocol.Request) notification.NotificationTrace {
	return notification.NotificationTrace{
		TraceID: req.TraceID, RequestID: req.RequestID,
		SourceEventID: req.SourceEventID, Harness: req.Harness,
		Profile: req.Account,
	}
}

func notificationTraceFromEvent(event protocol.SessionEvent) notification.NotificationTrace {
	return notification.NotificationTrace{
		TraceID: event.TraceID, RequestID: event.RequestID,
		SourceEventID: event.SourceEventID, Harness: event.ProducerHarness,
		Profile: event.Profile, SessionID: event.SessionID, RunID: event.RunID,
	}
}

func (h *Handler) recordNotificationDispositionBounded(disposition notification.NotificationDisposition) {
	if h == nil {
		return
	}
	h.recordNotificationDisposition(disposition)
}

func (h *Handler) recordNotificationDisposition(disposition notification.NotificationDisposition) {
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
		if len(h.notificationEffectOrder) > eventlog.MaxEventsPerWindow {
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
	if disposition.Backend != "" {
		envelope.Evidence["backend"] = disposition.Backend
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	h.decisionJournal.TryAppend(ctx, envelope)
}

func notificationEffectKey(windowID int, traceID string) string {
	return fmt.Sprintf("%d:%s", windowID, traceID)
}

func (h *Handler) enrichNotificationEvent(windowID int, event protocol.SessionEvent) protocol.SessionEvent {
	if h == nil || event.TraceID == "" {
		return event
	}
	// Delivery retries retain the original trace ID for correlation, but the
	// replay row did not cause the original request's notification. Mirror the
	// render-completion exclusion so export-time enrichment cannot manufacture
	// downstream notification causality for that row.
	if eventlog.IsDuplicateReplay(&event) {
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
		eventlog.LinkProbeTrace(st, parentTraceID, childTraceID)
	}
	h.stateMu.Unlock()
}

func (h *Handler) recordProbeStage(
	traceID, parentTraceID string,
	req protocol.Request,
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
	req protocol.Request,
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
