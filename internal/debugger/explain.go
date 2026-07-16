package debugger

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

const ExplainSchemaVersion = 1

const (
	CauseHookDisabled            = "hook_disabled"
	CauseHookUntrusted           = "hook_untrusted"
	CauseProducerAbsence         = "producer_absence"
	CauseOutsideKitty            = "outside_kitty"
	CauseInvalidPayload          = "invalid_payload"
	CauseDeliveryFailure         = "delivery_failure"
	CauseDuplicate               = "duplicate"
	CauseSaturation              = "saturation"
	CauseOwnerMismatch           = "owner_mismatch"
	CauseQuarantine              = "quarantine"
	CauseReducerSuppression      = "reducer_suppression"
	CauseRenderFailure           = "render_failure"
	CauseNotificationSuppression = "notification_suppression"
	CauseSuccessfulCompletion    = "successful_completion"
	CauseInsufficientEvidence    = "insufficient_evidence"
)

const (
	CertaintyProven    = "proven"
	CertaintyUncertain = "uncertain"
)

type ExplainOptions struct {
	Selector    Selector
	Recent      int
	Hooks       *HookReport
	StatusError string
	Now         time.Time
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

type ExplanationJournal struct {
	FilesRead         int `json:"files_read"`
	CorruptLines      int `json:"corrupt_lines"`
	ReconciledFiles   int `json:"reconciled_files"`
	ReconciledRecords int `json:"reconciled_records"`
}

type ExplanationReport struct {
	SchemaVersion     int                          `json:"schema_version"`
	GeneratedAt       time.Time                    `json:"generated_at"`
	Selector          Selector                     `json:"selector"`
	Recent            int                          `json:"recent"`
	Cause             ExplanationCause             `json:"cause"`
	Graph             EvidenceGraph                `json:"evidence_graph"`
	ExpectedSequences []ExpectedSequenceDiagnostic `json:"expected_sequences"`
	SuggestedCommands []string                     `json:"suggested_commands"`
	Journal           ExplanationJournal           `json:"journal"`
	Warnings          []string                     `json:"warnings,omitempty"`
}

// Explain reconciles and tolerantly reads the durable journal, then constructs
// a bounded causal graph. It never contacts or mutates the daemon.
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
	report.Journal = ExplanationJournal{
		FilesRead: tail.FilesRead, CorruptLines: tail.CorruptLines,
		ReconciledFiles: tail.ReconciledFiles, ReconciledRecords: tail.ReconciledRecords,
	}
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
	if err := options.Selector.Normalize(); err != nil {
		return err
	}
	if options.Recent == 0 {
		options.Recent = 10
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	} else {
		options.Now = options.Now.UTC()
	}
	return nil
}

func buildExplanation(options ExplainOptions, records []diagnostics.TraceEnvelope) ExplanationReport {
	selected := selectExplanationRecords(records, options.Selector, options.Recent)
	graph, nodeIDs := buildEvidenceGraph(selected, relevantHookProfiles(options.Hooks, options.Selector))
	report := ExplanationReport{
		SchemaVersion: ExplainSchemaVersion, GeneratedAt: options.Now, Selector: options.Selector,
		Recent: options.Recent, Graph: graph, ExpectedSequences: expectedSequences(selected, nodeIDs),
		SuggestedCommands: make([]string, 0),
	}
	if options.StatusError != "" {
		report.Warnings = append(report.Warnings, options.StatusError)
	}
	report.Cause = explainCause(selected, nodeIDs, graph.Nodes, options.Selector)
	report.SuggestedCommands = suggestedCommands(report.Cause, selected, graph.Nodes)
	return report
}

func selectExplanationRecords(records []diagnostics.TraceEnvelope, selector Selector, recent int) []diagnostics.TraceEnvelope {
	all := append([]diagnostics.TraceEnvelope(nil), records...)
	sortEnvelopes(all)
	seed := make(map[int]struct{})
	exact := selector.TraceID != "" || selector.RequestID != "" || selector.SourceEventID != ""
	for i, record := range all {
		if recordMatchesSelector(record, selector) {
			seed[i] = struct{}{}
		}
	}
	if !exact && len(seed) > recent {
		indices := make([]int, 0, len(seed))
		for i := range seed {
			indices = append(indices, i)
		}
		sort.Ints(indices)
		seed = make(map[int]struct{}, recent)
		seen := make(map[string]struct{})
		for i := len(indices) - 1; i >= 0 && len(seed) < recent; i-- {
			idx := indices[i]
			key := causalAnchor(all[idx])
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			seed[idx] = struct{}{}
		}
	}
	if len(seed) == 0 {
		return nil
	}
	for changed := true; changed; {
		changed = false
		joins := collectStrongJoins(all, seed)
		for i, record := range all {
			if _, ok := seed[i]; ok {
				continue
			}
			if joins.matches(record) {
				seed[i] = struct{}{}
				changed = true
			}
		}
	}
	selected := make([]diagnostics.TraceEnvelope, 0, len(seed))
	for i, record := range all {
		if _, ok := seed[i]; ok {
			selected = append(selected, record)
		}
	}
	return selected
}

func recordMatchesSelector(record diagnostics.TraceEnvelope, selector Selector) bool {
	if !selector.After.IsZero() && record.ObservedAt.Before(selector.After) ||
		!selector.Before.IsZero() && record.ObservedAt.After(selector.Before) {
		return false
	}
	if selector.TraceID != "" && record.TraceID != selector.TraceID ||
		selector.RequestID != "" && record.RequestID != selector.RequestID ||
		selector.Harness != "" && record.Harness != selector.Harness ||
		selector.Profile != "" && record.Profile != selector.Profile ||
		selector.WindowID != 0 && record.WindowID != selector.WindowID ||
		selector.SessionID != "" && record.SessionID != selector.SessionID ||
		selector.ASLRunID != "" && record.RunID != selector.ASLRunID ||
		selector.Event != "" && record.Evidence["event"] != selector.Event {
		return false
	}
	if selector.SourceEventID != "" && !containsString(sourceEventIDs(record), selector.SourceEventID) {
		return false
	}
	return containsOrNoFilter(selector.Stages, record.Stage) && containsOrNoFilter(selector.Outcomes, record.Outcome)
}

func causalAnchor(record diagnostics.TraceEnvelope) string {
	if record.TraceID != "" {
		return "trace:" + record.TraceID
	}
	if record.RequestID != "" {
		return "request:" + record.RequestID
	}
	if ids := sourceEventIDs(record); len(ids) > 0 {
		return "event:" + ids[0]
	}
	return "record:" + record.SourceID + ":" + strconv.FormatUint(record.SourceSequence, 10) + ":" + record.SpanID
}

type strongJoins struct {
	traces, parentTraces, requests, sourceEvents, generations, launches map[string]struct{}
	turns, toolUses, subagents, sessions, runs                          map[string]struct{}
}

func collectStrongJoins(records []diagnostics.TraceEnvelope, selected map[int]struct{}) strongJoins {
	j := strongJoins{
		traces: map[string]struct{}{}, parentTraces: map[string]struct{}{}, requests: map[string]struct{}{},
		sourceEvents: map[string]struct{}{}, generations: map[string]struct{}{}, launches: map[string]struct{}{},
		turns: map[string]struct{}{}, toolUses: map[string]struct{}{}, subagents: map[string]struct{}{},
		sessions: map[string]struct{}{}, runs: map[string]struct{}{},
	}
	for i := range selected {
		r := records[i]
		addJoin(j.traces, r.TraceID)
		addJoin(j.parentTraces, r.ParentTraceID)
		addJoin(j.requests, r.RequestID)
		for _, id := range sourceEventIDs(r) {
			addJoin(j.sourceEvents, id)
		}
		addJoin(j.generations, renderGenerationKey(r))
		addJoin(j.launches, r.LaunchID)
		addJoin(j.turns, scopedRecordID(r, r.Evidence["turn_id"]))
		addJoin(j.toolUses, scopedRecordID(r, r.Evidence["tool_use_id"]))
		if r.Evidence["source_kind"] == "subagent" {
			addJoin(j.subagents, scopedRecordID(r, r.Evidence["source_key"]))
		}
		addJoin(j.sessions, r.SessionID)
		addJoin(j.runs, r.RunID)
	}
	return j
}

func (j strongJoins) matches(record diagnostics.TraceEnvelope) bool {
	if hasJoin(j.traces, record.TraceID) || hasJoin(j.traces, record.ParentTraceID) ||
		hasJoin(j.parentTraces, record.TraceID) || hasJoin(j.requests, record.RequestID) ||
		hasJoin(j.generations, renderGenerationKey(record)) || hasJoin(j.launches, record.LaunchID) ||
		hasJoin(j.turns, scopedRecordID(record, record.Evidence["turn_id"])) ||
		hasJoin(j.toolUses, scopedRecordID(record, record.Evidence["tool_use_id"])) ||
		record.Evidence["source_kind"] == "subagent" && hasJoin(j.subagents, scopedRecordID(record, record.Evidence["source_key"])) {
		return true
	}
	if record.Stage == "session.launch" && (hasJoin(j.sessions, record.SessionID) || hasJoin(j.runs, record.RunID)) {
		return true
	}
	for _, id := range sourceEventIDs(record) {
		if hasJoin(j.sourceEvents, id) {
			return true
		}
	}
	return false
}

func addJoin(set map[string]struct{}, value string) {
	if value != "" {
		set[value] = struct{}{}
	}
}

func hasJoin(set map[string]struct{}, value string) bool {
	if value == "" {
		return false
	}
	_, ok := set[value]
	return ok
}

func scopedRecordID(record diagnostics.TraceEnvelope, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	scope := firstNonEmptyString(record.SessionID, record.RunID)
	if scope == "" && record.Harness != "" && record.WindowID > 0 {
		scope = record.Harness + ":window:" + strconv.Itoa(record.WindowID)
	}
	if scope == "" && record.WindowID > 0 {
		scope = "window:" + strconv.Itoa(record.WindowID)
	}
	if scope == "" {
		return id
	}
	return scope + ":" + id
}

func sourceEventIDs(record diagnostics.TraceEnvelope) []string {
	values := make([]string, 0, 2)
	for _, key := range []string{"source_event_id", "source_event_ids"} {
		for _, value := range strings.Split(record.Evidence[key], ",") {
			value = strings.TrimSpace(value)
			if value != "" && !containsString(values, value) {
				values = append(values, value)
			}
		}
	}
	return values
}

func renderGenerationKey(record diagnostics.TraceEnvelope) string {
	generation := strings.TrimSpace(record.Evidence["generation"])
	if generation == "" || generation == "0" {
		generation = strings.TrimSpace(record.Evidence["render_generation"])
	}
	if generation == "" || generation == "0" {
		return ""
	}
	return strconv.Itoa(record.WindowID) + ":" + generation
}

func relevantHookProfiles(report *HookReport, selector Selector) []HookProfile {
	if report == nil || selector.Harness == "" && selector.Profile == "" {
		return nil
	}
	profiles := make([]HookProfile, 0)
	for _, profile := range report.Profiles {
		if selector.Harness != "" && profile.Harness != selector.Harness || selector.Profile != "" && profile.Profile != selector.Profile {
			continue
		}
		profiles = append(profiles, profile)
	}
	return profiles
}

func buildEvidenceGraph(records []diagnostics.TraceEnvelope, hooks []HookProfile) (EvidenceGraph, []string) {
	graph := EvidenceGraph{Nodes: make([]EvidenceNode, 0, len(records)+len(hooks)), Edges: make([]EvidenceEdge, 0)}
	nodeIDs := make([]string, len(records))
	used := make(map[string]int)
	for i := range records {
		record := records[i]
		base := recordNodeID(record)
		used[base]++
		id := base
		if used[base] > 1 {
			id = base + ":" + strconv.Itoa(used[base])
		}
		nodeIDs[i] = id
		copyRecord := record
		graph.Nodes = append(graph.Nodes, EvidenceNode{ID: id, Kind: "trace", Identity: evidenceIdentity(record), Record: &copyRecord})
	}
	for i := range hooks {
		profile := hooks[i]
		id := "hook:" + profile.Profile + ":" + profile.Harness
		copyProfile := profile
		graph.Nodes = append(graph.Nodes, EvidenceNode{ID: id, Kind: "hook_status", Hook: &copyProfile})
	}
	graph.Edges = graphEdges(graph.Nodes)
	return graph, nodeIDs
}

func recordNodeID(record diagnostics.TraceEnvelope) string {
	if record.SourceID != "" && record.SourceSequence != 0 {
		return "trace:" + record.SourceID + ":" + strconv.FormatUint(record.SourceSequence, 10)
	}
	if record.SpanID != "" {
		return "span:" + record.SpanID
	}
	return "record:" + record.TraceID + ":" + record.Stage
}

func evidenceIdentity(record diagnostics.TraceEnvelope) EvidenceIdentity {
	normalizedSource := ""
	if kind, key := record.Evidence["source_kind"], record.Evidence["source_key"]; kind != "" || key != "" {
		normalizedSource = kind + ":" + key
	}
	return EvidenceIdentity{
		TraceID: record.TraceID, ParentTraceID: record.ParentTraceID, RequestID: record.RequestID,
		SourceEventIDs: sourceEventIDs(record), ProducerSourceID: record.SourceID,
		ProducerSourceSequence: record.SourceSequence, NormalizedSourceID: normalizedSource,
		NormalizedSourceSequence: parseUint(record.Evidence["source_sequence"]),
		DaemonSequence:           parseUint(record.Evidence["daemon_sequence"]),
		RenderGeneration:         parseUint(firstNonEmptyString(record.Evidence["generation"], record.Evidence["render_generation"])),
		LaunchID:                 record.LaunchID, WindowID: record.WindowID, SessionID: record.SessionID, ASLRunID: record.RunID,
		TurnID: record.Evidence["turn_id"], ToolUseID: record.Evidence["tool_use_id"],
	}
}

func graphEdges(nodes []EvidenceNode) []EvidenceEdge {
	groups := make(map[string][]string)
	values := make(map[string]string)
	for _, node := range nodes {
		id := node.Identity
		groupJoin(groups, values, "trace_id", id.TraceID, node.ID)
		groupJoin(groups, values, "request_id", id.RequestID, node.ID)
		for _, sourceEventID := range id.SourceEventIDs {
			groupJoin(groups, values, "source_event_id", sourceEventID, node.ID)
		}
		groupJoin(groups, values, "producer_source_id", id.ProducerSourceID, node.ID)
		groupJoin(groups, values, "normalized_source_id", id.NormalizedSourceID, node.ID)
		if id.DaemonSequence > 0 {
			groupJoin(groups, values, "daemon_sequence", strconv.FormatUint(id.DaemonSequence, 10), node.ID)
		}
		if id.RenderGeneration > 0 {
			groupJoin(groups, values, "render_generation", strconv.Itoa(id.WindowID)+":"+strconv.FormatUint(id.RenderGeneration, 10), node.ID)
		}
		groupJoin(groups, values, "launch_id", id.LaunchID, node.ID)
		if id.WindowID > 0 && id.SessionID != "" {
			groupJoin(groups, values, "window_session", strconv.Itoa(id.WindowID)+":"+id.SessionID, node.ID)
		}
		groupJoin(groups, values, "session_id", id.SessionID, node.ID)
		groupJoin(groups, values, "asl_run_id", id.ASLRunID, node.ID)
		if node.Record != nil {
			groupJoin(groups, values, "turn_id", scopedRecordID(*node.Record, id.TurnID), node.ID)
			groupJoin(groups, values, "tool_use_id", scopedRecordID(*node.Record, id.ToolUseID), node.ID)
		}
		if node.Hook != nil {
			groupJoin(groups, values, "hook_scope", node.Hook.Profile+":"+node.Hook.Harness, node.ID)
		}
		if node.Record != nil && node.Record.Profile != "" && node.Record.Harness != "" {
			groupJoin(groups, values, "hook_scope", node.Record.Profile+":"+node.Record.Harness, node.ID)
		}
	}
	edges := make([]EvidenceEdge, 0)
	seen := make(map[string]struct{})
	for key, ids := range groups {
		if len(ids) < 2 {
			continue
		}
		parts := strings.SplitN(key, "\x00", 2)
		for _, id := range ids[1:] {
			addGraphEdge(&edges, seen, EvidenceEdge{From: ids[0], To: id, Relation: parts[0], Value: values[key]})
		}
	}
	for _, child := range nodes {
		if child.Identity.ParentTraceID == "" {
			continue
		}
		for _, parent := range nodes {
			if parent.Identity.TraceID == child.Identity.ParentTraceID {
				addGraphEdge(&edges, seen, EvidenceEdge{From: parent.ID, To: child.ID, Relation: "parent_trace_id", Value: child.Identity.ParentTraceID})
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		if edges[i].Relation != edges[j].Relation {
			return edges[i].Relation < edges[j].Relation
		}
		return edges[i].Value < edges[j].Value
	})
	return edges
}

func groupJoin(groups map[string][]string, values map[string]string, relation, value, nodeID string) {
	if value == "" {
		return
	}
	key := relation + "\x00" + value
	groups[key] = append(groups[key], nodeID)
	values[key] = value
}

func addGraphEdge(edges *[]EvidenceEdge, seen map[string]struct{}, edge EvidenceEdge) {
	key := edge.From + "\x00" + edge.To + "\x00" + edge.Relation + "\x00" + edge.Value
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*edges = append(*edges, edge)
}

func explainCause(records []diagnostics.TraceEnvelope, nodeIDs []string, nodes []EvidenceNode, selector Selector) ExplanationCause {
	for i, record := range records {
		if classification, summary, ok := classifyFailureRecord(record); ok {
			return ExplanationCause{
				Classification: classification, Certainty: CertaintyProven, Summary: summary,
				FirstFailedStage: record.Stage, EvidenceIDs: []string{nodeIDs[i]},
			}
		}
	}
	if len(records) == 0 {
		for _, node := range nodes {
			if node.Hook == nil || node.Hook.Activation != "failure" {
				continue
			}
			for _, issue := range node.Hook.Issues {
				code := strings.ToLower(issue.Code)
				if !hookIssueAppliesToEvent(code, issue.Message, selector.Event) {
					continue
				}
				switch {
				case strings.Contains(code, "untrusted") || strings.Contains(code, "trust"):
					return ExplanationCause{Classification: CauseHookUntrusted, Certainty: CertaintyProven, Summary: issue.Message, FirstFailedStage: "hook activation", EvidenceIDs: []string{node.ID}}
				case strings.Contains(code, "disabled") || strings.Contains(code, "missing") || strings.Contains(code, "drifted"):
					return ExplanationCause{Classification: CauseHookDisabled, Certainty: CertaintyProven, Summary: issue.Message, FirstFailedStage: "hook activation", EvidenceIDs: []string{node.ID}}
				}
			}
		}
	}
	terminal := make([]string, 0, 2)
	for i, record := range records {
		if isSuccessfulTerminal(record) {
			terminal = append(terminal, nodeIDs[i])
		}
	}
	if len(terminal) > 0 {
		return ExplanationCause{
			Classification: CauseSuccessfulCompletion, Certainty: CertaintyProven,
			Summary: "The recorded Agent Mux decision path reached a successful terminal stage.", EvidenceIDs: terminal,
		}
	}
	missing := firstMissingStage(records)
	summary := "No matching causal evidence was recorded; this does not prove that the producer failed to invoke its hook."
	if len(records) > 0 {
		summary = "The available evidence has no proven terminal cause; missing stages remain uncertain rather than evidence of producer absence."
	}
	return ExplanationCause{
		Classification: CauseInsufficientEvidence, Certainty: CertaintyUncertain,
		Summary: summary, FirstMissingStage: missing, EvidenceIDs: []string{},
	}
}

func hookIssueAppliesToEvent(code, message, event string) bool {
	event = strings.ToLower(strings.TrimSpace(event))
	if event == "" {
		return true
	}
	specific := code == "events_disabled" || code == "codex_hook_untrusted" || code == "codex_hook_disabled"
	return !specific || strings.Contains(strings.ToLower(message), event)
}

func classifyFailureRecord(record diagnostics.TraceEnvelope) (string, string, bool) {
	reason := strings.ToLower(strings.TrimSpace(record.Reason))
	outcome := strings.ToLower(strings.TrimSpace(record.Outcome))
	stage := strings.ToLower(strings.TrimSpace(record.Stage))
	normalization := strings.ToLower(record.Evidence["normalization_outcome"])
	saturation := strings.ToLower(record.Evidence["saturation"])
	switch {
	case reason == "producer_not_invoked" || reason == "producer_absent" || strings.EqualFold(record.Evidence["producer_invoked"], "false"):
		return CauseProducerAbsence, "A record explicitly proves that the producer did not invoke the hook.", true
	case reason == "outside_kitty":
		return CauseOutsideKitty, "The hook ran outside Kitty, so no Agent Mux window identity was available.", true
	case strings.HasPrefix(reason, daemon.DecisionReasonInvalidPayload) || normalization == daemon.NormalizationOutcomeQuarantined && strings.Contains(reason, "invalid"):
		return CauseInvalidPayload, "The recorded payload failed validation and was not admitted as trusted input.", true
	case saturation != "" || strings.Contains(reason, "saturat") || strings.Contains(stage, "saturat") || evidencePositive(record.Evidence["queue_full"]):
		return CauseSaturation, "A bounded delivery component recorded saturation for this request.", true
	case reason == daemon.DecisionReasonRequestReplayed || reason == daemon.DecisionReasonDuplicateSequence || reason == daemon.DecisionReasonReorderedSequence || outcome == "duplicate" || outcome == "reordered":
		return CauseDuplicate, "The request or source sequence was recorded as a duplicate or replay.", true
	case reason == daemon.DecisionReasonOwnerMismatch:
		return CauseOwnerMismatch, "Routing rejected the event because the producer did not match the managed title owner.", true
	case outcome == "quarantined" || strings.Contains(normalization, "quarantined") || reason == daemon.DecisionReasonUntrustedIdentityFallback:
		return CauseQuarantine, "The daemon quarantined the event or used an explicitly untrusted compatibility identity.", true
	case strings.HasPrefix(stage, "daemon.renderer.") && (strings.HasSuffix(stage, ".failed") || outcome == "failed" || outcome == "error"):
		return CauseRenderFailure, "The renderer recorded a terminal failure for the causal generation.", true
	case strings.HasPrefix(stage, "daemon.notification.") && (outcome == "suppressed" || outcome == "cancelled" || outcome == "failed"):
		return CauseNotificationSuppression, "The notification path recorded a suppression, cancellation, or failed dispatch.", true
	case stage == "daemon.decision" && (outcome == "suppressed" || outcome == "bypassed"):
		return CauseReducerSuppression, "The daemon recorded a reducer suppression or intentional reducer bypass.", true
	case strings.HasPrefix(stage, "hook.") && (outcome == "error" || outcome == "failed" || outcome == "rejected" || outcome == "retryable_failure" || outcome == "permanent_failure"):
		return CauseDeliveryFailure, "The hook client recorded a delivery failure before successful completion.", true
	default:
		return "", "", false
	}
}

func isSuccessfulTerminal(record diagnostics.TraceEnvelope) bool {
	return record.Stage == "daemon.renderer.rendered" && record.Outcome == "success"
}

func firstMissingStage(records []diagnostics.TraceEnvelope) string {
	if len(records) == 0 {
		return "producer hook invocation"
	}
	hasHook, hasDelivery, hasDecision, hasRendererTerminal := false, false, false, false
	for _, record := range records {
		hasHook = hasHook || strings.HasPrefix(record.Stage, "hook.")
		hasDelivery = hasDelivery || record.Stage == "hook.delivery"
		hasDecision = hasDecision || record.Stage == "daemon.decision"
		hasRendererTerminal = hasRendererTerminal || record.Stage == "daemon.renderer.rendered" || record.Stage == "daemon.renderer.failed"
	}
	switch {
	case !hasHook && hasDecision:
		return "hook client stages before daemon admission"
	case hasHook && !hasDelivery:
		return "hook.delivery"
	case hasDelivery && !hasDecision:
		return "daemon.decision"
	case hasDecision && !hasRendererTerminal:
		return "terminal renderer evidence"
	default:
		return "terminal outcome"
	}
}

type sequenceOccurrence struct {
	record diagnostics.TraceEnvelope
	nodeID string
	event  string
}

type sequenceBoundary struct {
	kind, side, outcome string
}

func expectedSequences(records []diagnostics.TraceEnvelope, nodeIDs []string) []ExpectedSequenceDiagnostic {
	occurrences := lifecycleOccurrences(records, nodeIDs)
	pending := make(map[string][]sequenceOccurrence)
	diagnosticsOut := make([]ExpectedSequenceDiagnostic, 0)
	for _, occurrence := range occurrences {
		boundary, ok := lifecycleBoundary(occurrence.event)
		if !ok {
			continue
		}
		scope := sequenceScope(boundary.kind, occurrence.record)
		key := boundary.kind + "\x00" + scope
		if boundary.side == "start" {
			pending[key] = append(pending[key], occurrence)
			continue
		}
		if len(pending[key]) == 0 {
			diagnosticsOut = append(diagnosticsOut, ExpectedSequenceDiagnostic{
				Kind: boundary.kind, Scope: scope, Status: CertaintyUncertain, Outcome: boundary.outcome,
				ObservedSteps: []string{occurrence.event}, MissingSteps: []string{sequenceStartName(boundary.kind)},
				EvidenceIDs: []string{occurrence.nodeID}, Message: "A terminal event was recorded without matching start evidence; no conclusion is drawn about earlier producer activity.",
			})
			continue
		}
		start := pending[key][0]
		pending[key] = pending[key][1:]
		diagnosticsOut = append(diagnosticsOut, ExpectedSequenceDiagnostic{
			Kind: boundary.kind, Scope: scope, Status: "complete", Outcome: boundary.outcome,
			ObservedSteps: []string{start.event, occurrence.event}, EvidenceIDs: []string{start.nodeID, occurrence.nodeID},
			Message: "The expected lifecycle start and terminal events were both recorded.",
		})
	}
	for key, starts := range pending {
		parts := strings.SplitN(key, "\x00", 2)
		for _, start := range starts {
			diagnosticsOut = append(diagnosticsOut, ExpectedSequenceDiagnostic{
				Kind: parts[0], Scope: parts[1], Status: CertaintyUncertain,
				ObservedSteps: []string{start.event}, MissingSteps: []string{sequenceEndName(parts[0])},
				EvidenceIDs: []string{start.nodeID}, Message: "No matching terminal event was recorded; absence alone does not prove that the producer omitted it.",
			})
		}
	}
	sort.SliceStable(diagnosticsOut, func(i, j int) bool {
		left, right := diagnosticsOut[i], diagnosticsOut[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Scope != right.Scope {
			return left.Scope < right.Scope
		}
		return strings.Join(left.EvidenceIDs, ",") < strings.Join(right.EvidenceIDs, ",")
	})
	return diagnosticsOut
}

func lifecycleOccurrences(records []diagnostics.TraceEnvelope, nodeIDs []string) []sequenceOccurrence {
	type ranked struct {
		occurrence sequenceOccurrence
		rank       int
	}
	byEvent := make(map[string]ranked)
	order := make([]string, 0)
	for i, record := range records {
		event := strings.TrimSpace(record.Evidence["event"])
		if event == "" {
			continue
		}
		key := firstNonEmptyString(record.Evidence["source_event_id"], record.TraceID+":"+event)
		rank := 1
		if record.Stage == "hook.delivery" {
			rank = 2
		}
		if record.Stage == "daemon.decision" {
			rank = 3
		}
		if existing, ok := byEvent[key]; ok && existing.rank >= rank {
			continue
		}
		if _, ok := byEvent[key]; !ok {
			order = append(order, key)
		}
		byEvent[key] = ranked{occurrence: sequenceOccurrence{record: record, nodeID: nodeIDs[i], event: event}, rank: rank}
	}
	occurrences := make([]sequenceOccurrence, 0, len(order))
	for _, key := range order {
		occurrences = append(occurrences, byEvent[key].occurrence)
	}
	sort.SliceStable(occurrences, func(i, j int) bool {
		leftSequence := parseUint(occurrences[i].record.Evidence["daemon_sequence"])
		rightSequence := parseUint(occurrences[j].record.Evidence["daemon_sequence"])
		if leftSequence > 0 && rightSequence > 0 && occurrences[i].record.SourceID == occurrences[j].record.SourceID {
			return leftSequence < rightSequence
		}
		return occurrences[i].record.ObservedAt.Before(occurrences[j].record.ObservedAt)
	})
	return occurrences
}

func lifecycleBoundary(event string) (sequenceBoundary, bool) {
	canonical := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(event))
	switch canonical {
	case "sessionstart":
		return sequenceBoundary{kind: "session", side: "start"}, true
	case "sessionend":
		return sequenceBoundary{kind: "session", side: "end", outcome: "success"}, true
	case "userpromptsubmit", "userpromptsubmitted":
		return sequenceBoundary{kind: "prompt_stop", side: "start"}, true
	case "stop", "agentstop":
		return sequenceBoundary{kind: "prompt_stop", side: "end", outcome: "success"}, true
	case "stopfailure":
		return sequenceBoundary{kind: "prompt_stop", side: "end", outcome: "failure"}, true
	case "pretooluse":
		return sequenceBoundary{kind: "tool", side: "start"}, true
	case "posttooluse":
		return sequenceBoundary{kind: "tool", side: "end", outcome: "success"}, true
	case "posttoolusefailure":
		return sequenceBoundary{kind: "tool", side: "end", outcome: "failure"}, true
	case "precompact":
		return sequenceBoundary{kind: "compaction", side: "start"}, true
	case "postcompact", "postcompactmanual", "postcompactauto":
		return sequenceBoundary{kind: "compaction", side: "end", outcome: "success"}, true
	case "subagentstart":
		return sequenceBoundary{kind: "subagent", side: "start"}, true
	case "subagentstop":
		return sequenceBoundary{kind: "subagent", side: "end", outcome: "success"}, true
	default:
		return sequenceBoundary{}, false
	}
}

func sequenceScope(kind string, record diagnostics.TraceEnvelope) string {
	base := firstNonEmptyString(record.SessionID, record.RunID)
	if base == "" {
		if source := firstNonEmptyString(record.Evidence["source_key"], record.Evidence["source_kind"]); source != "" {
			base = record.Evidence["source_kind"] + ":" + record.Evidence["source_key"]
		}
	}
	if base == "" && record.WindowID > 0 {
		base = "window:" + strconv.Itoa(record.WindowID)
	}
	if base == "" {
		base = firstNonEmptyString(record.TraceID, "unknown")
	}
	switch kind {
	case "tool":
		if id := record.Evidence["tool_use_id"]; id != "" {
			return base + "/tool:" + id
		}
	case "prompt_stop", "compaction":
		if id := record.Evidence["turn_id"]; id != "" {
			return base + "/turn:" + id
		}
	case "subagent":
		if key := record.Evidence["source_key"]; key != "" {
			return base + "/agent:" + key
		}
	}
	return base
}

func sequenceStartName(kind string) string {
	return map[string]string{"session": "session start", "prompt_stop": "prompt", "tool": "tool start", "compaction": "compaction start", "subagent": "subagent start"}[kind]
}

func sequenceEndName(kind string) string {
	return map[string]string{"session": "session end", "prompt_stop": "stop", "tool": "tool success or failure", "compaction": "compaction end", "subagent": "subagent stop"}[kind]
}

func suggestedCommands(cause ExplanationCause, records []diagnostics.TraceEnvelope, nodes []EvidenceNode) []string {
	commands := make([]string, 0, 2)
	add := func(command string) {
		if command != "" && !containsString(commands, command) {
			commands = append(commands, command)
		}
	}
	if cause.Classification == CauseHookDisabled || cause.Classification == CauseHookUntrusted {
		add("amx config hooks-status")
		for _, node := range nodes {
			if node.Hook == nil {
				continue
			}
			for _, issue := range node.Hook.Issues {
				code := strings.ToLower(issue.Code)
				if strings.Contains(code, "missing") || strings.Contains(code, "drifted") {
					add("amx config hooks-sync")
				}
			}
		}
		return commands
	}
	requestID, windowID, harnessName := "", 0, ""
	for _, record := range records {
		requestID = firstNonEmptyString(requestID, record.RequestID)
		if windowID == 0 {
			windowID = record.WindowID
		}
		harnessName = firstNonEmptyString(harnessName, record.Harness)
	}
	switch cause.Classification {
	case CauseDeliveryFailure, CauseSaturation, CauseOwnerMismatch, CauseOutsideKitty:
		if windowID > 0 {
			command := "amx debug probe --window " + strconv.Itoa(windowID)
			if safeCommandToken(harnessName) {
				command += " --harness " + harnessName
			}
			add(command)
		}
	case CauseInvalidPayload, CauseDuplicate, CauseQuarantine, CauseReducerSuppression, CauseRenderFailure, CauseNotificationSuppression:
		if safeCommandToken(requestID) {
			add("amx debug tail --request " + requestID)
		}
	}
	return commands
}

func evidencePositive(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "true" || value == "yes" {
		return true
	}
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && n > 0
}

func parseUint(value string) uint64 {
	parsed, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return parsed
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func containsOrNoFilter(values []string, candidate string) bool {
	return len(values) == 0 || containsString(values, candidate)
}

func safeCommandToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		asciiLetterOrDigit := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !asciiLetterOrDigit && !strings.ContainsRune("._:-", r) {
			return false
		}
	}
	return true
}
