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

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	// projectionKey is the state.json member the projection is nested under.
	projectionKey = "agent_mux"
	// ProjectionSchema is the only `agent_mux` schema version this build reads.
	ProjectionSchema = 1
	// ContractSetPrefix is the contract-set family AgentMux speaks. The full ID
	// also carries a digest over ASL's fixture corpus; matching the family (and
	// the schema version inside the block) is what this build requires, because
	// the corpus is not vendored here yet and a fixture-only edit must not read
	// as an incompatible peer.
	ContractSetPrefix = "asl-amx-1."

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

// ReadProjection reads the projection nested in the state file at path.
//
// Every failure mode is a typed ProjectionError so the caller can tell an
// unreadable file from a peer speaking a contract set this build does not
// support — the two produce the same "no evidence" verdict but different
// operator remediation.
func ReadProjection(path string) (Projection, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Projection{}, projectionErrorf(ReasonUnreadableState, "%s does not exist", path)
	}
	if err != nil {
		return Projection{}, projectionErrorf(ReasonUnreadableState, "reading %s: %v", path, err)
	}
	return decodeStateProjection(data, path)
}

// decodeStateProjection pulls the `agent_mux` member out of one state.json
// document. The surrounding document is decoded only far enough to find that
// member: nothing else in it is contract.
func decodeStateProjection(data []byte, path string) (Projection, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return Projection{}, projectionErrorf(ReasonUnreadableState, "decoding %s: %v", path, err)
	}
	raw, ok := document[projectionKey]
	if !ok || isJSONNull(raw) {
		return Projection{}, projectionErrorf(ReasonMissingProjection,
			"%s carries no %s projection", path, projectionKey)
	}
	projection, err := DecodeProjection(raw)
	if err != nil {
		var perr *ProjectionError
		if errors.As(err, &perr) {
			return Projection{}, projectionErrorf(perr.Reason, "%s: %s", path, perr.Detail)
		}
		return Projection{}, projectionErrorf(ReasonInvalidProjection, "%s: %v", path, err)
	}
	return projection, nil
}

// DecodeProjection validates one `agent_mux` block against schema 1.
//
// The contract-set check runs first and reports its own reason: a peer on a
// different contract set is a pairing problem an operator fixes with
// `asl-sync`, not a corrupt file, and conflating the two would send them
// looking for damage that is not there.
func DecodeProjection(raw []byte) (Projection, error) {
	if len(raw) > maxProjectionBytes {
		return Projection{}, projectionErrorf(ReasonInvalidProjection,
			"projection is %d bytes, over the %d-byte contract cap", len(raw), maxProjectionBytes)
	}

	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return Projection{}, projectionErrorf(ReasonInvalidProjection, "decoding projection: %v", err)
	}

	version, err := projectionInt(members, "schema_version", 0, maxIntValue)
	if err != nil || version != ProjectionSchema {
		return Projection{}, projectionErrorf(ReasonUnsupportedContract,
			"projection schema %s is not %d", string(members["schema_version"]), ProjectionSchema)
	}
	contractSetID, err := projectionString(members, "contract_set_id", 128)
	if err != nil {
		return Projection{}, projectionErrorf(ReasonUnsupportedContract, "%v", err)
	}
	if !strings.HasPrefix(contractSetID, ContractSetPrefix) {
		return Projection{}, projectionErrorf(ReasonUnsupportedContract,
			"contract set %q is outside the %q family", contractSetID, ContractSetPrefix)
	}

	if err := requireExactMembers(members); err != nil {
		return Projection{}, err
	}

	projection := Projection{ContractSetID: contractSetID}
	if projection.RunID, err = projectionString(members, "run_id", maxRunIDChars); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.PlanDir, err = projectionAbsPath(members, "plan_dir", false); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.WorkspaceRoot, err = projectionAbsPath(members, "workspace_root", true); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.RegistryDigest, err = projectionNullableString(members, "registry_digest", maxRegistryChars); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.Harness, err = projectionNullableString(members, "harness", maxHarnessChars); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.KittyWindowID, err = projectionNullableInt(members, "kitty_window_id", 1, maxProjectionWindowID); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.Phase, err = projectionPhase(members); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.SessionID, err = projectionNullableString(members, "session_id", maxSessionIDChars); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.TurnIndex, err = projectionNullableInt(members, "turn_index", 0, maxIntValue); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.QuestionID, err = projectionNullableString(members, "question_id", maxQuestionIDChars); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.LifecycleSourceSequence, err = projectionUint(members, "lifecycle_source_sequence", 0); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.Generation, err = projectionUint(members, "projection_generation", 1); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.Process, err = projectionProcess(members); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.StartedAtRaw, projection.StartedAt, err = projectionTimestamp(members, "started_at", false); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.UpdatedAtRaw, projection.UpdatedAt, err = projectionTimestamp(members, "updated_at", false); err != nil {
		return Projection{}, invalidProjection(err)
	}
	if projection.FinishedAtRaw, _, err = projectionTimestamp(members, "finished_at", true); err != nil {
		return Projection{}, invalidProjection(err)
	}

	// The two resting phases have no process to name, so a projection that
	// names one is internally inconsistent and cannot be authenticated
	// coherently. Reject it rather than pick which half to believe.
	if projection.Process != nil && (projection.Phase == PhasePaused || projection.Phase == PhaseFinished) {
		return Projection{}, projectionErrorf(ReasonInvalidProjection,
			"phase %q must not name a process", projection.Phase)
	}
	return projection, nil
}

func invalidProjection(err error) error {
	var perr *ProjectionError
	if errors.As(err, &perr) {
		return perr
	}
	return projectionErrorf(ReasonInvalidProjection, "%v", err)
}

func requireExactMembers(members map[string]json.RawMessage) error {
	var missing, unknown []string
	expected := make(map[string]struct{}, len(projectionFields))
	for _, field := range projectionFields {
		expected[field] = struct{}{}
		if _, ok := members[field]; !ok {
			missing = append(missing, field)
		}
	}
	for field := range members {
		if _, ok := expected[field]; !ok {
			unknown = append(unknown, field)
		}
	}
	if len(missing) == 0 && len(unknown) == 0 {
		return nil
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	return projectionErrorf(ReasonInvalidProjection,
		"projection member set mismatch (missing: %s; unknown: %s)",
		strings.Join(missing, ","), strings.Join(unknown, ","))
}

func isJSONNull(raw json.RawMessage) bool {
	return string(bytesTrimSpace(raw)) == "null"
}

func bytesTrimSpace(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}

func projectionString(members map[string]json.RawMessage, key string, limit int) (string, error) {
	raw, ok := members[key]
	if !ok {
		return "", projectionErrorf(ReasonInvalidProjection, "%s is missing", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", projectionErrorf(ReasonInvalidProjection, "%s is not a string", key)
	}
	if value == "" || strings.TrimSpace(value) != value {
		return "", projectionErrorf(ReasonInvalidProjection, "%s is empty or not trimmed", key)
	}
	if len(value) > limit {
		return "", projectionErrorf(ReasonInvalidProjection, "%s exceeds %d characters", key, limit)
	}
	return value, nil
}

func projectionNullableString(members map[string]json.RawMessage, key string, limit int) (string, error) {
	if isJSONNull(members[key]) {
		return "", nil
	}
	return projectionString(members, key, limit)
}

func projectionAbsPath(members map[string]json.RawMessage, key string, nullable bool) (string, error) {
	value, err := func() (string, error) {
		if nullable {
			return projectionNullableString(members, key, maxPathChars)
		}
		return projectionString(members, key, maxPathChars)
	}()
	if err != nil || value == "" {
		return "", err
	}
	if !strings.HasPrefix(value, "/") {
		return "", projectionErrorf(ReasonInvalidProjection, "%s is not an absolute path", key)
	}
	return value, nil
}

func projectionInt(members map[string]json.RawMessage, key string, minimum, maximum int) (int, error) {
	raw, ok := members[key]
	if !ok {
		return 0, projectionErrorf(ReasonInvalidProjection, "%s is missing", key)
	}
	var value json.Number
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, projectionErrorf(ReasonInvalidProjection, "%s is not a number", key)
	}
	parsed, err := value.Int64()
	if err != nil {
		return 0, projectionErrorf(ReasonInvalidProjection, "%s is not an integer", key)
	}
	if parsed < int64(minimum) || parsed > int64(maximum) {
		return 0, projectionErrorf(ReasonInvalidProjection, "%s is outside [%d,%d]", key, minimum, maximum)
	}
	return int(parsed), nil
}

func projectionNullableInt(members map[string]json.RawMessage, key string, minimum, maximum int) (int, error) {
	if isJSONNull(members[key]) {
		return -1, nil
	}
	return projectionInt(members, key, minimum, maximum)
}

func projectionUint(members map[string]json.RawMessage, key string, minimum int) (uint64, error) {
	value, err := projectionInt(members, key, minimum, maxIntValue)
	if err != nil {
		return 0, err
	}
	return uint64(value), nil
}

func projectionPhase(members map[string]json.RawMessage) (string, error) {
	phase, err := projectionString(members, "phase", 32)
	if err != nil {
		return "", err
	}
	if _, ok := projectionPhases[phase]; !ok {
		return "", projectionErrorf(ReasonInvalidProjection, "phase %q is not in schema %d", phase, ProjectionSchema)
	}
	return phase, nil
}

func projectionTimestamp(members map[string]json.RawMessage, key string, nullable bool) (string, time.Time, error) {
	raw, err := func() (string, error) {
		if nullable {
			return projectionNullableString(members, key, maxTimestampChars)
		}
		return projectionString(members, key, maxTimestampChars)
	}()
	if err != nil || raw == "" {
		return "", time.Time{}, err
	}
	parsed, parseErr := time.Parse(time.RFC3339Nano, raw)
	if parseErr != nil {
		return "", time.Time{}, projectionErrorf(ReasonInvalidProjection, "%s is not an RFC 3339 timestamp", key)
	}
	return raw, parsed, nil
}

// projectionProcess decodes the nested process block with the same exact-member
// discipline as the projection itself.
func projectionProcess(members map[string]json.RawMessage) (*ProcessRef, error) {
	raw := members["process"]
	if isJSONNull(raw) {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, projectionErrorf(ReasonInvalidProjection, "process is not an object")
	}
	if len(fields) != 2 {
		return nil, projectionErrorf(ReasonInvalidProjection, "process must carry exactly pid and start_token")
	}
	pid, err := projectionInt(fields, "pid", 1, maxProjectionPID)
	if err != nil {
		return nil, err
	}
	token, err := projectionString(fields, "start_token", maxStartTokenChars)
	if err != nil {
		return nil, err
	}
	return &ProcessRef{PID: pid, StartToken: token}, nil
}
