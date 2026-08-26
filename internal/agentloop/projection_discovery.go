package agentloop

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Tri-state plan discovery.
//
// The question reconciliation actually needs to answer is not "which runs are
// alive" but "is this lease still justified". Those differ whenever evidence is
// missing, and collapsing them — treating "I could not tell" as "gone" — is
// what lets a corrupt state file, an unpaired ASL, or a transient read error
// retire a healthy run's lease and duplicate its work. So discovery returns one
// observation per plan directory with an explicit third answer.

// Presence is what a single scan could establish about one plan.
type Presence string

const (
	// PresencePresent means the run exists and holds its position: either a
	// live process authenticated against its published start token, or a
	// paused run, which is still awaiting a human and deliberately has no
	// process at all.
	PresencePresent Presence = "present"
	// PresenceConfirmedAbsent means the run is positively gone — ASL recorded
	// it finished, or the process it named has exited. Only this answer may
	// advance a miss streak toward retirement.
	PresenceConfirmedAbsent Presence = "confirmed_absent"
	// PresenceIndeterminate means the scan established nothing. It never
	// advances a miss streak and never resets one: absent evidence is not
	// evidence of presence either, so a run that goes quiet mid-streak resumes
	// where it left off once real evidence returns.
	PresenceIndeterminate Presence = "indeterminate"
)

// PlanObservation is one plan directory's evidence from one scan. Reason is
// always set, including for a present plan, so a diagnostic can explain any
// verdict without re-deriving it.
type PlanObservation struct {
	StateDir      string
	PlanDir       string
	RunID         string
	ContractSetID string
	Presence      Presence
	Reason        string
	// Generation and Sequence are the projection generation and lifecycle
	// source sequence this observation was read at. They order evidence: a
	// scan that started before a state write must not overwrite what the write
	// established.
	Generation uint64
	Sequence   uint64
	// Candidate is populated only when Presence is PresencePresent.
	Candidate Candidate
}

// DiscoveryResult is one full sweep of the state root.
type DiscoveryResult struct {
	StateRoot string
	// RootPresent reports whether the state root itself existed. An empty sweep
	// of a root that exists is real evidence — there are no plans — while an
	// empty sweep of a root that does not is a pairing or configuration
	// problem wearing the same shape. Only the first may retire anything.
	RootPresent  bool
	Observations []PlanObservation
}

// Candidates returns the present runs, newest-first. IncludePaused selects
// whether paused runs are included: reconciliation needs them (a paused run
// still owns its lease), while routing and loop-marker consumers must keep
// seeing live processes only.
func (r DiscoveryResult) Candidates(includePaused bool) []Candidate {
	candidates := make([]Candidate, 0, len(r.Observations))
	for _, observation := range r.Observations {
		if observation.Presence != PresencePresent {
			continue
		}
		if observation.Candidate.Paused && !includePaused {
			continue
		}
		candidates = append(candidates, observation.Candidate)
	}
	SortCandidates(candidates)
	return candidates
}

// PresenceByRunID indexes every observation carrying a run ID. A run ID is the
// only stable key across a restart — window IDs and state paths are not — so
// this is what lease reconciliation matches facts against.
func (r DiscoveryResult) PresenceByRunID() map[string]PlanObservation {
	index := make(map[string]PlanObservation, len(r.Observations))
	for _, observation := range r.Observations {
		if observation.RunID == "" {
			continue
		}
		// A run ID should not repeat across state directories, but if it does,
		// the weaker answer wins: a duplicate is itself a reason not to assert
		// absence.
		if existing, ok := index[observation.RunID]; ok && existing.Presence != observation.Presence {
			existing.Presence = PresenceIndeterminate
			existing.Reason = ReasonInvalidProjection
			index[observation.RunID] = existing
			continue
		}
		index[observation.RunID] = observation
	}
	return index
}

// HasIndeterminate reports whether any plan in the sweep withheld evidence.
// A lease whose run cannot be located at all is only safe to miss when the
// whole sweep was clean; one unreadable plan could be the very one that lease
// belongs to.
func (r DiscoveryResult) HasIndeterminate() bool {
	for _, observation := range r.Observations {
		if observation.Presence == PresenceIndeterminate {
			return true
		}
	}
	return false
}

// Counts summarizes a sweep for metrics.
func (r DiscoveryResult) Counts() (present, confirmedAbsent, indeterminate int) {
	for _, observation := range r.Observations {
		switch observation.Presence {
		case PresencePresent:
			present++
		case PresenceConfirmedAbsent:
			confirmedAbsent++
		case PresenceIndeterminate:
			indeterminate++
		}
	}
	return present, confirmedAbsent, indeterminate
}

// DiscoverPlans classifies every plan under the configured state root.
//
// Failures are always per-plan: a state root that cannot be listed is the only
// error returned, because that is the one failure that says nothing about any
// individual plan. One unreadable or corrupt plan yields one indeterminate
// observation and leaves every other plan's evidence intact.
func DiscoverPlans(opts DiscoverOptions) (DiscoveryResult, error) {
	root := opts.StateRoot
	if root == "" {
		root = DefaultStateRoot()
	}
	canonicalRoot, err := CanonicalizePath(root)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("agentloop: resolving state root %s: %w", root, err)
	}

	entries, err := os.ReadDir(canonicalRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// A state root ASL has never written is a healthy empty system,
			// not a failed scan.
			return DiscoveryResult{StateRoot: canonicalRoot}, nil
		}
		return DiscoveryResult{}, fmt.Errorf("agentloop: reading state root %s: %w", canonicalRoot, err)
	}

	pidRunning := opts.PIDRunning
	if pidRunning == nil {
		pidRunning = IsPIDRunning
	}
	birthLookup := opts.ProcessBirth
	if birthLookup == nil {
		birthLookup = processBirthTokens
	}

	// Decode first, probe once, then classify. Batching the birth probe keeps
	// the ten-second cadence to a single subprocess regardless of how many
	// plans exist, and means every plan in a sweep is authenticated against
	// the same observation of the process table.
	type decoded struct {
		stateDir   string
		projection Projection
		err        error
	}
	pending := make([]decoded, 0, len(entries))
	pids := make([]int, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		stateDir, canonErr := CanonicalizePath(filepath.Join(canonicalRoot, entry.Name()))
		if canonErr != nil {
			continue
		}
		statePath := filepath.Join(stateDir, stateFileName)
		seen[statePath] = struct{}{}
		projection, readErr := opts.Cache.read(statePath)
		pending = append(pending, decoded{stateDir: stateDir, projection: projection, err: readErr})
		if readErr == nil && projection.Process != nil {
			pids = append(pids, projection.Process.PID)
		}
	}
	opts.Cache.retain(seen)

	tokens, probed := birthLookup(pids)

	result := DiscoveryResult{
		StateRoot:    canonicalRoot,
		RootPresent:  true,
		Observations: make([]PlanObservation, 0, len(pending)),
	}
	for _, item := range pending {
		result.Observations = append(result.Observations,
			classifyPlan(item.stateDir, item.projection, item.err, pidRunning, tokens, probed))
	}
	return result, nil
}

// classifyPlan turns one decoded projection into an observation.
func classifyPlan(stateDir string, projection Projection, readErr error, pidRunning PIDChecker, tokens map[int]string, probed bool) PlanObservation {
	if readErr != nil {
		return PlanObservation{
			StateDir: stateDir,
			Presence: PresenceIndeterminate,
			Reason:   ProjectionReason(readErr),
		}
	}

	observation := PlanObservation{
		StateDir:      stateDir,
		RunID:         projection.RunID,
		ContractSetID: projection.ContractSetID,
		Generation:    projection.Generation,
		Sequence:      projection.LifecycleSourceSequence,
	}

	planDir, err := CanonicalizePath(projection.PlanDir)
	if err != nil || planDir == "" {
		observation.Presence = PresenceIndeterminate
		observation.Reason = ReasonInvalidProjection
		return observation
	}
	observation.PlanDir = planDir

	// ASL names each state directory after the hash of the plan it belongs to.
	// A mismatch means the state was copied, relocated, or hand-edited, and the
	// run it describes may well be alive somewhere else — so this withholds
	// evidence rather than reporting the run gone.
	if stateHash := filepath.Base(stateDir); isHexSHA256(stateHash) && stateHash != pathSHA256(planDir) {
		observation.Presence = PresenceIndeterminate
		observation.Reason = ReasonMisplacedState
		return observation
	}

	switch projection.Phase {
	case PhaseFinished:
		// ASL durably recorded the end of the run. This is the authoritative
		// absence: it survives a daemon restart and a missed hook alike.
		observation.Presence = PresenceConfirmedAbsent
		observation.Reason = ReasonFinished
		return observation
	case PhasePaused:
		// A pause finalizes the loop process, so a paused run has no PID to
		// check and is nonetheless still holding its position awaiting a human.
		// Phase alone decides it.
		observation.Presence = PresencePresent
		observation.Reason = ReasonPaused
		observation.Candidate = newCandidate(stateDir, planDir, projection)
		return observation
	}

	if projection.Process == nil {
		// A live phase with no process reference: ASL could not establish its
		// own birth, or the state was written by the migration tool rather than
		// by the loop. Either way nothing here attests liveness.
		observation.Presence = PresenceIndeterminate
		observation.Reason = ReasonProcessUnverified
		return observation
	}

	switch authenticateProcess(*projection.Process, pidRunning, tokens, probed) {
	case ProcessAlive:
		observation.Presence = PresencePresent
		observation.Reason = ReasonLiveProcess
		observation.Candidate = newCandidate(stateDir, planDir, projection)
	case ProcessExited:
		observation.Presence = PresenceConfirmedAbsent
		observation.Reason = ReasonProcessExited
	case ProcessReplaced:
		// The PID was recycled. This refuses to call the run present — which is
		// what stops a mutation being routed to an unrelated process — without
		// claiming it absent, because a recycled PID says nothing about where
		// the original run went.
		observation.Presence = PresenceIndeterminate
		observation.Reason = ReasonProcessReplaced
	default:
		observation.Presence = PresenceIndeterminate
		observation.Reason = ReasonProcessUnverified
	}
	return observation
}

// newCandidate projects one validated projection into the shape the rest of
// AgentMux consumes.
func newCandidate(stateDir, planDir string, projection Projection) Candidate {
	candidate := Candidate{
		PlanDir:         planDir,
		StateDir:        stateDir,
		Harness:         normalizeKnownHarness(projection.Harness),
		RunID:           projection.RunID,
		ContractSetID:   projection.ContractSetID,
		RegistryDigest:  projection.RegistryDigest,
		Phase:           projection.Phase,
		Paused:          projection.Phase == PhasePaused,
		SessionID:       projection.SessionID,
		TurnIndex:       projection.TurnIndex,
		QuestionID:      projection.QuestionID,
		SourceSequence:  projection.LifecycleSourceSequence,
		Generation:      projection.Generation,
		RunStartedAt:    projection.StartedAt,
		RunStartedAtRaw: projection.StartedAtRaw,
		UpdatedAt:       projection.UpdatedAt,
		UpdatedAtRaw:    projection.UpdatedAtRaw,
	}
	if projection.KittyWindowID > 0 {
		candidate.WindowID = projection.KittyWindowID
	}
	if projection.Process != nil {
		candidate.PID = projection.Process.PID
		candidate.StartToken = projection.Process.StartToken
	}
	// The projection carries the plan's workspace root, which for a workspace
	// plan is exactly the directory ASL runs in. A plan with no workspace has
	// none, and CWD-keyed routing degrades to window-ID routing for it rather
	// than matching some other plan's directory.
	if projection.WorkspaceRoot != "" {
		if cwd, err := CanonicalizePath(projection.WorkspaceRoot); err == nil {
			candidate.CWD = cwd
		}
	}
	return candidate
}
