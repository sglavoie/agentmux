package asl

// RunPositions records, per run ID, the furthest projection position AgentMux
// has folded in.
//
// Two independent counters make staleness decidable: the projection generation
// advances on every durable transition, and the lifecycle source sequence
// advances on every notification. Both are monotonic at the source, so a
// decrease in either can only mean the read that carried it is older than one
// already applied — a reordered filesystem read, or a scan that raced a write.
// Acting on it would resurrect a position the run has already left.
//
// Like MissStreaks this is CALLER-LOCKED and zero-value ready: acceptance is
// decided in the same critical section as the reducer mutation it authorizes.
type RunPositions struct {
	byRun map[string]runPosition
}

// runPosition is the furthest position accepted for one run.
type runPosition struct {
	generation uint64
	sequence   uint64
}

// Accept reports whether this observation is at least as new as the furthest
// one already folded in for the run, and records it when it is. An observation
// with no run identity is never acceptable: nothing could correlate it later.
func (p *RunPositions) Accept(runID string, generation, sequence uint64) bool {
	if runID == "" {
		return false
	}
	seen, ok := p.byRun[runID]
	if ok && (generation < seen.generation || (generation == seen.generation && sequence < seen.sequence)) {
		return false
	}
	if p.byRun == nil {
		p.byRun = make(map[string]runPosition)
	}
	p.byRun[runID] = runPosition{generation: generation, sequence: sequence}
	return true
}
