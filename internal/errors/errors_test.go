package errors

import (
	"errors"
	"strings"
	"testing"
)

// TestKittyError_WhitespaceOnlyOutputFallsBackToErr pins the fix that prevents
// `Output: "   \n"` (or any whitespace-only stderr capture) from producing the
// useless "output: " message and swallowing the underlying exec error. After
// TrimSpace, an empty trimmed output must fall through to the `Err` branch so
// the caller actually sees what went wrong.
func TestKittyError_WhitespaceOnlyOutputFallsBackToErr(t *testing.T) {
	underlying := errors.New("exit status 1")
	e := &KittyError{
		Command: []string{"kitten", "@", "ls"},
		Output:  "   \n\t",
		Err:     underlying,
	}
	msg := e.Error()
	if strings.Contains(msg, "output:") {
		t.Errorf("whitespace-only output should not produce an 'output:' line, got %q", msg)
	}
	if !strings.Contains(msg, "exit status 1") {
		t.Errorf("expected underlying error to appear in message, got %q", msg)
	}
}

func TestKittyError_RealOutputTrimmed(t *testing.T) {
	e := &KittyError{
		Command: []string{"kitten", "@", "ls"},
		Output:  "  boom: not connected\n",
	}
	msg := e.Error()
	if !strings.Contains(msg, "output: boom: not connected") {
		t.Errorf("expected trimmed output, got %q", msg)
	}
}

func TestKittyError_NoOutputNoErr(t *testing.T) {
	e := &KittyError{Command: []string{"kitten", "@", "ls"}}
	msg := e.Error()
	if !strings.Contains(msg, "kitty command failed: kitten @ ls") {
		t.Errorf("expected bare failure line, got %q", msg)
	}
}

func TestKittyError_Unwrap(t *testing.T) {
	underlying := errors.New("boom")
	e := &KittyError{Err: underlying}
	if !errors.Is(e, underlying) {
		t.Error("errors.Is should match underlying")
	}
}
