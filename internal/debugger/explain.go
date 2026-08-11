package debugger

import (
	"context"
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

const ExplainSchemaVersion = 2

const (
	DefaultExplainGraphDepth = 2
	MinExplainGraphDepth     = 1
	MaxExplainGraphDepth     = 4
	DefaultExplainMaxNodes   = 250
	MinExplainMaxNodes       = 1
	MaxExplainMaxNodes       = 2000
	DefaultExplainMaxEdges   = 1000
	MinExplainMaxEdges       = 1
	MaxExplainMaxEdges       = 8000
)

const (
	TruncationGraphDepth = "graph_depth"
	TruncationMaxNodes   = "max_nodes"
	TruncationMaxEdges   = "max_edges"
)

const (
	CauseHookDisabled             = "hook_disabled"
	CauseHookUntrusted            = "hook_untrusted"
	CauseProducerAbsence          = "producer_absence"
	CauseOutsideKitty             = "outside_kitty"
	CauseInvalidPayload           = "invalid_payload"
	CauseDeliveryFailure          = "delivery_failure"
	CauseDuplicate                = "duplicate"
	CauseSaturation               = "saturation"
	CauseOwnerMismatch            = "owner_mismatch"
	CauseQuarantine               = "quarantine"
	CauseReducerSuppression       = "reducer_suppression"
	CauseRenderFailure            = "render_failure"
	CauseNotificationFailure      = "notification_failure"
	CauseNotificationSuppression  = "notification_suppression"
	CauseProcessLifecycleRecovery = "process_lifecycle_recovery"
	CauseSuccessfulCompletion     = "successful_completion"
	CauseInsufficientEvidence     = "insufficient_evidence"
)

const (
	CertaintyProven    = "proven"
	CertaintyUncertain = "uncertain"
)

type ExplainOptions struct {
	Selector    Selector
	Recent      int
	GraphDepth  int
	MaxNodes    int
	MaxEdges    int
	Hooks       *HookReport
	StatusError string
	Now         time.Time
}

type ExplanationLimits struct {
	GraphDepth int `json:"graph_depth"`
	MaxNodes   int `json:"max_nodes"`
	MaxEdges   int `json:"max_edges"`
}

type ExplanationTruncation struct {
	Truncated            bool     `json:"truncated"`
	Reasons              []string `json:"reasons"`
	PrunedNodeCandidates int      `json:"pruned_node_candidates"`
	PrunedEdgeCandidates int      `json:"pruned_edge_candidates"`
}

type ExplanationCause struct {
	Classification    string   `json:"classification"`
	Certainty         string   `json:"certainty"`
	Summary           string   `json:"summary"`
	FirstFailedStage  string   `json:"first_failed_stage,omitempty"`
	FirstMissingStage string   `json:"first_missing_stage,omitempty"`
	EvidenceIDs       []string `json:"evidence_ids"`
}

type EvidenceIdentity struct {
	TraceID                  string   `json:"trace_id,omitempty"`
	ParentTraceID            string   `json:"parent_trace_id,omitempty"`
	RequestID                string   `json:"request_id,omitempty"`
	SourceEventIDs           []string `json:"source_event_ids,omitempty"`
	ProducerSourceID         string   `json:"producer_source_id,omitempty"`
	ProducerSourceSequence   uint64   `json:"producer_source_sequence,omitempty"`
	NormalizedSourceID       string   `json:"normalized_source_id,omitempty"`
	NormalizedSourceSequence uint64   `json:"normalized_source_sequence,omitempty"`
	DaemonSequence           uint64   `json:"daemon_sequence,omitempty"`
	RenderGeneration         uint64   `json:"render_generation,omitempty"`
	LaunchID                 string   `json:"launch_id,omitempty"`
	WindowID                 int      `json:"window_id,omitempty"`
	SessionID                string   `json:"session_id,omitempty"`
	ASLRunID                 string   `json:"asl_run_id,omitempty"`
	TurnID                   string   `json:"turn_id,omitempty"`
	ToolUseID                string   `json:"tool_use_id,omitempty"`
}

type EvidenceNode struct {
	ID       string                     `json:"id"`
	Kind     string                     `json:"kind"`
	Identity EvidenceIdentity           `json:"identity"`
	Record   *diagnostics.TraceEnvelope `json:"record,omitempty"`
	Hook     *HookProfile               `json:"hook_status,omitempty"`
}

type EvidenceEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Relation string `json:"relation"`
	Value    string `json:"value,omitempty"`
}

type EvidenceGraph struct {
	Nodes []EvidenceNode `json:"nodes"`
	Edges []EvidenceEdge `json:"edges"`
}

type ExpectedSequenceDiagnostic struct {
	Kind          string   `json:"kind"`
	Scope         string   `json:"scope"`
	Status        string   `json:"status"`
	Outcome       string   `json:"outcome,omitempty"`
	ObservedSteps []string `json:"observed_steps"`
	MissingSteps  []string `json:"missing_steps,omitempty"`
	EvidenceIDs   []string `json:"evidence_ids"`
	Message       string   `json:"message"`
}

type ExplanationReport struct {
	SchemaVersion     int                            `json:"schema_version"`
	GeneratedAt       time.Time                      `json:"generated_at"`
	Selector          Selector                       `json:"selector"`
	Recent            int                            `json:"recent"`
	Limits            ExplanationLimits              `json:"limits"`
	Truncation        ExplanationTruncation          `json:"truncation"`
	Cause             ExplanationCause               `json:"cause"`
	Graph             EvidenceGraph                  `json:"evidence_graph"`
	ExpectedSequences []ExpectedSequenceDiagnostic   `json:"expected_sequences"`
	SuggestedCommands []string                       `json:"suggested_commands"`
	JournalSnapshot   diagnostics.SnapshotStatistics `json:"journal_snapshot"`
	Warnings          []string                       `json:"warnings,omitempty"`
}

// Explain reads a tolerant durable-journal snapshot, then constructs a bounded
// causal graph. It never contacts or mutates the daemon.
func Explain(ctx context.Context, journal *diagnostics.Journal, options ExplainOptions) (ExplanationReport, error) {
	if journal == nil {
		return ExplanationReport{}, fmt.Errorf("debugger: journal is nil")
	}
	if err := normalizeExplainOptions(&options); err != nil {
		return ExplanationReport{}, err
	}
	// Exact IDs and stage filters select graph seeds, not graph boundaries. Read
	// the complete time range so connected renderer, launch, and child records
	// that omit producer fields can still be joined.
	tail, err := Tail(ctx, journal, TailOptions{Selector: Selector{
		After: options.Selector.After, Before: options.Selector.Before,
	}})
	if err != nil {
		return ExplanationReport{}, err
	}
	report := buildExplanation(options, tail.Records)
	report.JournalSnapshot = tail.JournalSnapshot
	return report, nil
}

// ExplainRecords builds the same report from caller-provided records without
// filesystem or spool mutation. Bundles and replay services use this after
// applying their own read-only evidence policy.
func ExplainRecords(options ExplainOptions, records []diagnostics.TraceEnvelope) (ExplanationReport, error) {
	if err := normalizeExplainOptions(&options); err != nil {
		return ExplanationReport{}, err
	}
	return buildExplanation(options, records), nil
}

func normalizeExplainOptions(options *ExplainOptions) error {
	if options == nil {
		return fmt.Errorf("debugger: explain options are nil")
	}
	if options.Recent < 0 {
		return fmt.Errorf("explain recent count must not be negative")
	}
	setExplainOptionDefaults(options)
	if options.GraphDepth < MinExplainGraphDepth || options.GraphDepth > MaxExplainGraphDepth {
		return fmt.Errorf("explain graph depth must be between %d and %d", MinExplainGraphDepth, MaxExplainGraphDepth)
	}
	if options.MaxNodes < MinExplainMaxNodes || options.MaxNodes > MaxExplainMaxNodes {
		return fmt.Errorf("explain max nodes must be between %d and %d", MinExplainMaxNodes, MaxExplainMaxNodes)
	}
	if options.MaxEdges < MinExplainMaxEdges || options.MaxEdges > MaxExplainMaxEdges {
		return fmt.Errorf("explain max edges must be between %d and %d", MinExplainMaxEdges, MaxExplainMaxEdges)
	}
	if err := options.Selector.Normalize(); err != nil {
		return err
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	} else {
		options.Now = options.Now.UTC()
	}
	return nil
}

func setExplainOptionDefaults(options *ExplainOptions) {
	if options.Recent == 0 {
		options.Recent = 10
	}
	if options.GraphDepth == 0 {
		options.GraphDepth = DefaultExplainGraphDepth
	}
	if options.MaxNodes == 0 {
		options.MaxNodes = DefaultExplainMaxNodes
	}
	if options.MaxEdges == 0 {
		options.MaxEdges = DefaultExplainMaxEdges
	}
}

func buildExplanation(options ExplainOptions, records []diagnostics.TraceEnvelope) ExplanationReport {
	setExplainOptionDefaults(&options)
	selected, truncation := traverseExplanationRecords(records, options)
	graph, nodeIDs := buildEvidenceGraph(
		selected,
		relevantHookProfiles(options.Hooks, options.Selector),
		options.MaxNodes,
		options.MaxEdges,
		&truncation,
	)
	finalizeExplanationTruncation(&truncation)
	report := ExplanationReport{
		SchemaVersion: ExplainSchemaVersion, GeneratedAt: options.Now, Selector: options.Selector,
		Recent:     options.Recent,
		Limits:     ExplanationLimits{GraphDepth: options.GraphDepth, MaxNodes: options.MaxNodes, MaxEdges: options.MaxEdges},
		Truncation: truncation, Graph: graph, ExpectedSequences: expectedSequences(selected, nodeIDs),
		SuggestedCommands: make([]string, 0),
	}
	if options.StatusError != "" {
		report.Warnings = append(report.Warnings, options.StatusError)
	}
	report.Cause = explainCause(selected, nodeIDs, graph.Nodes, options.Selector)
	report.SuggestedCommands = suggestedCommands(report.Cause, selected, graph.Nodes)
	return report
}
