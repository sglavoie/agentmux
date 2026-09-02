package state

import (
	"sort"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// Stable process-exit reason codes. The reducer returns one of these for every
// settlement attempt so later daemon diagnostics do not have to infer policy
// from the mutated facts.
const (
	HarnessProcessExitReasonPipe        = "pipe_process_exited"
	HarnessProcessExitReasonInteractive = "interactive_process_exited"
	HarnessProcessExitReasonReset       = "reset_process_exited"
	HarnessProcessExitReasonInvalid     = "invalid_process_identity"
)

// HarnessProcessExitOptions selects the deterministic policy for one exact
// process fingerprint. PipeMode is the classification captured with the hook
// evidence. ClearOnly is used by reset flows and takes precedence over it.
type HarnessProcessExitOptions struct {
	PipeMode  bool
	ClearOnly bool
}

// LifecycleGroupPipeMode reports the invocation mode a group of facts agrees on,
// and whether the group is internally consistent at all. It is the pure
// projection that supplies HarnessProcessExitOptions.PipeMode, which is why it
// lives beside the settlement policy rather than with the process inspector that
// discovers the exits.
func LifecycleGroupPipeMode(facts []protocol.SourceFactSnapshot) (bool, bool) {
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

// HarnessProcessExitResult describes one atomic reducer settlement. SourceID
// is the reducer fact identity, so the source slices name every fact withdrawn
// or subagent contribution cleared. The slices are sorted by kind and key.
type HarnessProcessExitResult struct {
	Changed                bool
	SynthesizedStop        bool
	WithdrawnFacts         int
	WithdrawnSources       []protocol.SourceID
	ClearedSubagents       int
	ClearedSubagentSources []protocol.SourceID
	Reason                 string
	Before                 EffectiveState
	After                  EffectiveState
}

// reducerHarnessProcessIdentity returns the canonical process identity an
// accepted reducer event may retain. Transport normalization already validates
// production events; this second boundary keeps direct/replay callers from
// attaching malformed, producer-mismatched, or forbidden ownership.
func reducerHarnessProcessIdentity(ev protocol.ReducerEvent) protocol.HarnessProcessIdentity {
	return protocol.FactProcessIdentity(ev.Source, ev.HarnessProcess)
}

// SettleHarnessProcessExit folds external evidence that one exact harness
// process has exited into the reducer. It is pure with respect to the daemon:
// callers provide the observation timestamp, and the method performs no I/O,
// rendering, notification, or clock reads.
//
// Pipe exits withdraw only disposable contributions (active-class, iconless,
// and clean-stop facts), preserving attention, warning, and error outcomes for
// acknowledgement. Interactive exits synthesize one clean stop from the newest
// live parent fact, retire older active-class parents for the same process, and
// clear active subagents belonging to that parent session. Clear-only/reset
// exits withdraw every matched non-ASL fact and never synthesize a stop.
func (r *Reducer) SettleHarnessProcessExit(
	identity protocol.HarnessProcessIdentity,
	at int64,
	options HarnessProcessExitOptions,
) HarnessProcessExitResult {
	result := HarnessProcessExitResult{Reason: processExitReason(options)}
	if r == nil {
		return result
	}
	result.Before = r.Reduce(at)
	result.After = result.Before

	identity, ok := protocol.CanonicalHarnessProcessIdentity(identity)
	if !ok {
		result.Reason = HarnessProcessExitReasonInvalid
		return result
	}

	matched := r.processFacts(identity)
	switch {
	case options.ClearOnly:
		for _, f := range matched {
			r.withdrawProcessFact(f, at, &result)
		}
	case options.PipeMode:
		r.settlePipeProcessExit(matched, at, &result)
	default:
		r.settleInteractiveProcessExit(identity, matched, at, &result)
	}

	protocol.SortSourceIDs(result.WithdrawnSources)
	protocol.SortSourceIDs(result.ClearedSubagentSources)
	result.WithdrawnFacts = len(result.WithdrawnSources)
	result.ClearedSubagents = len(result.ClearedSubagentSources)
	result.Changed = result.SynthesizedStop || result.WithdrawnFacts > 0 || result.ClearedSubagents > 0
	result.After = r.Reduce(at)
	return result
}

func processExitReason(options HarnessProcessExitOptions) string {
	if options.ClearOnly {
		return HarnessProcessExitReasonReset
	}
	if options.PipeMode {
		return HarnessProcessExitReasonPipe
	}
	return HarnessProcessExitReasonInteractive
}

// processFacts returns exact ordinary-source fingerprint matches in
// deterministic source order. ASL and probe facts are excluded even if
// corrupted or test-injected state carries a matching fingerprint: their
// lifecycles are governed only by their own events.
func (r *Reducer) processFacts(identity protocol.HarnessProcessIdentity) []*sourceFact {
	matched := make([]*sourceFact, 0)
	for _, f := range r.facts {
		if !f.set || f.harnessProcess != identity {
			continue
		}
		switch f.source.Kind {
		case protocol.SourceHarness, protocol.SourceLegacy, protocol.SourceSubagent:
		default:
			continue
		}
		matched = append(matched, f)
	}
	sort.Slice(matched, func(i, j int) bool {
		return protocol.SourceIDLess(matched[i].source, matched[j].source)
	})
	return matched
}

func (r *Reducer) settlePipeProcessExit(matched []*sourceFact, at int64, result *HarnessProcessExitResult) {
	for _, f := range matched {
		if f.acked || !f.pipeMode {
			continue
		}
		switch f.icon {
		case "", "bolt", "compact", "subagent", "stop":
			r.withdrawProcessFact(f, at, result)
		case "key", "question", "warning", "error":
			// Preserve outcomes that require acknowledgement.
		}
	}
}

func (r *Reducer) settleInteractiveProcessExit(
	identity protocol.HarnessProcessIdentity,
	matched []*sourceFact,
	at int64,
	result *HarnessProcessExitResult,
) {
	parent := newestInteractiveProcessParent(matched)
	if parent != nil && !parent.terminated {
		settledAt := processSettlementTime(at, parent)
		parent.action = "stop"
		parent.icon = "stop"
		parent.at = settledAt
		parent.reactivate()
		parent.terminated = true
		parent.terminalTurn = parent.turnID
		parent.terminalToolUse = parent.toolUse
		parent.terminalAt = settledAt
		r.stampCausalOrder(parent)
		result.SynthesizedStop = true
	}

	// Retire only older active-class parent facts for this exact process. The
	// selected parent is the newest live parent, so the recency check mainly
	// documents and defends the rule for restored legacy ties.
	if parent != nil {
		for _, f := range matched {
			if f == parent || f.acked || f.pipeMode || !processParentFact(f) {
				continue
			}
			if f.icon != "bolt" && f.icon != "compact" {
				continue
			}
			if moreRecent(f, parent) {
				continue
			}
			r.withdrawProcessFact(f, at, result)
		}
	}

	sessionID := ""
	if parent != nil {
		sessionID = parent.sessionID
	}
	result.ClearedSubagentSources = append(
		result.ClearedSubagentSources,
		r.clearProcessSubagents(sessionID, identity, at)...,
	)
}

func newestInteractiveProcessParent(matched []*sourceFact) *sourceFact {
	var newest *sourceFact
	for _, f := range matched {
		if f.acked || f.pipeMode || !processParentFact(f) {
			continue
		}
		if newest == nil || moreRecent(f, newest) {
			newest = f
		}
	}
	return newest
}

func processParentFact(f *sourceFact) bool {
	if f.source.Kind == protocol.SourceHarness {
		return true
	}
	return f.source.Kind == protocol.SourceLegacy && !protocol.IsLegacySubagentBucket(f.source.Key)
}

// clearProcessSubagents clears active subagents for the selected parent
// session. When the parent carries no session identity, only subagents stamped
// with the exact exiting process are eligible.
func (r *Reducer) clearProcessSubagents(
	sessionID string,
	identity protocol.HarnessProcessIdentity,
	at int64,
) []protocol.SourceID {
	candidates := make([]*sourceFact, 0)
	for _, f := range r.facts {
		if !f.set || f.acked || f.terminated || !activeSubagentFact(f) {
			continue
		}
		if sessionID != "" {
			switch f.source.Kind {
			case protocol.SourceSubagent:
				if f.sessionID != sessionID {
					continue
				}
			case protocol.SourceLegacy:
				if f.source.Key != protocol.LegacySubagentBucketKey(sessionID) {
					continue
				}
			}
		} else if f.harnessProcess != identity {
			continue
		}
		candidates = append(candidates, f)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return protocol.SourceIDLess(candidates[i].source, candidates[j].source)
	})

	cleared := make([]protocol.SourceID, 0, len(candidates))
	for _, f := range candidates {
		settledAt := processSettlementTime(at, f)
		switch f.source.Kind {
		case protocol.SourceSubagent:
			f.action = "stop"
			f.icon = ""
			f.terminated = true
			f.terminalTurn = ""
			f.terminalToolUse = ""
			f.terminalAt = settledAt
		case protocol.SourceLegacy:
			f.agents = 0
			f.action = "idle"
			f.icon = ""
		}
		r.stampCausalOrder(f)
		cleared = append(cleared, f.source)
	}
	return cleared
}

func activeSubagentFact(f *sourceFact) bool {
	switch f.source.Kind {
	case protocol.SourceSubagent:
		return f.icon == "subagent"
	case protocol.SourceLegacy:
		return protocol.IsLegacySubagentBucket(f.source.Key) && (f.agents > 0 || f.icon == "subagent")
	default:
		return false
	}
}

func (r *Reducer) withdrawProcessFact(f *sourceFact, at int64, result *HarnessProcessExitResult) {
	if f == nil || f.acked {
		return
	}
	f.acknowledge(at)
	result.WithdrawnSources = append(result.WithdrawnSources, f.source)
}

func processSettlementTime(at int64, f *sourceFact) int64 {
	if at > 0 {
		return at
	}
	if f != nil {
		if f.at > 0 {
			return f.at
		}
		if f.terminalAt > 0 {
			return f.terminalAt
		}
	}
	return 1
}
