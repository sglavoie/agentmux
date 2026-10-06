package errors

import (
	"errors"
	"io"
	"os"
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

func TestKittyError_OutputAndErrIncludesBoth(t *testing.T) {
	e := &KittyError{
		Command: []string{"kitten", "@", "ls"},
		Output:  "boom: not connected\n",
		Err:     errors.New("exit status 1"),
	}
	msg := e.Error()
	if !strings.Contains(msg, "output: boom: not connected") {
		t.Errorf("expected command output, got %q", msg)
	}
	if !strings.Contains(msg, "error: exit status 1") {
		t.Errorf("expected underlying error, got %q", msg)
	}
}

func TestKittyError_NoOutputNoErr(t *testing.T) {
	e := &KittyError{Command: []string{"kitten", "@", "ls"}}
	msg := e.Error()
	if !strings.Contains(msg, "kitty command failed: kitten @ ls") {
		t.Errorf("expected bare failure line, got %q", msg)
	}
}

func TestKittyError_EmptyCommandUsesPlaceholder(t *testing.T) {
	e := &KittyError{Err: errors.New("boom")}
	msg := e.Error()
	if !strings.Contains(msg, "kitty command failed: <unknown command>") {
		t.Fatalf("expected unknown-command placeholder, got %q", msg)
	}
	if !strings.Contains(msg, "error: boom") {
		t.Fatalf("expected underlying error, got %q", msg)
	}
}

func TestKittyError_Unwrap(t *testing.T) {
	underlying := errors.New("boom")
	e := &KittyError{Err: underlying}
	if !errors.Is(e, underlying) {
		t.Error("errors.Is should match underlying")
	}
}

func TestKittyError_NilReceiverIsSafe(t *testing.T) {
	var e *KittyError
	if got := e.Error(); got != "kitty command failed: <nil>" {
		t.Fatalf("nil KittyError Error() = %q", got)
	}
	if got := e.Unwrap(); got != nil {
		t.Fatalf("nil KittyError Unwrap() = %v, want nil", got)
	}
}

func TestConfigError_NilReceiverIsSafe(t *testing.T) {
	var e *ConfigError
	if got := e.Error(); got != "config error: <nil>" {
		t.Fatalf("nil ConfigError Error() = %q", got)
	}
}

func TestPrint_NilErrorDoesNotPrintMalformedMessage(t *testing.T) {
	output := captureStderr(t, func() {
		Print(nil)
	})
	if output != "" {
		t.Fatalf("Print(nil) wrote %q, want no output", output)
	}
}

func TestPrint_ErrorWritesMessage(t *testing.T) {
	output := captureStderr(t, func() {
		Print(errors.New("boom"))
	})
	if !strings.Contains(output, "error:") {
		t.Fatalf("Print(error) output = %q, want error prefix", output)
	}
	if !strings.Contains(output, "boom") {
		t.Fatalf("Print(error) output = %q, want error message", output)
	}
}

// Redirected stderr is not a terminal, so it must not carry escape sequences.
func TestPrint_RedirectedOutputIsPlain(t *testing.T) {
	output := captureStderr(t, func() {
		Print(errors.New("boom"))
	})
	if output != "error: boom\n" {
		t.Fatalf("Print to a pipe = %q, want plain text", output)
	}
}

func TestPrint_TerminalOutputIsRed(t *testing.T) {
	original := colorEnabledFor
	colorEnabledFor = func(*os.File) bool { return true }
	t.Cleanup(func() { colorEnabledFor = original })

	output := captureStderr(t, func() {
		Print(errors.New("boom"))
	})
	if output != colorRed+"error:"+colorReset+" boom\n" {
		t.Fatalf("Print to a terminal = %q, want a red prefix", output)
	}
}

func TestColorEnabledHonorsNoColor(t *testing.T) {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		t.Skip("no controlling terminal")
	}
	defer func() { _ = tty.Close() }()
	t.Setenv("NO_COLOR", "1")
	if colorEnabled(tty) {
		t.Fatal("NO_COLOR must disable color even on a terminal")
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = original
	})

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return string(data)
}
