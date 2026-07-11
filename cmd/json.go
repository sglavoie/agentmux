package cmd

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// JSONSchemaVersion is the version of the common command envelope.  Command
// payloads may grow additively while this value remains stable for v1 clients.
const JSONSchemaVersion = 1

// JSONEnvelope wraps successful non-interactive command output.
type JSONEnvelope struct {
	SchemaVersion int `json:"schema_version"`
	Data          any `json:"data"`
}

// JSONErrorEnvelope wraps a command failure without relying on stderr text.
type JSONErrorEnvelope struct {
	SchemaVersion int       `json:"schema_version"`
	Error         JSONError `json:"error"`
}

// Selector records a user-provided entity selector.  Commands use it instead
// of silently preferring one possible title/path match.
type Selector struct {
	Value string `json:"value"`
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

func writeJSON(cmd *cobra.Command, data any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetEscapeHTML(false)
	return encoder.Encode(JSONEnvelope{SchemaVersion: JSONSchemaVersion, Data: data})
}

func writeJSONError(cmd *cobra.Command, payload JSONError) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(JSONErrorEnvelope{SchemaVersion: JSONSchemaVersion, Error: payload}); err != nil {
		return err
	}
	return &ExitError{Code: 1, Err: errors.New(payload.Message), Quiet: true}
}

// writeJSONPartial emits the normal schema-v1 data envelope for work that
// completed durably before a later step failed. The quiet nonzero exit status
// lets scripts distinguish a launch failure without throwing away the
// finalized artifact path carried in data.
func writeJSONPartial(cmd *cobra.Command, data any, err error) error {
	if writeErr := writeJSON(cmd, data); writeErr != nil {
		return writeErr
	}
	return &ExitError{Code: 1, Err: err, Quiet: true}
}

func commandFailure(cmd *cobra.Command, jsonOutput bool, code string, err error) error {
	if !jsonOutput {
		return err
	}
	message := "command failed"
	if err != nil {
		message = err.Error()
	}
	return writeJSONError(cmd, JSONError{Code: code, Message: message})
}

func missingParameter(cmd *cobra.Command, jsonOutput bool, name, description string) error {
	message := fmt.Sprintf("%s is required", name)
	if !jsonOutput {
		return errors.New(message)
	}
	return writeJSONError(cmd, JSONError{
		Code: "missing_parameter", Message: message,
		Parameters: []ParameterHelp{{Name: name, Description: description, Required: true}},
	})
}
