package cli

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitErrorReportsWrappedMessage(t *testing.T) {
	err := &ExitError{Code: ExitInvalidInput, Err: errors.New("unreadable scenario")}
	if got, want := err.Error(), "unreadable scenario"; got != want {
		t.Fatalf("Error() = %q; want %q", got, want)
	}
}

// A scenario harness that reached a negative verdict has already printed its
// own report, so the exit carries a code with no underlying error. The bare
// status line is what Execute would otherwise print as an empty message.
func TestExitErrorWithoutCauseReportsStatusLine(t *testing.T) {
	err := &ExitError{Code: ExitScenarioFailure}
	if got, want := err.Error(), "exit status 2"; got != want {
		t.Fatalf("Error() = %q; want %q", got, want)
	}
}

// Execute recovers the status code with errors.As, so the wrapping a command
// applies on the way out must not hide it.
func TestExitErrorIsRecoverableThroughWrapping(t *testing.T) {
	cause := errors.New("boom")
	wrapped := fmt.Errorf("running gate: %w", &ExitError{Code: ExitScenarioFailure, Err: cause, Quiet: true})

	var exitErr *ExitError
	if !errors.As(wrapped, &exitErr) {
		t.Fatalf("errors.As did not recover the exit error from %v", wrapped)
	}
	if exitErr.Code != ExitScenarioFailure {
		t.Fatalf("Code = %d; want %d", exitErr.Code, ExitScenarioFailure)
	}
	if !exitErr.Quiet {
		t.Fatal("Quiet was lost through wrapping")
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("Unwrap did not expose the underlying cause")
	}
}

// The three codes are a consumer contract: automation branches on the process
// status alone, so a bad invocation must stay distinguishable from a negative
// verdict, and neither may collide with the generic failure status 1.
func TestExitCodesAreTheDocumentedContract(t *testing.T) {
	if ExitScenarioFailure != 2 {
		t.Errorf("ExitScenarioFailure = %d; want 2", ExitScenarioFailure)
	}
	if ExitInvalidInput != 3 {
		t.Errorf("ExitInvalidInput = %d; want 3", ExitInvalidInput)
	}
	if ExitHealthFailure != 2 {
		t.Errorf("ExitHealthFailure = %d; want 2", ExitHealthFailure)
	}
	if ExitInvalidInput == ExitScenarioFailure {
		t.Error("a bad invocation must not be reported as a negative verdict")
	}
	for name, code := range map[string]int{
		"ExitScenarioFailure": ExitScenarioFailure,
		"ExitInvalidInput":    ExitInvalidInput,
		"ExitHealthFailure":   ExitHealthFailure,
	} {
		if code <= 1 {
			t.Errorf("%s = %d; must not collide with success or the generic failure", name, code)
		}
	}
}
