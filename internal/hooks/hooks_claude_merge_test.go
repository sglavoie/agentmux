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

	// Should have entries for all installed Claude icon-relevant events.
	if got := len(hooks); got != 24 {
		t.Errorf("expected 24 hook events, got %d", got)
	}

	// Verify UserPromptSubmit uses event-name format.
	checkHookCommand(t, hooks, "UserPromptSubmit", testBin+" hook-indicator UserPromptSubmit")
}

func TestMergeInstallsNewEvents(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(accountDir, "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parsing settings.json: %v", err)
	}
	hooks := s["hooks"].(map[string]any)

	for _, event := range []string{
		"Setup",
		"UserPromptExpansion",
		"PostToolBatch",
		"StopFailure",
		"TaskCreated",
		"TeammateIdle",
		"ConfigChange",
		"PreCompact",
		"PostCompact",
		"PermissionDenied",
		"Elicitation",
		"ElicitationResult",
	} {
		checkHookCommand(t, hooks, event, testBin+" hook-indicator "+event)
	}
}

func TestMergeDisabledNewEvent(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig(
		[]config.Account{{Name: "work", ConfigDir: accountDir, Color: "blue"}},
		map[string]config.AccountHookOverride{
			"work": {DisabledEvents: []string{"StopFailure"}},
		},
	)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(accountDir, "settings.json"))
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	hooks := s["hooks"].(map[string]any)

	if _, ok := hooks["StopFailure"]; ok {
		t.Error("StopFailure should be absent (disabled)")
	}
	// Sibling new events remain installed.
	for _, event := range []string{
		"Setup",
		"PostToolBatch",
		"TaskCreated",
		"TeammateIdle",
		"ConfigChange",
		"PreCompact",
		"PostCompact",
		"PermissionDenied",
		"Elicitation",
		"ElicitationResult",
	} {
		if _, ok := hooks[event]; !ok {
			t.Errorf("%s should still be present when only StopFailure is disabled", event)
		}
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

// TestMergeOverrideKeyCaseInsensitive pins that a [hooks.overrides.NAME] entry
// is matched to its profile case-insensitively, mirroring session.findAccount
// and the hooks-sync filter. Before the fix, accountOverride did an exact-case
// map lookup, so [hooks.overrides.Work] against profile "work" silently
// produced no override and the disabled events were still installed.
func TestMergeOverrideKeyCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig(
		[]config.Account{{Name: "work", ConfigDir: accountDir, Color: "blue"}},
		map[string]config.AccountHookOverride{
			"Work": {DisabledEvents: []string{"Stop"}},
		},
	)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(accountDir, "settings.json"))
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	hooks := s["hooks"].(map[string]any)
	if _, ok := hooks["Stop"]; ok {
		t.Error("Stop should be absent — override is case-insensitive")
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

// TestMergeFilterByAccountCaseInsensitive verifies that the CLI filter
// (`amx config hooks-sync NAME`) accepts any casing — matching the
// case-insensitive contract enforced elsewhere (config validation rejects
// case-only duplicates and session.findAccount / session.RestartWithAccount
// already fold). Previously a user typing "Work" when the config had
// "work" got "account not found" and the hook merge silently skipped.
func TestMergeFilterByAccountCaseInsensitive(t *testing.T) {
	dir := t.TempDir()

	cfg := makeConfig([]config.Account{
		{Name: "work", ConfigDir: filepath.Join(dir, "work"), Color: "blue"},
		{Name: "personal", ConfigDir: filepath.Join(dir, "personal"), Color: "green"},
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
