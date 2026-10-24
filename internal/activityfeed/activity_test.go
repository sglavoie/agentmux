package activityfeed

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func transition(seq uint64, before, after string) diagnostics.TraceEnvelope {
	e := diagnostics.NewEnvelope("daemon.decision")
	e.SourceID, e.SourceSequence = "daemon-test", seq
	e.ObservedAt = time.Date(2026, 10, 15, 1, 0, int(seq), 0, time.UTC)
	e.Harness, e.SessionID, e.Outcome = "claude", "conversation", "applied"
	e.Evidence = map[string]string{"event": "Stop", "replay.before_icon": before, "replay.after_icon": after, "runtime_session_id": "runtime"}
	return e
}

func TestProjectionRequiresRecordedChangeAndAcceptedDecision(t *testing.T) {
	for _, outcome := range []string{"suppressed", "duplicate", "reordered", "quarantined"} {
		e := transition(1, "bolt", "stop")
		e.Outcome = outcome
		if _, ok := Project(e); ok {
			t.Fatalf("projected %s", outcome)
		}
	}
	e := transition(1, "bolt", "bolt")
	if _, ok := Project(e); ok {
		t.Fatal("unchanged state projected")
	}
	delete(e.Evidence, "replay.before_icon")
	e.Evidence["replay.after_icon"] = "key"
	if _, ok := Project(e); ok {
		t.Fatal("inferred missing before state")
	}
	e = transition(1, "bolt", "key")
	got, ok := Project(e)
	if !ok || got.Kind != "permission" || got.RuntimeSessionID != "runtime" || got.SessionID != "conversation" {
		t.Fatalf("entry: %+v", got)
	}
	// Storage bookkeeping and rereads must not change the read-marker identity.
	e.Reason = "updated metadata"
	same, _ := Project(e)
	if same.ID != got.ID {
		t.Fatal("identity changed")
	}
	e.SourceSequence++
	next, _ := Project(e)
	if next.ID == got.ID {
		t.Fatal("separate records share identity")
	}
}

func TestLifecycleAndExitDoNotRequireIconChanges(t *testing.T) {
	for event, title := range map[string]string{"AgentSessionLoopFinished": "ASL run finished", "AgentSessionLoopPaused": "ASL run paused", "AgentSessionLoopWaitStarted": "ASL answer requested"} {
		e := transition(1, "bolt", "bolt")
		e.Evidence["event"] = event
		e.RunID = "run"
		got, ok := Project(e)
		if !ok || got.Title != title || got.RunID != "run" {
			t.Fatalf("%s: %+v", event, got)
		}
	}
	e := transition(1, "bolt", "stop")
	e.Evidence["event"] = "HarnessProcessExited"
	if _, ok := Project(e); ok {
		t.Fatal("duplicate exit decision")
	}
	e.Stage, e.Outcome, e.Reason = "daemon.process_lifecycle", "settled", "exit status 1"
	got, ok := Project(e)
	if !ok || got.Kind != "exit" || got.Detail != "exit status 1" {
		t.Fatalf("exit: %+v", got)
	}
}

func TestJournalFiltersNoiseBeforeLimitAndReportsTruncation(t *testing.T) {
	journal, err := diagnostics.NewJournal(diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state")), diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 6; i++ {
		after := "bolt"
		if i <= 3 {
			after = "stop"
		}
		if err := journal.Append(context.Background(), transition(uint64(i), "bolt", after)); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Read(context.Background(), journal, time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 2 || report.Matched != 3 || !report.Truncated {
		t.Fatalf("report: %+v", report)
	}
	if !report.Entries[0].At.After(report.Entries[1].At) {
		t.Fatal("not newest first")
	}
	again, err := Read(context.Background(), journal, report.Since, 2)
	if err != nil || again.Entries[0].ID != report.Entries[0].ID {
		t.Fatal("reread identity unstable", err)
	}
}

// An empty journal has no earliest record: the report says so with an empty
// earliest_available, never the zero time, and keeps the key for the app.
func TestEmptyJournalReportsNoEarliestRecord(t *testing.T) {
	journal, err := diagnostics.NewJournal(diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state")), diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	report, err := Read(context.Background(), journal, since, 10)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"earliest_available":""`) || strings.Contains(string(encoded), "0001-01-01") {
		t.Fatalf("empty journal report = %s", encoded)
	}
	if err := journal.Append(context.Background(), transition(1, "bolt", "stop")); err != nil {
		t.Fatal(err)
	}
	if report, err = Read(context.Background(), journal, since, 10); err != nil || report.EarliestAvailable != "2026-10-15T01:00:01Z" {
		t.Fatalf("earliest = %q, err = %v", report.EarliestAvailable, err)
	}
}
