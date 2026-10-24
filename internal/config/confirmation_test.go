package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceConfirmationFollowsLivePolicyAndConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy")
	cfg := filepath.Join(dir, "config.toml")
	t.Setenv("AMX_CONFIG", cfg)
	t.Setenv(WorkspaceConfirmationEnv, path)
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !WorkspaceConfirmation(false) {
		t.Fatal("missing policy must confirm")
	}
	write(path, "never")
	if WorkspaceConfirmation(true) {
		t.Fatal("explicit never must override")
	}
	write(path, "always")
	if !WorkspaceConfirmation(false) {
		t.Fatal("already-open view must see policy change")
	}
	write(path, "follow-cli")
	write(cfg, "[defaults]\nconfirm_destructive = false\n")
	if WorkspaceConfirmation(true) {
		t.Fatal("inherit false")
	}
	write(cfg, "[defaults]\nconfirm_destructive = true\n")
	if !WorkspaceConfirmation(false) {
		t.Fatal("inherit updated true")
	}
	write(cfg, "invalid TOML[")
	if !WorkspaceConfirmation(false) {
		t.Fatal("invalid config must confirm")
	}
	write(path, "invalid")
	if !WorkspaceConfirmation(false) {
		t.Fatal("invalid policy must confirm")
	}
	t.Setenv(WorkspaceConfirmationEnv, "")
	if WorkspaceConfirmation(false) || !WorkspaceConfirmation(true) {
		t.Fatal("ordinary terminals must retain their config policy")
	}
}

func TestUnknownConfirmationPolicyListsTheValidOnes(t *testing.T) {
	_, err := ConfirmationRequired("sometimes")
	if err == nil || err.Error() != `unknown confirmation policy "sometimes": want one of follow-cli, always, never` {
		t.Fatalf("err = %v", err)
	}
	for _, policy := range ConfirmationPolicies()[1:] {
		if _, err := ConfirmationRequired(policy); err != nil {
			t.Errorf("%s: %v", policy, err)
		}
	}
}
