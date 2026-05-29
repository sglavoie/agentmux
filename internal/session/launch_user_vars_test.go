package session_test

import (
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestBuildUserVars_Parity is the contract guard for the user-var map written
// onto every managed tab. Both Launch and any future re-anchor helper must
// build the same set of keys with the same values — if this fails, hook-side
// consumers (cc_account, cc_config_dir, EffectiveProfile, ...) will see
// inconsistent state. Update the expected map deliberately when changing the
// contract.
func TestBuildUserVars_Parity(t *testing.T) {
	defaults := config.Defaults{
		Sort: "alpha",
		Args: []string{"--shared-default"},
	}
	acc := config.Account{
		Name:    "work",
		Harness: "claude",
		HomeDir: "~/.config/claude-work",
		Color:   "blue",
		Args:    []string{"--profile-default"},
	}
	sess := config.Session{
		Title:     "api",
		Directory: "~/projects/api",
		Profile:   "work",
		Account:   "work",
		Args:      []string{"--prompt", "hello world"},
		Env:       map[string]string{"DEBUG": "1", "PORT": "8080"},
		Groups:    []string{"backend", "infra"},
	}

	homeDir := config.ExpandTilde("~/.config/claude-work")
	projectDir := config.ExpandTilde("~/projects/api")
	quotedArgs := "--prompt 'hello world'"

	want := map[string]string{
		"amx_profile":       "work",
		"amx_harness":       "claude",
		"amx_home_dir":      homeDir,
		"amx_project":       "api",
		"amx_directory":     projectDir,
		"amx_args":          quotedArgs,
		"amx_env":           "DEBUG=1\x1fPORT=8080",
		"amx_groups":        "backend\x1finfra",
		"cc_account":        "work",
		"cc_config_dir":     homeDir,
		"cc_cwd":            projectDir,
		"cc_flags":          quotedArgs,
		"cc_explicit_flags": quotedArgs,
		"cc_env":            "DEBUG=1\x1fPORT=8080",
		"cc_groups":         "backend\x1finfra",
	}

	got := session.BuildUserVars(sess, acc, defaults)

	if len(got) != len(want) {
		t.Fatalf("BuildUserVars returned %d keys, expected %d\n got=%v\nwant=%v", len(got), len(want), got, want)
	}
	for k, v := range want {
		gv, ok := got[k]
		if !ok {
			t.Errorf("missing key %q in BuildUserVars output", k)
			continue
		}
		if gv != v {
			t.Errorf("key %q: got %q, want %q", k, gv, v)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected extra key %q in BuildUserVars output", k)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildUserVars map mismatch:\n got=%v\nwant=%v", got, want)
	}
}

// TestBuildUserVars_ProfileFallsBackToAccount verifies that when Session.Profile
// is empty but Account.Name is set, BuildUserVars uses the account name for
// both amx_profile and cc_account.
func TestBuildUserVars_ProfileFallsBackToAccount(t *testing.T) {
	defaults := config.Defaults{}
	acc := config.Account{
		Name:    "personal",
		Harness: "claude",
		HomeDir: "~/.config/claude",
	}
	sess := config.Session{
		Title:     "dotfiles",
		Directory: "/tmp/dotfiles",
		Account:   "personal",
	}

	got := session.BuildUserVars(sess, acc, defaults)
	if got["amx_profile"] != "personal" {
		t.Errorf("amx_profile: got %q, want %q", got["amx_profile"], "personal")
	}
	if got["cc_account"] != "personal" {
		t.Errorf("cc_account: got %q, want %q", got["cc_account"], "personal")
	}
}

func TestBuildUserVars_InheritedArgsDoNotSetExplicitFlags(t *testing.T) {
	defaults := config.Defaults{}
	acc := config.Account{
		Name:    "work",
		Harness: "claude",
		Args:    []string{"--account-flag"},
	}
	sess := config.Session{
		Title:     "api",
		Directory: "/tmp/api",
		Account:   "work",
	}

	got := session.BuildUserVars(sess, acc, defaults)
	if got["cc_flags"] != "--account-flag" {
		t.Fatalf("cc_flags: got %q, want %q", got["cc_flags"], "--account-flag")
	}
	if _, ok := got["cc_explicit_flags"]; ok {
		t.Fatalf("cc_explicit_flags should be omitted for inherited args, got %q", got["cc_explicit_flags"])
	}
}
