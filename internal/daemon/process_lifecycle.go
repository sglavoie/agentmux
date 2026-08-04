package daemon

// Process lifecycle reconciliation is an independent fail-safe for harnesses
// that exit without emitting Stop or SessionEnd. Candidate capture and commit
// happen under stateMu, but the process-table read is always off-lock. A window
// receipt plus exact fact fingerprints form the optimistic concurrency token:
// any newer hook, watchdog expiry, reset transition, restore merge, or probe
// mutation invalidates the whole observation before it can touch state.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

const (
	harnessProcessLifecycleInterval         = time.Second
	processInspectionFailureJournalInterval = time.Minute
	processLifecycleJournalStage            = "daemon.process_lifecycle"
	processTerminalHookEvidenceAbsent       = "absent"
	processTerminalHookEvidenceObserved     = "observed"
	// Retain the package-local name used by existing focused tests while the
	// exported constant is the public schema contract.
	eventHarnessProcessExited = EventHarnessProcessExited
)

type processLifecycleFactReceipt struct {
	source         SourceID
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
	identity HarnessProcessIdentity
	pipeMode bool
	facts    []processLifecycleFactReceipt
}

type pendingLifecycleNotificationDisposition struct {
	disposition NotificationDisposition
	cancelled   bool
}

type processLifecycleDiagnostic struct {
	windowID             int
	identity             HarnessProcessIdentity
	observation          HarnessProcessInspection
	mode                 string
	terminalHookEvidence string
	result               HarnessProcessExitResult
	sources              []ProcessLifecycleSourceEvidence
	renderGeneration     uint64
	sessionID            string
}

// SetHarnessProcessInspector replaces the production PID/lstart inspector.
// It is safe to call while runHarnessProcessLifecycle is active. A nil
// inspector disables lifecycle polling while retaining watchdog fallback
// behavior.
func (h *Handler) SetHarnessProcessInspector(inspector HarnessProcessInspector) {
	h.processInspectorMu.Lock()
	defer h.processInspectorMu.Unlock()
	h.processInspector = inspector
}

func (h *Handler) harnessProcessInspector() HarnessProcessInspector {
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
	pendingDispositions := make([]pendingLifecycleNotificationDisposition, 0)
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
		if status != HarnessProcessMissing && status != HarnessProcessReplacedInstance {
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
		result := st.Reducer.SettleHarnessProcessExit(candidate.identity, observedAt.Unix(), HarnessProcessExitOptions{
			PipeMode:  candidate.pipeMode,
			ClearOnly: st.PendingReset != resetFlowNone,
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
		st.ShadowSeq++
		st.Reducer.Compact(observedAt.Unix())
		after := st.Reducer.Reduce(observedAt.Unix())
		h.publishEffectiveLocked(candidate.windowID, st, after)
		h.syncLegacyLifecycleWinnerLocked(candidate.windowID, st, after, observedAt)
		h.clearIdleSamplesForWindowLocked(candidate.windowID)

		if h.notifier != nil {
			disposition, cancelled := h.notifier.cancelPending(candidate.windowID)
			pendingDispositions = append(pendingDispositions, pendingLifecycleNotificationDisposition{
				disposition: disposition,
				cancelled:   cancelled,
			})
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
	// handler state. Emit cancellation dispositions only after stateMu is free.
	for _, pending := range pendingDispositions {
		if pending.cancelled {
			h.notifier.emitDisposition(pending.disposition)
		}
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
		groups := make(map[HarnessProcessIdentity][]SourceFactSnapshot)
		for _, fact := range allFacts {
			if fact.Acked || !ordinaryLifecycleSource(fact.Kind) {
				continue
			}
			identity, ok := canonicalHarnessProcessIdentity(HarnessProcessIdentity{
				Harness: fact.HarnessProcessHarness, PID: fact.HarnessProcessPID,
				StartToken: fact.HarnessProcessStartToken,
			})
			if !ok {
				continue
			}
			groups[identity] = append(groups[identity], fact)
		}
		for identity, facts := range groups {
			pipeMode, consistent := lifecycleGroupPipeMode(facts)
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
				return sourceIDLess(factReceipts[i].source, factReceipts[j].source)
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
	observation HarnessProcessInspection,
	identity HarnessProcessIdentity,
	pipeMode bool,
	terminalHookEvidence string,
	result HarnessProcessExitResult,
	sources []ProcessLifecycleSourceEvidence,
	primary ProcessLifecycleSourceEvidence,
	renderGeneration uint64,
) {
	before, after := effectiveStateEvidence(result.Before), effectiveStateEvidence(result.After)
	withdrawnFacts, clearedSubagents, synthesizedStop := result.WithdrawnFacts, result.ClearedSubagents, result.SynthesizedStop
	originWindowID := primary.OriginWindowID
	if originWindowID <= 0 {
		originWindowID = windowID
	}
	h.appendEventLocked(windowID, SessionEvent{
		At: at.Unix(), Event: EventHarnessProcessExited, Action: action,
		Source: identity.Harness, Reason: result.Reason, ResultIconState: result.After.Icon,
		SourceKind: primary.SourceKind, SourceKey: primary.SourceKey,
		OriginWindowID: originWindowID, TargetWindowID: windowID,
		ProducerHarness: identity.Harness, TargetHarness: identity.Harness,
		ReducerDecision: DecisionApplied, ReducerReason: result.Reason, DecisionReason: result.Reason,
		EffectiveBefore: &before, EffectiveAfter: &after, RenderGeneration: renderGeneration,
		ProcessHarness: identity.Harness, ProcessPID: identity.PID,
		ProcessStartToken: identity.StartToken, ProcessObservation: string(observation),
		ProcessMode: processLifecycleMode(pipeMode), ProcessSources: sources,
		WithdrawnFacts: &withdrawnFacts, ClearedSubagents: &clearedSubagents,
		SynthesizedStop: &synthesizedStop, TerminalHookEvidence: terminalHookEvidence,
	})
	meta := h.windowMetadata[windowID]
	meta.Watchdog.LastEvent = EventHarnessProcessExited
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
	result HarnessProcessExitResult,
	facts []SourceFactSnapshot,
) ([]ProcessLifecycleSourceEvidence, ProcessLifecycleSourceEvidence, string) {
	factBySource := make(map[SourceID]SourceFactSnapshot, len(facts))
	for _, fact := range facts {
		factBySource[fact.Source()] = fact
	}
	affected := make(map[SourceID]bool)
	for _, source := range result.WithdrawnSources {
		affected[source] = true
	}
	for _, source := range result.ClearedSubagentSources {
		affected[source] = true
	}
	if result.SynthesizedStop {
		var newest *SourceFactSnapshot
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
	ids := make([]SourceID, 0, len(affected))
	for source := range affected {
		ids = append(ids, source)
	}
	sortSourceIDs(ids)
	sources := make([]ProcessLifecycleSourceEvidence, 0, len(ids))
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
		sources = append(sources, ProcessLifecycleSourceEvidence{
			SourceKind: string(source.Kind), SourceKey: source.Key, OriginWindowID: origin,
		})
	}
	primary := ProcessLifecycleSourceEvidence{OriginWindowID: candidate.windowID}
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
	identities []HarnessProcessIdentity,
	inspections map[HarnessProcessIdentity]HarnessProcessInspection,
) {
	if h == nil || h.decisionJournal == nil {
		return
	}
	unknown := 0
	for _, identity := range identities {
		if inspections[identity] == HarnessProcessUnknown || inspections[identity] == "" {
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
	primary := ProcessLifecycleSourceEvidence{}
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
		"event":                  EventHarnessProcessExited,
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

func ordinaryLifecycleSource(kind SourceKind) bool {
	switch kind {
	case SourceHarness, SourceLegacy, SourceSubagent:
		return true
	default:
		return false
	}
}

func lifecycleGroupPipeMode(facts []SourceFactSnapshot) (bool, bool) {
	if len(facts) == 0 {
		return false, false
	}
	pipeMode := facts[0].PipeMode
	for _, fact := range facts[1:] {
		if fact.PipeMode != pipeMode {
			// One OS process cannot change invocation mode. Treat an internally
			// inconsistent restored group as untrackable rather than guessing.
			return false, false
		}
	}
	return pipeMode, true
}

func lifecycleGroupCanChange(
	reset resetFlowState,
	identity HarnessProcessIdentity,
	pipeMode bool,
	group []SourceFactSnapshot,
	allFacts []SourceFactSnapshot,
) bool {
	if reset != resetFlowNone {
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

	var parent *SourceFactSnapshot
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
			if (fact.Kind == SourceSubagent && fact.SessionID == sessionID) ||
				(fact.Kind == SourceLegacy && fact.Key == legacySubagentBucketKey(sessionID)) {
				return true
			}
			continue
		}
		factIdentity, ok := canonicalHarnessProcessIdentity(HarnessProcessIdentity{
			Harness: fact.HarnessProcessHarness, PID: fact.HarnessProcessPID,
			StartToken: fact.HarnessProcessStartToken,
		})
		if ok && factIdentity == identity {
			return true
		}
	}
	return false
}

func lifecycleParentFact(fact SourceFactSnapshot) bool {
	if fact.Kind == SourceHarness {
		return true
	}
	return fact.Kind == SourceLegacy && !isLegacySubagentBucket(fact.Key)
}

func lifecycleActiveSubagent(fact SourceFactSnapshot) bool {
	switch fact.Kind {
	case SourceSubagent:
		return fact.Icon == "subagent"
	case SourceLegacy:
		return isLegacySubagentBucket(fact.Key) && (fact.Agents > 0 || fact.Icon == "subagent")
	default:
		return false
	}
}

func lifecycleFactMoreRecent(left, right SourceFactSnapshot) bool {
	if left.At != right.At {
		return left.At > right.At
	}
	if left.CausalOrder != right.CausalOrder {
		return left.CausalOrder > right.CausalOrder
	}
	if left.Source() == right.Source() {
		return left.Seq > right.Seq
	}
	return sourceIDLess(right.Source(), left.Source())
}

func deduplicatedLifecycleIdentities(candidates []processLifecycleCandidate) []HarnessProcessIdentity {
	seen := make(map[HarnessProcessIdentity]bool, len(candidates))
	identities := make([]HarnessProcessIdentity, 0, len(candidates))
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
	reset resetFlowState,
	allFacts []SourceFactSnapshot,
) bool {
	current := make(map[SourceID]SourceFactSnapshot, len(allFacts))
	group := make([]SourceFactSnapshot, 0, len(candidate.facts))
	for _, fact := range allFacts {
		current[fact.Source()] = fact
		identity, ok := canonicalHarnessProcessIdentity(HarnessProcessIdentity{
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
		identity, valid := canonicalHarnessProcessIdentity(HarnessProcessIdentity{
			Harness: fact.HarnessProcessHarness, PID: fact.HarnessProcessPID,
			StartToken: fact.HarnessProcessStartToken,
		})
		if !valid || identity != candidate.identity {
			return false
		}
	}
	pipeMode, consistent := lifecycleGroupPipeMode(group)
	return consistent && pipeMode == candidate.pipeMode &&
		lifecycleGroupCanChange(reset, candidate.identity, pipeMode, group, allFacts)
}

// syncLegacyLifecycleWinnerLocked projects the actual reduced winner back into
// compatibility bookkeeping. Durable profile/session/harness identity is left
// intact for post-mortem exports; only lifecycle-derived scalar fields move.
// Callers hold stateMu.
func (h *Handler) syncLegacyLifecycleWinnerLocked(
	windowID int,
	st *WindowState,
	effective EffectiveState,
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
		st.CopilotToolPermissionState = copilotToolPermissionPending
	} else {
		st.CopilotToolPermissionState = copilotToolPermissionNone
	}
	st.Activity = at
	st.NotifiedStale = false
	st.watchdogSeeded = false
	st.watchdogIdle = 0
	st.watchdogFactIdle = nil

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

func lifecycleActiveSubagentCount(facts []SourceFactSnapshot) int {
	total := 0
	for _, fact := range facts {
		if fact.Acked || fact.Terminated || !lifecycleActiveSubagent(fact) {
			continue
		}
		if fact.Kind == SourceLegacy && fact.Agents > 0 {
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
