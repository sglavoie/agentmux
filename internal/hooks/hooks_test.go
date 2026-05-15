package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

const testBin = "/usr/local/bin/amx"

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

	// Should have entries for all 18 events.
	if got := len(hooks); got != 18 {
		t.Errorf("expected 18 hook events, got %d", got)
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
		"StopFailure",
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
	for _, event := range []string{"PreCompact", "PostCompact", "PermissionDenied", "Elicitation", "ElicitationResult"} {
		if _, ok := hooks[event]; !ok {
			t.Errorf("%s should still be present when only StopFailure is disabled", event)
		}
	}
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

	// Should have 2 groups: user hook + amx hook.
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

func TestStatusMissingNoamxHooks(t *testing.T) {
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

	// Write settings.json with stale amx hook (old binary path).
	writeJSON(t, filepath.Join(accountDir, "settings.json"), map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "/old/path/amx hook-indicator active",
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

// Copilot hook tests

func makeCopilotConfig(accounts []config.Account, overrides map[string]config.AccountHookOverride) *config.Config {
	cfg := &config.Config{Accounts: accounts}
	if overrides != nil {
		cfg.Hooks.Overrides = overrides
	}
	return cfg
}

func makeCopilotAccount(name, dir string) config.Account {
	return config.Account{Name: name, ConfigDir: dir, Harness: "copilot", Color: "blue"}
}

func TestCopilotHookContract_InstallPath(t *testing.T) {
	contract := harness.Lookup(harness.Copilot).HookContract()
	homeDir := "/home/user/.copilot"
	want := filepath.Join(homeDir, "hooks", "agent-mux.json")
	if got := contract.InstallPath(homeDir); got != want {
		t.Errorf("InstallPath(%q) = %q, want %q", homeDir, got, want)
	}
}

func TestMergeCopilotFirstInstall(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "copilot")

	cfg := makeCopilotConfig([]config.Account{makeCopilotAccount("work", accountDir)}, nil)

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

	hooksPath := filepath.Join(accountDir, "hooks", "agent-mux.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("reading hooks file: %v", err)
	}

	var f map[string]any
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parsing hooks file: %v", err)
	}
	hooks, ok := f["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key missing or wrong type")
	}
	if len(hooks) == 0 {
		t.Error("expected non-empty hooks map")
	}
	// Verify a known event is present with the correct command.
	if _, ok := hooks["sessionStart"]; !ok {
		t.Error("expected sessionStart hook to be installed")
	}
}

func TestMergeCopilotResync(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "copilot")

	cfg := makeCopilotConfig([]config.Account{makeCopilotAccount("work", accountDir)}, nil)

	// First merge creates the file.
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("first MergeAll: %v", err)
	}

	// Second merge should be skipped (already in sync).
	results, err := MergeAll(cfg, "", testBin)
	if err != nil {
		t.Fatalf("second MergeAll: %v", err)
	}
	if results[0].Status != MergeSkipped {
		t.Errorf("expected skipped on second merge, got %q", results[0].Status)
	}
}

func TestMergeCopilotDrifted(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "copilot")

	cfg := makeCopilotConfig([]config.Account{makeCopilotAccount("work", accountDir)}, nil)

	// Install with old binary.
	if _, err := MergeAll(cfg, "", "/old/amx"); err != nil {
		t.Fatalf("first MergeAll: %v", err)
	}

	// Re-merge with new binary — should overwrite.
	results, err := MergeAll(cfg, "", testBin)
	if err != nil {
		t.Fatalf("second MergeAll: %v", err)
	}
	if results[0].Status != MergeUpdated {
		t.Errorf("expected updated, got %q", results[0].Status)
	}

	// Backup should exist.
	hooksPath := filepath.Join(accountDir, "hooks", "agent-mux.json")
	if _, err := os.Stat(hooksPath + ".bak"); os.IsNotExist(err) {
		t.Error("expected .bak file to exist after overwrite")
	}
}

func TestMergeCopilotDisabledEvents(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "copilot")

	cfg := makeCopilotConfig(
		[]config.Account{makeCopilotAccount("work", accountDir)},
		map[string]config.AccountHookOverride{
			"work": {DisabledEvents: []string{"sessionStart"}},
		},
	)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(accountDir, "hooks", "agent-mux.json"))
	var f map[string]any
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	hooks := f["hooks"].(map[string]any)

	if _, ok := hooks["sessionStart"]; ok {
		t.Error("sessionStart should be absent (disabled)")
	}
	if _, ok := hooks["sessionEnd"]; !ok {
		t.Error("sessionEnd should be present")
	}
}

func TestCopilotParseInstalledEvents_NonExistent(t *testing.T) {
	contract := harness.Lookup(harness.Copilot).HookContract()
	events, err := contract.ParseInstalledEvents("/nonexistent/path/agent-mux.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if events != nil {
		t.Errorf("expected nil for nonexistent file, got %v", events)
	}
}

func TestCopilotParseInstalledEvents_AfterMerge(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "copilot")

	cfg := makeCopilotConfig([]config.Account{makeCopilotAccount("work", accountDir)}, nil)
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	contract := harness.Lookup(harness.Copilot).HookContract()
	hooksPath := filepath.Join(accountDir, "hooks", "agent-mux.json")
	events, err := contract.ParseInstalledEvents(hooksPath)
	if err != nil {
		t.Fatalf("ParseInstalledEvents: %v", err)
	}
	if len(events) == 0 {
		t.Error("expected installed events after merge, got none")
	}
	// Verify known events are present.
	eventSet := make(map[string]bool)
	for _, e := range events {
		eventSet[e] = true
	}
	for _, want := range []string{"sessionStart", "sessionEnd", "preToolUse"} {
		if !eventSet[want] {
			t.Errorf("expected event %q in installed events", want)
		}
	}
}

func TestCopilotStatusMissing(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "copilot")

	cfg := makeCopilotConfig([]config.Account{makeCopilotAccount("work", accountDir)}, nil)

	results, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if results[0].Status != SyncMissing {
		t.Errorf("expected missing, got %q", results[0].Status)
	}
}

func TestCopilotStatusInSync(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "copilot")

	cfg := makeCopilotConfig([]config.Account{makeCopilotAccount("work", accountDir)}, nil)
	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	results, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if results[0].Status != SyncInSync {
		t.Errorf("expected in_sync, got %q", results[0].Status)
	}
}

func TestCopilotStatusDrifted(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "copilot")

	cfg := makeCopilotConfig([]config.Account{makeCopilotAccount("work", accountDir)}, nil)

	// Install with old binary.
	if _, err := MergeAll(cfg, "", "/old/amx"); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	results, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if results[0].Status != SyncDrifted {
		t.Errorf("expected drifted, got %q", results[0].Status)
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
	t.Errorf("no amx hook found for event %q", event)
}
