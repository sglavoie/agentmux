package flightlog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// FromEnvelope projects retained diagnostics for an explicit, partial import.
// Live state counts are recorded at the effective-state publication boundary.
func FromEnvelope(record diagnostics.TraceEnvelope) (Event, bool) {
	e := Event{At: record.ObservedAt, Harness: record.Harness, Profile: record.Profile, LaunchID: record.LaunchID, RunID: record.RunID, TraceID: record.TraceID,
		RuntimeSessionID: record.Evidence["runtime_session_id"], Project: record.Evidence["project"], Partial: true}
	if record.SessionID != "" && record.Harness != "" {
		e.ConversationID = record.Harness + ":" + record.SessionID
	}
	if e.RuntimeSessionID != "" {
		e.Key = e.RuntimeSessionID
	} else if e.LaunchID != "" {
		e.Key = "launch:" + e.LaunchID
	} else if e.ConversationID != "" {
		e.Key = "import:" + e.ConversationID
		e.Partial = true
	} else if record.RunID != "" {
		e.Key = "import:run:" + record.RunID
	} else {
		return Event{}, false
	}
	switch record.Stage {
	case "session.launch":
		if record.Outcome != "success" || e.LaunchID == "" {
			return Event{}, false
		}
		e.Kind = "launch"
		e.Operation = record.Evidence["operation"]
		if e.Operation == "fork" {
			e.ConversationID = ""
		}
	case "daemon.decision":
		if record.Outcome != "applied" && record.Outcome != "acknowledged" && record.Outcome != "bypassed" {
			return Event{}, false
		}
		name := record.Evidence["event"]
		if strings.HasPrefix(name, "AgentSessionLoop") {
			e.Kind = "run"
			e.Name = name
		} else {
			before, bok := record.Evidence["replay.before_icon"]
			after, aok := record.Evidence["replay.after_icon"]
			if !bok || !aok {
				return Event{}, false
			}
			e.Kind = "state"
			e.BeforeIcon = before
			e.Icon = after
			e.BeforeLooping, _ = strconv.ParseBool(record.Evidence["replay.before_looping"])
			e.Looping, _ = strconv.ParseBool(record.Evidence["replay.after_looping"])
			if !changed(e) {
				return Event{}, false
			}
		}
	default:
		return Event{}, false
	}
	identity := []byte(record.SourceID + ":" + strconv.FormatUint(record.SourceSequence, 10))
	if record.SourceID == "" || record.SourceSequence == 0 {
		identity, _ = json.Marshal(record)
	}
	hash := sha256.Sum256(identity)
	e.ID = "import:" + hex.EncodeToString(hash[:])
	return e, !e.At.IsZero()
}
