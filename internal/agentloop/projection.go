package agentloop

// The strict nested projection contract.
//
// agent-session-loop publishes one atomic `agent_mux` block inside state.json.
// That block is the ONLY thing AgentMux reads out of ASL's durable state: the
// loop's own top-level bookkeeping (active_turn, active_verification,
// waiting_for_reply, paused, run_finished_at) and its lock file are private
// implementation detail and are never interpreted here.
//
// Compatibility is a hard paired cutover. State carrying no projection this
// build supports yields no facts at all rather than being re-read by the older
// parser — but "no facts" is deliberately reported as *indeterminate*, not as
// "this run is gone", so an unpaired or half-upgraded ASL can never retire a
// healthy lease.
//
// This file holds the contract's vocabulary and the shape a valid projection
// decodes into. Reading one out of a state document is projection_decode.go,
// and the per-field validation it calls is projection_fields.go.

import (
	"errors"
	"fmt"
	"time"

	"github.com/sglavoie/agentmux/internal/aslcontract"
)

const (
	// projectionKey is the state.json member the projection is nested under.
	projectionKey = "agent_mux"
	// ProjectionSchema is the only `agent_mux` schema version this build reads.
	ProjectionSchema = 1
	// ContractSetID is the one contract set this build reads a projection under.
	//
	// It used to be a family prefix, because AgentMux held no copy of ASL's
	// corpus and so could not tell a fixture-only edit from an incompatible
	// peer. It now vendors that corpus at internal/aslcontract, so the digest is
	// checkable and the family match is not: two builds inside the family whose
	// digests differ disagree about the wire, which is the whole reason the ID
	// carries a digest at all.
	ContractSetID = aslcontract.ContractSetID

	// ContractSetPrefix remains the family every well-formed ID carries. It is
	// used only to describe a mismatch, never to accept one.
	ContractSetPrefix = aslcontract.Prefix

	maxRunIDChars         = 128
	maxSessionIDChars     = 64
	maxQuestionIDChars    = 128
	maxHarnessChars       = 64
	maxStartTokenChars    = 128
	maxRegistryChars      = 64
	maxPathChars          = 1024
	maxTimestampChars     = 64
	maxProjectionPID      = 4194304
	maxProjectionWindowID = 1 << 31
	// maxProjectionBytes is the contract's document cap. The per-field limits
	// already bound a well-formed projection well under it; this bounds the
	// work done on a malformed one before those limits ever apply.
	maxProjectionBytes = 4096
)

// Classification reasons. Every observation carries exactly one, and the
// vocabulary is stable: metrics, watchdog journal records, and diagnostics key
// off these strings.
const (
	// Indeterminate: the projection could not be established.
	ReasonUnreadableState     = "unreadable-state"
	ReasonMissingProjection   = "missing-projection"
	ReasonUnsupportedContract = "unsupported-contract"
	ReasonInvalidProjection   = "invalid-projection"
	ReasonMisplacedState      = "misplaced-state"
	ReasonProcessUnverified   = "process-unverified"

	// Present.
	ReasonLiveProcess = "live-process"
	ReasonPaused      = "paused"

	// Confirmed absent.
	ReasonFinished        = "finished"
	ReasonProcessExited   = "process-exited"
	ReasonProcessReplaced = "process-replaced"
)

// projectionFields is the exact member set schema 1 requires. ASL spells
// absence as an explicit null rather than omission, so an exact set comparison
// enforces `required` and `additionalProperties: false` in one step: a field a
// newer ASL added and a field an older ASL omitted are both rejected, and both
// are reported as an unusable projection rather than silently defaulted.
var projectionFields = []string{
	"contract_set_id",
	"finished_at",
	"harness",
	"kitty_window_id",
	"lifecycle_source_sequence",
	"phase",
	"plan_dir",
	"process",
	"projection_generation",
	"question_id",
	"registry_digest",
	"run_id",
	"schema_version",
	"session_id",
	"started_at",
	"turn_index",
	"updated_at",
	"workspace_root",
}

// projectionPhases is the phase vocabulary schema 1 defines. An unknown phase
// is an invalid projection rather than a degraded one: guessing at a phase a
// newer loop invented would let AgentMux assert a lease state ASL never meant.
var projectionPhases = map[string]struct{}{
	PhaseStarting:     {},
	PhaseTurn:         {},
	PhaseVerification: {},
	PhaseBetween:      {},
	PhaseWaiting:      {},
	PhasePaused:       {},
	PhaseFinished:     {},
}

// ProcessRef is the loop process a recycled PID cannot impersonate. A PID
// alone is not identity — the kernel hands it back out — so the start token
// (the birth time `ps` reports, fixed for the life of the process) travels
// with it. ASL writes the pair only while the run holds a live process.
type ProcessRef struct {
	PID        int
	StartToken string
}

// Projection is one run's supervision-relevant position exactly as ASL
// published it. Absence is spelled by the zero value: an empty string or a nil
// Process means ASL wrote null, never "the field was missing" — a missing
// field never produces a Projection at all.
type Projection struct {
	ContractSetID  string
	RunID          string
	PlanDir        string
	WorkspaceRoot  string
	RegistryDigest string
	Harness        string
	KittyWindowID  int
	Phase          string
	SessionID      string
	// TurnIndex is -1 when ASL reported null.
	TurnIndex  int
	QuestionID string
	// LifecycleSourceSequence is how far the notification stream has got, and
	// Generation how far the durable record has. Neither derives from the
	// other: a run whose hooks all failed still advances its generation, and a
	// run that emits several hooks inside one transition advances it once.
	LifecycleSourceSequence uint64
	Generation              uint64
	Process                 *ProcessRef

	StartedAt     time.Time
	StartedAtRaw  string
	UpdatedAt     time.Time
	UpdatedAtRaw  string
	FinishedAtRaw string
}

// ProjectionError explains why a state file yielded no usable projection.
// Reason is one of the stable indeterminate reasons above; every one of them
// preserves evidence rather than asserting absence.
type ProjectionError struct {
	Reason string
	Detail string
}

func (e *ProjectionError) Error() string {
	if e.Detail == "" {
		return e.Reason
	}
	return e.Reason + ": " + e.Detail
}

func projectionErrorf(reason, format string, args ...any) *ProjectionError {
	return &ProjectionError{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// ProjectionReason returns err's stable classification reason, or
// ReasonInvalidProjection for an error that is not a ProjectionError.
func ProjectionReason(err error) string {
	var perr *ProjectionError
	if errors.As(err, &perr) {
		return perr.Reason
	}
	return ReasonInvalidProjection
}
