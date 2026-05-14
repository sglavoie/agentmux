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
	cmd := strings.Join(e.Command, " ")
	if e.Output != "" {
		return fmt.Sprintf("kitty command failed: %s\n  output: %s", cmd, strings.TrimSpace(e.Output))
	}
	if e.Err != nil {
		return fmt.Sprintf("kitty command failed: %s\n  error: %s", cmd, e.Err)
	}
	return fmt.Sprintf("kitty command failed: %s", cmd)
}

func (e *KittyError) Unwrap() error { return e.Err }

// Fatal prints a red "error: " prefixed message to stderr and exits 1.
func Fatal(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%serror:%s %s\n", colorRed, colorReset, msg)
	os.Exit(1)
}

// Print prints an error to stderr without exiting.
func Print(err error) {
	fmt.Fprintf(os.Stderr, "%serror:%s %s\n", colorRed, colorReset, err)
}
