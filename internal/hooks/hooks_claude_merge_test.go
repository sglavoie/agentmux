package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestMergeFreshWrite(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
	}, nil)

	results, err := MergeAll(cfg, "", testBin)
	if err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != MergeCreated {
		t.Errorf("expected created, got %q", results[0].Status)
	}

	settingsPath := filepath.Join(accountDir, "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}

	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parsing settings.json: %v", err)
	}

	hooks, ok := s["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key missing or wrong type")
	}

	// Should have entries for all installed Claude icon-relevant events.
	if got := len(hooks); got != 24 {
		t.Errorf("expected 24 hook events, got %d", got)
	}

	// Verify UserPromptSubmit uses event-name format.
	checkHookCommand(t, hooks, "UserPromptSubmit", testBin+" hook-indicator UserPromptSubmit")
}

// TestMergeExcludesWorktreeHooks pins the CLAUDE.md guarantee that the
// WorktreeCreate and WorktreeRemove control hooks are never installed:
// these hooks must print the worktree path on stdout, which amx hook-indicator
// does not, and installing them breaks subagent spawning with
// "WorktreeCreate hook failed: no successful output".
func TestMergeExcludesWorktreeHooks(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
	}, nil)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(accountDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	hooks := s["hooks"].(map[string]any)
	for _, banned := range []string{"WorktreeCreate", "WorktreeRemove"} {
		if _, ok := hooks[banned]; ok {
			t.Errorf("%s must never be installed (would break subagent spawning)", banned)
		}
	}
}

func TestMergeSkippedWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
	}, nil)

	// First merge creates the file.
	_, _ = MergeAll(cfg, "", testBin)

	// Second merge should be skipped.
	results, err := MergeAll(cfg, "", testBin)
	if err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}
	if results[0].Status != MergeSkipped {
		t.Errorf("expected skipped on second merge, got %q", results[0].Status)
	}
}

func TestMergeBakBackupCreated(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write existing settings.json with stale amx hooks.
	existing := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/old/amx hook-indicator active",
							"async":   true,
						},
					},
				},
			},
		},
	}
	writeJSON(t, filepath.Join(accountDir, "settings.json"), existing)

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
	}, nil)

	results, _ := MergeAll(cfg, "", testBin)
	if results[0].Status != MergeUpdated {
		t.Errorf("expected updated, got %q", results[0].Status)
	}

	bakPath := filepath.Join(accountDir, "settings.json.bak")
	if _, err := os.Stat(bakPath); os.IsNotExist(err) {
		t.Error("expected .bak file to be created, but it does not exist")
	}
}

func TestMergeFilterByAccount(t *testing.T) {
	dir := t.TempDir()

	cfg := makeConfig([]config.Account{
		{Name: "work", ConfigDir: filepath.Join(dir, "work")},
		{Name: "personal", ConfigDir: filepath.Join(dir, "personal")},
	}, nil)

	results, err := MergeAll(cfg, "work", testBin)
	if err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}
	if len(results) != 1 || results[0].Account != "work" {
		t.Errorf("filter did not work: %v", results)
	}
}

func TestMergeFilterUnknownAccount(t *testing.T) {
	cfg := makeConfig([]config.Account{
		{Name: "work", ConfigDir: t.TempDir()},
	}, nil)

	_, err := MergeAll(cfg, "nobody", testBin)
	if err == nil {
		t.Error("expected error for unknown account filter, got nil")
	}
}

// TestMergeFilterByAccountCaseInsensitive verifies that the CLI filter
// (`amx config hooks-sync NAME`) accepts any casing — matching the
// case-insensitive contract enforced elsewhere (config validation rejects
// case-only duplicates and session.findAccount / session.RestartWithAccount
// already fold). Previously a user typing "Work" when the config had
// "work" got "account not found" and the hook merge silently skipped.
func TestMergeFilterByAccountCaseInsensitive(t *testing.T) {
	dir := t.TempDir()

	cfg := makeConfig([]config.Account{
		{Name: "work", ConfigDir: filepath.Join(dir, "work")},
		{Name: "personal", ConfigDir: filepath.Join(dir, "personal")},
	}, nil)

	results, err := MergeAll(cfg, "WORK", testBin)
	if err != nil {
		t.Fatalf("MergeAll(filter=WORK) error: %v", err)
	}
	if len(results) != 1 || results[0].Account != "work" {
		t.Fatalf("expected only work matched by case-folded filter, got %+v", results)
	}
}

func TestMergeUpdatesStaleHooks(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Install with old binary path.
	oldBin := "/old/amx"
	cfgOld := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
	}, nil)
	_, _ = MergeAll(cfgOld, "", oldBin)

	// Now merge with new binary.
	cfgNew := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
	}, nil)
	results, err := MergeAll(cfgNew, "", testBin)
	if err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}
	if results[0].Status != MergeUpdated {
		t.Errorf("expected updated, got %q", results[0].Status)
	}

	// Status should now be in_sync.
	statuses, _ := StatusAll(cfgNew, testBin)
	if statuses[0].Status != SyncInSync {
		t.Errorf("expected in_sync after update, got %q", statuses[0].Status)
	}
}

func TestMergePreservesSettingsSymlink(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	targetDir := filepath.Join(dir, "managed")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}

	targetPath := filepath.Join(targetDir, "settings.json")
	linkPath := filepath.Join(accountDir, "settings.json")
	original := []byte(`{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"/old/amx hook-indicator UserPromptSubmit","async":true}]}]}}`)
	if err := os.WriteFile(targetPath, original, 0o644); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	relTarget, err := filepath.Rel(accountDir, targetPath)
	if err != nil {
		t.Fatalf("relative symlink target: %v", err)
	}
	if err := os.Symlink(relTarget, linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
	}, nil)

	results, err := MergeAll(cfg, "", testBin)
	if err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}
	if results[0].Status != MergeUpdated {
		t.Fatalf("expected updated, got %q", results[0].Status)
	}
	if results[0].Path != linkPath {
		t.Fatalf("result path = %q, want install path %q", results[0].Path, linkPath)
	}

	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("settings symlink was replaced with mode %v", info.Mode())
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("json.Unmarshal target: %v", err)
	}
	hooks := s["hooks"].(map[string]any)
	checkHookCommand(t, hooks, "UserPromptSubmit", testBin+" hook-indicator UserPromptSubmit")

	bak, err := os.ReadFile(targetPath + ".bak")
	if err != nil {
		t.Fatalf("expected target .bak file, got: %v", err)
	}
	if string(bak) != string(original) {
		t.Fatalf(".bak content mismatch: got %q want %q", bak, original)
	}
}
