package diagnostics

import (
	"strings"
	"time"
)

type Selector struct {
	After         time.Time
	Before        time.Time
	TraceID       string
	RequestID     string
	SourceEventID string
	Harness       string
	Profile       string
	SessionID     string
	WindowID      int
	RunID         string
	Event         string
	Stages        []string
	Outcomes      []string
	Limit         int
}

func (s Selector) matches(envelope TraceEnvelope) bool {
	if !s.After.IsZero() && envelope.ObservedAt.Before(s.After) {
		return false
	}
	if !s.Before.IsZero() && envelope.ObservedAt.After(s.Before) {
		return false
	}
	// Coalesced records (renderer stages) carry one primary trace/request ID
	// plus the full causal set in trace_ids/request_ids evidence lists; an
	// exact-ID selector must match through either representation.
	if s.TraceID != "" && envelope.TraceID != s.TraceID && !envelopeHasEvidenceID(envelope, s.TraceID, "trace_ids") {
		return false
	}
	if s.RequestID != "" && envelope.RequestID != s.RequestID && !envelopeHasEvidenceID(envelope, s.RequestID, "request_ids") {
		return false
	}
	if s.Harness != "" && envelope.Harness != s.Harness || s.Profile != "" && envelope.Profile != s.Profile ||
		s.SessionID != "" && envelope.SessionID != s.SessionID || s.WindowID != 0 && envelope.WindowID != s.WindowID ||
		s.RunID != "" && envelope.RunID != s.RunID {
		return false
	}
	if s.SourceEventID != "" && !envelopeHasEvidenceID(envelope, s.SourceEventID, "source_event_id", "source_event_ids") {
		return false
	}
	if s.Event != "" && envelope.Evidence["event"] != s.Event {
		return false
	}
	return containsOrEmpty(s.Stages, envelope.Stage) && containsOrEmpty(s.Outcomes, envelope.Outcome)
}

func envelopeHasEvidenceID(envelope TraceEnvelope, id string, keys ...string) bool {
	for _, key := range keys {
		for _, value := range strings.Split(envelope.Evidence[key], ",") {
			if strings.TrimSpace(value) == id {
				return true
			}
		}
	}
	return false
}

func containsOrEmpty(values []string, candidate string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
