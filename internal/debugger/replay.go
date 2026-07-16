package debugger

// This file intentionally contains no daemon client or Kitty dependency.
// Replay reads immutable diagnostic artifacts and drives only daemon.Reducer,
// whose inputs and clock are explicit and side-effect free.

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/diagnostics"
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
	Index              int                    `json:"index"`
	TraceID            string                 `json:"trace_id,omitempty"`
	RequestID          string                 `json:"request_id,omitempty"`
	WindowID           string                 `json:"window_id,omitempty"`
	Event              string                 `json:"event,omitempty"`
	Action             string                 `json:"action,omitempty"`
	SourceKind         string                 `json:"source_kind,omitempty"`
	SourceKey          string                 `json:"source_key,omitempty"`
	SourceSequence     uint64                 `json:"source_sequence,omitempty"`
	DaemonSequence     uint64                 `json:"daemon_sequence,omitempty"`
	RestartGeneration  int                    `json:"restart_generation,omitempty"`
	RecordedDecision   daemon.ReducerDecision `json:"recorded_decision,omitempty"`
	RecomputedDecision daemon.ReducerDecision `json:"recomputed_decision,omitempty"`
	RecordedReason     string                 `json:"recorded_reason,omitempty"`
	RecomputedReason   string                 `json:"recomputed_reason,omitempty"`
	RecordedBefore     *ReplayState           `json:"recorded_before,omitempty"`
	RecomputedBefore   ReplayState            `json:"recomputed_before"`
	RecordedAfter      *ReplayState           `json:"recorded_after,omitempty"`
	RecomputedAfter    ReplayState            `json:"recomputed_after"`
	DecisionMatch      *bool                  `json:"decision_match,omitempty"`
	ReasonMatch        *bool                  `json:"reason_match,omitempty"`
	StateMatch         *bool                  `json:"state_match,omitempty"`
	UsedFallback       bool                   `json:"used_fallback_identity,omitempty"`
	Status             string                 `json:"status"`
	Note               string                 `json:"note,omitempty"`
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

type replayRecord struct {
	schemaVersion int
	traceID       string
	requestID     string
	parentTraceID string
	sourceEventID string
	harness       string
	profile       string
	sessionID     string
	windowID      string
	runID         string
	stage         string
	outcome       string
	reason        string
	observedAt    time.Time
	evidence      map[string]string
	legacyV8      bool
}

type replaySource struct {
	input       ReplayInput
	records     []replayRecord
	diagnostics []ReplayDiagnostic
}

type replayDecision struct {
	record           replayRecord
	event            string
	action           string
	sourceKind       string
	sourceKey        string
	seq              uint64
	at               int64
	originWindowID   int
	prompt           bool
	pipeMode         bool
	turnID           string
	toolUseID        string
	runID            string
	phase            string
	sessionID        string
	endsSession      bool
	subagentDelta    int
	cfg              daemon.ReducerConfig
	recordedDecision daemon.ReducerDecision
	recordedReason   string
	recordedBefore   *ReplayState
	recordedAfter    *ReplayState
	usedFallback     bool
	normalization    string
	daemonSeq        uint64
	renderGeneration uint64
	exactInput       bool
	canApply         bool
	requestReplay    bool
	route            replayRoute
	legacyResultIcon string
}

type replayRoute struct {
	present         bool
	snapshotReady   bool
	lookupOutcome   string
	sourceWindowID  string
	targetWindowID  string
	sourceMatched   bool
	ownerMatched    bool
	selectionReason string
	chosen          string
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

		recomputedDecision := daemon.ReducerDecision("")
		recomputedReason := ""
		switch {
		case decision.requestReplay:
			recomputedDecision = daemon.DecisionDuplicate
			recomputedReason = daemon.DecisionReasonRequestReplayed
		case decision.recordedDecision == daemon.DecisionBypassed:
			recomputedDecision = daemon.DecisionBypassed
			recomputedReason = decision.recordedReason
		case decision.recordedDecision == daemon.DecisionQuarantined && !decision.usedFallback && !decision.canApply:
			recomputedDecision = daemon.DecisionQuarantined
			recomputedReason = daemon.DecisionReasonInvalidPayload
		case decision.canApply:
			recomputedDecision = reducer.Apply(daemon.ReducerEvent{
				Source: daemon.SourceID{Kind: daemon.SourceKind(decision.sourceKind), Key: decision.sourceKey},
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

func decodeReplayDecision(record replayRecord) replayDecision {
	evidence := record.evidence
	value := func(replayKey, legacyKey string) string {
		if candidate := evidence[replayKey]; candidate != "" {
			return candidate
		}
		return evidence[legacyKey]
	}
	d := replayDecision{
		record: record, event: value("replay.event", "event"),
		action:     firstNonEmptyReplay(evidence["replay.action"], evidence["reducer_action"], evidence["action"]),
		sourceKind: value("replay.source_kind", "source_kind"), sourceKey: value("replay.source_key", "source_key"),
		turnID: value("replay.turn_id", "turn_id"), toolUseID: value("replay.tool_use_id", "tool_use_id"),
		sessionID: firstNonEmptyReplay(value("replay.session_id", ""), record.sessionID),
		runID:     firstNonEmptyReplay(value("replay.run_id", ""), record.runID),
		phase:     evidence["replay.asl_phase"], recordedReason: value("replay.recorded_reason", "reducer_reason"),
		usedFallback:     parseBool(evidence["replay.used_fallback_identity"], evidence["used_fallback_identity"]),
		normalization:    value("replay.normalization_outcome", "normalization_outcome"),
		legacyResultIcon: evidence["result_icon_state"],
	}
	d.seq = parseUint64(value("replay.source_sequence", "source_sequence"))
	d.daemonSeq = parseUint64(evidence["daemon_sequence"])
	d.renderGeneration = parseUint64(value("replay.render_generation", "render_generation"))
	d.recordedDecision = daemon.ReducerDecision(firstNonEmptyReplay(value("replay.recorded_decision", ""), record.outcome))
	d.at, _ = strconv.ParseInt(evidence["replay.emitted_at"], 10, 64)
	if d.at == 0 {
		d.at = record.observedAt.Unix()
	}
	d.originWindowID = parseInt(evidence["replay.origin_window_id"])
	d.prompt = parseBool(evidence["replay.is_user_submission"])
	d.pipeMode = parseBool(evidence["replay.pipe_mode"])
	d.endsSession = parseBool(evidence["replay.ends_session"])
	d.subagentDelta = parseInt(evidence["replay.subagent_delta"])
	d.cfg = daemon.ReducerConfig{
		GuardDuration: parseInt64(evidence["replay.guard_duration"]),
		StaleActive:   parseInt64(evidence["replay.stale_active"]),
		StaleSubagent: parseInt64(evidence["replay.stale_subagent"]),
		StaleCompact:  parseInt64(evidence["replay.stale_compact"]),
	}
	d.recordedBefore = decodeReplayState(evidence, "before")
	d.recordedAfter = decodeReplayState(evidence, "after")
	d.exactInput = evidence["replay.version"] == "1" && d.sourceKind != "" && d.sourceKey != "" && d.seq > 0 && hasEvidenceKeys(evidence,
		"replay.emitted_at",
		"replay.is_user_submission", "replay.pipe_mode", "replay.ends_session", "replay.subagent_delta",
		"replay.guard_duration", "replay.stale_active", "replay.stale_subagent", "replay.stale_compact")
	if !d.exactInput {
		inferReplayInput(&d)
	}
	d.canApply = d.sourceKind != "" && d.sourceKey != "" && d.seq > 0 && (d.action != "" || d.phase != "")
	d.requestReplay = d.recordedReason == daemon.DecisionReasonRequestReplayed || record.reason == daemon.DecisionReasonRequestReplayed
	d.route = decodeReplayRoute(evidence)
	return d
}

func inferReplayInput(decision *replayDecision) {
	if decision == nil {
		return
	}
	switch strings.ToLower(decision.event) {
	case "userpromptsubmit", "userpromptsubmitted":
		decision.prompt = true
	case "agentsessionloopstarted", "agentsessionloopturnfinished", "agentsessionloopverificationfinished":
		decision.phase = daemon.ASLPhaseBetween
	case "agentsessionloopturnstarted", "agentsessionloopturnheartbeat":
		decision.phase = daemon.ASLPhaseTurn
	case "agentsessionloopverificationstarted", "agentsessionloopverificationheartbeat":
		decision.phase = daemon.ASLPhaseVerification
	case "agentsessionlooppaused":
		decision.phase = daemon.ASLPhasePaused
	case "agentsessionloopfinished":
		decision.phase = daemon.ASLPhaseFinished
	case "sessionend":
		decision.endsSession = true
	}
	if decision.sourceKind == string(daemon.SourceLegacy) && strings.Contains(strings.ToLower(decision.event), "subagent") {
		if strings.Contains(strings.ToLower(decision.event), "stop") {
			decision.subagentDelta = -1
		} else {
			decision.subagentDelta = 1
		}
	}
}

func decodeReplayState(evidence map[string]string, prefix string) *ReplayState {
	iconKey := "replay." + prefix + "_icon"
	loopKey := "replay." + prefix + "_looping"
	if _, ok := evidence[iconKey]; ok {
		return &ReplayState{
			Icon: evidence[iconKey], Looping: parseBool(evidence[loopKey]),
			SourceKind: evidence["replay."+prefix+"_source_kind"], SourceKey: evidence["replay."+prefix+"_source_key"],
		}
	}
	raw := evidence["effective_"+prefix]
	if raw == "" {
		return nil
	}
	var decoded struct {
		Icon       string `json:"icon"`
		Looping    bool   `json:"looping"`
		SourceKind string `json:"source_kind"`
		SourceKey  string `json:"source_key"`
	}
	if json.Unmarshal([]byte(raw), &decoded) != nil {
		return nil
	}
	return &ReplayState{Icon: decoded.Icon, Looping: decoded.Looping, SourceKind: decoded.SourceKind, SourceKey: decoded.SourceKey}
}

func decodeReplayRoute(evidence map[string]string) replayRoute {
	value := func(replayKey, legacyKey string) string {
		if candidate, ok := evidence[replayKey]; ok {
			return candidate
		}
		return evidence[legacyKey]
	}
	chosen, present := evidence["replay.route.chosen"]
	if !present {
		chosen, present = evidence["route.chosen"]
	}
	return replayRoute{
		present: present, snapshotReady: parseBool(value("replay.route.snapshot_ready", "route.snapshot_ready")),
		lookupOutcome:   value("replay.route.lookup_outcome", "route.lookup_outcome"),
		sourceWindowID:  value("replay.route.source_window_id", "route.source_window_id"),
		targetWindowID:  value("replay.route.target_window_id", "route.target_window_id"),
		sourceMatched:   parseBool(value("replay.route.source_matched", "route.source_matched")),
		ownerMatched:    parseBool(value("replay.route.owner_matched", "route.owner_matched")),
		selectionReason: value("replay.route.selection_reason", "route.selection_reason"), chosen: chosen,
	}
}

func readReplaySource(ctx context.Context, inputPath string) (replaySource, error) {
	inputPath = strings.TrimSpace(inputPath)
	if inputPath == "" {
		return replaySource{}, fmt.Errorf("debugger: replay input path is empty")
	}
	info, err := os.Lstat(inputPath)
	if err != nil {
		return replaySource{}, fmt.Errorf("debugger: opening replay input: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return replaySource{}, fmt.Errorf("debugger: replay input must not be a symbolic link")
	}
	if info.IsDir() {
		return readReplayJournalDirectory(ctx, inputPath)
	}
	if !info.Mode().IsRegular() {
		return replaySource{}, fmt.Errorf("debugger: replay rejects live endpoints and non-regular paths")
	}
	if info.Size() > maxReplayInputBytes {
		return replaySource{}, fmt.Errorf("debugger: replay input exceeds %d bytes", maxReplayInputBytes)
	}
	file, err := os.Open(inputPath)
	if err != nil {
		return replaySource{}, err
	}
	defer func() { _ = file.Close() }()
	var magic [2]byte
	n, _ := file.Read(magic[:])
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return replaySource{}, err
	}
	if n == 2 && magic == [2]byte{0x1f, 0x8b} {
		return readReplayBundle(ctx, inputPath, file)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxReplayInputBytes+1))
	if err != nil {
		return replaySource{}, err
	}
	if int64(len(data)) > maxReplayInputBytes {
		return replaySource{}, fmt.Errorf("debugger: replay input exceeds %d bytes", maxReplayInputBytes)
	}
	return decodeReplayFile(inputPath, data)
}

func readReplayJournalDirectory(ctx context.Context, directory string) (replaySource, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return replaySource{}, err
	}
	type journalFile struct {
		path    string
		name    string
		modTime time.Time
	}
	files := make([]journalFile, 0)
	var totalBytes int64
	for _, entry := range entries {
		name := entry.Name()
		isJournalSegment := name == "events.jsonl" || strings.HasPrefix(name, "events-") && strings.HasSuffix(name, ".jsonl")
		if !isJournalSegment {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return replaySource{}, fmt.Errorf("debugger: journal member %s is not a regular file", name)
		}
		info, err := entry.Info()
		if err != nil {
			return replaySource{}, err
		}
		if info.Size() < 0 || info.Size() > maxReplayInputBytes || totalBytes+info.Size() > maxReplayInputBytes {
			return replaySource{}, fmt.Errorf("debugger: journal directory exceeds the %d-byte replay limit", maxReplayInputBytes)
		}
		totalBytes += info.Size()
		files = append(files, journalFile{path: filepath.Join(directory, name), name: name, modTime: info.ModTime()})
	}
	if len(files) == 0 {
		return replaySource{}, fmt.Errorf("debugger: directory is not a diagnostic journal (no events*.jsonl files)")
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].name == "events.jsonl" {
			return false
		}
		if files[j].name == "events.jsonl" {
			return true
		}
		if files[i].modTime.Equal(files[j].modTime) {
			return files[i].name < files[j].name
		}
		return files[i].modTime.Before(files[j].modTime)
	})
	source := replaySource{input: ReplayInput{Path: directory, Format: "journal", SchemaVersion: diagnostics.SchemaVersion}}
	for _, item := range files {
		if err := ctx.Err(); err != nil {
			return replaySource{}, err
		}
		file, err := os.Open(item.path)
		if err != nil {
			return replaySource{}, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxReplayInputBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return replaySource{}, readErr
		}
		if closeErr != nil {
			return replaySource{}, closeErr
		}
		if int64(len(data)) > maxReplayInputBytes {
			return replaySource{}, fmt.Errorf("debugger: journal member %s exceeds the replay limit", item.name)
		}
		records, corrupt, err := decodeJournalJSONL(data)
		if err != nil {
			return replaySource{}, fmt.Errorf("debugger: %s: %w", item.name, err)
		}
		source.records = append(source.records, records...)
		source.input.CorruptLines += corrupt
	}
	source.input.Records = len(source.records)
	if source.input.CorruptLines > 0 {
		source.diagnostics = append(source.diagnostics, ReplayDiagnostic{Severity: "warning", Code: "corrupt_journal_lines", Message: fmt.Sprintf("%d corrupt journal line(s) were skipped without modifying the input", source.input.CorruptLines)})
	}
	return source, nil
}

func decodeReplayFile(inputPath string, data []byte) (replaySource, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return replaySource{}, fmt.Errorf("debugger: replay input is empty")
	}
	if json.Valid(trimmed) {
		var header map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &header); err != nil {
			return replaySource{}, err
		}
		if _, ok := header["windows"]; ok {
			return decodeEventsExport(inputPath, trimmed)
		}
		if _, ok := header["records"]; ok {
			records, schema, corrupt, err := decodeCausalRecords(trimmed)
			if err != nil {
				return replaySource{}, err
			}
			return replaySource{input: ReplayInput{Path: inputPath, Format: "causal-records", SchemaVersion: schema, Records: len(records), CorruptLines: corrupt}, records: records}, nil
		}
		record, err := decodeFlexibleTrace(trimmed)
		if err == nil {
			return replaySource{input: ReplayInput{Path: inputPath, Format: "journal", SchemaVersion: diagnostics.SchemaVersion, Records: 1}, records: []replayRecord{record}}, nil
		}
	}
	records, corrupt, err := decodeJournalJSONL(data)
	if err != nil {
		return replaySource{}, err
	}
	if len(records) == 0 {
		return replaySource{}, fmt.Errorf("debugger: input is neither a supported journal nor a diagnostic bundle")
	}
	source := replaySource{input: ReplayInput{Path: inputPath, Format: "journal", SchemaVersion: diagnostics.SchemaVersion, Records: len(records), CorruptLines: corrupt}, records: records}
	if corrupt > 0 {
		source.diagnostics = append(source.diagnostics, ReplayDiagnostic{Severity: "warning", Code: "corrupt_journal_lines", Message: fmt.Sprintf("%d corrupt journal line(s) were skipped without modifying the input", corrupt)})
	}
	return source, nil
}

func decodeJournalJSONL(data []byte) ([]replayRecord, int, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64<<10), int(diagnostics.MaxSegmentBytes)+1)
	records := make([]replayRecord, 0)
	corrupt := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		record, err := decodeFlexibleTrace(line)
		if err != nil {
			var header struct {
				SchemaVersion int `json:"schema_version"`
			}
			if json.Unmarshal(line, &header) == nil && header.SchemaVersion != 0 && header.SchemaVersion != diagnostics.SchemaVersion {
				return nil, corrupt, fmt.Errorf("unsupported journal trace schema %d", header.SchemaVersion)
			}
			corrupt++
			continue
		}
		records = append(records, record)
	}
	return records, corrupt, scanner.Err()
}

func decodeFlexibleTrace(data []byte) (replayRecord, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return replayRecord{}, err
	}
	schema := rawInt(object["schema_version"])
	if schema != diagnostics.SchemaVersion {
		return replayRecord{}, fmt.Errorf("unsupported journal trace schema %d", schema)
	}
	stage := rawString(object["stage"])
	if stage == "" {
		return replayRecord{}, fmt.Errorf("journal record has no stage")
	}
	var evidence map[string]string
	if raw := object["evidence"]; len(raw) != 0 {
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return replayRecord{}, fmt.Errorf("invalid journal evidence: %w", err)
		}
		evidence = make(map[string]string, len(values))
		for key, value := range values {
			var decoded string
			if err := json.Unmarshal(value, &decoded); err != nil {
				return replayRecord{}, fmt.Errorf("journal evidence %q is not a string", key)
			}
			evidence[key] = decoded
		}
	}
	observedValue := rawString(object["observed_at"])
	observedAt, err := time.Parse(time.RFC3339Nano, observedValue)
	if err != nil {
		return replayRecord{}, fmt.Errorf("journal record has invalid observed_at %q", observedValue)
	}
	return replayRecord{
		schemaVersion: schema, traceID: rawString(object["trace_id"]), requestID: rawString(object["request_id"]),
		parentTraceID: rawString(object["parent_trace_id"]), harness: rawString(object["harness"]),
		profile: rawString(object["profile"]), sessionID: rawString(object["session_id"]),
		windowID: rawString(object["window_id"]), runID: rawString(object["run_id"]),
		stage: stage, outcome: rawString(object["outcome"]), reason: rawString(object["reason"]),
		observedAt: observedAt.UTC(), evidence: evidence,
		sourceEventID: firstNonEmptyReplay(evidence["replay.source_event_id"], evidence["source_event_id"]),
	}, nil
}

func readReplayBundle(ctx context.Context, inputPath string, reader io.Reader) (replaySource, error) {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return replaySource{}, fmt.Errorf("debugger: invalid replay bundle gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tarReader := tar.NewReader(gz)
	members := make(map[string][]byte)
	var total int64
	memberCount := 0
	for {
		if err := ctx.Err(); err != nil {
			return replaySource{}, err
		}
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return replaySource{}, fmt.Errorf("debugger: invalid replay bundle tar: %w", err)
		}
		memberCount++
		if memberCount > maxReplayMembers {
			return replaySource{}, fmt.Errorf("debugger: replay bundle exceeds member-count limit of %d", maxReplayMembers)
		}
		clean := path.Clean(header.Name)
		if clean != header.Name || path.IsAbs(header.Name) || strings.Contains(clean, "/") || clean == "." || strings.HasPrefix(clean, "..") {
			return replaySource{}, fmt.Errorf("debugger: unsafe bundle member %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg {
			return replaySource{}, fmt.Errorf("debugger: bundle member %q is not a regular file", header.Name)
		}
		if header.Size < 0 || header.Size > maxReplayMemberBytes || total+header.Size > maxReplayInputBytes {
			return replaySource{}, fmt.Errorf("debugger: bundle member %q exceeds replay limits", header.Name)
		}
		if _, duplicate := members[header.Name]; duplicate {
			return replaySource{}, fmt.Errorf("debugger: duplicate bundle member %q", header.Name)
		}
		data, err := io.ReadAll(io.LimitReader(tarReader, maxReplayMemberBytes+1))
		if err != nil {
			return replaySource{}, err
		}
		if int64(len(data)) != header.Size {
			return replaySource{}, fmt.Errorf("debugger: truncated bundle member %q", header.Name)
		}
		total += int64(len(data))
		members[header.Name] = data
	}
	manifestData := members["manifest.json"]
	if len(manifestData) == 0 {
		return replaySource{}, fmt.Errorf("debugger: bundle is incomplete: manifest.json is missing")
	}
	var manifest BundleManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return replaySource{}, fmt.Errorf("debugger: invalid bundle manifest: %w", err)
	}
	if !supportedBundleSchema(manifest.SchemaVersion) {
		return replaySource{}, fmt.Errorf("debugger: unsupported bundle schema %d", manifest.SchemaVersion)
	}
	if manifest.Mode != BundleModeShare && manifest.Mode != BundleModeForensic {
		return replaySource{}, fmt.Errorf("debugger: unsupported bundle mode %q", manifest.Mode)
	}
	if manifest.LocalOnly != (manifest.Mode == BundleModeForensic) {
		return replaySource{}, fmt.Errorf("debugger: bundle mode and local_only policy disagree")
	}
	var causalEntry *BundleManifestEntry
	causalEntries := 0
	for index := range manifest.Entries {
		entry := &manifest.Entries[index]
		if entry.Member == "causal-records.json" {
			causalEntries++
			causalEntry = entry
		}
	}
	if causalEntries != 1 || causalEntry == nil || causalEntry.Decision != "included" {
		return replaySource{}, fmt.Errorf("debugger: bundle is incomplete: manifest does not include causal-records.json")
	}
	causalData := members["causal-records.json"]
	if len(causalData) == 0 {
		return replaySource{}, fmt.Errorf("debugger: bundle is incomplete: causal-records.json is missing")
	}
	if causalEntry.Bytes > 0 && causalEntry.Bytes != len(causalData) {
		return replaySource{}, fmt.Errorf("debugger: bundle causal-records.json size does not match the manifest")
	}
	records, schema, corrupt, err := decodeCausalRecords(causalData)
	if err != nil {
		return replaySource{}, err
	}
	if schema != manifest.SchemaVersion {
		return replaySource{}, fmt.Errorf("debugger: bundle schema %d does not match causal-records schema %d", manifest.SchemaVersion, schema)
	}
	source := replaySource{
		input: ReplayInput{Path: inputPath, Format: "bundle", SchemaVersion: schema, BundleMode: manifest.Mode,
			LocalOnly: manifest.LocalOnly, Records: len(records), CorruptLines: corrupt, Truncated: manifest.Truncated},
		records: records,
	}
	if manifest.LocalOnly {
		source.diagnostics = append(source.diagnostics, ReplayDiagnostic{Severity: "warning", Code: "forensic_local_only", Message: firstNonEmptyReplay(manifest.Warning, "forensic bundle is local-only and must not be shared")})
	}
	if manifest.Truncated {
		source.diagnostics = append(source.diagnostics, ReplayDiagnostic{Severity: "warning", Code: "bundle_truncated", Message: "the bundle manifest reports truncated evidence; missing prefixes are treated as incomplete"})
	}
	if corrupt > 0 {
		source.diagnostics = append(source.diagnostics, ReplayDiagnostic{Severity: "warning", Code: "corrupt_journal_lines", Message: fmt.Sprintf("the bundle reports %d corrupt journal line(s)", corrupt)})
	}
	return source, nil
}

func decodeCausalRecords(data []byte) ([]replayRecord, int, int, error) {
	var wrapper struct {
		SchemaVersion   int                             `json:"schema_version"`
		CorruptLines    int                             `json:"corrupt_lines"`
		JournalSnapshot *diagnostics.SnapshotStatistics `json:"journal_snapshot"`
		Records         []json.RawMessage               `json:"records"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, 0, 0, fmt.Errorf("debugger: invalid causal-records.json: %w", err)
	}
	if !supportedBundleSchema(wrapper.SchemaVersion) {
		return nil, wrapper.SchemaVersion, 0, fmt.Errorf("debugger: unsupported causal-records schema %d", wrapper.SchemaVersion)
	}
	corruptLines := wrapper.CorruptLines
	if wrapper.SchemaVersion == BundleSchemaVersion {
		if wrapper.JournalSnapshot == nil {
			return nil, wrapper.SchemaVersion, 0, fmt.Errorf("debugger: causal-records schema %d is missing journal_snapshot", wrapper.SchemaVersion)
		}
		stats := *wrapper.JournalSnapshot
		if stats.SegmentFilesRead < 0 || stats.SpoolFilesRead < 0 || stats.BytesScanned < 0 || stats.RecordsScanned < 0 ||
			stats.RecordsMatched < 0 || stats.DuplicatesOmitted < 0 || stats.CorruptLines < 0 {
			return nil, wrapper.SchemaVersion, 0, fmt.Errorf("debugger: causal-records journal_snapshot values must not be negative")
		}
		corruptLines = stats.CorruptLines
	}
	if corruptLines < 0 {
		return nil, wrapper.SchemaVersion, 0, fmt.Errorf("debugger: causal-records corrupt_lines must not be negative")
	}
	records := make([]replayRecord, 0, len(wrapper.Records))
	for index, raw := range wrapper.Records {
		record, err := decodeFlexibleTrace(raw)
		if err != nil {
			return nil, wrapper.SchemaVersion, corruptLines, fmt.Errorf("debugger: causal record %d: %w", index+1, err)
		}
		records = append(records, record)
	}
	return records, wrapper.SchemaVersion, corruptLines, nil
}

func supportedBundleSchema(schema int) bool {
	return schema == bundleSchemaVersionV1 || schema == bundleSchemaVersionV2
}

func decodeEventsExport(inputPath string, data []byte) (replaySource, error) {
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return replaySource{}, err
	}
	if header.SchemaVersion != daemon.EventsExportSchemaVersion && header.SchemaVersion != daemon.EventsExportSchemaV8 {
		return replaySource{}, fmt.Errorf("debugger: unsupported events export schema %d", header.SchemaVersion)
	}
	var payload struct {
		Windows []struct {
			WindowID int                   `json:"window_id"`
			Events   []daemon.SessionEvent `json:"events"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return replaySource{}, fmt.Errorf("debugger: invalid events export: %w", err)
	}
	records := make([]replayRecord, 0)
	for _, window := range payload.Windows {
		for _, event := range window.Events {
			records = append(records, replayRecordFromSessionEvent(window.WindowID, event, header.SchemaVersion == daemon.EventsExportSchemaV8))
		}
	}
	source := replaySource{input: ReplayInput{Path: inputPath, Format: "events-export", SchemaVersion: header.SchemaVersion, Records: len(records)}, records: records}
	if header.SchemaVersion == daemon.EventsExportSchemaV8 {
		source.diagnostics = append(source.diagnostics, ReplayDiagnostic{Severity: "warning", Code: "schema_v8_partial", Message: "schema-v8 exports lack reducer decisions and complete normalized inputs; replay compares only cases with sufficient source/action/icon evidence"})
	}
	return source, nil
}

func replayRecordFromSessionEvent(windowID int, event daemon.SessionEvent, legacyV8 bool) replayRecord {
	evidence := map[string]string{
		"replay.event": event.Event, "replay.action": event.ReducerInputAction,
		"replay.source_kind": event.SourceKind, "replay.source_key": event.SourceKey,
		"replay.source_sequence": strconv.FormatUint(event.SourceSeq, 10),
		"replay.turn_id":         event.TurnID, "replay.tool_use_id": event.ToolUseID,
		"replay.session_id": event.SessionID, "replay.run_id": event.RunID,
		"replay.recorded_decision": string(event.ReducerDecision), "replay.recorded_reason": event.ReducerReason,
		"replay.used_fallback_identity": strconv.FormatBool(event.UsedFallbackIdentity),
		"replay.normalization_outcome":  event.NormalizationOutcome,
		"replay.render_generation":      strconv.FormatUint(event.RenderGeneration, 10),
		"daemon_sequence":               strconv.FormatUint(event.DaemonSeq, 10),
		"source_event_id":               event.SourceEventID,
	}
	if event.ReducerInput != nil {
		input := event.ReducerInput
		evidence["replay.version"] = "1"
		evidence["replay.emitted_at"] = strconv.FormatInt(input.EmittedAt, 10)
		evidence["replay.origin_window_id"] = strconv.Itoa(input.OriginWindowID)
		evidence["replay.is_user_submission"] = strconv.FormatBool(input.Prompt)
		evidence["replay.pipe_mode"] = strconv.FormatBool(input.PipeMode)
		evidence["replay.asl_phase"] = input.ASLPhase
		evidence["replay.ends_session"] = strconv.FormatBool(input.EndsSession)
		evidence["replay.subagent_delta"] = strconv.Itoa(input.SubagentDelta)
		evidence["replay.guard_duration"] = strconv.FormatInt(input.GuardDuration, 10)
		evidence["replay.stale_active"] = strconv.FormatInt(input.StaleActive, 10)
		evidence["replay.stale_subagent"] = strconv.FormatInt(input.StaleSubagent, 10)
		evidence["replay.stale_compact"] = strconv.FormatInt(input.StaleCompact, 10)
	}
	encodeSessionEventState(evidence, "before", event.EffectiveBefore)
	encodeSessionEventState(evidence, "after", event.EffectiveAfter)
	if route := event.RouteEvidence; route != nil {
		evidence["replay.route.chosen"] = route.ChosenRoute
		evidence["replay.route.source_window_id"] = strconv.Itoa(route.SourceWindowID)
		evidence["replay.route.target_window_id"] = strconv.Itoa(route.TargetWindowID)
		evidence["replay.route.owner_matched"] = strconv.FormatBool(route.OwnerMatched)
		evidence["replay.route.source_matched"] = strconv.FormatBool(route.SourceMatched)
		evidence["replay.route.selection_reason"] = route.SelectionReason
	}
	action := event.ReducerInputAction
	if action == "" {
		action = event.Action
	}
	if legacyV8 {
		evidence["replay.action"] = action
		evidence["result_icon_state"] = event.ResultIconState
		evidence["current_icon_state"] = event.CurrentIconState
	}
	observedAt := time.Unix(event.At, 0).UTC()
	if event.ReceivedAtNs != 0 {
		observedAt = time.Unix(0, event.ReceivedAtNs).UTC()
	}
	return replayRecord{
		schemaVersion: headerSchema(legacyV8), traceID: event.TraceID, requestID: event.RequestID,
		sourceEventID: event.SourceEventID, harness: event.ProducerHarness, profile: event.Profile,
		sessionID: event.SessionID, windowID: strconv.Itoa(windowID), runID: event.RunID,
		stage: "daemon.decision", outcome: string(event.ReducerDecision), reason: event.DecisionReason,
		observedAt: observedAt, evidence: evidence, legacyV8: legacyV8,
	}
}

func headerSchema(legacy bool) int {
	if legacy {
		return daemon.EventsExportSchemaV8
	}
	return daemon.EventsExportSchemaVersion
}

func encodeSessionEventState(out map[string]string, prefix string, state *daemon.EffectiveStateEvidence) {
	if state == nil {
		return
	}
	out["replay."+prefix+"_icon"] = state.Icon
	out["replay."+prefix+"_looping"] = strconv.FormatBool(state.Looping)
	out["replay."+prefix+"_source_kind"] = state.SourceKind
	out["replay."+prefix+"_source_key"] = state.SourceKey
}

func replayRecordMatches(record replayRecord, selector Selector) bool {
	if !selector.After.IsZero() && record.observedAt.Before(selector.After) || !selector.Before.IsZero() && record.observedAt.After(selector.Before) {
		return false
	}
	if selector.TraceID != "" && record.traceID != selector.TraceID || selector.RequestID != "" && record.requestID != selector.RequestID ||
		selector.Harness != "" && record.harness != selector.Harness || selector.Profile != "" && record.profile != selector.Profile ||
		selector.SessionID != "" && record.sessionID != selector.SessionID || selector.ASLRunID != "" && record.runID != selector.ASLRunID ||
		selector.WindowID != 0 && record.windowID != strconv.Itoa(selector.WindowID) {
		return false
	}
	event := firstNonEmptyReplay(record.evidence["replay.event"], record.evidence["event"])
	if selector.Event != "" && event != selector.Event {
		return false
	}
	if selector.SourceEventID != "" && record.sourceEventID != selector.SourceEventID && record.evidence["source_event_id"] != selector.SourceEventID {
		return false
	}
	return stringInOrEmpty(selector.Stages, record.stage) && stringInOrEmpty(selector.Outcomes, record.outcome)
}

func stableReplayReason(trace daemon.ReducerTrace) string {
	if trace.Reason == "" {
		return string(trace.Decision)
	}
	switch trace.Reason {
	case "duplicate sequence":
		return daemon.DecisionReasonDuplicateSequence
	case "older sequence arrived after a newer one":
		return daemon.DecisionReasonReorderedSequence
	case "idle preserves attention/terminal icon":
		return daemon.DecisionReasonIdlePreservesTerminal
	case "same identified turn after terminal", "stopped subagent never restarts", "same-source same-tool terminal suppression", "same-source same-turn terminal suppression":
		return daemon.DecisionReasonTerminalTurnSuppression
	case "legacy terminal grace window", "terminal trailing-event grace":
		return daemon.DecisionReasonTerminalGraceSuppression
	case "user acknowledged visible facts; ASL leases preserved":
		return daemon.DecisionReasonUserAcknowledged
	case "session end cleared its remaining subagent facts":
		return daemon.DecisionReasonSessionAgentsCleared
	default:
		return strings.TrimSpace(trace.Reason)
	}
}

func stateFromEffective(state daemon.EffectiveState) ReplayState {
	return ReplayState{Icon: state.Icon, Looping: state.Looping, SourceKind: string(state.Source.Kind), SourceKey: state.Source.Key}
}

func equalReplayState(left, right ReplayState) bool {
	return left.Icon == right.Icon && left.Looping == right.Looping && left.SourceKind == right.SourceKind && left.SourceKey == right.SourceKey
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func rawInt(raw json.RawMessage) int {
	value, _ := strconv.Atoi(rawString(raw))
	return value
}

func parseBool(values ...string) bool {
	for _, value := range values {
		if parsed, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
			return parsed
		}
	}
	return false
}

func parseInt(value string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(value))
	return parsed
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func parseUint64(value string) uint64 {
	parsed, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return parsed
}

func parseUint64List(value string) []uint64 {
	result := make([]uint64, 0)
	for _, part := range strings.Split(value, ",") {
		if parsed := parseUint64(part); parsed > 0 {
			result = append(result, parsed)
		}
	}
	return result
}

func hasEvidenceKeys(evidence map[string]string, keys ...string) bool {
	for _, key := range keys {
		if _, ok := evidence[key]; !ok {
			return false
		}
	}
	return true
}

func firstNonEmptyReplay(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func stringInOrEmpty(values []string, candidate string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func addCSVSet(set map[string]struct{}, values string) {
	for _, value := range strings.Split(values, ",") {
		addSet(set, strings.TrimSpace(value))
	}
}

func addSet(set map[string]struct{}, value string) {
	if value != "" {
		set[value] = struct{}{}
	}
}

func sortedStringSet(set map[string]struct{}) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func sortedUint64Set(set map[uint64]struct{}) []uint64 {
	values := make([]uint64, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}

func equalNonEmptySets(recorded, recomputed map[string]struct{}) bool {
	if len(recorded) == 0 {
		return true
	}
	if len(recorded) != len(recomputed) {
		return false
	}
	for value := range recorded {
		if _, ok := recomputed[value]; !ok {
			return false
		}
	}
	return true
}

func laterRenderStage(left, right string) string {
	rank := map[string]int{"queued": 1, "attempted": 2, "persisted": 3, "rendered": 4, "failed": 5}
	if rank[right] >= rank[left] {
		return right
	}
	return left
}
