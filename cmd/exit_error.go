package cmd

import "fmt"

// Exit status codes for the amx CLI. Public commands surface any failure as the
// generic status 1 through Execute; the hidden tui-test harness distinguishes a
// genuine scenario regression from a bad invocation so automation can branch on
// the process exit code alone.
const (
	// exitScenarioFailure marks a harness run that executed cleanly but had at
	// least one failing assertion or detected layout issue (a real regression).
	exitScenarioFailure = 2
	// exitInvalidInput marks a bad invocation: an unknown fixture, an unreadable
	// or malformed scenario, or an isolated-config staging failure.
	exitInvalidInput = 3
)

// ExitError carries an explicit process status code out of a Cobra RunE so
// Execute can exit with a harness-specific code instead of the generic 1. When
// Quiet is set the command has already written a complete (usually JSON) result
// to stdout, so Execute must not follow it with a redundant stderr line.
type ExitError struct {
	Code  int
	Err   error
	Quiet bool
}

// Error reports the wrapped error's message, or a bare status line when the
// exit carries no underlying error (a quiet scenario-failure exit).
func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

// Unwrap exposes the underlying error for errors.Is/As traversal.
func (e *ExitError) Unwrap() error { return e.Err }
