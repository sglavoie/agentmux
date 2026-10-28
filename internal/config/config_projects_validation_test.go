package config_test

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestValidate_DuplicateProjectTitlePerProfile(t *testing.T) {
	// Two projects with the same (profile, title) pair are ambiguous: launch dedup
	// keys on this pair, so duplicates would mask one of them and silently confuse
	// the user. Validation must surface this rather than wait for runtime confusion.
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work"},
			{Title: "api", Directory: "/tmp/b", Profile: "work"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for duplicate project title under same profile, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") || !strings.Contains(err.Error(), "api") {
		t.Errorf("expected duplicate/api in error, got %q", err.Error())
	}
}

// TestValidate_DuplicateProjectTitleCaseInsensitive pins the dedup contract:
// launch-time SessionKey lowercases both the profile name and project title
// before deduping, so validation must use the same case-folding. Two projects
// "api" and "API" under profile "work" would silently collide at runtime if
// validation only checked exact-case equality.
func TestValidate_DuplicateProjectTitleCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work"},
			{Title: "API", Directory: "/tmp/b", Profile: "work"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for case-insensitive duplicate title under same profile, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate in error, got %q", err.Error())
	}
}

// TestValidate_DuplicateProjectTitleProfileCaseInsensitive pins the same dedup
// contract on the profile side: "Work" and "work" lowercase to the same key
// in SessionKey, so they share a runtime dedup namespace and validation must
// surface the collision rather than wait for confusing launch behavior.
func TestValidate_DuplicateProjectTitleProfileCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
			{Name: "Work", Harness: "claude", HomeDir: "/tmp/h2"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work"},
			{Title: "api", Directory: "/tmp/b", Profile: "Work"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for case-insensitive profile collision, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate in error, got %q", err.Error())
	}
}

func TestValidate_DuplicateProjectTitleAcrossProfilesAllowed(t *testing.T) {
	// Same title under different profiles is allowed: launch dedup keys on
	// (account, title), so they are distinct sessions.
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/w", Color: "blue"},
			{Name: "personal", Harness: "claude", HomeDir: "/tmp/p", Color: "blue"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work"},
			{Title: "api", Directory: "/tmp/b", Profile: "personal"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("unexpected error for same-title-across-profiles: %v", err)
	}
}

// TestValidate_ProjectProfileReferenceCaseInsensitive pins the contract that
// project.profile references resolve case-insensitively: runtime findAccount
// uses strings.EqualFold, so a project listing profile = "Work" against a
// configured profile name "work" works at launch time and validation must not
// reject it as unknown.
func TestValidate_ProjectProfileReferenceCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "Work"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("unexpected error for case-insensitive profile reference: %v", err)
	}
}

// TestValidate_ProjectEnvKeysRejectUnserializableNames pins that project env
// keys must be representable as KEY=VALUE pairs. Empty keys and keys
// containing "=" cannot round-trip through the session env user-var format,
// so validation should catch them before launch/restart loses or corrupts the
// entry.
func TestValidate_ProjectEnvKeysRejectUnserializableNames(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
		Projects: []config.Session{
			{
				Title:     "api",
				Directory: "/tmp/a",
				Profile:   "work",
				Env: map[string]string{
					"":        "missing",
					"BAD=KEY": "value",
					"DEBUG":   "a=b",
				},
			},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unserializable env keys, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "env key is required") {
		t.Errorf("expected missing env key error, got %q", msg)
	}
	if !strings.Contains(msg, `env key "BAD=KEY" must not contain "="`) {
		t.Errorf("expected '=' env key error, got %q", msg)
	}
	if strings.Contains(msg, "DEBUG") {
		t.Errorf("valid env value containing '=' should not be rejected, got %q", msg)
	}
}

func TestValidate_ProjectEnvRejectsSessionMetadataSeparator(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles:      []config.Account{{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"}},
		Projects: []config.Session{{
			Title: "api", Directory: "/tmp/a", Profile: "work",
			Env: map[string]string{
				"VALID":      "a=b",
				"BAD\x1fKEY": "value",
				"BAD\x00KEY": "value",
				"TOKEN":      "first\x1fINJECTED=second",
				"BINARY":     "first\x00second",
			},
		}},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for env entries that cannot round-trip through session metadata")
	}
	for _, want := range []string{
		`env key "BAD\x1fKEY"`,
		`env key "BAD\x00KEY" must not contain NUL`,
		`env value for "TOKEN"`,
		`env value for "BINARY" must not contain NUL`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("validation error %q does not mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "VALID") {
		t.Errorf("valid env entry rejected: %v", err)
	}
}

func TestValidate_ProjectGroupsRejectSessionMetadataSeparator(t *testing.T) {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Profiles:      []config.Account{{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"}},
		Projects: []config.Session{{
			Title: "api", Directory: "/tmp/a", Profile: "work",
			Groups: []string{"backend\x1finfra"},
		}},
	}
	err := config.Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), `group "backend\x1finfra"`) {
		t.Fatalf("expected a group separator validation error, got %v", err)
	}
}
