package agentloop

// Decoding one published projection: locating the `agent_mux` block inside a
// state document, then validating that block field by field against schema 1.
// The vocabulary and the shape it decodes into live in projection.go; the
// per-field readers it calls live in projection_fields.go.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/aslcontract"
)

// ReadProjection reads the projection nested in the state file at path.
//
// Every failure mode is a typed ProjectionError so the caller can tell an
// unreadable file from a peer speaking a contract set this build does not
// support — the two produce the same "no evidence" verdict but different
// operator remediation.
func ReadProjection(path string) (Projection, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Projection{}, projectionErrorf(ReasonNoState, "%s does not exist", path)
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

	contractSetID, err := requireSupportedContract(members)
	if err != nil {
		return Projection{}, err
	}
	if err := requireExactMembers(members); err != nil {
		return Projection{}, err
	}

	projection, err := decodeProjectionMembers(members, contractSetID)
	if err != nil {
		return Projection{}, err
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

// requireSupportedContract checks the two members that decide whether this
// build may read the block at all, and reports the pairing reason rather than
// the corruption one for either failure.
func requireSupportedContract(members map[string]json.RawMessage) (string, error) {
	version, err := projectionInt(members, "schema_version", 0, maxIntValue)
	if err != nil || version != ProjectionSchema {
		return "", projectionErrorf(ReasonUnsupportedContract,
			"projection schema %s is not %d", string(members["schema_version"]), ProjectionSchema)
	}
	contractSetID, err := projectionString(members, "contract_set_id", 128)
	if err != nil {
		return "", projectionErrorf(ReasonUnsupportedContract, "%v", err)
	}
	// Match reviewed exact identities. A change to unrelated bridge methods
	// does not invalidate identical loop components; unknown peers stay refused.
	if !aslcontract.SupportsLoopContract(contractSetID) {
		return "", projectionErrorf(ReasonUnsupportedContract,
			"contract set %q is not %q", contractSetID, ContractSetID)
	}
	return contractSetID, nil
}

// decodeProjectionMembers reads every payload field of an already
// contract-checked, exact-membered block. Any failure here is a malformed
// projection, never a pairing problem.
func decodeProjectionMembers(members map[string]json.RawMessage, contractSetID string) (Projection, error) {
	projection := Projection{ContractSetID: contractSetID}
	var err error
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
