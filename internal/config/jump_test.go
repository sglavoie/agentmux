package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// [defaults.jump] is retired: the bracket chords it configured are unbound, and
// `n`/`N` walk the Needs You group without consulting it. What these tests pin
// is the compatibility contract that outlived the feature — a file written
// against the old dashboard still loads, still validates, and is reported as
// deprecated rather than rewritten. See TestLoad_JumpIsReportedAsDeprecated for
// the notice itself.

// TestLoad_JumpDefaults verifies that omitting [defaults.jump] still fills the
// canonical attention-state set. The fill is kept so the field's zero value
// cannot be mistaken for a deliberate empty set by anything that reads it
// during a migration; nothing in the TUI reads it at all.
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

// TestLoad_JumpEmptyListFillsDefaults verifies that an explicit empty list is
// still treated the same as an omitted section, which is the shape the old
// "disable the feature" spelling produced.
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
// preserved verbatim, order included. The load path does not "migrate" the
// value by rewriting or dropping it: the file on disk is the operator's, and an
// upgrade that silently edited it would be a worse answer than a notice.
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

// TestLoad_JumpIsReportedAsDeprecated verifies the notice a retired-but-accepted
// key owes its operator: the table loads without error and is recorded in
// cfg.Deprecations, which the dashboard's health reading surfaces as a
// dismissible configuration warning.
//
// It is reported for the canonical default set too, not only for a customised
// one. The old deprecations in this list say "you wrote the legacy spelling of
// something that still works"; this one says "this no longer selects anything",
// and that is equally true of a value identical to the default.
func TestLoad_JumpIsReportedAsDeprecated(t *testing.T) {
	for name, body := range map[string]string{
		"custom set": "[defaults.jump]\nattention_states = [\"stop\", \"key\"]\n",
		"canonical set": "[defaults.jump]\n" +
			"attention_states = [\"key\", \"error\", \"warning\", \"question\"]\n",
		"empty table": "[defaults.jump]\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := config.Load(writeTempConfig(t, body))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			var found *config.Deprecation
			for i, d := range cfg.Deprecations {
				if d.Key == "defaults.jump.attention_states" {
					found = &cfg.Deprecations[i]
				}
			}
			if found == nil {
				t.Fatalf("no deprecation recorded for [defaults.jump]; got %+v", cfg.Deprecations)
			}
			if found.Location != "[defaults.jump]" {
				t.Errorf("Location: got %q, want the table the operator has to find", found.Location)
			}
			// The health detail renders "key → replacement", so the replacement
			// text is the whole instruction the operator reads. There is nothing
			// to rename it to, so it has to say what to do instead.
			if !strings.Contains(found.Replacement, "delete") || !strings.Contains(found.Replacement, "n/N") {
				t.Errorf("Replacement %q does not say to delete the key or name what replaced it", found.Replacement)
			}
		})
	}
}

// TestLoad_NoJumpTableIsNotDeprecated is the control: a config that never
// mentioned the retired table must not be told to remove it.
func TestLoad_NoJumpTableIsNotDeprecated(t *testing.T) {
	cfg, err := config.Load(writeTempConfig(t, "[defaults]\nsort = \"alpha\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, d := range cfg.Deprecations {
		if strings.HasPrefix(d.Key, "defaults.jump") {
			t.Errorf("reported %+v for a config with no [defaults.jump]", d)
		}
	}
}

// TestValidate_JumpUnknownState verifies that an unknown state name still
// produces a validation error naming the offending value and the valid set.
// Validation is deliberately unchanged by the retirement: a typo that was an
// error before the upgrade stays an error after it, so an operator who fixes
// the reported deprecation is not also silently absolved of a mistake in the
// same table.
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
