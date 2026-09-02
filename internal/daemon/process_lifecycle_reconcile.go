package daemon

import (
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func lifecycleParentFact(fact protocol.SourceFactSnapshot) bool {
	if fact.Kind == protocol.SourceHarness {
		return true
	}
	return fact.Kind == protocol.SourceLegacy && !isLegacySubagentBucket(fact.Key)
}

func lifecycleActiveSubagent(fact protocol.SourceFactSnapshot) bool {
	switch fact.Kind {
	case protocol.SourceSubagent:
		return fact.Icon == "subagent"
	case protocol.SourceLegacy:
		return isLegacySubagentBucket(fact.Key) && (fact.Agents > 0 || fact.Icon == "subagent")
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
	return sourceIDLess(right.Source(), left.Source())
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
	reset resetFlowState,
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
