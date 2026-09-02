package cli

import "fmt"

// Exit status codes for the amx CLI. Public commands surface any failure as the
// generic status 1 through the root command's Execute; the diagnostic surfaces
// and the hidden harnesses (tui-test, release-gate) distinguish a genuine
// regression from a bad invocation so automation can branch on the process exit
// code alone.
const (
	// ExitScenarioFailure marks a harness run that executed cleanly but reached
	// a negative verdict: a failing TUI assertion or detected layout issue, or a
	// release gate whose pair did not pass -- including one whose tagging left a
	// ref behind that has to be cleared before another attempt. The harness did
	// its job; the subject failed.
	ExitScenarioFailure = 2
	// ExitInvalidInput marks a bad invocation: an unknown fixture, an unreadable
	// or malformed scenario, an isolated-config staging failure, or a release
	// gate pointed at a missing checkout, an unreadable signing key, or a pair
	// that is already tagged.
	ExitInvalidInput = 3
	// ExitHealthFailure is the strict-mode exit status when any
	// failure-severity finding is present. It is shared by `amx doctor
	// --strict`, `amx daemon audit`, and `amx debug probe`, which is why it
	// lives beside the harness codes rather than with any one of them.
	ExitHealthFailure = 2
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
