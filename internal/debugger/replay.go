package debugger

// This file intentionally contains no daemon client or Kitty dependency.
// Replay reads immutable diagnostic artifacts and drives only daemon.Reducer,
// whose inputs and clock are explicit and side-effect free.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

const (
	ReplaySchemaVersion  = 1
	maxReplayInputBytes  = int64(128 << 20)
	maxReplayMemberBytes = int64(64 << 20)
	maxReplayMembers     = 1024
)

type ReplayOptions struct {
	Selector Selector
}

type ReplayInput struct {
	Path          string     `json:"path"`
	Format        string     `json:"format"`
	SchemaVersion int        `json:"schema_version"`
	BundleMode    BundleMode `json:"bundle_mode,omitempty"`
	LocalOnly     bool       `json:"local_only,omitempty"`
	Records       int        `json:"records"`
	CorruptLines  int        `json:"corrupt_lines,omitempty"`
	Truncated     bool       `json:"truncated,omitempty"`
}

type ReplayDiagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type ReplayState struct {
	Icon       string `json:"icon"`
	Looping    bool   `json:"looping"`
	SourceKind string `json:"source_kind,omitempty"`
	SourceKey  string `json:"source_key,omitempty"`
}

type ReplayComparison struct {
	Index              int                      `json:"index"`
	TraceID            string                   `json:"trace_id,omitempty"`
	RequestID          string                   `json:"request_id,omitempty"`
	WindowID           string                   `json:"window_id,omitempty"`
	Event              string                   `json:"event,omitempty"`
	Action             string                   `json:"action,omitempty"`
	SourceKind         string                   `json:"source_kind,omitempty"`
	SourceKey          string                   `json:"source_key,omitempty"`
	SourceSequence     uint64                   `json:"source_sequence,omitempty"`
	DaemonSequence     uint64                   `json:"daemon_sequence,omitempty"`
	RestartGeneration  int                      `json:"restart_generation,omitempty"`
	RecordedDecision   protocol.ReducerDecision `json:"recorded_decision,omitempty"`
	RecomputedDecision protocol.ReducerDecision `json:"recomputed_decision,omitempty"`
	RecordedReason     string                   `json:"recorded_reason,omitempty"`
	RecomputedReason   string                   `json:"recomputed_reason,omitempty"`
	RecordedBefore     *ReplayState             `json:"recorded_before,omitempty"`
	RecomputedBefore   ReplayState              `json:"recomputed_before"`
	RecordedAfter      *ReplayState             `json:"recorded_after,omitempty"`
	RecomputedAfter    ReplayState              `json:"recomputed_after"`
	DecisionMatch      *bool                    `json:"decision_match,omitempty"`
	ReasonMatch        *bool                    `json:"reason_match,omitempty"`
	StateMatch         *bool                    `json:"state_match,omitempty"`
	UsedFallback       bool                     `json:"used_fallback_identity,omitempty"`
	Status             string                   `json:"status"`
	Note               string                   `json:"note,omitempty"`
}

type ReplayRouteComparison struct {
	TraceID          string `json:"trace_id,omitempty"`
	WindowID         string `json:"window_id,omitempty"`
	RecordedRoute    string `json:"recorded_route,omitempty"`
	RecomputedRoute  string `json:"recomputed_route,omitempty"`
	SourceWindowID   string `json:"source_window_id,omitempty"`
	RecordedTargetID string `json:"recorded_target_window_id,omitempty"`
	RecomputedTarget string `json:"recomputed_target_window_id,omitempty"`
	Match            *bool  `json:"match,omitempty"`
	Status           string `json:"status"`
	Note             string `json:"note,omitempty"`
}

type ReplayRenderComparison struct {
	WindowID             string   `json:"window_id,omitempty"`
	Generation           uint64   `json:"generation"`
	SourceGenerations    []uint64 `json:"source_generations,omitempty"`
	RecordedTraceIDs     []string `json:"recorded_trace_ids,omitempty"`
	RecomputedTraceIDs   []string `json:"recomputed_trace_ids,omitempty"`
	RecordedRequestIDs   []string `json:"recorded_request_ids,omitempty"`
	RecomputedRequestIDs []string `json:"recomputed_request_ids,omitempty"`
	RecordedSourceEvents []string `json:"recorded_source_event_ids,omitempty"`
	RecomputedSourceIDs  []string `json:"recomputed_source_event_ids,omitempty"`
	TerminalStage        string   `json:"terminal_stage,omitempty"`
	Match                *bool    `json:"match,omitempty"`
	Status               string   `json:"status"`
	Note                 string   `json:"note,omitempty"`
}

type ReplaySummary struct {
	DecisionComparisons int `json:"decision_comparisons"`
	RouteComparisons    int `json:"route_comparisons"`
	RenderGroups        int `json:"render_groups"`
	Matched             int `json:"matched"`
	Mismatched          int `json:"mismatched"`
	Incomplete          int `json:"incomplete"`
	DaemonRestarts      int `json:"daemon_restarts"`
}

type ReplayReport struct {
	SchemaVersion int                      `json:"schema_version"`
	Input         ReplayInput              `json:"input"`
	Selector      Selector                 `json:"selector"`
	Success       bool                     `json:"success"`
	Complete      bool                     `json:"complete"`
	Summary       ReplaySummary            `json:"summary"`
	Comparisons   []ReplayComparison       `json:"comparisons"`
	Routes        []ReplayRouteComparison  `json:"routes,omitempty"`
	Renders       []ReplayRenderComparison `json:"renders,omitempty"`
	Diagnostics   []ReplayDiagnostic       `json:"diagnostics,omitempty"`
}

// Replay opens one immutable journal, bundle, or export artifact and compares
// its recorded causal outcomes with a fresh pure reducer. It never discovers a
// daemon, opens a socket, reconciles a journal, or creates a Kitty client.
func Replay(ctx context.Context, inputPath string, options ReplayOptions) (ReplayReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := options.Selector.Normalize(); err != nil {
		return ReplayReport{}, err
	}
	source, err := readReplaySource(ctx, inputPath)
	if err != nil {
		return ReplayReport{}, err
	}
	report := ReplayReport{
		SchemaVersion: ReplaySchemaVersion,
		Input:         source.input,
		Selector:      options.Selector,
		Comparisons:   make([]ReplayComparison, 0),
		Diagnostics:   append([]ReplayDiagnostic(nil), source.diagnostics...),
	}
	filtered := make([]replayRecord, 0, len(source.records))
	for _, record := range source.records {
		if err := ctx.Err(); err != nil {
			return ReplayReport{}, err
		}
		if replayRecordMatches(record, options.Selector) {
			filtered = append(filtered, record)
		}
	}
	if len(filtered) == 0 {
		report.Diagnostics = append(report.Diagnostics, ReplayDiagnostic{
			Severity: "warning", Code: "no_matching_evidence",
			Message: "the input contains no diagnostic records matching the replay selector",
		})
		report.Summary.Incomplete++
		report.Success = true
		report.Complete = false
		return report, nil
	}

	decisions := make([]replayDecision, 0)
	for _, record := range filtered {
		if record.stage != "daemon.decision" {
			continue
		}
		decisions = append(decisions, decodeReplayDecision(record))
	}
	replayDecisions(&report, decisions)
	replayRoutes(&report, decisions)
	replayRenderGroups(&report, filtered, decisions)
	missingRoutes := 0
	for _, decision := range decisions {
		// IPC request replays are audit rows emitted after dedupe and never run
		// routing discovery again. Their copied route fields are correlation
		// context, not a second route decision to recompute.
		if decision.requestReplay {
			continue
		}
		if !decision.route.present {
			missingRoutes++
		}
	}
	if missingRoutes > 0 {
		report.Diagnostics = append(report.Diagnostics, ReplayDiagnostic{
			Severity: "warning", Code: "routing_evidence_incomplete",
			Message: fmt.Sprintf("%d decision record(s) lack the captured routing projection", missingRoutes),
		})
		report.Summary.Incomplete += missingRoutes
	}
	if len(decisions) == 0 {
		report.Diagnostics = append(report.Diagnostics, ReplayDiagnostic{
			Severity: "warning", Code: "decision_evidence_unavailable",
			Message: "no daemon.decision records were available; reducer outcomes cannot be recomputed",
		})
		report.Summary.Incomplete++
	}
	if report.Input.CorruptLines > 0 {
		report.Summary.Incomplete++
	}
	if report.Input.Truncated {
		report.Summary.Incomplete++
	}
	report.Success = report.Summary.Mismatched == 0
	report.Complete = report.Success && report.Summary.Incomplete == 0
	return report, nil
}

func replayDecisions(report *ReplayReport, decisions []replayDecision) {
	reducers := make(map[string]*daemon.Reducer)
	effectiveByWindow := make(map[string]ReplayState)
	lastDaemonSeq := make(map[string]uint64)
	restartGeneration := make(map[string]int)
	tainted := make(map[string]bool)
	seenWindow := make(map[string]bool)
	for index, decision := range decisions {
		windowID := decision.record.windowID
		if decision.daemonSeq > 0 {
			if previous := lastDaemonSeq[windowID]; previous > 0 && decision.daemonSeq < previous {
				restartGeneration[windowID]++
				report.Summary.DaemonRestarts++
			}
			lastDaemonSeq[windowID] = decision.daemonSeq
		}
		reducer := reducers[windowID]
		if reducer == nil {
			reducer = daemon.NewReducer(decision.cfg)
			reducers[windowID] = reducer
		}
		reducer.SetConfig(decision.cfg)
		before := effectiveByWindow[windowID]
		comparison := ReplayComparison{
			Index: index + 1, TraceID: decision.record.traceID, RequestID: decision.record.requestID,
			WindowID: windowID, Event: decision.event, Action: decision.action,
			SourceKind: decision.sourceKind, SourceKey: decision.sourceKey, SourceSequence: decision.seq,
			DaemonSequence: decision.daemonSeq, RestartGeneration: restartGeneration[windowID],
			RecordedDecision: decision.recordedDecision, RecordedReason: decision.recordedReason,
			RecordedBefore: decision.recordedBefore, RecomputedBefore: before,
			RecordedAfter: decision.recordedAfter, UsedFallback: decision.usedFallback,
		}
		if decision.recordedBefore != nil && !equalReplayState(*decision.recordedBefore, before) && !seenWindow[windowID] {
			tainted[windowID] = true
		}
		seenWindow[windowID] = true

		recomputedDecision := protocol.ReducerDecision("")
		recomputedReason := ""
		switch {
		case decision.requestReplay:
			recomputedDecision = protocol.DecisionDuplicate
			recomputedReason = protocol.DecisionReasonRequestReplayed
		case decision.recordedDecision == protocol.DecisionBypassed:
			recomputedDecision = protocol.DecisionBypassed
			recomputedReason = decision.recordedReason
		case decision.recordedDecision == protocol.DecisionQuarantined && !decision.usedFallback && !decision.canApply:
			recomputedDecision = protocol.DecisionQuarantined
			recomputedReason = protocol.DecisionReasonInvalidPayload
		case decision.canApply:
			recomputedDecision = reducer.Apply(protocol.ReducerEvent{
				Source: protocol.SourceID{Kind: protocol.SourceKind(decision.sourceKind), Key: decision.sourceKey},
				Action: decision.action, Seq: decision.seq, TurnID: decision.turnID, At: decision.at,
				OriginWindowID: decision.originWindowID, Prompt: decision.prompt, ToolUse: decision.toolUseID,
				PipeMode: decision.pipeMode, RunID: decision.runID, Phase: decision.phase,
				SessionID: decision.sessionID, EndsSession: decision.endsSession, SubagentDelta: decision.subagentDelta,
			})
			if trace, ok := reducer.LatestTrace(); ok {
				recomputedReason = stableReplayReason(trace)
			}
			reducer.Compact(decision.at)
		}
		after := before
		if decision.canApply {
			after = stateFromEffective(reducer.Reduce(decision.at))
			effectiveByWindow[windowID] = after
		}
		comparison.RecomputedDecision = recomputedDecision
		comparison.RecomputedReason = recomputedReason
		comparison.RecomputedAfter = after

		complete := decision.exactInput && decision.recordedDecision != "" && decision.recordedAfter != nil
		if decision.record.legacyV8 {
			complete = false
			if decision.legacyResultIcon != "" && decision.legacyResultIcon != "preserve" {
				matched := after.Icon == decision.legacyResultIcon
				comparison.StateMatch = &matched
			}
		}
		if decision.recordedDecision != "" && recomputedDecision != "" {
			matched := decision.recordedDecision == recomputedDecision
			comparison.DecisionMatch = &matched
		}
		if decision.recordedReason != "" && recomputedReason != "" {
			matched := decision.recordedReason == recomputedReason
			comparison.ReasonMatch = &matched
		}
		if decision.recordedAfter != nil {
			matched := equalReplayState(*decision.recordedAfter, after)
			comparison.StateMatch = &matched
		}

		mismatch := false
		for _, matched := range []*bool{comparison.DecisionMatch, comparison.ReasonMatch, comparison.StateMatch} {
			mismatch = mismatch || matched != nil && !*matched
		}
		switch {
		case tainted[windowID]:
			comparison.Status = "incomplete"
			comparison.Note = "the available evidence begins after non-empty reducer state; the missing prefix prevents authoritative comparison"
			report.Summary.Incomplete++
		case mismatch:
			comparison.Status = "mismatch"
			report.Summary.Mismatched++
		case !complete:
			comparison.Status = "incomplete"
			comparison.Note = incompleteDecisionNote(decision)
			report.Summary.Incomplete++
		default:
			comparison.Status = "matched"
			report.Summary.Matched++
		}
		report.Comparisons = append(report.Comparisons, comparison)
	}
	report.Summary.DecisionComparisons = len(report.Comparisons)
}

func incompleteDecisionNote(decision replayDecision) string {
	if decision.record.legacyV8 {
		return "schema-v8 evidence can compare supported effective icons but does not contain schema-v9 reducer decisions or complete normalized inputs"
	}
	if !decision.canApply {
		return "the record does not contain a complete reducer admission input"
	}
	if decision.recordedAfter == nil {
		return "the record does not contain the recorded effective-after projection"
	}
	return "the record predates versioned offline replay inputs; inferred fields were used"
}

func replayRoutes(report *ReplayReport, decisions []replayDecision) {
	for _, decision := range decisions {
		if decision.requestReplay {
			continue
		}
		route := decision.route
		if !route.present {
			continue
		}
		comparison := ReplayRouteComparison{
			TraceID: decision.record.traceID, WindowID: decision.record.windowID,
			RecordedRoute: route.chosen, SourceWindowID: route.sourceWindowID,
			RecordedTargetID: route.targetWindowID,
		}
		recomputed, target, complete := recomputeRoute(route)
		comparison.RecomputedRoute = recomputed
		comparison.RecomputedTarget = target
		if !complete {
			comparison.Status = "incomplete"
			comparison.Note = "the captured routing projection lacks source/target ownership evidence"
			report.Summary.Incomplete++
		} else {
			matched := recomputed == route.chosen && target == route.targetWindowID
			comparison.Match = &matched
			if matched {
				comparison.Status = "matched"
				report.Summary.Matched++
			} else {
				comparison.Status = "mismatch"
				report.Summary.Mismatched++
			}
		}
		report.Routes = append(report.Routes, comparison)
	}
	report.Summary.RouteComparisons = len(report.Routes)
}

func recomputeRoute(route replayRoute) (string, string, bool) {
	if route.targetWindowID == "" || route.targetWindowID == "0" {
		return "rejected", route.targetWindowID, route.chosen == "rejected"
	}
	if route.ownerMatched {
		return "owner", route.targetWindowID, true
	}
	if route.sourceWindowID == "" || route.sourceWindowID == "0" || route.targetWindowID != route.sourceWindowID {
		return "", "", false
	}
	if route.selectionReason == "lookup_fallback" {
		return "source_fallback", route.sourceWindowID, true
	}
	return "source", route.sourceWindowID, true
}

type renderGroup struct {
	windowID          string
	generation        uint64
	sourceGenerations map[uint64]struct{}
	traceIDs          map[string]struct{}
	requestIDs        map[string]struct{}
	sourceEventIDs    map[string]struct{}
	terminalStage     string
}

func replayRenderGroups(report *ReplayReport, records []replayRecord, decisions []replayDecision) {
	groups := make(map[string]*renderGroup)
	for _, record := range records {
		if !strings.HasPrefix(record.stage, "daemon.renderer.") {
			continue
		}
		generation, _ := strconv.ParseUint(record.evidence["generation"], 10, 64)
		if generation == 0 {
			continue
		}
		key := record.windowID + "/" + strconv.FormatUint(generation, 10)
		group := groups[key]
		if group == nil {
			group = &renderGroup{
				windowID: record.windowID, generation: generation, sourceGenerations: make(map[uint64]struct{}),
				traceIDs: make(map[string]struct{}), requestIDs: make(map[string]struct{}), sourceEventIDs: make(map[string]struct{}),
			}
			groups[key] = group
		}
		for _, value := range parseUint64List(record.evidence["source_generations"]) {
			group.sourceGenerations[value] = struct{}{}
		}
		addCSVSet(group.traceIDs, record.evidence["trace_ids"])
		addCSVSet(group.requestIDs, record.evidence["request_ids"])
		addCSVSet(group.sourceEventIDs, record.evidence["source_event_ids"])
		group.terminalStage = laterRenderStage(group.terminalStage, strings.TrimPrefix(record.stage, "daemon.renderer."))
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		if len(group.sourceGenerations) == 0 {
			group.sourceGenerations[group.generation] = struct{}{}
		}
		recomputedTrace := make(map[string]struct{})
		recomputedRequest := make(map[string]struct{})
		recomputedSource := make(map[string]struct{})
		for _, decision := range decisions {
			if decision.record.windowID != group.windowID {
				continue
			}
			if decision.requestReplay {
				continue
			}
			if _, ok := group.sourceGenerations[decision.renderGeneration]; !ok {
				continue
			}
			addSet(recomputedTrace, decision.record.traceID)
			addSet(recomputedRequest, decision.record.requestID)
			addSet(recomputedSource, firstNonEmptyReplay(decision.record.sourceEventID, decision.record.evidence["source_event_id"]))
		}
		comparison := ReplayRenderComparison{
			WindowID: group.windowID, Generation: group.generation,
			SourceGenerations: sortedUint64Set(group.sourceGenerations), TerminalStage: group.terminalStage,
			RecordedTraceIDs: sortedStringSet(group.traceIDs), RecomputedTraceIDs: sortedStringSet(recomputedTrace),
			RecordedRequestIDs: sortedStringSet(group.requestIDs), RecomputedRequestIDs: sortedStringSet(recomputedRequest),
			RecordedSourceEvents: sortedStringSet(group.sourceEventIDs), RecomputedSourceIDs: sortedStringSet(recomputedSource),
		}
		if len(group.traceIDs) == 0 && len(group.requestIDs) == 0 && len(group.sourceEventIDs) == 0 {
			comparison.Status = "incomplete"
			comparison.Note = "renderer evidence does not retain a causal identifier set"
			report.Summary.Incomplete++
		} else if len(recomputedTrace) == 0 && len(recomputedRequest) == 0 && len(recomputedSource) == 0 {
			comparison.Status = "incomplete"
			comparison.Note = "matching reducer decisions are unavailable, possibly because the journal prefix was truncated"
			report.Summary.Incomplete++
		} else {
			matched := equalNonEmptySets(group.traceIDs, recomputedTrace) &&
				equalNonEmptySets(group.requestIDs, recomputedRequest) && equalNonEmptySets(group.sourceEventIDs, recomputedSource)
			comparison.Match = &matched
			if matched {
				comparison.Status = "matched"
				report.Summary.Matched++
			} else {
				comparison.Status = "mismatch"
				report.Summary.Mismatched++
			}
		}
		report.Renders = append(report.Renders, comparison)
	}
	report.Summary.RenderGroups = len(report.Renders)
}
