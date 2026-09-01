package debugger

// The ASL export fixture the correlation tests read, and the canaries that
// prove no question body or secret reaches a bundle member.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/aslexport"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

const testASLContractSet = "asl-amx-1.6f30193cd1744a98"

// aslSecretCanaries are secret-shaped strings. No bundle may carry them in any
// mode: they are what the redactor's patterns exist for, and a reason string
// that quoted a harness error could carry one into an export that ASL itself
// considers control-plane.
var aslSecretCanaries = []string{
	"sk-abcdefghijklmnopqrstuvwxyz",
	"ghp_1234567890abcdefghij",
	"AKIA1234567890ABCDEF",
	"password=hunter2",
}

// aslBodyCanaries are ordinary prose. A share bundle must never carry them,
// because a shareable archive has no business republishing free text that came
// out of another process. A forensic bundle is local-only by construction and
// deliberately keeps controlled free text, so it is exempt.
//
// Note where these are *not* planted: the export has no field for a question
// body, a reply, a prompt or bridge stderr, so there is no place in this
// fixture to put them. That containment is asserted at the decode boundary in
// aslexport, which is where a newer ASL could try to introduce one.
var aslBodyCanaries = []string{
	"QUESTION-BODY-CANARY",
	"REPLY-TEXT-CANARY",
	"PROMPT-CANARY-DO-NOT-SHARE",
	"BRIDGE-STDERR-CANARY",
}

func allASLCanaries() []string {
	return append(append([]string(nil), aslSecretCanaries...), aslBodyCanaries...)
}

// aslExportFixture is a frozen, fully populated export document. Nothing in it
// is derived from the clock or from the filesystem, so a bundle built over it
// is reproducible byte for byte.
func aslExportFixture() *aslexport.Document {
	turn := 4
	attempts := 2
	sequence := 7
	generation := 12
	stateSchema := 3
	duration := 1.5
	return &aslexport.Document{
		SchemaVersion: aslexport.SchemaVersion,
		Kind:          aslexport.Kind,
		ContractSetID: testASLContractSet,
		Build: aslexport.Build{
			Product: "agent-session-loop", Version: "1.4.0",
			Executable: "/Users/alice/.local/bin/asl", BinaryHash: strings.Repeat("a", 64),
			ContractSetID: testASLContractSet, StateSchemaVersion: 3,
			ProjectionSchemaVersion: 1, HookSchemaVersion: 2, BridgeProtocol: 2,
		},
		StateRoot:        "/Users/alice/.local/share/agent-plans",
		StateRootPresent: true,
		Limits: aslexport.Limits{
			MaxRuns: 25, MaxDeliveryRecords: 50, MaxControlRecords: 50,
			MaxReceiptRecords: 50, MaxLogLines: 20000, MaxExportBytes: 1 << 20,
		},
		Counts: aslexport.Counts{
			PlansScanned: 2, RunsMatched: 1, RunsExported: 1,
			DeliveryRecords: 2, ControlRecords: 1, ReceiptRecords: 1,
		},
		RedactionPolicy: []aslexport.RedactionPolicyEntry{
			{Field: "mailbox.text", Reason: "excluded-body"},
			{Field: "harness.prompt", Reason: "excluded-body"},
		},
		Runs: []aslexport.Run{{
			PlanDigest: strings.Repeat("b", 64), RunID: "run-alpha",
			StateSchemaVersion: &stateSchema, StateAvailable: true,
			ProjectionStatus: "present",
			Projection: &aslexport.Projection{
				SchemaVersion: 1, ContractSetID: testASLContractSet, RunID: "run-alpha",
				Harness: "claude", Phase: "waiting_input", SessionID: "session-alpha",
				TurnIndex: &turn, QuestionID: "question-alpha",
				LifecycleSourceSequence: 7, ProjectionGeneration: 12,
				StartedAt: "2026-07-15T12:00:00Z", UpdatedAt: "2026-07-15T12:05:00Z",
			},
			Phase: "waiting_input", ProjectionGeneration: &generation,
			LifecycleSourceSequence: &sequence,
			Assurance: &aslexport.Assurance{
				State: "clean", Eligible: true, Reason: "ok",
				IssueIDs: []string{"issue-one"}, IssueCount: 1, NewIssueCount: 1,
				AttemptCount: 1, ArtifactCount: 2,
				StartedAt: "2026-07-15T12:06:00Z", CompletedAt: "2026-07-15T12:06:01Z",
				DurationSeconds: &duration,
			},
			Events: aslexport.EventSection{
				Available: true, Scanned: 3, Total: 3,
				ByName: []aslexport.EventTally{{Event: "run_started", Count: 1}, {Event: "turn_started", Count: 2}},
			},
			Delivery: aslexport.DeliverySection{
				Total: 2, Exported: 2, Acknowledged: 1,
				ByStatus: []aslexport.StatusTally{{Status: "accepted", Count: 1}, {Status: "failed", Count: 1}},
				Records: []aslexport.Delivery{
					{
						RunID: "run-alpha", AgentMuxEvent: "asl.run.started", Harness: "claude",
						SourceEventID: "source-event-alpha", SourceSequence: &sequence,
						RequestID: "request-matched", TraceID: "trace-alpha",
						AttemptCount: &attempts, FinalStatus: "accepted", Reason: "ok", Acknowledged: true,
					},
					{
						RunID: "run-alpha", AgentMuxEvent: "asl.run.waiting", Harness: "claude",
						SourceEventID: "source-event-beta", RequestID: "request-unmatched",
						FinalStatus: "failed", Reason: "bridge unavailable",
						ErrorClassification: "transport",
					},
				},
			},
			Control: aslexport.ControlSection{
				Total: 1, Exported: 1,
				ByAction:     []aslexport.ActionTally{{Action: "pause", Count: 1}},
				OperationIDs: []string{"operation-alpha"},
				Records: []aslexport.Control{{
					Action: "pause", CommandID: "operation-alpha", QueueCommand: "pause",
					StopAfterCurrent: true, Session: "session-alpha", TurnIndex: &turn,
				}},
			},
			Receipts: aslexport.ReceiptSection{
				Available: true, Total: 1, Exported: 1,
				ByKind:     []aslexport.KindTally{{Kind: "reply", Count: 1}},
				ClosureIDs: []string{"closure-alpha"},
				Records: []aslexport.Receipt{{
					ID: "mail-alpha", Kind: "reply", To: "question-alpha", Action: "reply",
					ClosureID: "closure-alpha", RunID: "run-alpha", SessionID: "session-alpha",
					TurnIndex: &turn, CreatedAt: "2026-07-15T12:04:00Z",
					TextChars: 128, NoteChars: 0,
				}},
			},
		}},
		Omissions: []aslexport.Omission{
			{Section: "mailbox", Reason: "excluded-body", Count: 1, SHA256: strings.Repeat("d", 64)},
		},
		Digest: strings.Repeat("c", 64),
		OK:     true,
		Reason: "ok",
	}
}

// aslBundleFixture builds one bundle over frozen inputs.
func aslBundleFixture(t *testing.T, mode BundleMode, result aslexport.Result, requested bool) (BundleOptions, *diagnostics.Journal) {
	t.Helper()
	now := time.Date(2026, 7, 15, 12, 34, 56, 0, time.UTC)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	record := diagnostics.NewEnvelope("asl.lifecycle.delivered")
	record.RequestID = "request-matched"
	record.RunID = "run-alpha"
	record.Stage = "bridge_dispatch"
	record.Outcome = "accepted"
	record.Evidence = map[string]string{
		"asl_run_id": "run-alpha",
		"message":    strings.Join(allASLCanaries(), " "),
	}
	if err := journal.Append(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	return BundleOptions{
		Paths: paths, Mode: mode, Now: now, Journal: journal,
		Build:            buildinfo.Info{Version: "1.4.0", BinaryHash: strings.Repeat("e", 64)},
		ASL:              result,
		ASLRequested:     requested,
		ASLContractSetID: testASLContractSet,
	}, journal
}

func decodeCorrelation(t *testing.T, members map[string][]byte) ASLCorrelation {
	t.Helper()
	raw, ok := members["asl-correlation.json"]
	if !ok {
		t.Fatal("asl-correlation.json is missing from the bundle")
	}
	var correlation ASLCorrelation
	if err := json.Unmarshal(raw, &correlation); err != nil {
		t.Fatalf("decode correlation: %v\n%s", err, raw)
	}
	return correlation
}
