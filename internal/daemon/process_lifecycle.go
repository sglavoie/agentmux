package daemon

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// Process lifecycle reconciliation is an independent fail-safe for harnesses
// that exit without emitting Stop or SessionEnd. Candidate capture and commit
// happen under stateMu, but the process-table read is always off-lock. A window
// receipt plus exact fact fingerprints form the optimistic concurrency token:
// any newer hook, watchdog expiry, reset transition, restore merge, or probe
// mutation invalidates the whole observation before it can touch state.

const (
	harnessProcessLifecycleInterval         = time.Second
	processInspectionFailureJournalInterval = time.Minute
	processLifecycleJournalStage            = "daemon.process_lifecycle"
	processTerminalHookEvidenceAbsent       = "absent"
	processTerminalHookEvidenceObserved     = "observed"
	// Retain the package-local name used by existing focused tests while the
	// exported constant is the public schema contract.
	eventHarnessProcessExited = protocol.EventHarnessProcessExited
)

type processLifecycleFactReceipt struct {
	source         protocol.SourceID
	seq            uint64
	causalOrder    uint64
	pipeMode       bool
	originWindowID int
	sessionID      string
	terminated     bool
}

type processLifecycleCandidate struct {
	windowID int
	receipt  uint64
	identity protocol.HarnessProcessIdentity
	pipeMode bool
	facts    []processLifecycleFactReceipt
}

type processLifecycleDiagnostic struct {
	windowID             int
	identity             protocol.HarnessProcessIdentity
	observation          probes.HarnessProcessInspection
	mode                 string
	terminalHookEvidence string
	result               state.HarnessProcessExitResult
	sources              []protocol.ProcessLifecycleSourceEvidence
	renderGeneration     uint64
	sessionID            string
}

// SetHarnessProcessInspector replaces the production PID/lstart inspector.
// It is safe to call while runHarnessProcessLifecycle is active. A nil
// inspector disables lifecycle polling while retaining watchdog fallback
// behavior.
func (h *Handler) SetHarnessProcessInspector(inspector probes.HarnessProcessInspector) {
	h.processInspectorMu.Lock()
	defer h.processInspectorMu.Unlock()
	h.processInspector = inspector
}

func (h *Handler) harnessProcessInspector() probes.HarnessProcessInspector {
	h.processInspectorMu.RLock()
	defer h.processInspectorMu.RUnlock()
	return h.processInspector
}

func (h *Handler) runHarnessProcessLifecycle(ctx context.Context) {
	h.runHarnessProcessLifecycleWithInterval(ctx, harnessProcessLifecycleInterval)
}

func (h *Handler) runHarnessProcessLifecycleWithInterval(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = harnessProcessLifecycleInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.processLifecycleTick(ctx, now)
		}
	}
}

// processLifecycleTick performs one capture/inspect/revalidate pass. It
// returns the number of grouped settlements committed, primarily as a focused
// test seam. Missing and replaced-instance observations are both confirmed
// exits for the old fingerprint; alive and unknown observations are no-ops.
func (h *Handler) processLifecycleTick(ctx context.Context, observedAt time.Time) int {
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	inspector := h.harnessProcessInspector()
	if inspector == nil {
		return 0
	}
	candidates := h.collectProcessLifecycleCandidates()
	if len(candidates) == 0 {
		return 0
	}

	identities := deduplicatedLifecycleIdentities(candidates)
	inspections := inspector.InspectHarnessProcesses(ctx, identities)
	if ctx.Err() != nil {
		return 0
	}
	h.recordProcessInspectionFailures(observedAt, identities, inspections)

	committed := 0
	pendingCancellations := make([]notification.PendingCancellation, 0)
	diagnosticsToRecord := make([]processLifecycleDiagnostic, 0)
	bypassedHolds := make([]pendingStopHold, 0)
	// A verified process exit is genuinely terminal, so it settles the window
	// directly and never enters the confirmed-idle gate. Taking the commit mutex
	// before stateMu — the order applyCodexClearObservation established — is what
	// makes that bypass complete: a hold callback that already passed its claim is
	// parked here until this pass has withdrawn the window's facts, and then finds
	// its entry gone instead of re-applying a stop over the settled state.
	h.resetStopCommitMu.Lock()
	h.stateMu.Lock()
	for _, candidate := range candidates {
		status := inspections[candidate.identity]
		if status != probes.HarnessProcessMissing && status != probes.HarnessProcessReplacedInstance {
			continue
		}
		st, ok := h.windowStateLocked(candidate.windowID)
		if !ok || st.Reducer == nil || st.ShadowSeq != candidate.receipt {
			continue
		}
		facts := st.Reducer.Facts()
		if !processLifecycleCandidateCurrent(candidate, st.PendingReset, facts) {
			continue
		}

		before := st.Effective
		result := st.Reducer.SettleHarnessProcessExit(candidate.identity, observedAt.Unix(), state.HarnessProcessExitOptions{
			PipeMode:  candidate.pipeMode,
			ClearOnly: st.PendingReset != state.ResetFlowNone,
		})
		if !result.Changed {
			continue
		}
		// Only a settlement of this exact producer retires the hold: a pass that
		// changed nothing has not replaced the withheld stop with anything, and a
		// pass that settled a different process in the same window has not either.
		bypassedTerminalHold := false
		if dropped, held := h.cancelStopHoldForProcessExitLocked(candidate.windowID, candidate.identity, facts); held {
			bypassedHolds = append(bypassedHolds, dropped)
			bypassedTerminalHold = dropped.class == stopHoldClassTerminal
		}
		// The harness is gone, so the operator's forced ⚡ no longer describes
		// anything running in the pane.
		st.Reducer.RetireUserActive(observedAt.Unix())
		st.ShadowSeq++
		st.Reducer.Compact(observedAt.Unix())
		after := st.Reducer.Reduce(observedAt.Unix())
		h.publishEffectiveLocked(candidate.windowID, st, after)
		h.syncLegacyLifecycleWinnerLocked(candidate.windowID, st, after, observedAt)
		h.clearIdleSamplesForWindowLocked(candidate.windowID)

		if h.notifier != nil {
			pendingCancellations = append(pendingCancellations, h.notifier.CancelPendingDisposition(candidate.windowID))
		}

		action := processLifecycleActionForIcon(after.Icon)
		if action == "" {
			action = "idle"
		}
		visibleChanged := after.Icon != before.Icon || after.Looping != before.Looping
		renderGeneration := st.Seq + 1
		sources, primary, sessionID := processLifecycleSources(candidate, result, facts)
		terminalEvidence := processTerminalHookEvidenceAbsent
		if !result.SynthesizedStop {
			for _, receipt := range candidate.facts {
				if receipt.terminated {
					terminalEvidence = processTerminalHookEvidenceObserved
					break
				}
			}
		}
		if bypassedTerminalHold {
			// The confirmed-idle gate was withholding a native terminal hook that
			// genuinely arrived, so no fact carries it. The exit still synthesizes
			// the reducer stop, but calling the hook absent would contradict this
			// window's own held and bypassed rows.
			terminalEvidence = processTerminalHookEvidenceObserved
		}
		h.appendProcessLifecycleEventLocked(
			candidate.windowID, observedAt, action, status, candidate.identity,
			candidate.pipeMode, terminalEvidence, result, sources, primary, renderGeneration,
		)
		if visibleChanged {
			h.markWindowDirtyLocked(candidate.windowID)
		} else {
			h.markWindowActivityDirtyLocked(candidate.windowID)
		}
		h.syncRendererMetadataLocked(candidate.windowID)
		diagnosticsToRecord = append(diagnosticsToRecord, processLifecycleDiagnostic{
			windowID: candidate.windowID, identity: candidate.identity, observation: status,
			mode: processLifecycleMode(candidate.pipeMode), terminalHookEvidence: terminalEvidence,
			result: result, sources: sources, renderGeneration: renderGeneration, sessionID: sessionID,
		})
		committed++
	}
	h.stateMu.Unlock()
	h.resetStopCommitMu.Unlock()

	// Observer callbacks may perform bounded diagnostic work and can re-enter
	// handler state, and a retraction reaches the companion. Finish the
	// cancellations only after stateMu is free.
	for _, pending := range pendingCancellations {
		h.notifier.FinishCancellation(pending)
	}
	for _, dropped := range bypassedHolds {
		h.recordBypassedStopHold(dropped)
	}
	for _, record := range diagnosticsToRecord {
		h.recordProcessLifecycleSettlement(observedAt, record)
	}
	return committed
}

// collectProcessLifecycleCandidates snapshots only ordinary, unacknowledged
// process groups for which the reducer's exit policy can currently mutate at
// least one fact. Protected pipe-mode attention/error-only groups and already
// terminal interactive groups with no live agents are left on acknowledgement
// or watchdog policy and are not polled forever.
func (h *Handler) collectProcessLifecycleCandidates() []processLifecycleCandidate {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()

	candidates := make([]processLifecycleCandidate, 0)
	for windowID, st := range h.store {
		if st == nil || st.Reducer == nil {
			continue
		}
		allFacts := st.Reducer.Facts()
		groups := make(map[protocol.HarnessProcessIdentity][]protocol.SourceFactSnapshot)
		for _, fact := range allFacts {
			if fact.Acked || !ordinaryLifecycleSource(fact.Kind) {
				continue
			}
			identity, ok := protocol.CanonicalHarnessProcessIdentity(protocol.HarnessProcessIdentity{
				Harness: fact.HarnessProcessHarness, PID: fact.HarnessProcessPID,
				StartToken: fact.HarnessProcessStartToken,
			})
			if !ok {
				continue
			}
			groups[identity] = append(groups[identity], fact)
		}
		for identity, facts := range groups {
			pipeMode, consistent := state.LifecycleGroupPipeMode(facts)
			if !consistent || !lifecycleGroupCanChange(st.PendingReset, identity, pipeMode, facts, allFacts) {
				continue
			}
			factReceipts := make([]processLifecycleFactReceipt, 0, len(facts))
			for _, fact := range facts {
				factReceipts = append(factReceipts, processLifecycleFactReceipt{
					source: fact.Source(), seq: fact.Seq, causalOrder: fact.CausalOrder, pipeMode: fact.PipeMode,
					originWindowID: fact.OriginWindowID, sessionID: fact.SessionID, terminated: fact.Terminated,
				})
			}
			sort.Slice(factReceipts, func(i, j int) bool {
				return protocol.SourceIDLess(factReceipts[i].source, factReceipts[j].source)
			})
			candidates = append(candidates, processLifecycleCandidate{
				windowID: windowID, receipt: st.ShadowSeq, identity: identity,
				pipeMode: pipeMode, facts: factReceipts,
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.windowID != right.windowID {
			return left.windowID < right.windowID
		}
		if left.identity.Harness != right.identity.Harness {
			return left.identity.Harness < right.identity.Harness
		}
		if left.identity.PID != right.identity.PID {
			return left.identity.PID < right.identity.PID
		}
		return left.identity.StartToken < right.identity.StartToken
	})
	return candidates
}

// appendProcessLifecycleEventLocked publishes one schema-v10 process exit row
// and updates the durable intervention counters in the same critical section
// as reducer settlement. Callers hold stateMu.
func (h *Handler) appendProcessLifecycleEventLocked(
	windowID int,
	at time.Time,
	action string,
	observation probes.HarnessProcessInspection,
	identity protocol.HarnessProcessIdentity,
	pipeMode bool,
	terminalHookEvidence string,
	result state.HarnessProcessExitResult,
	sources []protocol.ProcessLifecycleSourceEvidence,
	primary protocol.ProcessLifecycleSourceEvidence,
	renderGeneration uint64,
) {
	before, after := events.EffectiveStateEvidence(result.Before), events.EffectiveStateEvidence(result.After)
	withdrawnFacts, clearedSubagents, synthesizedStop := result.WithdrawnFacts, result.ClearedSubagents, result.SynthesizedStop
	originWindowID := primary.OriginWindowID
	if originWindowID <= 0 {
		originWindowID = windowID
	}
	h.appendEventLocked(windowID, protocol.SessionEvent{
		At: at.Unix(), Event: protocol.EventHarnessProcessExited, Action: action,
		Source: identity.Harness, Reason: result.Reason, ResultIconState: result.After.Icon,
		SourceKind: primary.SourceKind, SourceKey: primary.SourceKey,
		OriginWindowID: originWindowID, TargetWindowID: windowID,
		ProducerHarness: identity.Harness, TargetHarness: identity.Harness,
		ReducerDecision: protocol.DecisionApplied, ReducerReason: result.Reason, DecisionReason: result.Reason,
		EffectiveBefore: &before, EffectiveAfter: &after, RenderGeneration: renderGeneration,
		ProcessHarness: identity.Harness, ProcessPID: identity.PID,
		ProcessStartToken: identity.StartToken, ProcessObservation: string(observation),
		ProcessMode: processLifecycleMode(pipeMode), ProcessSources: sources,
		WithdrawnFacts: &withdrawnFacts, ClearedSubagents: &clearedSubagents,
		SynthesizedStop: &synthesizedStop, TerminalHookEvidence: terminalHookEvidence,
	})
	meta := h.windowMetadata[windowID]
	meta.Watchdog.LastEvent = protocol.EventHarnessProcessExited
	meta.Watchdog.LastReason = result.Reason
	meta.Watchdog.LastAt = at.Unix()
	meta.Watchdog.ProcessExits++
	meta.Watchdog.LastProcessExitReason = result.Reason
	meta.Watchdog.LastProcessExitObservation = string(observation)
	meta.Watchdog.LastProcessExitAt = at.Unix()
	h.windowMetadata[windowID] = meta
}

func processLifecycleMode(pipeMode bool) string {
	if pipeMode {
		return "pipe"
	}
	return "interactive"
}

func processLifecycleSources(
	candidate processLifecycleCandidate,
	result state.HarnessProcessExitResult,
	facts []protocol.SourceFactSnapshot,
) ([]protocol.ProcessLifecycleSourceEvidence, protocol.ProcessLifecycleSourceEvidence, string) {
	factBySource := make(map[protocol.SourceID]protocol.SourceFactSnapshot, len(facts))
	for _, fact := range facts {
		factBySource[fact.Source()] = fact
	}
	affected := make(map[protocol.SourceID]bool)
	for _, source := range result.WithdrawnSources {
		affected[source] = true
	}
	for _, source := range result.ClearedSubagentSources {
		affected[source] = true
	}
	if result.SynthesizedStop {
		var newest *protocol.SourceFactSnapshot
		for _, receipt := range candidate.facts {
			fact, ok := factBySource[receipt.source]
			if !ok || fact.PipeMode || !lifecycleParentFact(fact) || fact.Acked {
				continue
			}
			if newest == nil || lifecycleFactMoreRecent(fact, *newest) {
				candidateFact := fact
				newest = &candidateFact
			}
		}
		if newest != nil {
			affected[newest.Source()] = true
		}
	}
	if len(affected) == 0 && len(candidate.facts) > 0 {
		affected[candidate.facts[0].source] = true
	}
	ids := make([]protocol.SourceID, 0, len(affected))
	for source := range affected {
		ids = append(ids, source)
	}
	protocol.SortSourceIDs(ids)
	sources := make([]protocol.ProcessLifecycleSourceEvidence, 0, len(ids))
	sessionID := ""
	for _, source := range ids {
		fact := factBySource[source]
		origin := fact.OriginWindowID
		if origin <= 0 {
			origin = candidate.windowID
		}
		if sessionID == "" {
			sessionID = fact.SessionID
		}
		sources = append(sources, protocol.ProcessLifecycleSourceEvidence{
			SourceKind: string(source.Kind), SourceKey: source.Key, OriginWindowID: origin,
		})
	}
	primary := protocol.ProcessLifecycleSourceEvidence{OriginWindowID: candidate.windowID}
	if len(sources) > 0 {
		primary = sources[0]
	}
	return sources, primary, sessionID
}

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

func lifecycleParentFact(fact protocol.SourceFactSnapshot) bool {
	if fact.Kind == protocol.SourceHarness {
		return true
	}
	return fact.Kind == protocol.SourceLegacy && !protocol.IsLegacySubagentBucket(fact.Key)
}

func lifecycleActiveSubagent(fact protocol.SourceFactSnapshot) bool {
	switch fact.Kind {
	case protocol.SourceSubagent:
		return fact.Icon == "subagent"
	case protocol.SourceLegacy:
		return protocol.IsLegacySubagentBucket(fact.Key) && (fact.Agents > 0 || fact.Icon == "subagent")
	default:
		return false
	}
}

func lifecycleFactMoreRecent(left, right protocol.SourceFactSnapshot) bool {
	if left.At != right.At {
		return left.At > right.At
	}
	if left.CausalOrder != right.CausalOrder {
		return left.CausalOrder > right.CausalOrder
	}
	if left.Source() == right.Source() {
		return left.Seq > right.Seq
	}
	return protocol.SourceIDLess(right.Source(), left.Source())
}

func deduplicatedLifecycleIdentities(candidates []processLifecycleCandidate) []protocol.HarnessProcessIdentity {
	seen := make(map[protocol.HarnessProcessIdentity]bool, len(candidates))
	identities := make([]protocol.HarnessProcessIdentity, 0, len(candidates))
	for _, candidate := range candidates {
		if seen[candidate.identity] {
			continue
		}
		seen[candidate.identity] = true
		identities = append(identities, candidate.identity)
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].Harness != identities[j].Harness {
			return identities[i].Harness < identities[j].Harness
		}
		if identities[i].PID != identities[j].PID {
			return identities[i].PID < identities[j].PID
		}
		return identities[i].StartToken < identities[j].StartToken
	})
	return identities
}

func processLifecycleCandidateCurrent(
	candidate processLifecycleCandidate,
	reset state.ResetFlowState,
	allFacts []protocol.SourceFactSnapshot,
) bool {
	current := make(map[protocol.SourceID]protocol.SourceFactSnapshot, len(allFacts))
	group := make([]protocol.SourceFactSnapshot, 0, len(candidate.facts))
	for _, fact := range allFacts {
		current[fact.Source()] = fact
		identity, ok := protocol.CanonicalHarnessProcessIdentity(protocol.HarnessProcessIdentity{
			Harness: fact.HarnessProcessHarness, PID: fact.HarnessProcessPID,
			StartToken: fact.HarnessProcessStartToken,
		})
		if ok && identity == candidate.identity && !fact.Acked && ordinaryLifecycleSource(fact.Kind) {
			group = append(group, fact)
		}
	}
	if len(group) != len(candidate.facts) {
		return false
	}
	for _, receipt := range candidate.facts {
		fact, ok := current[receipt.source]
		if !ok || fact.Acked || fact.Seq != receipt.seq || fact.CausalOrder != receipt.causalOrder || fact.PipeMode != receipt.pipeMode {
			return false
		}
		identity, valid := protocol.CanonicalHarnessProcessIdentity(protocol.HarnessProcessIdentity{
			Harness: fact.HarnessProcessHarness, PID: fact.HarnessProcessPID,
			StartToken: fact.HarnessProcessStartToken,
		})
		if !valid || identity != candidate.identity {
			return false
		}
	}
	pipeMode, consistent := state.LifecycleGroupPipeMode(group)
	return consistent && pipeMode == candidate.pipeMode &&
		lifecycleGroupCanChange(reset, candidate.identity, pipeMode, group, allFacts)
}

// syncLegacyLifecycleWinnerLocked projects the actual reduced winner back into
// compatibility bookkeeping. Durable profile/session/harness identity is left
// intact for post-mortem exports; only lifecycle-derived scalar fields move.
// Callers hold stateMu.
func (h *Handler) syncLegacyLifecycleWinnerLocked(
	windowID int,
	st *state.WindowState,
	effective state.EffectiveState,
	at time.Time,
) {
	facts := st.Reducer.Facts()
	action := processLifecycleActionForIcon(effective.Icon)
	winningPipeMode := false
	winningHarness := ""
	for _, fact := range facts {
		if fact.Source() == effective.Source {
			winningPipeMode = fact.PipeMode
			winningHarness = fact.HarnessProcessHarness
			break
		}
	}
	st.LastAction = action
	st.LastPipeMode = winningPipeMode
	// The scalar event name has no source identity. Once one process is removed,
	// it cannot be proven to describe the surviving winner even when both share
	// the same action and pipe mode, so clear it instead of attributing stale
	// child activity to the parent that was just revealed.
	st.LastActiveEvent = ""
	st.SubagentCount = lifecycleActiveSubagentCount(facts)
	if action == "permission" && winningHarness == "copilot" {
		st.CopilotToolPermissionState = state.CopilotToolPermissionPending
	} else {
		st.CopilotToolPermissionState = state.CopilotToolPermissionNone
	}
	st.Activity = at
	st.NotifiedStale = false
	st.ResetIdleAccounting()

	if h.guard != nil {
		h.guard.Clear(windowID)
		if action != "" {
			h.guard.TryUpdate(windowID, action, false)
		}
	}

	meta := h.windowMetadata[windowID]
	meta.Durable.PipeMode = winningPipeMode
	meta.Durable.ActivityAt = at.Unix()
	meta.Durable.LastAction = action
	meta.Durable.LastActiveEvent = st.LastActiveEvent
	meta.Durable.SubagentCount = st.SubagentCount
	h.windowMetadata[windowID] = meta
}

func lifecycleActiveSubagentCount(facts []protocol.SourceFactSnapshot) int {
	total := 0
	for _, fact := range facts {
		if fact.Acked || fact.Terminated || !lifecycleActiveSubagent(fact) {
			continue
		}
		if fact.Kind == protocol.SourceLegacy && fact.Agents > 0 {
			total += fact.Agents
		} else {
			total++
		}
	}
	return total
}

func processLifecycleActionForIcon(icon string) string {
	switch icon {
	case "bolt":
		return "active"
	case "compact":
		return "compact"
	case "subagent":
		return "subagent"
	case "stop":
		return "stop"
	case "question":
		return "elicitation"
	case "key":
		return "permission"
	case "warning":
		return "failure"
	case "error":
		return "stop_failure"
	default:
		return ""
	}
}
