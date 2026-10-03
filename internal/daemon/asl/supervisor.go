package asl

// Supervision state.
//
// A supervised run publishes a small fixed-size lease record next to its plan
// state. Polling that record is how the daemon learns a human is being waited
// on without rescanning the whole ASL state root, so this is the bookkeeping
// that makes the poll cheap and correct: which runs are worth polling, what
// each last said, and which closure markers have already retired an open set.
//
// It holds no reducer knowledge. Raising or clearing the human-actionable
// question icon is a reducer mutation and stays in the daemon root.

import (
	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// questionProbePrefix keys the supervisor question probe source. It is a wire
// value: it appears in exported session events and durable window state, so it
// must not be renamed.
const questionProbePrefix = "asl_supervisor:"

// QuestionSource is the probe source that carries one run's supervisor question
// evidence.
func QuestionSource(runID string) protocol.SourceID {
	return protocol.SourceID{Kind: protocol.SourceProbe, Key: questionProbePrefix + runID}
}

// SupervisorTarget is one live ASL run whose adjacent fixed-size supervisor
// lease can be refreshed without rescanning the complete ASL state root.
type SupervisorTarget struct {
	WindowID       int
	OriginWindowID int
	Candidate      agentloop.Candidate
}

// PendingQuestion pairs a registered target with the projection that currently
// reports unanswered questions for it.
type PendingQuestion struct {
	Target     SupervisorTarget
	Projection agentloop.SupervisorQuestionProjection
}

// SupervisorRegistry retains, per lease, the run's poll target, the projection
// it last published, and the closure sequence that already retired its open
// set. All three are keyed by the same LeaseRef and are retired together.
//
// Like the other trackers in this package it is CALLER-LOCKED and zero-value
// ready: the daemon reads and updates it in the same stateMu critical section
// as the reducer state it is about to settle.
type SupervisorRegistry struct {
	targets     map[LeaseRef]SupervisorTarget
	projections map[LeaseRef]agentloop.SupervisorQuestionProjection
	cutoffs     map[LeaseRef]uint64
}

// Register records one poll target. A target with no window, no run identity,
// or no state directory is dropped: none of the three can be recovered later,
// and a target missing any of them could never be matched against evidence.
func (r *SupervisorRegistry) Register(target SupervisorTarget) {
	if target.WindowID <= 0 || target.Candidate.RunID == "" || target.Candidate.StateDir == "" {
		return
	}
	if target.OriginWindowID <= 0 {
		target.OriginWindowID = target.WindowID
	}
	if r.targets == nil {
		r.targets = make(map[LeaseRef]SupervisorTarget)
	}
	r.targets[LeaseRef{WindowID: target.WindowID, Key: target.Candidate.RunID}] = target
}

// Target returns the registered poll target for one lease.
func (r *SupervisorRegistry) Target(ref LeaseRef) (SupervisorTarget, bool) {
	target, ok := r.targets[ref]
	return target, ok
}

// StateDir returns the plan state directory registered for one lease, or the
// empty string when the lease has no plan association at all.
func (r *SupervisorRegistry) StateDir(ref LeaseRef) string {
	return r.targets[ref].Candidate.StateDir
}

// Targets snapshots every registered poll target, so the refresh can read the
// lease records off the daemon's lock.
func (r *SupervisorRegistry) Targets() []SupervisorTarget {
	targets := make([]SupervisorTarget, 0, len(r.targets))
	for _, target := range r.targets {
		targets = append(targets, target)
	}
	return targets
}

// RunTargets returns the leases whose target is exactly this run at exactly
// this plan, which is what a closure marker must match before it may retire
// anything.
func (r *SupervisorRegistry) RunTargets(runID, stateDir string) []LeaseRef {
	var refs []LeaseRef
	for ref, target := range r.targets {
		if ref.Key != runID || target.Candidate.RunID != runID || target.Candidate.StateDir != stateDir {
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}

// TargetCount reports how many poll targets are registered.
func (r *SupervisorRegistry) TargetCount() int {
	return len(r.targets)
}

// Projection returns the projection this lease last published.
func (r *SupervisorRegistry) Projection(ref LeaseRef) (agentloop.SupervisorQuestionProjection, bool) {
	projection, ok := r.projections[ref]
	return projection, ok
}

// SetProjection caches the projection this lease last published.
func (r *SupervisorRegistry) SetProjection(ref LeaseRef, projection agentloop.SupervisorQuestionProjection) {
	if r.projections == nil {
		r.projections = make(map[LeaseRef]agentloop.SupervisorQuestionProjection)
	}
	r.projections[ref] = projection
}

// ClearProjection forgets one lease's cached projection.
func (r *SupervisorRegistry) ClearProjection(ref LeaseRef) {
	delete(r.projections, ref)
}

// PendingForWindow returns every registered lease on one window whose cached
// projection reports unanswered questions. A projection with no surviving
// target is skipped: without the target there is nothing to attribute it to.
func (r *SupervisorRegistry) PendingForWindow(windowID int) []PendingQuestion {
	var pending []PendingQuestion
	for ref, projection := range r.projections {
		if ref.WindowID != windowID || projection.UnansweredQuestions <= 0 {
			continue
		}
		target, ok := r.targets[ref]
		if !ok {
			continue
		}
		pending = append(pending, PendingQuestion{Target: target, Projection: projection})
	}
	return pending
}

// Pending returns every registered lease, on any window, whose cached
// projection reports unanswered questions — PendingForWindow without the
// window filter, for a consumer that summarizes the whole daemon at once. The
// order is unspecified; a caller that publishes it sorts it.
func (r *SupervisorRegistry) Pending() []PendingQuestion {
	var pending []PendingQuestion
	for ref, projection := range r.projections {
		if projection.UnansweredQuestions <= 0 {
			continue
		}
		target, ok := r.targets[ref]
		if !ok {
			continue
		}
		pending = append(pending, PendingQuestion{Target: target, Projection: projection})
	}
	return pending
}

// ClosureBlocks reports whether an authoritative closure marker has already
// retired everything up to seq for this lease. A projection newer than the
// cutoff supersedes it, and consumes it, so one closure can never suppress an
// open set raised after it.
func (r *SupervisorRegistry) ClosureBlocks(ref LeaseRef, seq uint64) bool {
	cutoff, ok := r.cutoffs[ref]
	if !ok {
		return false
	}
	if seq <= cutoff {
		return true
	}
	delete(r.cutoffs, ref)
	return false
}

// RaiseClosureCutoff advances the sequence below which this lease's projections
// are already known to be closed. It never moves backwards.
func (r *SupervisorRegistry) RaiseClosureCutoff(ref LeaseRef, seq uint64) {
	if seq <= r.cutoffs[ref] {
		return
	}
	if r.cutoffs == nil {
		r.cutoffs = make(map[LeaseRef]uint64)
	}
	r.cutoffs[ref] = seq
}

// ClosureCutoff returns the recorded closure cutoff for one lease.
func (r *SupervisorRegistry) ClosureCutoff(ref LeaseRef) (uint64, bool) {
	cutoff, ok := r.cutoffs[ref]
	return cutoff, ok
}

// CutoffCount reports how many leases carry a closure cutoff.
func (r *SupervisorRegistry) CutoffCount() int {
	return len(r.cutoffs)
}

// Retire drops one lease's poll target and closure cutoff. The cached
// projection is left to the retraction path, which needs it to decide what to
// say about the icon it is clearing.
func (r *SupervisorRegistry) Retire(ref LeaseRef) {
	delete(r.targets, ref)
	delete(r.cutoffs, ref)
}

// RetainWindows drops every lease whose window keep rejects. A closed window's
// panes can never corroborate a run again, so its whole supervision record —
// target, projection, and cutoff — goes with it.
func (r *SupervisorRegistry) RetainWindows(keep func(windowID int) bool) {
	for ref := range r.targets {
		if keep(ref.WindowID) {
			continue
		}
		delete(r.targets, ref)
		delete(r.projections, ref)
		delete(r.cutoffs, ref)
	}
}
