package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newCapturingCommand() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "probe"}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	return cmd, &out
}

func TestWriteJSONEmitsTheVersionedDataEnvelope(t *testing.T) {
	cmd, out := newCapturingCommand()

	if err := WriteJSON(cmd, map[string]string{"title": "api"}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var envelope struct {
		SchemaVersion int               `json:"schema_version"`
		Data          map[string]string `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	if envelope.SchemaVersion != JSONSchemaVersion {
		t.Errorf("schema_version = %d; want %d", envelope.SchemaVersion, JSONSchemaVersion)
	}
	if envelope.Data["title"] != "api" {
		t.Errorf("data = %v; want the command payload", envelope.Data)
	}
}

// HTML escaping is off so a directory or branch name containing &, < or > round
// trips as itself instead of reaching the caller as &.
func TestWriteJSONDoesNotEscapeHTML(t *testing.T) {
	cmd, out := newCapturingCommand()

	if err := WriteJSON(cmd, map[string]string{"branch": "feature/a&b<c>"}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if !strings.Contains(out.String(), "feature/a&b<c>") {
		t.Fatalf("output = %q; want the unescaped value", out.String())
	}
}

func TestWriteJSONErrorEmitsTheStructuredErrorEnvelopeAndQuietExit(t *testing.T) {
	cmd, out := newCapturingCommand()

	err := WriteJSONError(cmd, JSONError{Code: "ambiguous_selector", Message: "selector matches 2 worktrees"})

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v; want an ExitError", err)
	}
	if exitErr.Code != 1 || !exitErr.Quiet {
		t.Fatalf("exit = %+v; want a quiet status 1", exitErr)
	}

	var envelope JSONErrorEnvelope
	if decodeErr := json.Unmarshal(out.Bytes(), &envelope); decodeErr != nil {
		t.Fatalf("decode %q: %v", out.String(), decodeErr)
	}
	if envelope.SchemaVersion != JSONSchemaVersion {
		t.Errorf("schema_version = %d; want %d", envelope.SchemaVersion, JSONSchemaVersion)
	}
	if envelope.Error.Code != "ambiguous_selector" {
		t.Errorf("code = %q; want the caller's code", envelope.Error.Code)
	}
	if envelope.Error.Message != "selector matches 2 worktrees" {
		t.Errorf("message = %q; want the caller's message", envelope.Error.Message)
	}
}

// Partial durable success keeps the data envelope: the finalized artifact path
// must survive the later failure, and only the exit status reports it.
func TestWriteJSONPartialKeepsTheDataEnvelope(t *testing.T) {
	cmd, out := newCapturingCommand()

	err := WriteJSONPartial(cmd, map[string]string{"handoff_path": "/tmp/pack.md"}, errors.New("launch failed"))

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err = %v; want an ExitError", err)
	}
	if exitErr.Code != 1 || !exitErr.Quiet {
		t.Fatalf("exit = %+v; want a quiet status 1", exitErr)
	}
	if exitErr.Err == nil || exitErr.Err.Error() != "launch failed" {
		t.Fatalf("exit cause = %v; want the failing step's error", exitErr.Err)
	}

	var envelope jsonEnvelope
	if decodeErr := json.Unmarshal(out.Bytes(), &envelope); decodeErr != nil {
		t.Fatalf("decode %q: %v", out.String(), decodeErr)
	}
	if envelope.SchemaVersion != JSONSchemaVersion {
		t.Errorf("schema_version = %d; want the success envelope", envelope.SchemaVersion)
	}
	if strings.Contains(out.String(), "\"error\"") {
		t.Errorf("output = %q; a partial success must not emit an error envelope", out.String())
	}
}

// A text caller gets its error back untouched; only a --json caller pays for
// the envelope.
func TestCommandFailurePassesTextErrorsThrough(t *testing.T) {
	cmd, out := newCapturingCommand()
	cause := errors.New("worktree is dirty")

	if err := CommandFailure(cmd, false, "dirty_worktree", cause); !errors.Is(err, cause) {
		t.Fatalf("err = %v; want the original error", err)
	}
	if out.Len() != 0 {
		t.Fatalf("text mode wrote %q to stdout", out.String())
	}
}

func TestCommandFailureWithoutCauseStillCarriesACode(t *testing.T) {
	cmd, out := newCapturingCommand()

	if err := CommandFailure(cmd, true, "command_failure", nil); err == nil {
		t.Fatal("CommandFailure returned nil for a JSON caller")
	}

	var envelope JSONErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	if envelope.Error.Code != "command_failure" {
		t.Errorf("code = %q; want the caller's code", envelope.Error.Code)
	}
	if envelope.Error.Message != "command failed" {
		t.Errorf("message = %q; want the generic fallback", envelope.Error.Message)
	}
}

func TestMissingParameterTellsAJSONCallerHowToRecover(t *testing.T) {
	cmd, out := newCapturingCommand()

	if err := MissingParameter(cmd, true, "branch", "target branch name"); err == nil {
		t.Fatal("MissingParameter returned nil for a JSON caller")
	}

	var envelope JSONErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	if envelope.Error.Code != "missing_parameter" {
		t.Errorf("code = %q; want missing_parameter", envelope.Error.Code)
	}
	if envelope.Error.Message != "branch is required" {
		t.Errorf("message = %q; want the named parameter", envelope.Error.Message)
	}
	want := ParameterHelp{Name: "branch", Description: "target branch name", Required: true}
	if len(envelope.Error.Parameters) != 1 || envelope.Error.Parameters[0] != want {
		t.Errorf("parameters = %+v; want %+v", envelope.Error.Parameters, want)
	}
}

func TestMissingParameterStaysPlainForTextCallers(t *testing.T) {
	cmd, out := newCapturingCommand()

	err := MissingParameter(cmd, false, "branch", "target branch name")
	if err == nil || err.Error() != "branch is required: target branch name" {
		t.Fatalf("err = %v; want the requirement followed by its description", err)
	}
	if out.Len() != 0 {
		t.Fatalf("text mode wrote %q to stdout", out.String())
	}
}

// Candidates are how an ambiguous selector stays safe: the command never
// guesses, it reports the ordered alternatives it found.
func TestJSONErrorCarriesOrderedCandidates(t *testing.T) {
	cmd, out := newCapturingCommand()

	_ = WriteJSONError(cmd, JSONError{
		Code:    "ambiguous_selector",
		Message: "selector matches 2 worktrees",
		Candidates: []Candidate{
			{ID: "api", DisplayName: "API", CanonicalPath: "/w/api"},
			{ID: "api-2", DisplayName: "API 2"},
		},
	})

	var envelope JSONErrorEnvelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	if len(envelope.Error.Candidates) != 2 {
		t.Fatalf("candidates = %+v; want both, in order", envelope.Error.Candidates)
	}
	if envelope.Error.Candidates[0].ID != "api" || envelope.Error.Candidates[1].ID != "api-2" {
		t.Errorf("candidate order = %+v; want the caller's order preserved", envelope.Error.Candidates)
	}
	// An omitted canonical path must not appear as an empty string field.
	if strings.Count(out.String(), "canonical_path") != 1 {
		t.Errorf("output = %q; want canonical_path only on the candidate that has one", out.String())
	}
}
