package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// JSONSchemaVersion is the version of the common command envelope.  Command
// payloads may grow additively while this value remains stable for v1 clients.
const JSONSchemaVersion = 1

// jsonEnvelope wraps successful non-interactive command output. Unlike the
// error envelope it is unexported: `Data any` makes it useless as a decode
// target, so every consumer decodes the schema-v1 success shape into a struct
// carrying its own command payload type instead.
type jsonEnvelope struct {
	SchemaVersion int `json:"schema_version"`
	Data          any `json:"data"`
}

// JSONErrorEnvelope wraps a command failure without relying on stderr text.
type JSONErrorEnvelope struct {
	SchemaVersion int       `json:"schema_version"`
	Error         JSONError `json:"error"`
}

// Candidate is a safe, deterministic summary for an ambiguous selector.
type Candidate struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	CanonicalPath string `json:"canonical_path,omitempty"`
}

// ParameterHelp lets JSON callers recover from missing input without an
// interactive prompt.
type ParameterHelp struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

// JSONError is the stable machine-readable command error payload.
type JSONError struct {
	Code       string          `json:"code"`
	Message    string          `json:"message"`
	Candidates []Candidate     `json:"candidates,omitempty"`
	Parameters []ParameterHelp `json:"parameters,omitempty"`
}

// WriteJSON emits the schema-v1 data envelope on the command's stdout.
func WriteJSON(cmd *cobra.Command, data any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetEscapeHTML(false)
	return encoder.Encode(jsonEnvelope{SchemaVersion: JSONSchemaVersion, Data: data})
}

// WriteJSONError emits the common structured error envelope and returns the
// quiet nonzero exit that keeps Execute from appending a redundant stderr line.
func WriteJSONError(cmd *cobra.Command, payload JSONError) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(JSONErrorEnvelope{SchemaVersion: JSONSchemaVersion, Error: payload}); err != nil {
		return err
	}
	return &ExitError{Code: 1, Err: errors.New(payload.Message), Quiet: true}
}

// WriteJSONPartial emits the normal schema-v1 data envelope for work that
// completed durably before a later step failed. The quiet nonzero exit status
// lets scripts distinguish a launch failure without throwing away the
// finalized artifact path carried in data.
func WriteJSONPartial(cmd *cobra.Command, data any, err error) error {
	if writeErr := WriteJSON(cmd, data); writeErr != nil {
		return writeErr
	}
	return &ExitError{Code: 1, Err: err, Quiet: true}
}

// CommandFailure maps a command failure onto the caller's chosen output mode:
// the bare error for text callers, the structured envelope for JSON callers.
func CommandFailure(cmd *cobra.Command, jsonOutput bool, code string, err error) error {
	if !jsonOutput {
		return err
	}
	message := "command failed"
	if err != nil {
		message = err.Error()
	}
	return WriteJSONError(cmd, JSONError{Code: code, Message: message})
}

// MissingParameter reports required input that the invocation did not supply,
// carrying the parameter description so a JSON caller can recover without an
// interactive prompt.
func MissingParameter(cmd *cobra.Command, jsonOutput bool, name, description string) error {
	message := fmt.Sprintf("%s is required", name)
	if !jsonOutput {
		return errors.New(message)
	}
	return WriteJSONError(cmd, JSONError{
		Code: "missing_parameter", Message: message,
		Parameters: []ParameterHelp{{Name: name, Description: description, Required: true}},
	})
}
