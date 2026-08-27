package debugger

import (
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/aslexport"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// ASLCorrelationSchemaVersion versions the cross-system join independently of
// the bundle. The join is derived, so it can gain a dimension without changing
// what any member it draws from means.
const ASLCorrelationSchemaVersion = 1

// maxCorrelatedRuns bounds the join. The ASL export is already bounded on its
// side, but a join is a product of two bounded sets and this is where that
// product is capped rather than where it is discovered.
const maxCorrelatedRuns = 50

// Correlation note codes. These are stable JSON consumer contracts: the code
// survives share-mode redaction while the prose detail beside it does not, so a
// shared bundle still names every condition the join detected.
const (
	NoteASLUnavailable      = "asl-export-unavailable"
	NoteContractDisagree    = "contract-set-disagreement"
	NoteCompareBinaryHashes = "compare-binary-hashes"
	NoteCorrelationCapped   = "correlation-capped"
	NoteUnmatchedDeliveries = "unmatched-deliveries"
)

// Note is one condition the join detected, as a stable code plus prose.
type Note struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// ContractSetAgreement records whether both halves implement the same contract
// set. Disagreement is the single most useful fact in a bundle from a broken
// paired install: it explains a whole class of "the cockpit shows nothing"
// reports without any further reading.
type ContractSetAgreement struct {
	ASL      string `json:"asl"`
	AgentMux string `json:"agent_mux"`
	Agrees   bool   `json:"agrees"`
}

// BuildPair carries both build identities so a reader can compare versions and
// binary hashes side by side. The interesting failure is the one where the two
// sides agree on version and disagree on behavior, which only the hashes show.
type BuildPair struct {
	ASL      aslexport.Build `json:"asl"`
	AgentMux buildinfo.Info  `json:"agent_mux"`
}

// DeliveryLink is one lifecycle notification followed across both systems: what
// ASL recorded sending, and what AgentMux's journal recorded receiving under
// the same request ID.
type DeliveryLink struct {
	RunID         string `json:"run_id"`
	AgentMuxEvent string `json:"agent_mux_event"`
	SourceEventID string `json:"source_event_id"`
	RequestID     string `json:"request_id"`
	TraceID       string `json:"trace_id"`

	ASLStatus       string `json:"asl_status"`
	ASLAcknowledged bool   `json:"asl_acknowledged"`
	ASLAttempts     *int   `json:"asl_attempts"`

	// JournalRecords is how many AgentMux journal records carry the same
	// request ID. Zero with an ASL status of "accepted" is the asymmetry a
	// bundle exists to surface: ASL believes it delivered and AgentMux has no
	// record of receiving.
	JournalRecords  int      `json:"journal_records"`
	JournalStages   []string `json:"journal_stages"`
	JournalOutcomes []string `json:"journal_outcomes"`
	Matched         bool     `json:"matched"`
}

// RunCorrelation is one run's causal path across both systems.
type RunCorrelation struct {
	RunID      string `json:"run_id"`
	PlanDigest string `json:"plan_digest"`
	Phase      string `json:"phase"`

	// ProjectionGeneration and LifecycleSourceSequence are the two independent
	// counters AgentMux's monotonic acceptance gate compares. They are reported
	// together because a stalled cockpit is usually one of them frozen, and
	// which one names the failing half.
	ProjectionGeneration    *int   `json:"projection_generation"`
	LifecycleSourceSequence *int   `json:"lifecycle_source_sequence"`
	ProjectionStatus        string `json:"projection_status"`

	Deliveries []DeliveryLink `json:"deliveries"`

	// OperationIDs are bridge operation IDs ASL durably observed, and ClosureIDs
	// are what reply and resolve produced. Both are ASL-side identities that a
	// journal record may name.
	OperationIDs []string `json:"operation_ids"`
	ClosureIDs   []string `json:"closure_ids"`

	JournalRecords  int                     `json:"journal_records"`
	JournalStages   []string                `json:"journal_stages"`
	JournalOutcomes []aslexport.StatusTally `json:"journal_outcomes"`

	AssuranceState string `json:"assurance_state"`
	IssueCount     int    `json:"issue_count"`
}

// ASLCorrelation is the derived member that answers the bundle's actual
// question: does one causal path explain both halves, and where does it break?
type ASLCorrelation struct {
	SchemaVersion int                    `json:"schema_version"`
	Availability  aslexport.Availability `json:"availability"`
	ContractSet   ContractSetAgreement   `json:"contract_set"`
	Builds        BuildPair              `json:"builds"`
	ExportDigest  string                 `json:"export_digest"`
	Runs          []RunCorrelation       `json:"runs"`
	Summary       ASLCorrelationSummary  `json:"summary"`
	Omissions     []aslexport.Omission   `json:"asl_omissions"`
	Limits        aslexport.Limits       `json:"asl_limits"`
	Truncated     bool                   `json:"truncated"`
	Notes         []Note                 `json:"notes"`
}

// ASLCorrelationSummary is the whole join reduced to the counts an operator
// reads first.
type ASLCorrelationSummary struct {
	RunsCorrelated       int `json:"runs_correlated"`
	DeliveriesTotal      int `json:"deliveries_total"`
	DeliveriesMatched    int `json:"deliveries_matched"`
	DeliveriesUnmatched  int `json:"deliveries_unmatched"`
	JournalRecordsJoined int `json:"journal_records_joined"`
	RunsWithoutJournal   int `json:"runs_without_journal"`
}

// buildASLCorrelation joins the ASL export against the journal records already
// selected for this bundle.
//
// The journal slice is the bundle's own bounded snapshot rather than a fresh
// read, so the correlation describes exactly the records the bundle carries. A
// join against evidence the archive does not contain would be unverifiable by
// whoever opens it.
func buildASLCorrelation(
	result aslexport.Result,
	records []diagnostics.TraceEnvelope,
	agentMuxContractSet string,
	build buildinfo.Info,
) ASLCorrelation {
	correlation := ASLCorrelation{
		SchemaVersion: ASLCorrelationSchemaVersion,
		Availability:  result.Availability,
		ContractSet:   ContractSetAgreement{AgentMux: agentMuxContractSet},
		Builds:        BuildPair{AgentMux: build},
		Runs:          make([]RunCorrelation, 0),
		Notes:         make([]Note, 0),
	}
	if result.Document == nil {
		correlation.Notes = append(correlation.Notes, Note{
			Code:   NoteASLUnavailable,
			Detail: "no ASL export was available; AgentMux-side records stand alone",
		})
		return correlation
	}

	document := result.Document
	correlation.ContractSet.ASL = document.ContractSetID
	correlation.ContractSet.Agrees = agentMuxContractSet != "" &&
		document.ContractSetID == agentMuxContractSet
	correlation.Builds.ASL = document.Build
	correlation.ExportDigest = document.Digest
	correlation.Omissions = document.Omissions
	correlation.Limits = document.Limits
	correlation.Truncated = document.Truncated

	if !correlation.ContractSet.Agrees && agentMuxContractSet != "" {
		correlation.Notes = append(correlation.Notes, Note{
			Code:   NoteContractDisagree,
			Detail: "contract sets disagree; projection and lifecycle field meanings are not comparable",
		})
	}
	if document.Build.BinaryHash != "" && build.BinaryHash != "" &&
		document.Build.Version != "" && build.Version != "" {
		correlation.Notes = append(correlation.Notes, Note{
			Code:   NoteCompareBinaryHashes,
			Detail: "compare build.binary_hash on both sides before trusting matching version strings",
		})
	}

	byRequest := make(map[string][]diagnostics.TraceEnvelope, len(records))
	byRun := make(map[string][]diagnostics.TraceEnvelope, len(records))
	for _, record := range records {
		if record.RequestID != "" {
			byRequest[record.RequestID] = append(byRequest[record.RequestID], record)
		}
		for _, runID := range runIdentitiesOf(record) {
			byRun[runID] = append(byRun[runID], record)
		}
	}

	runs := document.Runs
	if len(runs) > maxCorrelatedRuns {
		correlation.Notes = append(correlation.Notes, Note{
			Code:   NoteCorrelationCapped,
			Detail: "correlation was capped; see asl-export.json for the remaining runs",
		})
		correlation.Truncated = true
		runs = runs[:maxCorrelatedRuns]
	}

	for _, run := range runs {
		entry := RunCorrelation{
			RunID:                   run.RunID,
			PlanDigest:              run.PlanDigest,
			Phase:                   run.Phase,
			ProjectionGeneration:    run.ProjectionGeneration,
			LifecycleSourceSequence: run.LifecycleSourceSequence,
			ProjectionStatus:        run.ProjectionStatus,
			Deliveries:              make([]DeliveryLink, 0, len(run.Delivery.Records)),
			OperationIDs:            append([]string(nil), run.Control.OperationIDs...),
			ClosureIDs:              append([]string(nil), run.Receipts.ClosureIDs...),
			JournalOutcomes:         make([]aslexport.StatusTally, 0),
		}
		if run.Assurance != nil {
			entry.AssuranceState = run.Assurance.State
			entry.IssueCount = run.Assurance.IssueCount
		}

		journalForRun := byRun[run.RunID]
		entry.JournalRecords = len(journalForRun)
		stages := map[string]struct{}{}
		outcomes := map[string]int{}
		for _, record := range journalForRun {
			if record.Stage != "" {
				stages[record.Stage] = struct{}{}
			}
			if record.Outcome != "" {
				outcomes[record.Outcome]++
			}
		}
		entry.JournalStages = sortedKeys(stages)
		for _, outcome := range sortedCountKeys(outcomes) {
			entry.JournalOutcomes = append(entry.JournalOutcomes,
				aslexport.StatusTally{Status: outcome, Count: outcomes[outcome]})
		}

		for _, delivery := range run.Delivery.Records {
			link := DeliveryLink{
				RunID:           delivery.RunID,
				AgentMuxEvent:   delivery.AgentMuxEvent,
				SourceEventID:   delivery.SourceEventID,
				RequestID:       delivery.RequestID,
				TraceID:         delivery.TraceID,
				ASLStatus:       delivery.FinalStatus,
				ASLAcknowledged: delivery.Acknowledged,
				ASLAttempts:     delivery.AttemptCount,
				JournalStages:   make([]string, 0),
				JournalOutcomes: make([]string, 0),
			}
			matches := byRequest[delivery.RequestID]
			if delivery.RequestID == "" {
				matches = nil
			}
			link.JournalRecords = len(matches)
			link.Matched = len(matches) > 0
			linkStages := map[string]struct{}{}
			linkOutcomes := map[string]struct{}{}
			for _, record := range matches {
				if record.Stage != "" {
					linkStages[record.Stage] = struct{}{}
				}
				if record.Outcome != "" {
					linkOutcomes[record.Outcome] = struct{}{}
				}
			}
			link.JournalStages = sortedKeys(linkStages)
			link.JournalOutcomes = sortedKeys(linkOutcomes)

			correlation.Summary.DeliveriesTotal++
			if link.Matched {
				correlation.Summary.DeliveriesMatched++
				correlation.Summary.JournalRecordsJoined += link.JournalRecords
			} else {
				correlation.Summary.DeliveriesUnmatched++
			}
			entry.Deliveries = append(entry.Deliveries, link)
		}

		if entry.JournalRecords == 0 {
			correlation.Summary.RunsWithoutJournal++
		}
		correlation.Runs = append(correlation.Runs, entry)
	}
	correlation.Summary.RunsCorrelated = len(correlation.Runs)

	if correlation.Summary.DeliveriesUnmatched > 0 {
		correlation.Notes = append(correlation.Notes, Note{
			Code:   NoteUnmatchedDeliveries,
			Detail: "an unmatched delivery means ASL recorded sending a notification this bundle's journal window has no record of receiving",
		})
	}
	return correlation
}

// aslUnavailableReason renders one availability as a manifest reason.
//
// The reason string is the export's own vocabulary rather than prose, so the
// manifest names a condition a reader can act on — and so the same condition
// reads identically in the manifest and in asl-correlation.json.
func aslUnavailableReason(availability aslexport.Availability) string {
	reason := strings.TrimSpace(availability.Reason)
	if reason == "" {
		reason = aslexport.ReasonNotRequested
	}
	if detail := strings.TrimSpace(availability.Detail); detail != "" {
		return reason + ": " + detail
	}
	return reason
}

// runIdentitiesOf returns every run ID a journal record can be joined on. The
// daemon records an ASL run under its own field on some stages and under an
// evidence key on others, and a join that knew only one of them would report
// half a causal path as a missing one.
func runIdentitiesOf(record diagnostics.TraceEnvelope) []string {
	seen := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	add(record.RunID)
	add(record.Evidence["asl_run_id"])
	add(record.Evidence["run_id"])
	return sortedKeys(seen)
}

// sortedCountKeys orders a tally deterministically. Bundles are compared byte
// for byte across captures, so every derived collection is ordered by content
// rather than by whatever order the journal happened to arrive in.
func sortedCountKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
