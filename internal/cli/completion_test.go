package cli

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestCompleteProfilesFiltersByPrefix(t *testing.T) {
	old := completionLoadConfig
	t.Cleanup(func() { completionLoadConfig = old })
	completionLoadConfig = func() (*config.Config, error) {
		return &config.Config{Profiles: []config.Account{{Name: "codex-work"}, {Name: "claude-work"}, {Name: "codex-home"}}}, nil
	}
	got, _ := CompleteProfiles(nil, nil, "codex")
	if want := []string{"codex-work", "codex-home"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("completions = %v, want %v", got, want)
	}
}

func TestCompleteProfilesDegradesToNothingOnBrokenConfig(t *testing.T) {
	old := completionLoadConfig
	t.Cleanup(func() { completionLoadConfig = old })
	completionLoadConfig = func() (*config.Config, error) { return nil, errors.New("broken") }
	if got, _ := CompleteProfiles(nil, nil, ""); got != nil {
		t.Fatalf("completions = %v, want none", got)
	}
}

func TestCompleteHarnessFirstArgOnlyCompletesTheHarness(t *testing.T) {
	got, _ := CompleteHarnessFirstArg(nil, nil, "c")
	if want := []string{"claude", "copilot", "codex"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("completions = %v, want %v", got, want)
	}
	if got, _ := CompleteHarnessFirstArg(nil, []string{"claude"}, ""); got != nil {
		t.Fatalf("session-id position completed %v, want nothing", got)
	}
}
