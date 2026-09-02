package cli

import (
	"context"
	"log/slog"
	"testing"
)

func withDebugFlag(t *testing.T, value bool) {
	t.Helper()
	previous := DebugFlag
	DebugFlag = value
	t.Cleanup(func() { DebugFlag = previous })
}

// The flag is unconditional: an operator who passed --debug gets debug output
// even when the environment says otherwise.
func TestDebugEnabledFlagWinsOverTheEnvironment(t *testing.T) {
	withDebugFlag(t, true)
	t.Setenv("AMX_DEBUG", "0")
	t.Setenv("amx_DEBUG", "off")

	if !DebugEnabled() {
		t.Fatal("--debug did not enable debug output")
	}
}

func TestDebugEnabledReadsBothEnvironmentSpellings(t *testing.T) {
	for _, key := range []string{"amx_DEBUG", "AMX_DEBUG"} {
		t.Run(key, func(t *testing.T) {
			withDebugFlag(t, false)
			t.Setenv(key, "1")

			if !DebugEnabled() {
				t.Fatalf("%s=1 did not enable debug output", key)
			}
		})
	}
}

func TestDebugEnabledAcceptsTheTruthyVocabulary(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", "Yes", "on", " on ", "\ton\n"} {
		t.Run(value, func(t *testing.T) {
			withDebugFlag(t, false)
			t.Setenv("AMX_DEBUG", value)

			if !DebugEnabled() {
				t.Fatalf("AMX_DEBUG=%q did not enable debug output", value)
			}
		})
	}
}

// Anything outside the truthy vocabulary leaves logging quiet, including the
// values an operator most plausibly means as "off".
func TestDebugEnabledRejectsEverythingElse(t *testing.T) {
	for _, value := range []string{"", "0", "false", "no", "off", "maybe"} {
		t.Run(value, func(t *testing.T) {
			withDebugFlag(t, false)
			t.Setenv("AMX_DEBUG", value)

			if DebugEnabled() {
				t.Fatalf("AMX_DEBUG=%q enabled debug output", value)
			}
		})
	}
}

func TestConfigureLoggingSetsTheRequestedLevel(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })

	ConfigureLogging(false)
	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Error("an ordinary run emits debug records")
	}
	if !slog.Default().Enabled(context.Background(), slog.LevelInfo) {
		t.Error("an ordinary run drops info records")
	}

	ConfigureLogging(true)
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Error("a debug run drops debug records")
	}
}
