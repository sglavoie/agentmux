// Package activityfeed projects recorded daemon transitions into an operator's
// activity history. It never reconstructs events from the current session list.
package activityfeed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

type Entry struct {
	ID               string    `json:"id"`
	At               time.Time `json:"at"`
	Kind             string    `json:"kind"`
	Title            string    `json:"title"`
	Detail           string    `json:"detail"`
	Event            string    `json:"event"`
	Harness          string    `json:"harness"`
	Profile          string    `json:"profile"`
	SessionID        string    `json:"session_id"`
	RuntimeSessionID string    `json:"runtime_session_id"`
	RunID            string    `json:"run_id"`
	TraceID          string    `json:"trace_id"`
}

// Report is one read of the activity history. EarliestAvailable is the
// oldest record the journal still holds, in RFC 3339, and empty when it holds
// none: a zero time ("0001-01-01T00:00:00Z") would read as a journal that goes
// back two thousand years. The key stays present, empty, because Agent
// Mux.app decodes it as a required string.
type Report struct {
	Entries           []Entry   `json:"entries"`
	Since             time.Time `json:"since"`
	EarliestAvailable string    `json:"earliest_available"`
	Truncated         bool      `json:"truncated"`
	CorruptLines      int       `json:"corrupt_lines"`
	Matched           int       `json:"matched"`
}

func Read(ctx context.Context, journal *diagnostics.Journal, since time.Time, limit int) (Report, error) {
	if journal == nil || limit < 1 || limit > 1000 {
		return Report{}, fmt.Errorf("activity requires a journal and a limit from 1 to 1000")
	}
	snapshot, err := journal.Snapshot(ctx, diagnostics.Selector{
		After: since, Limit: limit,
		Stages:       []string{"daemon.decision", "daemon.process_lifecycle"},
		RecordFilter: func(record diagnostics.TraceEnvelope) bool { _, ok := Project(record); return ok },
	}, nil)
	if err != nil {
		return Report{}, err
	}
	report := Report{Entries: []Entry{}, Since: since,
		Truncated: snapshot.Statistics.RecordsMatched > limit, Matched: snapshot.Statistics.RecordsMatched,
		CorruptLines: snapshot.Statistics.CorruptLines}
	if earliest := snapshot.Health.EarliestObservedAt; !earliest.IsZero() {
		report.EarliestAvailable = earliest.Format(time.RFC3339Nano)
	}
	for i := len(snapshot.Records) - 1; i >= 0; i-- {
		if entry, ok := Project(snapshot.Records[i]); ok {
			report.Entries = append(report.Entries, entry)
		}
	}
	return report, nil
}

func Project(record diagnostics.TraceEnvelope) (Entry, bool) {
	entry := Entry{At: record.ObservedAt, Event: record.Evidence["event"], Harness: record.Harness,
		Profile: record.Profile, SessionID: record.SessionID, RuntimeSessionID: record.Evidence["runtime_session_id"],
		RunID: record.RunID, TraceID: record.TraceID}
	if entry.At.IsZero() {
		return Entry{}, false
	}
	switch record.Stage {
	case "daemon.process_lifecycle":
		if record.Outcome != "settled" {
			return Entry{}, false
		}
		entry.Kind, entry.Title = "exit", "Agent process exited"
		if record.Evidence["process_mode"] == "pipe" {
			entry.Title = "Worker process exited"
		}
		entry.Detail = record.Reason
	case "daemon.decision":
		if record.Outcome != "applied" && record.Outcome != "acknowledged" && record.Outcome != "bypassed" {
			return Entry{}, false
		}
		if entry.Event == "HarnessProcessExited" {
			return Entry{}, false
		} // the settlement above is the durable exit record
		switch entry.Event {
		case "AgentSessionLoopFinished":
			entry.Kind, entry.Title = "run", "ASL run finished"
		case "AgentSessionLoopPaused":
			entry.Kind, entry.Title = "run", "ASL run paused"
		case "AgentSessionLoopStarted":
			entry.Kind, entry.Title = "run", "ASL run started"
		case "AgentSessionLoopWaitStarted":
			entry.Kind, entry.Title = "question", "ASL answer requested"
		case "AgentSessionLoopVerificationFinished":
			entry.Kind, entry.Title = "run", "ASL verification finished"
		default:
			before, beforeOK := record.Evidence["replay.before_icon"]
			after, afterOK := record.Evidence["replay.after_icon"]
			if !beforeOK || !afterOK || before == after {
				return Entry{}, false
			}
			entry.Kind, entry.Title = "state", stateTitle(after)
			switch after {
			case "key":
				entry.Kind = "permission"
			case "question":
				entry.Kind = "question"
			case "error":
				entry.Kind = "error"
			case "stop":
				entry.Kind = "completed"
			}
			entry.Detail = stateTitle(before) + " → " + stateTitle(after)
		}
		if reason := record.Evidence["reducer_reason"]; reason != "" {
			if entry.Detail != "" {
				entry.Detail += " · "
			}
			entry.Detail += reason
		}
	default:
		return Entry{}, false
	}
	var identity []byte
	if record.SourceID != "" && record.SourceSequence != 0 {
		identity = []byte(fmt.Sprintf("%s\x00%d", record.SourceID, record.SourceSequence))
	} else {
		identity, _ = json.Marshal(record)
	}
	hash := sha256.Sum256(identity)
	entry.ID = hex.EncodeToString(hash[:])
	return entry, true
}

func stateTitle(icon string) string {
	switch icon {
	case "", "idle":
		return "Idle"
	case "bolt":
		return "Working"
	case "stop":
		return "Turn finished"
	case "key":
		return "Permission requested"
	case "question":
		return "Question"
	case "error":
		return "Error"
	case "warning":
		return "Warning"
	case "compact":
		return "Compacting context"
	case "subagent":
		return "Subagent working"
	default:
		return "State: " + icon
	}
}
