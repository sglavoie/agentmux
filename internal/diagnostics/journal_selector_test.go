package diagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestJournalSelectorsSanitizedEvidenceAndLimit(t *testing.T) {
	t.Parallel()

	journal, _ := testJournal(t, JournalOptions{})
	base := time.Now().UTC().Add(-time.Hour)
	for i, stage := range []string{"ingress", "decision", "decision"} {
		envelope := testEnvelope(stage, strconv.Itoa(i))
		envelope.TraceID = "trace"
		envelope.WindowID = 42
		envelope.ObservedAt = base.Add(time.Duration(i) * time.Minute)
		envelope.Evidence = map[string]string{"token_value": "secret", "safe": "ok\nvalue", "source_event_ids": "other,source-" + strconv.Itoa(i)}
		if err := journal.Append(context.Background(), envelope); err != nil {
			t.Fatal(err)
		}
	}
	records, err := journal.Read(context.Background(), Selector{
		TraceID: "trace", WindowID: 42, Stages: []string{"decision"}, Limit: 1,
	})
	if err != nil || len(records) != 1 || records[0].RequestID != "2" {
		t.Fatalf("selected records = %+v, err = %v", records, err)
	}
	if records[0].Evidence["token_value"] != "[redacted]" || strings.Contains(records[0].Evidence["safe"], "\n") {
		t.Fatalf("sanitized evidence = %+v", records[0].Evidence)
	}
	bySourceEvent, err := journal.Read(context.Background(), Selector{SourceEventID: "source-2"})
	if err != nil || len(bySourceEvent) != 1 || bySourceEvent[0].RequestID != "2" {
		t.Fatalf("source-event records = %+v, err = %v", bySourceEvent, err)
	}
}

func TestSelectorMatchesCoalescedTraceAndRequestIDEvidenceLists(t *testing.T) {
	t.Parallel()

	journal, _ := testJournal(t, JournalOptions{})
	envelope := testEnvelope("daemon.renderer.rendered", "request-a")
	envelope.TraceID = "trace-a"
	envelope.Evidence = map[string]string{
		"trace_ids":   "trace-a,trace-b",
		"request_ids": "request-a,request-b",
	}
	if err := journal.Append(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		selector Selector
		want     int
	}{
		{name: "primary trace field", selector: Selector{TraceID: "trace-a"}, want: 1},
		{name: "coalesced trace via evidence", selector: Selector{TraceID: "trace-b"}, want: 1},
		{name: "unknown trace", selector: Selector{TraceID: "trace-c"}, want: 0},
		{name: "primary request field", selector: Selector{RequestID: "request-a"}, want: 1},
		{name: "coalesced request via evidence", selector: Selector{RequestID: "request-b"}, want: 1},
		{name: "unknown request", selector: Selector{RequestID: "request-c"}, want: 0},
		{name: "coalesced trace with stage filter", selector: Selector{TraceID: "trace-b", Stages: []string{"daemon.renderer.rendered"}}, want: 1},
	} {
		records, err := journal.Read(context.Background(), tc.selector)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(records) != tc.want {
			t.Fatalf("%s: records = %+v, want %d", tc.name, records, tc.want)
		}
	}
}

func TestJournalTolerantReadAndSpoolReconciliationPreserveLaterValidLines(t *testing.T) {
	t.Parallel()

	journal, paths := testJournal(t, JournalOptions{})
	first := testEnvelope("tail", "main-first")
	first.Evidence = map[string]string{"event": "PreToolUse"}
	if err := journal.Append(context.Background(), first); err != nil {
		t.Fatal(err)
	}

	current, err := os.OpenFile(paths.JournalFile, os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	later := testEnvelope("tail", "main-later")
	later.Evidence = map[string]string{"event": "PreToolUse"}
	laterJSON, err := json.Marshal(later)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(current, "{corrupt-main}\n%s\n", laterJSON); err != nil {
		t.Fatal(err)
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}

	spoolPath := filepath.Join(paths.SpoolDir, "journal-99999.jsonl")
	spooledFirst := testEnvelope("tail", "spool-first")
	spooledFirst.Evidence = map[string]string{"event": "PreToolUse"}
	spooledLater := testEnvelope("tail", "spool-later")
	spooledLater.Evidence = map[string]string{"event": "PreToolUse"}
	spooledFirstJSON, _ := json.Marshal(spooledFirst)
	spooledLaterJSON, _ := json.Marshal(spooledLater)
	spoolData := append(append(append(spooledFirstJSON, '\n'), []byte("{corrupt-spool}\n")...), spooledLaterJSON...)
	spoolData = append(spoolData, '\n')
	if err := os.WriteFile(spoolPath, spoolData, fileMode); err != nil {
		t.Fatal(err)
	}

	maintenance, err := journal.ReconcileSpool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if maintenance.ReconciledRecords != 2 || maintenance.CorruptLines != 1 || len(maintenance.QuarantinedFiles) != 1 {
		t.Fatalf("reconciliation = %+v", maintenance)
	}
	report, err := journal.ReadTolerant(context.Background(), Selector{Event: "PreToolUse"})
	if err != nil {
		t.Fatal(err)
	}
	if report.CorruptLines != 1 || len(report.Records) != 4 {
		t.Fatalf("tolerant report = %+v", report)
	}
	requests := make([]string, 0, len(report.Records))
	for _, record := range report.Records {
		requests = append(requests, record.RequestID)
	}
	for _, want := range []string{"main-first", "main-later", "spool-first", "spool-later"} {
		if !slices.Contains(requests, want) {
			t.Fatalf("requests = %v, missing %q", requests, want)
		}
	}
	health, err := journal.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.CorruptLines != 1 || health.QuarantinedFiles != 1 || health.SpoolFiles != 0 {
		t.Fatalf("health = %+v", health)
	}
}
