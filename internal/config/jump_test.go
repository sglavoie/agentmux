package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestLoad_JumpDefaults verifies that omitting [defaults.jump] fills the
// canonical attention-state set so the TUI bracket-key navigation has a
// usable default without forcing every user to copy the list into config.
func TestLoad_JumpDefaults(t *testing.T) {
	path := writeTempConfig(t, `[defaults]
sort = "alpha"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"key", "error", "warning", "question"}
	if !reflect.DeepEqual(cfg.Defaults.Jump.AttentionStates, want) {
		t.Errorf("Jump.AttentionStates: got %v, want %v", cfg.Defaults.Jump.AttentionStates, want)
	}
}

// TestLoad_JumpEmptyListFillsDefaults verifies that an explicit empty list
// is treated the same as an omitted section. Without this, a user who wrote
// `attention_states = []` to "disable" the feature would silently get no
// bracket-key matches at all.
func TestLoad_JumpEmptyListFillsDefaults(t *testing.T) {
	path := writeTempConfig(t, `[defaults.jump]
attention_states = []
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"key", "error", "warning", "question"}
	if !reflect.DeepEqual(cfg.Defaults.Jump.AttentionStates, want) {
		t.Errorf("Jump.AttentionStates: got %v, want %v", cfg.Defaults.Jump.AttentionStates, want)
	}
}

// TestLoad_JumpCustomListPreserved verifies that a user-supplied list is
// preserved verbatim (order included) so the Model can rely on the slice
// directly when building its predicate.
func TestLoad_JumpCustomListPreserved(t *testing.T) {
	path := writeTempConfig(t, `[defaults.jump]
attention_states = ["stop", "key"]
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"stop", "key"}
	if !reflect.DeepEqual(cfg.Defaults.Jump.AttentionStates, want) {
		t.Errorf("Jump.AttentionStates: got %v, want %v", cfg.Defaults.Jump.AttentionStates, want)
	}
}

// TestValidate_JumpUnknownState verifies that an unknown state name produces
// a validation error mentioning both the offending value and the set of
// valid states. The error message helps the user fix their config without
// reading the source.
func TestValidate_JumpUnknownState(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	cfg.Defaults.Jump.AttentionStates = []string{"bogus"}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unknown jump state")
	}
	msg := err.Error()
	if !strings.Contains(msg, "defaults.jump.attention_states") {
		t.Errorf("error %q does not mention defaults.jump.attention_states", msg)
	}
	if !strings.Contains(msg, `"bogus"`) {
		t.Errorf("error %q does not quote offending state %q", msg, "bogus")
	}
	if !strings.Contains(msg, "valid states") {
		t.Errorf("error %q does not list valid states", msg)
	}
}

// TestValidate_JumpKnownStatesAccepted verifies that every state in
// palette.StateOrder() passes validation. Pinning this here means a future
// addition to palette must either expand the test or break it loudly.
func TestValidate_JumpKnownStatesAccepted(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	cfg.Defaults.Jump.AttentionStates = []string{"bolt", "stop", "key", "warning", "error", "subagent", "compact", "question"}
	if err := config.Validate(cfg); err != nil {
		t.Errorf("validation rejected known states: %v", err)
	}
}
