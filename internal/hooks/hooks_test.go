package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
)

const testBin = "/usr/local/bin/ccm"

func makeConfig(accounts []config.Account, overrides map[string]config.AccountHookOverride) *config.Config {
	cfg := &config.Config{Accounts: accounts}
	if overrides != nil {
		cfg.Hooks.Overrides = overrides
	}
	return cfg
}

func TestMergeFreshWrite(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
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

	// Should have entries for all 12 events.
	if got := len(hooks); got != 12 {
		t.Errorf("expected 12 hook events, got %d", got)
	}

	// Verify UserPromptSubmit uses event-name format.
	checkHookCommand(t, hooks, "UserPromptSubmit", testBin+" hook-indicator UserPromptSubmit")
}

func TestMergePreservesUserHooks(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write existing settings.json with a user-defined hook.
	existing := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "my-custom-script",
							"async":   false,
						},
					},
				},
			},
		},
	}
	writeJSON(t, filepath.Join(accountDir, "settings.json"), existing)

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)

	_, err := MergeAll(cfg, "", testBin)
	if err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(accountDir, "settings.json"))
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	hooks := s["hooks"].(map[string]any)
	ups := hooks["UserPromptSubmit"].([]any)

	// Should have 2 groups: user hook + ccm hook.
	if len(ups) != 2 {
		t.Errorf("expected 2 hook groups for UserPromptSubmit, got %d", len(ups))
	}

	// Verify user hook is preserved.
	group0 := ups[0].(map[string]any)
	inner0 := group0["hooks"].([]any)
	entry0 := inner0[0].(map[string]any)
	if entry0["command"] != "my-custom-script" {
		t.Errorf("user hook not preserved: %v", entry0["command"])
	}
}

func TestMergeDisabledEvents(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig(
		[]config.Account{{Name: "work", ConfigDir: accountDir, Color: "blue"}},
		map[string]config.AccountHookOverride{
			"work": {DisabledEvents: []string{"UserPromptSubmit", "Stop"}},
		},
	)

	results, err := MergeAll(cfg, "", testBin)
	if err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}
	if results[0].Status != MergeCreated {
		t.Errorf("expected created, got %q", results[0].Status)
	}

	data, _ := os.ReadFile(filepath.Join(accountDir, "settings.json"))
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	hooks := s["hooks"].(map[string]any)

	// Disabled events should not be present.
	if _, ok := hooks["UserPromptSubmit"]; ok {
		t.Error("UserPromptSubmit should be absent (disabled)")
	}
	if _, ok := hooks["Stop"]; ok {
		t.Error("Stop should be absent (disabled)")
	}

	// Other events should be present.
	if _, ok := hooks["PermissionRequest"]; !ok {
		t.Error("PermissionRequest should be present")
	}
}

func TestMergeSkippedWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
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

	// Write existing settings.json with stale ccm hooks.
	existing := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/old/ccm hook-indicator active",
							"async":   true,
						},
					},
				},
			},
		},
	}
	writeJSON(t, filepath.Join(accountDir, "settings.json"), existing)

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
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
		{Name: "work", ConfigDir: filepath.Join(dir, "work"), Color: "blue"},
		{Name: "personal", ConfigDir: filepath.Join(dir, "personal"), Color: "green"},
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
		{Name: "work", ConfigDir: t.TempDir(), Color: "blue"},
	}, nil)

	_, err := MergeAll(cfg, "nobody", testBin)
	if err == nil {
		t.Error("expected error for unknown account filter, got nil")
	}
}

// Status tests

func TestStatusMissingNoFile(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)

	results, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll error: %v", err)
	}
	if results[0].Status != SyncMissing {
		t.Errorf("expected missing, got %q", results[0].Status)
	}
}

func TestStatusMissingNoCcmHooks(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeJSON(t, filepath.Join(accountDir, "settings.json"), map[string]any{
		"hooks": map[string]any{},
	})

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)

	results, _ := StatusAll(cfg, testBin)
	if results[0].Status != SyncMissing {
		t.Errorf("expected missing, got %q", results[0].Status)
	}
}

func TestStatusInSync(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)

	// Merge to install correct hooks.
	_, _ = MergeAll(cfg, "", testBin)

	results, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll error: %v", err)
	}
	if results[0].Status != SyncInSync {
		t.Errorf("expected in_sync, got %q", results[0].Status)
	}
}

func TestStatusDrifted(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write settings.json with stale ccm hook (old binary path).
	writeJSON(t, filepath.Join(accountDir, "settings.json"), map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/old/path/ccm hook-indicator active",
							"async":   true,
						},
					},
				},
			},
		},
	})

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)

	results, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll error: %v", err)
	}
	if results[0].Status != SyncDrifted {
		t.Errorf("expected drifted, got %q", results[0].Status)
	}
}

func TestMergeUpdatesStaleHooks(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Install with old binary path.
	oldBin := "/old/ccm"
	cfgOld := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)
	_, _ = MergeAll(cfgOld, "", oldBin)

	// Now merge with new binary.
	cfgNew := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
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

// helpers

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshaling JSON: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func checkHookCommand(t *testing.T, hooks map[string]any, event, wantCmd string) {
	t.Helper()
	groups, ok := hooks[event].([]any)
	if !ok || len(groups) == 0 {
		t.Errorf("no hook groups for event %q", event)
		return
	}
	for _, g := range groups {
		group := g.(map[string]any)
		entries := group["hooks"].([]any)
		for _, e := range entries {
			entry := e.(map[string]any)
			cmd, _ := entry["command"].(string)
			if strings.Contains(cmd, "hook-indicator") {
				if cmd != wantCmd {
					t.Errorf("event %q: got command %q, want %q", event, cmd, wantCmd)
				}
				return
			}
		}
	}
	t.Errorf("no ccm hook found for event %q", event)
}
