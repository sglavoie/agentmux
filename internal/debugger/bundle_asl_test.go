package debugger

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/aslexport"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func TestForensicBundleCorrelatesTheCausalPathAcrossBothSystems(t *testing.T) {
	document := aslExportFixture()
	options, _ := aslBundleFixture(t, BundleModeForensic, aslexport.Result{
		Availability: aslexport.Availability{Available: true, Reason: aslexport.ReasonOK},
		Document:     document, Bytes: 4096,
	}, true)
	options.OutputPath = filepath.Join(t.TempDir(), "bundle.tar.gz")
	if _, err := CreateBundle(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	members := readBundleMembers(t, options.OutputPath)
	for _, name := range []string{"asl-export.json", "asl-correlation.json"} {
		if _, ok := members[name]; !ok {
			t.Fatalf("bundle member %s missing", name)
		}
	}

	correlation := decodeCorrelation(t, members)
	if correlation.SchemaVersion != ASLCorrelationSchemaVersion {
		t.Fatalf("correlation schema = %d", correlation.SchemaVersion)
	}
	if !correlation.ContractSet.Agrees || correlation.ContractSet.ASL != testASLContractSet {
		t.Fatalf("contract set = %+v", correlation.ContractSet)
	}
	if correlation.Builds.ASL.BinaryHash == "" || correlation.Builds.AgentMux.BinaryHash == "" {
		t.Fatalf("both binary hashes must be reported: %+v", correlation.Builds)
	}
	if correlation.ExportDigest != document.Digest {
		t.Fatalf("export digest = %q", correlation.ExportDigest)
	}
	if len(correlation.Omissions) != 1 || correlation.Omissions[0].Reason != "excluded-body" {
		t.Fatalf("omission ledger = %+v", correlation.Omissions)
	}
	if correlation.Limits.MaxRuns != 25 {
		t.Fatalf("limits = %+v", correlation.Limits)
	}

	if len(correlation.Runs) != 1 {
		t.Fatalf("runs = %d", len(correlation.Runs))
	}
	run := correlation.Runs[0]
	// Identifier hashing is mode-independent, so a run ID is compared for
	// internal consistency rather than for its literal value. The plan digest
	// is not an identifier and is the key that stays readable in every mode.
	if run.RunID == "" || run.PlanDigest != strings.Repeat("b", 64) {
		t.Fatalf("run identity = %+v", run)
	}
	if run.ProjectionGeneration == nil || *run.ProjectionGeneration != 12 ||
		run.LifecycleSourceSequence == nil || *run.LifecycleSourceSequence != 7 {
		t.Fatalf("both counters must be reported: %+v", run)
	}
	if len(run.OperationIDs) != 1 || run.OperationIDs[0] == "" {
		t.Fatalf("operation ids = %v", run.OperationIDs)
	}
	if len(run.ClosureIDs) != 1 || run.ClosureIDs[0] == "" {
		t.Fatalf("closure ids = %v", run.ClosureIDs)
	}
	if run.AssuranceState != "clean" || run.IssueCount != 1 {
		t.Fatalf("assurance summary = %q/%d", run.AssuranceState, run.IssueCount)
	}
	if run.JournalRecords != 1 || len(run.JournalStages) != 1 || run.JournalStages[0] != "bridge_dispatch" {
		t.Fatalf("run journal join = %+v", run)
	}
	if len(run.JournalOutcomes) != 1 || run.JournalOutcomes[0] != (aslexport.StatusTally{Status: "accepted", Count: 1}) {
		t.Fatalf("run journal outcomes = %+v", run.JournalOutcomes)
	}

	if len(run.Deliveries) != 2 {
		t.Fatalf("deliveries = %d", len(run.Deliveries))
	}
	matched, unmatched := run.Deliveries[0], run.Deliveries[1]
	if matched.RunID != run.RunID || unmatched.RunID != run.RunID {
		t.Fatalf("delivery run identities do not join to the run: %+v", run)
	}
	if !matched.Matched || matched.JournalRecords != 1 ||
		matched.SourceEventID == "" || matched.SourceEventID == unmatched.SourceEventID {
		t.Fatalf("matched delivery = %+v", matched)
	}
	if unmatched.Matched || unmatched.JournalRecords != 0 || unmatched.ASLStatus != "failed" {
		t.Fatalf("unmatched delivery = %+v", unmatched)
	}
	want := ASLCorrelationSummary{
		RunsCorrelated: 1, DeliveriesTotal: 2, DeliveriesMatched: 1,
		DeliveriesUnmatched: 1, JournalRecordsJoined: 1,
	}
	if correlation.Summary != want {
		t.Fatalf("summary = %+v, want %+v", correlation.Summary, want)
	}
	if !hasNote(correlation, NoteUnmatchedDeliveries) || !hasNote(correlation, NoteCompareBinaryHashes) {
		t.Fatalf("notes = %+v", correlation.Notes)
	}
}

func hasNote(correlation ASLCorrelation, code string) bool {
	for _, note := range correlation.Notes {
		if note.Code == code {
			return true
		}
	}
	return false
}

func TestBundleDoesNotReadASLPrivateStateFormats(t *testing.T) {
	options, _ := aslBundleFixture(t, BundleModeForensic, aslexport.Result{
		Availability: aslexport.Availability{Available: true, Reason: aslexport.ReasonOK},
		Document:     aslExportFixture(),
	}, true)
	options.OutputPath = filepath.Join(t.TempDir(), "bundle.tar.gz")
	if _, err := CreateBundle(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	members := readBundleMembers(t, options.OutputPath)
	manifest := members["manifest.json"]
	if !bytes.Contains(manifest, []byte("private formats")) {
		t.Fatalf("manifest does not record why ASL state files are excluded: %s", manifest)
	}
	for name := range members {
		for _, private := range []string{"state.json", "events.jsonl", "mailbox.jsonl"} {
			if strings.Contains(name, private) {
				t.Fatalf("bundle carries ASL private format %s", name)
			}
		}
	}
}

func TestASLBundleMembersAreDeterministicAndCanaryFree(t *testing.T) {
	for _, mode := range []BundleMode{BundleModeShare, BundleModeForensic} {
		t.Run(string(mode), func(t *testing.T) {
			document := aslExportFixture()
			// Plant the canaries in every free-text field a document does have:
			// the reason strings, which ASL considers control-plane and which a
			// harness could still have written anything into.
			document.Runs[0].Delivery.Records[1].Reason = strings.Join(allASLCanaries(), " ")
			document.Runs[0].Assurance.Reason = strings.Join(allASLCanaries(), " ")
			document.Runs[0].Events.Reason = strings.Join(allASLCanaries(), " ")
			document.StateRoot = "/Users/alice/secret/" + aslBodyCanaries[0]

			result := aslexport.Result{
				Availability: aslexport.Availability{Available: true, Reason: aslexport.ReasonOK},
				Document:     document,
			}
			options, _ := aslBundleFixture(t, mode, result, true)
			first := filepath.Join(t.TempDir(), "first.tar.gz")
			second := filepath.Join(t.TempDir(), "second.tar.gz")
			options.OutputPath = first
			if _, err := CreateBundle(t.Context(), options); err != nil {
				t.Fatal(err)
			}
			options.OutputPath = second
			if _, err := CreateBundle(t.Context(), options); err != nil {
				t.Fatal(err)
			}
			firstBytes, err := os.ReadFile(first)
			if err != nil {
				t.Fatal(err)
			}
			secondBytes, err := os.ReadFile(second)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(firstBytes, secondBytes) {
				t.Fatal("repeated capture over frozen inputs is not byte-identical")
			}

			members := readBundleMembers(t, first)
			all := bytes.Join(mapValues(members), []byte("\n"))
			canaries := aslSecretCanaries
			if mode == BundleModeShare {
				canaries = allASLCanaries()
			}
			for _, canary := range canaries {
				if bytes.Contains(all, []byte(canary)) {
					t.Errorf("%s bundle leaked canary %q", mode, canary)
				}
			}
			if bytes.Contains(all, []byte("/Users/alice")) {
				t.Errorf("%s bundle leaked an absolute home path", mode)
			}
			if mode == BundleModeForensic && !bytes.Contains(all, []byte("LOCAL")) {
				t.Error("a forensic bundle keeps controlled free text and must say so")
			}
		})
	}
}

func TestShareBundleKeepsTheASLCorrelationReadable(t *testing.T) {
	// Share mode drops every key it does not know, so the correlation is only
	// useful there if its whole vocabulary is allowlisted. This is the test
	// that fails when a new field is added without extending the allowlist.
	options, _ := aslBundleFixture(t, BundleModeShare, aslexport.Result{
		Availability: aslexport.Availability{Available: true, Reason: aslexport.ReasonOK},
		Document:     aslExportFixture(),
	}, true)
	options.OutputPath = filepath.Join(t.TempDir(), "bundle.tar.gz")
	if _, err := CreateBundle(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	members := readBundleMembers(t, options.OutputPath)
	correlation := decodeCorrelation(t, members)
	if !correlation.ContractSet.Agrees {
		t.Fatalf("share mode lost the contract-set agreement: %+v", correlation.ContractSet)
	}
	if correlation.Summary != (ASLCorrelationSummary{
		RunsCorrelated: 1, DeliveriesTotal: 2, DeliveriesMatched: 1,
		DeliveriesUnmatched: 1, JournalRecordsJoined: 1,
	}) {
		t.Fatalf("share mode lost the summary: %+v", correlation.Summary)
	}
	if len(correlation.Runs) != 1 {
		t.Fatalf("share mode lost the runs: %+v", correlation.Runs)
	}
	run := correlation.Runs[0]
	if run.Phase != "waiting_input" || run.AssuranceState != "clean" || run.ProjectionStatus != "present" {
		t.Fatalf("share mode lost controlled enums: %+v", run)
	}
	if run.ProjectionGeneration == nil || run.LifecycleSourceSequence == nil {
		t.Fatalf("share mode lost the acceptance counters: %+v", run)
	}
	if len(run.Deliveries) != 2 || run.Deliveries[0].ASLStatus != "accepted" ||
		!run.Deliveries[0].Matched || run.Deliveries[1].Matched {
		t.Fatalf("share mode lost the delivery join: %+v", run.Deliveries)
	}
	if run.Deliveries[0].AgentMuxEvent != "asl.run.started" {
		t.Fatalf("share mode lost the lifecycle event name: %+v", run.Deliveries[0])
	}
	if len(correlation.Omissions) != 1 || correlation.Omissions[0].Section != "mailbox" ||
		correlation.Omissions[0].SHA256 != strings.Repeat("d", 64) {
		t.Fatalf("share mode lost the omission ledger: %+v", correlation.Omissions)
	}
	if correlation.ExportDigest != strings.Repeat("c", 64) {
		t.Fatalf("share mode lost the export digest: %q", correlation.ExportDigest)
	}
	if !hasNote(correlation, NoteUnmatchedDeliveries) {
		t.Fatalf("share mode lost the note codes: %+v", correlation.Notes)
	}

	// Identifiers still go through the HMAC, and a run identity is one.
	if run.RunID == "run-alpha" {
		t.Fatal("share mode published a raw run identifier")
	}
	if bytes.Contains(members["asl-export.json"], []byte("/Users/alice/.local/bin/asl")) {
		t.Fatal("share mode published the ASL executable path")
	}
}

func TestBundleSurvivesEveryASLUnavailableCondition(t *testing.T) {
	cases := []struct {
		name         string
		requested    bool
		availability aslexport.Availability
		reason       string
	}{
		{name: "not requested", requested: false, reason: aslexport.ReasonNotRequested},
		{
			name: "not installed", requested: true,
			availability: aslexport.Availability{Reason: aslexport.ReasonNotFound, Detail: "asl not on PATH"},
			reason:       aslexport.ReasonNotFound,
		},
		{
			name: "too old", requested: true,
			availability: aslexport.Availability{Reason: aslexport.ReasonUnsupported},
			reason:       aslexport.ReasonUnsupported,
		},
		{
			name: "hung", requested: true,
			availability: aslexport.Availability{Reason: aslexport.ReasonTimeout},
			reason:       aslexport.ReasonTimeout,
		},
		{
			name: "turned off", requested: true,
			availability: aslexport.Availability{Reason: aslexport.ReasonDisabled},
			reason:       aslexport.ReasonDisabled,
		},
		{
			name: "foreign contract set", requested: true,
			availability: aslexport.Availability{Reason: aslexport.ReasonForeignContract},
			reason:       aslexport.ReasonForeignContract,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			options, _ := aslBundleFixture(t, BundleModeForensic,
				aslexport.Result{Availability: testCase.availability}, testCase.requested)
			options.OutputPath = filepath.Join(t.TempDir(), "bundle.tar.gz")
			result, err := CreateBundle(t.Context(), options)
			if err != nil {
				t.Fatalf("a degraded ASL must not fail bundle creation: %v", err)
			}
			if result.Bytes == 0 {
				t.Fatal("bundle is empty")
			}
			members := readBundleMembers(t, options.OutputPath)
			if _, present := members["asl-export.json"]; present {
				t.Fatal("an unavailable export must not produce a member")
			}
			if !bytes.Contains(members["manifest.json"], []byte(testCase.reason)) {
				t.Fatalf("manifest does not name %q: %s", testCase.reason, members["manifest.json"])
			}
			correlation := decodeCorrelation(t, members)
			if correlation.Availability.Reason != testCase.reason {
				t.Fatalf("correlation availability = %+v", correlation.Availability)
			}
			if len(correlation.Runs) != 0 {
				t.Fatalf("correlation invented runs: %+v", correlation.Runs)
			}
			if !hasNote(correlation, NoteASLUnavailable) {
				t.Fatalf("correlation does not explain the absence: %+v", correlation.Notes)
			}
			if correlation.Builds.AgentMux.BinaryHash == "" {
				t.Fatal("the AgentMux build identity must stand alone")
			}
		})
	}
}

func TestCorrelationFlagsContractSetDisagreement(t *testing.T) {
	document := aslExportFixture()
	document.ContractSetID = "asl-amx-9.deadbeefdeadbeef"
	correlation := buildASLCorrelation(
		aslexport.Result{
			Availability: aslexport.Availability{Available: true, Reason: aslexport.ReasonOK},
			Document:     document,
		},
		nil, testASLContractSet, buildinfo.Info{Version: "1.4.0"},
	)
	if correlation.ContractSet.Agrees {
		t.Fatal("disagreeing contract sets were reported as agreeing")
	}
	if !hasNote(correlation, NoteContractDisagree) {
		t.Fatalf("notes = %+v", correlation.Notes)
	}
}

func TestCorrelationCapsTheJoinAndSaysSo(t *testing.T) {
	document := aslExportFixture()
	template := document.Runs[0]
	document.Runs = make([]aslexport.Run, 0, maxCorrelatedRuns+5)
	for index := 0; index < maxCorrelatedRuns+5; index++ {
		run := template
		run.RunID = "run-" + strings.Repeat("x", index%3) + string(rune('a'+index%26))
		document.Runs = append(document.Runs, run)
	}
	correlation := buildASLCorrelation(
		aslexport.Result{
			Availability: aslexport.Availability{Available: true, Reason: aslexport.ReasonOK},
			Document:     document,
		},
		nil, testASLContractSet, buildinfo.Info{},
	)
	if len(correlation.Runs) != maxCorrelatedRuns || !correlation.Truncated {
		t.Fatalf("runs = %d truncated = %t", len(correlation.Runs), correlation.Truncated)
	}
	if !hasNote(correlation, NoteCorrelationCapped) {
		t.Fatalf("a silent cap reads as complete coverage: %+v", correlation.Notes)
	}
}

func TestCorrelationJoinsRunsRecordedUnderEitherIdentityField(t *testing.T) {
	byField := diagnostics.NewEnvelope("asl.lifecycle.delivered")
	byField.RunID = "run-alpha"
	byField.Stage = "bridge_dispatch"
	byEvidence := diagnostics.NewEnvelope("asl.lifecycle.delivered")
	byEvidence.Stage = "daemon_reduce"
	byEvidence.Evidence = map[string]string{"asl_run_id": "run-alpha"}

	correlation := buildASLCorrelation(
		aslexport.Result{
			Availability: aslexport.Availability{Available: true, Reason: aslexport.ReasonOK},
			Document:     aslExportFixture(),
		},
		[]diagnostics.TraceEnvelope{byField, byEvidence}, testASLContractSet, buildinfo.Info{},
	)
	if len(correlation.Runs) != 1 || correlation.Runs[0].JournalRecords != 2 {
		t.Fatalf("run join = %+v", correlation.Runs)
	}
	if len(correlation.Runs[0].JournalStages) != 2 {
		t.Fatalf("stages = %v", correlation.Runs[0].JournalStages)
	}
}
