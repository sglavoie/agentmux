package errors

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

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
