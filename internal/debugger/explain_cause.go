package debugger

import (
	"sort"
	"strconv"
	"strings"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func explainCause(records []diagnostics.TraceEnvelope, nodeIDs []string, nodes []EvidenceNode, selector Selector) ExplanationCause {
	for i, record := range records {
		if classification, summary, ok := classifyFailureRecord(record); ok {
			return ExplanationCause{
				Classification: classification, Certainty: CertaintyProven, Summary: summary,
				FirstFailedStage: record.Stage, EvidenceIDs: []string{nodeIDs[i]},
			}
		}
	}
	for i, record := range records {
		if summary, ok := processLifecycleRecoverySummary(record); ok {
			return ExplanationCause{
				Classification: CauseProcessLifecycleRecovery, Certainty: CertaintyProven,
				Summary: summary, EvidenceIDs: []string{nodeIDs[i]},
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

func processLifecycleRecoverySummary(record diagnostics.TraceEnvelope) (string, bool) {
	if record.Stage != "daemon.process_lifecycle" || record.Outcome != "settled" {
		return "", false
	}
	switch record.Reason {
	case daemon.HarnessProcessExitReasonPipe, daemon.HarnessProcessExitReasonInteractive, daemon.HarnessProcessExitReasonReset:
	default:
		return "", false
	}
	if record.Evidence["terminal_hook_evidence"] != "absent" {
		return "", false
	}
	return "No terminal hook was observed; the daemon retired the bolt after the exact harness process exited.", true
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
	case strings.HasPrefix(reason, protocol.DecisionReasonInvalidPayload) || normalization == protocol.NormalizationOutcomeQuarantined && strings.Contains(reason, "invalid"):
		return CauseInvalidPayload, "The recorded payload failed validation and was not admitted as trusted input.", true
	case saturation != "" || strings.Contains(reason, "saturat") || strings.Contains(stage, "saturat") || evidencePositive(record.Evidence["queue_full"]):
		return CauseSaturation, "A bounded delivery component recorded saturation for this request.", true
	case reason == protocol.DecisionReasonRequestReplayed || reason == protocol.DecisionReasonDuplicateSequence || reason == protocol.DecisionReasonReorderedSequence || outcome == "duplicate" || outcome == "reordered":
		return CauseDuplicate, "The request or source sequence was recorded as a duplicate or replay.", true
	case reason == protocol.DecisionReasonOwnerMismatch:
		return CauseOwnerMismatch, "Routing rejected the event because the producer did not match the managed title owner.", true
	case outcome == "quarantined" || strings.Contains(normalization, "quarantined") || reason == protocol.DecisionReasonUntrustedIdentityFallback:
		return CauseQuarantine, "The daemon quarantined the event or used an explicitly untrusted compatibility identity.", true
	case strings.HasPrefix(stage, "daemon.renderer.") && (strings.HasSuffix(stage, ".failed") || outcome == "failed" || outcome == "error"):
		return CauseRenderFailure, "The renderer recorded a terminal failure for the causal generation.", true
	case notificationFailure(stage, outcome, reason):
		return CauseNotificationFailure, "The notification path recorded a dispatch, backend, or click-focus failure.", true
	case strings.HasPrefix(stage, "daemon.notification.") && (outcome == "suppressed" || outcome == "cancelled"):
		return CauseNotificationSuppression, "The notification path recorded an intentional suppression, cancellation, dismissal, or lack of interaction.", true
	case stage == "daemon.decision" && (outcome == "suppressed" || outcome == "bypassed"):
		return CauseReducerSuppression, "The daemon recorded a reducer suppression or intentional reducer bypass.", true
	case strings.HasPrefix(stage, "hook.") && (outcome == "error" || outcome == "failed" || outcome == "rejected" || outcome == "retryable_failure" || outcome == "permanent_failure"):
		return CauseDeliveryFailure, "The hook client recorded a delivery failure before successful completion.", true
	default:
		return "", "", false
	}
}

func notificationFailure(stage, outcome, reason string) bool {
	if !strings.HasPrefix(stage, "daemon.notification.") || outcome != "failed" && outcome != "error" {
		return false
	}
	if reason == "daemon_shutdown" {
		return false
	}
	return strings.HasSuffix(stage, ".dispatch") || strings.HasSuffix(stage, ".backend") || strings.HasSuffix(stage, ".focus")
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
	case CauseInvalidPayload, CauseDuplicate, CauseQuarantine, CauseReducerSuppression, CauseRenderFailure, CauseNotificationFailure, CauseNotificationSuppression:
		if safeCommandToken(requestID) {
			add("amx debug tail --request " + requestID)
		}
	case CauseProcessLifecycleRecovery:
		if windowID > 0 {
			add("amx debug tail --stage daemon.process_lifecycle --window " + strconv.Itoa(windowID))
			add("amx daemon export-events --window " + strconv.Itoa(windowID) + " --last 100")
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
