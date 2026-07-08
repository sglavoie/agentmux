package config_test

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestValidate_HarnessEmojisUnknownHarnessRejected pins that an entry keyed
// by an unsupported harness name is reported with the valid set appended.
func TestValidate_HarnessEmojisUnknownHarnessRejected(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊", "bogus": "🐙"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unknown harness emoji key, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `[harness_emojis] unknown harness "bogus"`) {
		t.Errorf("expected unknown-harness error, got %q", msg)
	}
	if !strings.Contains(msg, "valid harnesses:") {
		t.Errorf("expected message to list valid harnesses, got %q", msg)
	}
}

// TestValidate_HarnessEmojisEmptyValueRejected pins that an empty string
// value is rejected — every present entry must carry an emoji.
func TestValidate_HarnessEmojisEmptyValueRejected(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": ""},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for empty harness emoji value, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[harness_emojis].claude") {
		t.Errorf("expected per-harness location in error, got %q", msg)
	}
}

// TestValidate_HarnessEmojisMultiGraphemeValueRejected pins that a value
// like "ab" — two width-1 characters that sum to display width 2 — is
// rejected even though the bare width check alone would accept it.
func TestValidate_HarnessEmojisMultiGraphemeValueRejected(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "ab"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for multi-grapheme harness emoji value, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[harness_emojis].claude") {
		t.Errorf("expected per-harness location in error, got %q", msg)
	}
	if !strings.Contains(msg, `"ab"`) {
		t.Errorf("expected offending value in error, got %q", msg)
	}
}

// TestValidate_HarnessEmojisSingleEmojiAccepted pins that a typical
// single-emoji value validates cleanly.
func TestValidate_HarnessEmojisSingleEmojiAccepted(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("single-emoji harness emoji must validate cleanly: %v", err)
	}
}

// TestValidate_HarnessEmojisDuplicateNormalizedKeysRejected pins that two
// spellings of the same harness key (e.g. "Claude" and "claude") surface
// as a duplicate-key error instead of one silently overwriting the other.
func TestValidate_HarnessEmojisDuplicateNormalizedKeysRejected(t *testing.T) {
	path := writeTempConfig(t, `
[harness_emojis]
Claude = "🦊"
claude = "🐙"

[[profiles]]
name = "work"
harness = "claude"
home_dir = "/tmp/h"
color = "blue"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for duplicate normalized harness emoji keys, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[harness_emojis] duplicate keys normalize to the same harness") {
		t.Errorf("expected duplicate-key error, got %q", msg)
	}
	if !strings.Contains(msg, "Claude") || !strings.Contains(msg, "claude") {
		t.Errorf("expected error to include both raw keys, got %q", msg)
	}
}

// TestHarnessEmoji_NormalizesLookupKey pins that Config.HarnessEmoji
// accepts raw or effective harness names interchangeably.
func TestHarnessEmoji_NormalizesLookupKey(t *testing.T) {
	path := writeTempConfig(t, `
[harness_emojis]
Claude = "🦊"

[[profiles]]
name = "work"
harness = "claude"
home_dir = "/tmp/h"
color = "blue"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := cfg.HarnessEmoji("CLAUDE"); got != "🦊" {
		t.Errorf("HarnessEmoji(%q): got %q, want %q", "CLAUDE", got, "🦊")
	}
	if got := cfg.HarnessEmoji("claude"); got != "🦊" {
		t.Errorf("HarnessEmoji(%q): got %q, want %q", "claude", got, "🦊")
	}
	if _, ok := cfg.HarnessEmojis["Claude"]; ok {
		t.Errorf("HarnessEmojis retained unnormalized key %q: %#v", "Claude", cfg.HarnessEmojis)
	}
}

// TestValidate_HarnessEmojisMissingEntryCitesProfiles pins that the
// validator names every profile that references a harness without an entry
// in [harness_emojis], modeled on the missing-color check from the prior
// [harness_colors] suite.
func TestValidate_HarnessEmojisMissingEntryCitesProfiles(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
			{Name: "personal", Harness: "copilot", HomeDir: "/tmp/h", Color: "green"},
			{Name: "side", Harness: "copilot", HomeDir: "/tmp/h", Color: "purple"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for harness with no [harness_emojis] entry, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `[harness_emojis] is missing an entry for harness "copilot"`) {
		t.Errorf("expected missing-entry error, got %q", msg)
	}
	if !strings.Contains(msg, "personal") || !strings.Contains(msg, "side") {
		t.Errorf("expected message to list referring profiles, got %q", msg)
	}
}

// TestValidate_HarnessEmojisParserCollisionRejected pins that any glyph the
// title parser would consume as a leading marker is rejected — U+FE0F
// variation-selector variants of state icons and the star/mute/loop markers
// included — and that the error names the marker that consumed it. The
// validator round-trips the value through palette.ParseTitleParts, so it
// cannot drift from the parser's fallback rules.
func TestValidate_HarnessEmojisParserCollisionRejected(t *testing.T) {
	cases := []struct {
		name   string
		emoji  string
		marker string
	}{
		{"plain warning fallback", "⚠", "warning"},
		{"bolt with variation selector", "⚡️", "bolt"},
		{"star marker", "★", "star marker"},
		{"mute marker", "🔇", "mute marker"},
		{"loop marker", "🔁", "loop marker"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				HarnessEmojis: map[string]string{"claude": tc.emoji},
				Profiles: []config.Account{
					{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
				},
			}
			err := config.Validate(cfg)
			if err == nil {
				t.Fatalf("expected validation error for parser-collision emoji %q, got nil", tc.emoji)
			}
			msg := err.Error()
			if !strings.Contains(msg, "[harness_emojis].claude") {
				t.Errorf("expected per-harness location in error, got %q", msg)
			}
			if !strings.Contains(msg, tc.marker) {
				t.Errorf("expected error to name the %q marker, got %q", tc.marker, msg)
			}
		})
	}
}

// TestValidate_HarnessEmojisBenignEmojiAccepted pins that a glyph the title
// parser does not consume (🦀) still validates cleanly under the round-trip
// probe — the collision check must not over-reject ordinary emojis.
func TestValidate_HarnessEmojisBenignEmojiAccepted(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦀"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("benign emoji must validate cleanly: %v", err)
	}
}

// TestValidate_HarnessEmojisStateIconCollisionRejected pins that a glyph
// listed in the palette state-icon table is rejected. Otherwise the tab
// title parser would consume the harness emoji as if it were a state, and
// every tab would render with that spurious state icon.
func TestValidate_HarnessEmojisStateIconCollisionRejected(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🤖"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for state-icon-collision harness emoji, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[harness_emojis].claude") {
		t.Errorf("expected per-harness location in error, got %q", msg)
	}
	if !strings.Contains(msg, "subagent") {
		t.Errorf("expected error to name the colliding state, got %q", msg)
	}
}
