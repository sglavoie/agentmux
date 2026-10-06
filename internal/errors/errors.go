package errors

import (
	"fmt"
	"os"
	"strings"
)

const (
	colorRed   = "\033[31m"
	colorReset = "\033[0m"
)

// ConfigError is a config validation or load failure.
type ConfigError struct {
	Message string
	Hint    string
}

func (e *ConfigError) Error() string {
	if e == nil {
		return "config error: <nil>"
	}
	if e.Hint != "" {
		return fmt.Sprintf("%s\n  → %s", e.Message, e.Hint)
	}
	return e.Message
}

// KittyError is a failure from a kitten @ command.
type KittyError struct {
	Command []string
	Output  string
	Err     error
}

func (e *KittyError) Error() string {
	if e == nil {
		return "kitty command failed: <nil>"
	}
	cmd := strings.Join(e.Command, " ")
	if cmd == "" {
		cmd = "<unknown command>"
	}
	trimmed := strings.TrimSpace(e.Output)
	if trimmed != "" {
		if e.Err != nil {
			return fmt.Sprintf("kitty command failed: %s\n  output: %s\n  error: %s", cmd, trimmed, e.Err)
		}
		return fmt.Sprintf("kitty command failed: %s\n  output: %s", cmd, trimmed)
	}
	if e.Err != nil {
		return fmt.Sprintf("kitty command failed: %s\n  error: %s", cmd, e.Err)
	}
	return fmt.Sprintf("kitty command failed: %s", cmd)
}

func (e *KittyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Fatal prints an "error: " prefixed message to stderr and exits 1.
func Fatal(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s %s\n", errorPrefix(os.Stderr), msg)
	os.Exit(1)
}

// Print prints an error to stderr without exiting.
func Print(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s %s\n", errorPrefix(os.Stderr), err)
}

// errorPrefix is red only when it will be read in a terminal. Redirected
// output — a log, a pipe into grep, a CI capture — would otherwise carry raw
// escape sequences, and NO_COLOR (https://no-color.org) opts out entirely.
func errorPrefix(f *os.File) string {
	if !colorEnabled(f) {
		return "error:"
	}
	return colorRed + "error:" + colorReset
}

// colorEnabledFor is a seam for tests, which cannot hand Print a terminal.
var colorEnabledFor = func(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func colorEnabled(f *os.File) bool {
	return f != nil && colorEnabledFor(f)
}
