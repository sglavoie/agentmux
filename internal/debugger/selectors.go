// Package debugger provides the offline-first diagnostic services — status,
// query (tail, cursor, and selectors), explanation, bundle, and replay — shared
// by the debug CLI, doctor, and hook-status surfaces.
//
// Offline-first means these services read immutable diagnostics artifacts and
// work with no daemon running. That is the boundary against
// internal/daemon/eventlog, which builds the live per-window rows and the
// daemon.decision journal record this package decodes after the fact, and
// which deliberately does not absorb the tail, cursor, and replay services
// kept here.
package debugger

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
)

// Selector is the common investigation scope accepted by every debug command.
type Selector struct {
	Harness       string    `json:"harness,omitempty"`
	Profile       string    `json:"profile,omitempty"`
	WindowID      int       `json:"window_id,omitempty"`
	SessionID     string    `json:"session_id,omitempty"`
	ASLRunID      string    `json:"asl_run_id,omitempty"`
	Event         string    `json:"event,omitempty"`
	TraceID       string    `json:"trace_id,omitempty"`
	RequestID     string    `json:"request_id,omitempty"`
	SourceEventID string    `json:"source_event_id,omitempty"`
	Outcomes      []string  `json:"outcomes,omitempty"`
	Stages        []string  `json:"stages,omitempty"`
	After         time.Time `json:"after,omitempty"`
	Before        time.Time `json:"before,omitempty"`
}

// Normalize trims selector values, canonicalizes known harness names, and
// rejects impossible ranges before any filesystem or daemon work begins.
func (s *Selector) Normalize() error {
	if s == nil {
		return nil
	}
	if s.WindowID < 0 {
		return fmt.Errorf("window ID must not be negative")
	}
	if value := strings.TrimSpace(s.Harness); value != "" {
		if known := harness.NormalizeKnown(value); known != "" {
			s.Harness = known
		} else {
			s.Harness = strings.ToLower(value)
		}
	}
	s.Profile = strings.TrimSpace(s.Profile)
	s.SessionID = strings.TrimSpace(s.SessionID)
	s.ASLRunID = strings.TrimSpace(s.ASLRunID)
	s.Event = strings.TrimSpace(s.Event)
	s.TraceID = strings.TrimSpace(s.TraceID)
	s.RequestID = strings.TrimSpace(s.RequestID)
	s.SourceEventID = strings.TrimSpace(s.SourceEventID)
	s.Outcomes = cleanValues(s.Outcomes)
	s.Stages = cleanValues(s.Stages)
	if !s.After.IsZero() {
		s.After = s.After.UTC()
	}
	if !s.Before.IsZero() {
		s.Before = s.Before.UTC()
	}
	if !s.After.IsZero() && !s.Before.IsZero() && s.After.After(s.Before) {
		return fmt.Errorf("after time must not be later than before time")
	}
	return nil
}

func cleanValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// JournalSelector maps the common scope to durable trace fields.
func (s Selector) JournalSelector(limit int) diagnostics.Selector {
	return diagnostics.Selector{
		After: s.After, Before: s.Before, TraceID: s.TraceID, RequestID: s.RequestID, SourceEventID: s.SourceEventID,
		Harness: s.Harness, Profile: s.Profile, SessionID: s.SessionID,
		WindowID: s.WindowID, RunID: s.ASLRunID, Event: s.Event,
		Stages: append([]string(nil), s.Stages...), Outcomes: append([]string(nil), s.Outcomes...), Limit: limit,
	}
}

// ParseTimeBound accepts RFC3339 timestamps, Unix seconds, or a duration such
// as "30m" meaning that duration before now.
func ParseTimeBound(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if duration, err := time.ParseDuration(strings.TrimPrefix(value, "-")); err == nil {
		return now.UTC().Add(-duration), nil
	}
	if unix, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(unix, 0).UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time %q (use RFC3339, Unix seconds, or a duration such as 30m)", value)
	}
	return parsed.UTC(), nil
}
