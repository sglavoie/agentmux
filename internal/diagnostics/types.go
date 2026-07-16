// Package diagnostics provides private, fail-open persistence for AgentMux
// causal traces and operational logs.
package diagnostics

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/sglavoie/agentmux/internal/buildinfo"
)

const SchemaVersion = 1

// maxEvidenceFields accommodates the bounded routing snapshot plus the
// offline-replay reducer projection while keeping every journal row bounded.
const maxEvidenceFields = 96

var processSequence atomic.Uint64

var processSourceID = newID()

// TraceEnvelope is the stable causal record shared by hook, daemon, probe,
// renderer, and replay stages. Raw payload material is represented only by a
// keyed HMAC and byte count.
type TraceEnvelope struct {
	SchemaVersion  int               `json:"schema_version"`
	TraceID        string            `json:"trace_id"`
	RequestID      string            `json:"request_id,omitempty"`
	SpanID         string            `json:"span_id"`
	ParentTraceID  string            `json:"parent_trace_id,omitempty"`
	SourceID       string            `json:"source_id"`
	SourceSequence uint64            `json:"source_sequence"`
	Harness        string            `json:"harness,omitempty"`
	Profile        string            `json:"profile,omitempty"`
	SessionID      string            `json:"session_id,omitempty"`
	WindowID       int               `json:"window_id,omitempty"`
	RunID          string            `json:"run_id,omitempty"`
	LaunchID       string            `json:"launch_id,omitempty"`
	Stage          string            `json:"stage"`
	Outcome        string            `json:"outcome,omitempty"`
	Reason         string            `json:"reason,omitempty"`
	ObservedAt     time.Time         `json:"observed_at"`
	StartedAt      *time.Time        `json:"started_at,omitempty"`
	FinishedAt     *time.Time        `json:"finished_at,omitempty"`
	PayloadHMAC    string            `json:"payload_hmac,omitempty"`
	PayloadSize    int64             `json:"payload_size,omitempty"`
	LatencyMS      float64           `json:"latency_ms,omitempty"`
	Build          buildinfo.Info    `json:"build"`
	Evidence       map[string]string `json:"evidence,omitempty"`
}

// NewEnvelope initializes process identity, monotonic source sequence, build
// identity, IDs, and timestamp. Callers add the stage-specific fields.
func NewEnvelope(stage string) TraceEnvelope {
	return TraceEnvelope{
		SchemaVersion:  SchemaVersion,
		TraceID:        newID(),
		SpanID:         newID(),
		SourceID:       processSourceID,
		SourceSequence: processSequence.Add(1),
		Stage:          strings.TrimSpace(stage),
		ObservedAt:     time.Now().UTC(),
		Build:          buildinfo.Current(),
	}
}

// NewTraceID allocates a diagnostic correlation identity without creating a
// journal envelope. Daemon-side legacy ingress uses it when an older client did
// not supply a trace ID, so every causal SessionEvent remains independently
// addressable.
func NewTraceID() string { return newID() }

func (e *TraceEnvelope) normalize(now time.Time) error {
	if e.SchemaVersion == 0 {
		e.SchemaVersion = SchemaVersion
	}
	if e.SchemaVersion != SchemaVersion {
		return errors.New("diagnostics: unsupported trace schema version")
	}
	if strings.TrimSpace(e.Stage) == "" {
		return errors.New("diagnostics: trace stage is empty")
	}
	if e.TraceID == "" {
		e.TraceID = newID()
	}
	if e.SpanID == "" {
		e.SpanID = newID()
	}
	if e.SourceID == "" {
		e.SourceID = processSourceID
	}
	if e.SourceSequence == 0 {
		e.SourceSequence = processSequence.Add(1)
	}
	if e.ObservedAt.IsZero() {
		e.ObservedAt = now.UTC()
	} else {
		e.ObservedAt = e.ObservedAt.UTC()
	}
	e.Evidence = SanitizeEvidence(e.Evidence)
	return nil
}

// SanitizeEvidence removes control characters, bounds keys and values, and
// redacts fields whose names commonly carry secrets. It deliberately accepts
// strings only so arbitrary payload objects cannot slip into journal records.
func SanitizeEvidence(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for rawKey, rawValue := range input {
		if len(out) >= maxEvidenceFields {
			break
		}
		key := cleanEvidenceText(rawKey, 128)
		if key == "" {
			continue
		}
		value := cleanEvidenceText(rawValue, 2048)
		lower := strings.ToLower(key)
		for _, sensitive := range []string{"authorization", "cookie", "password", "secret", "token", "payload", "prompt", "transcript"} {
			if strings.Contains(lower, sensitive) {
				value = "[redacted]"
				break
			}
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cleanEvidenceText(value string, max int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	if len(value) > max {
		value = value[:max] + "...(truncated)"
	}
	return value
}

func newID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000000000")))
}
