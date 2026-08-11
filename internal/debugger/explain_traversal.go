package debugger

import (
	"sort"
	"strconv"
	"strings"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func traverseExplanationRecords(records []diagnostics.TraceEnvelope, options ExplainOptions) ([]diagnostics.TraceEnvelope, ExplanationTruncation) {
	all := append([]diagnostics.TraceEnvelope(nil), records...)
	sortEnvelopes(all)
	index := buildExplanationIndex(all)
	seeds := explanationSeedIndices(all, options.Selector, options.Recent)
	if len(seeds) == 0 {
		return nil, ExplanationTruncation{Reasons: make([]string, 0)}
	}

	admitted := make(map[int]struct{}, minInt(len(all), options.MaxNodes))
	causal := make(map[int]struct{}, minInt(len(all), options.MaxNodes))
	prunedNodes := make(map[int]map[string]struct{})
	truncation := ExplanationTruncation{Reasons: make([]string, 0)}
	markPruned := func(candidate int, reason string) {
		if _, ok := admitted[candidate]; ok {
			return
		}
		reasons := prunedNodes[candidate]
		if reasons == nil {
			reasons = make(map[string]struct{})
			prunedNodes[candidate] = reasons
		}
		reasons[reason] = struct{}{}
	}
	admitCausal := func(candidate int) bool {
		if _, ok := causal[candidate]; ok {
			return false
		}
		if _, ok := admitted[candidate]; !ok {
			if len(admitted) >= options.MaxNodes {
				markPruned(candidate, TruncationMaxNodes)
				return false
			}
			admitted[candidate] = struct{}{}
			delete(prunedNodes, candidate)
		}
		causal[candidate] = struct{}{}
		return true
	}

	frontier := make([]int, 0, len(seeds))
	for _, candidate := range seeds {
		if admitCausal(candidate) {
			frontier = append(frontier, candidate)
		}
	}
	for depth := 0; depth < options.GraphDepth && len(frontier) > 0; depth++ {
		candidates := index.causalCandidates(frontier, causal)
		next := make([]int, 0, len(candidates))
		for _, candidate := range candidates {
			if admitCausal(candidate) {
				next = append(next, candidate)
			}
		}
		frontier = next
	}
	// Inspect only the next causal frontier. This reports directly pruned depth
	// candidates without recursively calculating an unbounded omitted total.
	for _, candidate := range index.causalCandidates(frontier, causal) {
		markPruned(candidate, TruncationGraphDepth)
	}

	// Context records are attached only after causal traversal so a high-fan-out
	// render/session/tool join cannot consume capacity needed by causal evidence.
	causalIndices := sortedExplanationIndices(causal)
	retractedDepthFrontier := make([]int, 0)
	for _, candidate := range index.contextualCandidates(causalIndices, admitted) {
		if len(admitted) >= options.MaxNodes {
			markPruned(candidate, TruncationMaxNodes)
			continue
		}
		_, wasDepthPruned := prunedNodes[candidate][TruncationGraphDepth]
		admitted[candidate] = struct{}{}
		delete(prunedNodes, candidate)
		if wasDepthPruned {
			retractedDepthFrontier = append(retractedDepthFrontier, candidate)
		}
	}
	// A contextually admitted depth-frontier record may expose a direct causal
	// child that remains excluded. Inspect that next frontier once without
	// admitting it or recursively fanning out from contextual evidence.
	for _, candidate := range index.causalCandidates(retractedDepthFrontier, admitted) {
		markPruned(candidate, TruncationGraphDepth)
	}

	for _, reasons := range prunedNodes {
		for reason := range reasons {
			addExplanationTruncationReason(&truncation, reason)
		}
	}
	truncation.PrunedNodeCandidates = len(prunedNodes)
	selected := make([]diagnostics.TraceEnvelope, 0, len(admitted))
	for _, candidate := range sortedExplanationIndices(admitted) {
		selected = append(selected, all[candidate])
	}
	return selected, truncation
}

func explanationSeedIndices(records []diagnostics.TraceEnvelope, selector Selector, recent int) []int {
	indices := make([]int, 0)
	for i, record := range records {
		if recordMatchesSelector(record, selector) {
			indices = append(indices, i)
		}
	}
	exact := selector.TraceID != "" || selector.RequestID != "" || selector.SourceEventID != ""
	if exact || len(indices) <= recent {
		return indices
	}
	selected := make([]int, 0, recent)
	seen := make(map[string]struct{})
	for i := len(indices) - 1; i >= 0 && len(selected) < recent; i-- {
		candidate := indices[i]
		anchor := causalAnchor(records[candidate])
		if _, ok := seen[anchor]; ok {
			continue
		}
		seen[anchor] = struct{}{}
		selected = append(selected, candidate)
	}
	sort.Ints(selected)
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

type explanationIndex struct {
	records      []diagnostics.TraceEnvelope
	traces       map[string][]int
	parentTraces map[string][]int
	requests     map[string][]int
	sourceEvents map[string][]int
	generations  map[string][]int
	launches     map[string][]int
	turns        map[string][]int
	toolUses     map[string][]int
	subagents    map[string][]int
	sessions     map[string][]int
	runs         map[string][]int
}

func buildExplanationIndex(records []diagnostics.TraceEnvelope) explanationIndex {
	index := explanationIndex{
		records: records, traces: make(map[string][]int), parentTraces: make(map[string][]int),
		requests: make(map[string][]int), sourceEvents: make(map[string][]int), generations: make(map[string][]int),
		launches: make(map[string][]int), turns: make(map[string][]int), toolUses: make(map[string][]int),
		subagents: make(map[string][]int), sessions: make(map[string][]int), runs: make(map[string][]int),
	}
	for i, record := range records {
		addExplanationIndex(index.traces, record.TraceID, i)
		addExplanationIndex(index.parentTraces, record.ParentTraceID, i)
		addExplanationIndex(index.requests, record.RequestID, i)
		for _, id := range sourceEventIDs(record) {
			addExplanationIndex(index.sourceEvents, id, i)
		}
		addExplanationIndex(index.generations, renderGenerationKey(record), i)
		addExplanationIndex(index.launches, record.LaunchID, i)
		addExplanationIndex(index.turns, scopedRecordID(record, record.Evidence["turn_id"]), i)
		addExplanationIndex(index.toolUses, scopedRecordID(record, record.Evidence["tool_use_id"]), i)
		if record.Evidence["source_kind"] == "subagent" {
			addExplanationIndex(index.subagents, scopedRecordID(record, record.Evidence["source_key"]), i)
		}
		addExplanationIndex(index.sessions, record.SessionID, i)
		addExplanationIndex(index.runs, record.RunID, i)
	}
	return index
}

func (index explanationIndex) causalCandidates(frontier []int, admitted map[int]struct{}) []int {
	candidates := make(map[int]struct{})
	for _, current := range frontier {
		record := index.records[current]
		addExplanationCandidates(candidates, index.traces[record.TraceID])
		addExplanationCandidates(candidates, index.requests[record.RequestID])
		for _, id := range sourceEventIDs(record) {
			addExplanationCandidates(candidates, index.sourceEvents[id])
		}
		addExplanationCandidates(candidates, index.traces[record.ParentTraceID])
		addExplanationCandidates(candidates, index.parentTraces[record.TraceID])
	}
	return newExplanationCandidates(candidates, admitted)
}

func (index explanationIndex) contextualCandidates(causal []int, admitted map[int]struct{}) []int {
	candidates := make(map[int]struct{})
	for _, current := range causal {
		record := index.records[current]
		addExplanationCandidates(candidates, index.generations[renderGenerationKey(record)])
		addExplanationCandidates(candidates, index.launches[record.LaunchID])
		addExplanationCandidates(candidates, index.turns[scopedRecordID(record, record.Evidence["turn_id"])])
		addExplanationCandidates(candidates, index.toolUses[scopedRecordID(record, record.Evidence["tool_use_id"])])
		if record.Evidence["source_kind"] == "subagent" {
			addExplanationCandidates(candidates, index.subagents[scopedRecordID(record, record.Evidence["source_key"])])
		}
		addExplanationCandidates(candidates, index.sessions[record.SessionID])
		addExplanationCandidates(candidates, index.runs[record.RunID])
	}
	return newExplanationCandidates(candidates, admitted)
}

func addExplanationIndex(index map[string][]int, key string, record int) {
	if key != "" {
		index[key] = append(index[key], record)
	}
}

func addExplanationCandidates(candidates map[int]struct{}, records []int) {
	for _, record := range records {
		candidates[record] = struct{}{}
	}
}

func newExplanationCandidates(candidates, admitted map[int]struct{}) []int {
	for candidate := range admitted {
		delete(candidates, candidate)
	}
	return sortedExplanationIndices(candidates)
}

func sortedExplanationIndices(values map[int]struct{}) []int {
	indices := make([]int, 0, len(values))
	for value := range values {
		indices = append(indices, value)
	}
	sort.Ints(indices)
	return indices
}

func addExplanationTruncationReason(truncation *ExplanationTruncation, reason string) {
	if !containsString(truncation.Reasons, reason) {
		truncation.Reasons = append(truncation.Reasons, reason)
	}
}

func finalizeExplanationTruncation(truncation *ExplanationTruncation) {
	rank := map[string]int{TruncationGraphDepth: 0, TruncationMaxNodes: 1, TruncationMaxEdges: 2}
	sort.Slice(truncation.Reasons, func(i, j int) bool { return rank[truncation.Reasons[i]] < rank[truncation.Reasons[j]] })
	truncation.Truncated = len(truncation.Reasons) > 0
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
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
	sort.SliceStable(profiles, func(i, j int) bool {
		if profiles[i].Profile != profiles[j].Profile {
			return profiles[i].Profile < profiles[j].Profile
		}
		return profiles[i].Harness < profiles[j].Harness
	})
	return profiles
}
