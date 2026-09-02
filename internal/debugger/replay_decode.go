package debugger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

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
	cfg              state.ReducerConfig
	recordedDecision protocol.ReducerDecision
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
	d.recordedDecision = protocol.ReducerDecision(firstNonEmptyReplay(value("replay.recorded_decision", ""), record.outcome))
	d.at, _ = strconv.ParseInt(evidence["replay.emitted_at"], 10, 64)
	if d.at == 0 {
		d.at = record.observedAt.Unix()
	}
	d.originWindowID = parseInt(evidence["replay.origin_window_id"])
	d.prompt = parseBool(evidence["replay.is_user_submission"])
	d.pipeMode = parseBool(evidence["replay.pipe_mode"])
	d.endsSession = parseBool(evidence["replay.ends_session"])
	d.subagentDelta = parseInt(evidence["replay.subagent_delta"])
	d.cfg = state.ReducerConfig{
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
	d.requestReplay = d.recordedReason == protocol.DecisionReasonRequestReplayed || record.reason == protocol.DecisionReasonRequestReplayed
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
	case "agentsessionloopstarted", "agentsessionloopturnfinished",
		"agentsessionloopverificationfinished", "agentsessionloopwaitended":
		decision.phase = state.ASLPhaseBetween
	case "agentsessionloopwaitstarted":
		decision.phase = state.ASLPhaseWaiting
	case "agentsessionloopturnstarted", "agentsessionloopturnheartbeat",
		"agentsessionloopturnhangsuspected", "agentsessionloopturnhangcleared":
		decision.phase = state.ASLPhaseTurn
	case "agentsessionloopverificationstarted", "agentsessionloopverificationheartbeat":
		decision.phase = state.ASLPhaseVerification
	case "agentsessionlooppaused":
		decision.phase = state.ASLPhasePaused
	case "agentsessionloopfinished":
		decision.phase = state.ASLPhaseFinished
	case "sessionend":
		decision.endsSession = true
	}
	if decision.sourceKind == string(protocol.SourceLegacy) && strings.Contains(strings.ToLower(decision.event), "subagent") {
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

func replayRecordFromSessionEvent(windowID int, event protocol.SessionEvent, schemaVersion int) replayRecord {
	legacyV8 := schemaVersion == protocol.EventsExportSchemaV8
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
		schemaVersion: schemaVersion, traceID: event.TraceID, requestID: event.RequestID,
		sourceEventID: event.SourceEventID, harness: event.ProducerHarness, profile: event.Profile,
		sessionID: event.SessionID, windowID: strconv.Itoa(windowID), runID: event.RunID,
		stage: "daemon.decision", outcome: string(event.ReducerDecision), reason: event.DecisionReason,
		observedAt: observedAt, evidence: evidence, legacyV8: legacyV8,
	}
}

func encodeSessionEventState(out map[string]string, prefix string, state *protocol.EffectiveStateEvidence) {
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

func stableReplayReason(trace state.ReducerTrace) string {
	if trace.Reason == "" {
		return string(trace.Decision)
	}
	switch trace.Reason {
	case "duplicate sequence":
		return protocol.DecisionReasonDuplicateSequence
	case "older sequence arrived after a newer one":
		return protocol.DecisionReasonReorderedSequence
	case "idle preserves attention/terminal icon":
		return protocol.DecisionReasonIdlePreservesTerminal
	case "same identified turn after terminal", "stopped subagent never restarts", "same-source same-tool terminal suppression", "same-source same-turn terminal suppression":
		return protocol.DecisionReasonTerminalTurnSuppression
	case "legacy terminal grace window", "terminal trailing-event grace":
		return protocol.DecisionReasonTerminalGraceSuppression
	case "user acknowledged visible facts; ASL leases preserved":
		return protocol.DecisionReasonUserAcknowledged
	case "session end cleared its remaining subagent facts":
		return protocol.DecisionReasonSessionAgentsCleared
	default:
		return strings.TrimSpace(trace.Reason)
	}
}

func stateFromEffective(effective state.EffectiveState) ReplayState {
	return ReplayState{Icon: effective.Icon, Looping: effective.Looping, SourceKind: string(effective.Source.Kind), SourceKey: effective.Source.Key}
}

func equalReplayState(left, right ReplayState) bool {
	return left.Icon == right.Icon && left.Looping == right.Looping && left.SourceKind == right.SourceKind && left.SourceKey == right.SourceKey
}
