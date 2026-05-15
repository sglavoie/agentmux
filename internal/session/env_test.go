package session_test

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestResolveFlags_Empty(t *testing.T) {
	sess := config.Session{ClaudeFlags: nil}
	if got := session.ResolveFlags(sess, config.Account{}, config.Defaults{}); got != "" {
		t.Errorf("ResolveFlags(nil) = %q, want %q", got, "")
	}
}

func TestResolveFlags_Single(t *testing.T) {
	sess := config.Session{ClaudeFlags: []string{"--dangerously-skip-permissions"}}
	want := "--dangerously-skip-permissions"
	if got := session.ResolveFlags(sess, config.Account{}, config.Defaults{}); got != want {
		t.Errorf("ResolveFlags = %q, want %q", got, want)
	}
}

func TestResolveFlags_Multiple(t *testing.T) {
	sess := config.Session{ClaudeFlags: []string{"--flag-a", "--flag-b"}}
	want := "--flag-a --flag-b"
	if got := session.ResolveFlags(sess, config.Account{}, config.Defaults{}); got != want {
		t.Errorf("ResolveFlags = %q, want %q", got, want)
	}
}

func TestResolveFlags_DefaultsFallback(t *testing.T) {
	sess := config.Session{ClaudeFlags: nil}
	defaults := config.Defaults{ClaudeFlags: []string{"--dangerously-skip-permissions"}}
	want := "--dangerously-skip-permissions"
	if got := session.ResolveFlags(sess, config.Account{}, defaults); got != want {
		t.Errorf("ResolveFlags fallback = %q, want %q", got, want)
	}
}

func TestResolveFlags_AccountFallback(t *testing.T) {
	sess := config.Session{}
	acc := config.Account{Args: []string{"--account-flag"}}
	want := "--account-flag"
	if got := session.ResolveFlags(sess, acc, config.Defaults{}); got != want {
		t.Errorf("ResolveFlags account fallback = %q, want %q", got, want)
	}
}

func TestResolveFlags_ProjectOverridesAccount(t *testing.T) {
	sess := config.Session{Args: []string{"--project-flag"}}
	acc := config.Account{Args: []string{"--account-flag"}}
	want := "--project-flag"
	if got := session.ResolveFlags(sess, acc, config.Defaults{}); got != want {
		t.Errorf("ResolveFlags project over account = %q, want %q", got, want)
	}
}

func TestResolveFlags_AccountOverridesDefaults(t *testing.T) {
	sess := config.Session{}
	acc := config.Account{Args: []string{"--account-flag"}}
	defaults := config.Defaults{ClaudeFlags: []string{"--defaults-flag"}}
	want := "--account-flag"
	if got := session.ResolveFlags(sess, acc, defaults); got != want {
		t.Errorf("ResolveFlags account over defaults = %q, want %q", got, want)
	}
}

func TestResolveFlags_LegacyClaudeFlagsSession(t *testing.T) {
	sess := config.Session{ClaudeFlags: []string{"--legacy-flag"}}
	want := "--legacy-flag"
	if got := session.ResolveFlags(sess, config.Account{}, config.Defaults{}); got != want {
		t.Errorf("ResolveFlags legacy ClaudeFlags = %q, want %q", got, want)
	}
}

func TestSerializeSessionEnv_Empty(t *testing.T) {
	if got := session.SerializeSessionEnv(nil); got != "" {
		t.Errorf("SerializeSessionEnv(nil) = %q, want %q", got, "")
	}
	if got := session.SerializeSessionEnv(map[string]string{}); got != "" {
		t.Errorf("SerializeSessionEnv({}) = %q, want %q", got, "")
	}
}

func TestSerializeSessionEnv_Single(t *testing.T) {
	got := session.SerializeSessionEnv(map[string]string{"KEY": "value"})
	if got != "KEY=value" {
		t.Errorf("SerializeSessionEnv = %q, want %q", got, "KEY=value")
	}
}

func TestSerializeSessionEnv_MultiSorted(t *testing.T) {
	// Keys should be sorted for stable output.
	env := map[string]string{"Z_KEY": "z", "A_KEY": "a"}
	got := session.SerializeSessionEnv(env)
	want := "A_KEY=a\x1fZ_KEY=z"
	if got != want {
		t.Errorf("SerializeSessionEnv = %q, want %q", got, want)
	}
}

func TestResolveEnv_SetsCLAUDE_CONFIG_DIR(t *testing.T) {
	acc := config.Account{
		Name:      "work",
		ConfigDir: "~/.config/claude-work",
		Color:     "blue",
	}
	sess := config.Session{Account: "work"}
	env := session.ResolveEnv(sess, acc)
	if _, ok := env["CLAUDE_CONFIG_DIR"]; !ok {
		t.Error("expected CLAUDE_CONFIG_DIR to be set for non-default config dir")
	}
}

func TestResolveEnv_SessionOverrides(t *testing.T) {
	acc := config.Account{Name: "work", ConfigDir: "~/.config/claude-work"}
	sess := config.Session{
		Account: "work",
		Env:     map[string]string{"DEBUG": "1", "NODE_ENV": "development"},
	}
	env := session.ResolveEnv(sess, acc)
	if env["DEBUG"] != "1" {
		t.Errorf("DEBUG: got %q, want %q", env["DEBUG"], "1")
	}
	if env["NODE_ENV"] != "development" {
		t.Errorf("NODE_ENV: got %q, want %q", env["NODE_ENV"], "development")
	}
}
