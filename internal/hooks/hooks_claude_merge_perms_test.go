package hooks

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestMergePreservesRestrictiveFilePerms pins the security contract:
// when the user has tightened the existing settings.json to a restrictive
// mode (e.g. 0o600 to keep tokens or env secrets out of group/other
// readers), the merge must preserve that mode on both the rewritten
// settings.json and its .bak. Previously both writes hard-coded 0o644 and
// silently widened the permissions on every sync, leaking sensitive
// contents to anyone with local shell access.
func TestMergePreservesRestrictiveFilePerms(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(accountDir, "settings.json")

	// Seed with a non-empty settings.json carrying stale amx hooks so the
	// merge produces a real rewrite (and therefore a .bak).
	existing := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/old/amx hook-indicator UserPromptSubmit",
							"async":   true,
						},
					},
				},
			},
		},
	}
	writeJSON(t, settingsPath, existing)
	if err := os.Chmod(settingsPath, 0o600); err != nil {
		t.Fatalf("chmod seed: %v", err)
	}

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
	}, nil)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	info, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("stat settings: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("settings.json perms widened: got %o, want 0600", got)
	}

	bakInfo, err := os.Stat(settingsPath + ".bak")
	if err != nil {
		t.Fatalf("stat .bak: %v", err)
	}
	if got := bakInfo.Mode().Perm(); got != 0o600 {
		t.Errorf(".bak perms widened: got %o, want 0600", got)
	}
}

// TestMergePreservesRestrictiveFilePermsAfterTightening pins the .bak side of
// the perm-preservation contract when a stale .bak already exists from a
// prior, less-restrictive sync. os.WriteFile only applies the requested mode
// when the destination is newly created — an existing .bak silently kept its
// previous (looser) perms while being overwritten with potentially
// secret-bearing content, leaking it to other local users.
func TestMergePreservesRestrictiveFilePermsAfterTightening(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(accountDir, "settings.json")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
	}, nil)

	// First merge installs the hooks with default 0o644 perms.
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("first MergeAll: %v", err)
	}

	// Pre-create a stale .bak with looser perms to mimic a prior run where
	// the original was 0o644. A future merge after the user tightens the
	// settings.json must still publish a .bak honoring the restrictive mode.
	if err := os.WriteFile(settingsPath+".bak", []byte(`{"old":true}`), 0o644); err != nil {
		t.Fatalf("seed stale .bak: %v", err)
	}

	// Tighten settings.json to 0o600 and dirty its contents so the next merge
	// rewrites the file (and therefore the .bak).
	dirty := []byte(`{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"/old/amx hook-indicator UserPromptSubmit","async":true}]}]}}`)
	if err := os.WriteFile(settingsPath, dirty, 0o600); err != nil {
		t.Fatalf("seed tightened settings: %v", err)
	}
	if err := os.Chmod(settingsPath, 0o600); err != nil {
		t.Fatalf("chmod tightened settings: %v", err)
	}

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("second MergeAll: %v", err)
	}

	bakInfo, err := os.Stat(settingsPath + ".bak")
	if err != nil {
		t.Fatalf("stat .bak: %v", err)
	}
	if got := bakInfo.Mode().Perm(); got != 0o600 {
		t.Errorf(".bak perms not narrowed across rewrite: got %o, want 0600", got)
	}
}

func TestMergeOverwritesReadOnlyBackup(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(accountDir, "settings.json")

	dirty := []byte(`{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"/old/amx hook-indicator UserPromptSubmit","async":true}]}]}}`)
	if err := os.WriteFile(settingsPath, dirty, 0o400); err != nil {
		t.Fatalf("seed read-only settings: %v", err)
	}
	if err := os.Chmod(settingsPath, 0o400); err != nil {
		t.Fatalf("chmod read-only settings: %v", err)
	}

	staleBackup := settingsPath + ".bak"
	if err := os.WriteFile(staleBackup, []byte(`{"stale":true}`), 0o400); err != nil {
		t.Fatalf("seed read-only .bak: %v", err)
	}
	if err := os.Chmod(staleBackup, 0o400); err != nil {
		t.Fatalf("chmod read-only .bak: %v", err)
	}

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
	}, nil)

	results, err := MergeAll(cfg, "", testBin)
	if err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	if results[0].Status != MergeUpdated {
		t.Fatalf("expected updated, got %q", results[0].Status)
	}

	bak, err := os.ReadFile(staleBackup)
	if err != nil {
		t.Fatalf("read .bak: %v", err)
	}
	if string(bak) != string(dirty) {
		t.Fatalf(".bak content mismatch: got %q want %q", bak, dirty)
	}
	bakInfo, err := os.Stat(staleBackup)
	if err != nil {
		t.Fatalf("stat .bak: %v", err)
	}
	if got := bakInfo.Mode().Perm(); got != 0o400 {
		t.Errorf(".bak mode changed: got %o, want 0400", got)
	}
	settingsInfo, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("stat settings: %v", err)
	}
	if got := settingsInfo.Mode().Perm(); got != 0o400 {
		t.Errorf("settings mode changed: got %o, want 0400", got)
	}
}

// TestMergeFreshWriteUsesDefaultPerm pins the fallback contract: when no
// existing settings.json is on disk, the merge writes the new file with
// the conventional 0o644 default (subject to the process umask). This
// guards the case where a user has not seeded any settings file at all,
// so there is no prior mode to inherit.
func TestMergeFreshWriteUsesDefaultPerm(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
	}, nil)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	info, err := os.Stat(filepath.Join(accountDir, "settings.json"))
	if err != nil {
		t.Fatalf("stat settings: %v", err)
	}
	// Mask against the umask: compare to 0o644 & ^umask.
	want := os.FileMode(0o644) &^ currentUmask()
	if got := info.Mode().Perm(); got != want {
		t.Errorf("fresh-write perms: got %o, want %o", got, want)
	}
}

func TestMergeFreshWriteHonorsRestrictiveUmask(t *testing.T) {
	oldUmask := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(oldUmask) })

	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
	}, nil)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	info, err := os.Stat(filepath.Join(accountDir, "settings.json"))
	if err != nil {
		t.Fatalf("stat settings: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("fresh-write perms ignored restrictive umask: got %o, want 0600", got)
	}
}
