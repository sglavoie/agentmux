package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// recordProcessInspectionFailures emits one bounded degraded record per minute
// while any candidate remains unknown. Successful alive/missing/replaced polls
// are intentionally silent.
func (h *Handler) recordProcessInspectionFailures(
	observedAt time.Time,
	identities []protocol.HarnessProcessIdentity,
	inspections map[protocol.HarnessProcessIdentity]probes.HarnessProcessInspection,
) {
	if h == nil || h.decisionJournal == nil {
		return
	}
	unknown := 0
	for _, identity := range identities {
		if inspections[identity] == probes.HarnessProcessUnknown || inspections[identity] == "" {
			unknown++
		}
	}
	if unknown == 0 {
		return
	}
	h.processDiagnosticsMu.Lock()
	if !h.lastProcessInspectionFailureAt.IsZero() {
		elapsed := observedAt.Sub(h.lastProcessInspectionFailureAt)
		if elapsed >= 0 && elapsed < processInspectionFailureJournalInterval {
			h.processDiagnosticsMu.Unlock()
			return
		}
	}
	h.lastProcessInspectionFailureAt = observedAt
	h.processDiagnosticsMu.Unlock()

	envelope := diagnostics.NewEnvelope(processLifecycleJournalStage)
	envelope.ObservedAt = observedAt
	envelope.Outcome = "degraded"
	envelope.Reason = "inspection_unknown"
	envelope.Evidence = map[string]string{
		"attempted_processes": fmt.Sprint(len(identities)),
		"unknown_processes":   fmt.Sprint(unknown),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	h.decisionJournal.TryAppend(ctx, envelope)
}

func (h *Handler) recordProcessLifecycleSettlement(at time.Time, record processLifecycleDiagnostic) {
	if h == nil || h.decisionJournal == nil {
		return
	}
	primary := protocol.ProcessLifecycleSourceEvidence{}
	if len(record.sources) > 0 {
		primary = record.sources[0]
	}
	envelope := diagnostics.NewEnvelope(processLifecycleJournalStage)
	envelope.Harness = record.identity.Harness
	envelope.WindowID = record.windowID
	envelope.SessionID = record.sessionID
	envelope.ObservedAt = at
	envelope.Outcome = "settled"
	envelope.Reason = record.result.Reason
	envelope.Evidence = map[string]string{
		"event":                  protocol.EventHarnessProcessExited,
		"process_harness":        record.identity.Harness,
		"process_pid":            fmt.Sprint(record.identity.PID),
		"process_start_token":    record.identity.StartToken,
		"process_observation":    string(record.observation),
		"process_mode":           record.mode,
		"source_kind":            primary.SourceKind,
		"source_key":             primary.SourceKey,
		"origin_window_id":       fmt.Sprint(primary.OriginWindowID),
		"before_icon":            record.result.Before.Icon,
		"before_looping":         fmt.Sprint(record.result.Before.Looping),
		"before_source_kind":     string(record.result.Before.Source.Kind),
		"before_source_key":      record.result.Before.Source.Key,
		"after_icon":             record.result.After.Icon,
		"after_looping":          fmt.Sprint(record.result.After.Looping),
		"after_source_kind":      string(record.result.After.Source.Kind),
		"after_source_key":       record.result.After.Source.Key,
		"withdrawn_facts":        fmt.Sprint(record.result.WithdrawnFacts),
		"cleared_subagents":      fmt.Sprint(record.result.ClearedSubagents),
		"synthesized_stop":       fmt.Sprint(record.result.SynthesizedStop),
		"terminal_hook_evidence": record.terminalHookEvidence,
		"render_generation":      fmt.Sprint(record.renderGeneration),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	h.decisionJournal.TryAppend(ctx, envelope)
}

func ordinaryLifecycleSource(kind protocol.SourceKind) bool {
	switch kind {
	case protocol.SourceHarness, protocol.SourceLegacy, protocol.SourceSubagent:
		return true
	default:
		return false
	}
}

func lifecycleGroupCanChange(
	reset state.ResetFlowState,
	identity protocol.HarnessProcessIdentity,
	pipeMode bool,
	group []protocol.SourceFactSnapshot,
	allFacts []protocol.SourceFactSnapshot,
) bool {
	if reset != state.ResetFlowNone {
		return len(group) > 0
	}
	if pipeMode {
		for _, fact := range group {
			switch fact.Icon {
			case "", "bolt", "compact", "subagent", "stop":
				return true
			}
		}
		return false
	}

	var parent *protocol.SourceFactSnapshot
	for i := range group {
		fact := &group[i]
		if fact.PipeMode || !lifecycleParentFact(*fact) {
			continue
		}
		if parent == nil || lifecycleFactMoreRecent(*fact, *parent) {
			parent = fact
		}
	}
	if parent != nil && !parent.Terminated {
		return true
	}
	// A terminal newest parent can still retire older active parents.
	if parent != nil {
		for _, fact := range group {
			if fact.Source() == parent.Source() || fact.PipeMode || !lifecycleParentFact(fact) {
				continue
			}
			if fact.Icon == "bolt" || fact.Icon == "compact" {
				return true
			}
		}
	}

	// Interactive settlement clears the selected parent's active agents even
	// when those agent facts could not carry a process identity themselves.
	sessionID := ""
	if parent != nil {
		sessionID = parent.SessionID
	}
	for _, fact := range allFacts {
		if fact.Acked || fact.Terminated || !lifecycleActiveSubagent(fact) {
			continue
		}
		if sessionID != "" {
			if (fact.Kind == protocol.SourceSubagent && fact.SessionID == sessionID) ||
				(fact.Kind == protocol.SourceLegacy && fact.Key == protocol.LegacySubagentBucketKey(sessionID)) {
				return true
			}
			continue
		}
		factIdentity, ok := protocol.CanonicalHarnessProcessIdentity(protocol.HarnessProcessIdentity{
			Harness: fact.HarnessProcessHarness, PID: fact.HarnessProcessPID,
			StartToken: fact.HarnessProcessStartToken,
		})
		if ok && factIdentity == identity {
			return true
		}
	}
	return false
}
