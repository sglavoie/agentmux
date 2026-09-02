package daemon

import (
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

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
	observation HarnessProcessInspection,
	identity protocol.HarnessProcessIdentity,
	pipeMode bool,
	terminalHookEvidence string,
	result state.HarnessProcessExitResult,
	sources []protocol.ProcessLifecycleSourceEvidence,
	primary protocol.ProcessLifecycleSourceEvidence,
	renderGeneration uint64,
) {
	before, after := effectiveStateEvidence(result.Before), effectiveStateEvidence(result.After)
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
