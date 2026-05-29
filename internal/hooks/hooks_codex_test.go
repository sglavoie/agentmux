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

func makeCodexConfig(accounts []config.Account, overrides map[string]config.AccountHookOverride) *config.Config {
	cfg := &config.Config{Accounts: accounts}
	if overrides != nil {
		cfg.Hooks.Overrides = overrides
	}
	return cfg
}

func makeCodexAccount(name, dir string) config.Account {
	return config.Account{Name: name, HomeDir: dir, Harness: "codex"}
}

func TestCodexHookContract_InstallPath(t *testing.T) {
	contract := harness.Lookup(harness.Codex).HookContract()
	homeDir := "/home/user/.codex"
	want := filepath.Join(homeDir, "hooks.json")
	if got := contract.InstallPath(homeDir); got != want {
		t.Errorf("InstallPath(%q) = %q, want %q", homeDir, got, want)
	}
}

func TestMergeCodexFirstInstall(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "codex")

	cfg := makeCodexConfig([]config.Account{makeCodexAccount("work", accountDir)}, nil)

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

	data, err := os.ReadFile(filepath.Join(accountDir, "hooks.json"))
	if err != nil {
		t.Fatalf("reading hooks.json: %v", err)
	}

	var f struct {
		Hooks map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parsing hooks.json: %v", err)
	}
	if len(f.Hooks) == 0 {
		t.Fatal("expected non-empty hooks map")
	}
	if _, ok := f.Hooks["SessionStart"]; !ok {
		t.Error("expected SessionStart hook to be installed")
	}
}

func TestCodexHookContract_CommandSchema(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "codex")

	cfg := makeCodexConfig([]config.Account{makeCodexAccount("work", accountDir)}, nil)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(accountDir, "hooks.json"))
	if err != nil {
		t.Fatalf("reading hooks.json: %v", err)
	}

	var f struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parsing hooks.json: %v", err)
	}
	if len(f.Hooks) == 0 {
		t.Fatal("expected non-empty hooks map")
	}

	for event, groups := range f.Hooks {
		if event == "state" {
			continue
		}
		if len(groups) == 0 {
			t.Errorf("event %q has no matcher groups", event)
			continue
		}
		group := groups[0]
		if _, present := group["matcher"]; present {
			t.Errorf("event %q first group: matcher should be absent when omitted, got %v", event, group["matcher"])
		}
		hooks, ok := group["hooks"].([]any)
		if !ok || len(hooks) == 0 {
			t.Errorf("event %q first group: expected hooks array, got %v", event, group["hooks"])
			continue
		}
		entry, ok := hooks[0].(map[string]any)
		if !ok {
			t.Errorf("event %q first hook: expected object, got %T", event, hooks[0])
			continue
		}
		if typ, _ := entry["type"].(string); typ != "command" {
			t.Errorf("event %q first hook: type = %q, want %q", event, typ, "command")
		}
		cmd, _ := entry["command"].(string)
		if cmd == "" {
			t.Errorf("event %q first hook: expected non-empty command", event)
		}
		if !strings.Contains(cmd, "hook-indicator "+event) {
			t.Errorf("event %q first hook: command = %q, expected to invoke hook-indicator %s", event, cmd, event)
		}
		if timeout, _ := entry["timeout"].(float64); timeout != 5 {
			t.Errorf("event %q first hook: timeout = %v, want 5", event, entry["timeout"])
		}
		if _, present := entry["async"]; present {
			t.Errorf("event %q first hook: async must be absent, got %v", event, entry["async"])
		}
	}
}

func TestMergeCodexDisabledEvents(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "codex")

	cfg := makeCodexConfig(
		[]config.Account{makeCodexAccount("work", accountDir)},
		map[string]config.AccountHookOverride{
			"work": {DisabledEvents: []string{"SessionStart"}},
		},
	)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(accountDir, "hooks.json"))
	if err != nil {
		t.Fatalf("reading hooks.json: %v", err)
	}
	var f map[string]any
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	hooks := f["hooks"].(map[string]any)

	if _, ok := hooks["SessionStart"]; ok {
		t.Error("SessionStart should be absent (disabled)")
	}
	if _, ok := hooks["Stop"]; !ok {
		t.Error("Stop should be present")
	}
}

func TestMergeCodexPreservesUserHooksAndState(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "codex")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}

	existing := map[string]any{
		"hooks": map[string]any{
			"state": map[string]any{
				"file:/tmp/hooks.json:pre_tool_use:0:0": map[string]any{
					"trusted_hash": "sha256:abc123",
				},
			},
			"PreToolUse": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type": "prompt",
						},
					},
				},
			},
		},
	}
	writeJSON(t, filepath.Join(accountDir, "hooks.json"), existing)

	cfg := makeCodexConfig([]config.Account{makeCodexAccount("work", accountDir)}, nil)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(accountDir, "hooks.json"))
	if err != nil {
		t.Fatalf("reading hooks.json: %v", err)
	}
	var f map[string]any
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	hooks := f["hooks"].(map[string]any)
	state := hooks["state"].(map[string]any)
	if _, ok := state["file:/tmp/hooks.json:pre_tool_use:0:0"]; !ok {
		t.Fatalf("expected hooks.state to be preserved, got %v", state)
	}

	preTool := hooks["PreToolUse"].([]any)
	if len(preTool) != 2 {
		t.Fatalf("expected 2 matcher groups for PreToolUse, got %d", len(preTool))
	}
	group0 := preTool[0].(map[string]any)
	entries := group0["hooks"].([]any)
	entry0 := entries[0].(map[string]any)
	if entry0["type"] != "prompt" {
		t.Fatalf("expected preserved prompt hook, got %v", entry0)
	}
}

func TestMergeCodexReplacesPriorAmxEntries(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "codex")

	cfg := makeCodexConfig([]config.Account{makeCodexAccount("work", accountDir)}, nil)

	if _, err := MergeAll(cfg, "", "/old/amx"); err != nil {
		t.Fatalf("first MergeAll: %v", err)
	}

	results, err := MergeAll(cfg, "", testBin)
	if err != nil {
		t.Fatalf("second MergeAll: %v", err)
	}
	if results[0].Status != MergeUpdated {
		t.Errorf("expected updated, got %q", results[0].Status)
	}

	data, err := os.ReadFile(filepath.Join(accountDir, "hooks.json"))
	if err != nil {
		t.Fatalf("reading hooks.json: %v", err)
	}
	var f map[string]any
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	stopGroups := f["hooks"].(map[string]any)["Stop"].([]any)
	amxCount := 0
	for _, rawGroup := range stopGroups {
		group := rawGroup.(map[string]any)
		for _, rawEntry := range group["hooks"].([]any) {
			entry := rawEntry.(map[string]any)
			cmd, _ := entry["command"].(string)
			if strings.Contains(cmd, "hook-indicator Stop") {
				amxCount++
				if cmd != testBin+" hook-indicator Stop" {
					t.Fatalf("amx hook command = %q, want %q", cmd, testBin+" hook-indicator Stop")
				}
			}
		}
	}
	if amxCount != 1 {
		t.Fatalf("expected exactly one amx Stop hook after resync, got %d", amxCount)
	}
	if _, err := os.Stat(filepath.Join(accountDir, "hooks.json.bak")); os.IsNotExist(err) {
		t.Error("expected hooks.json.bak after overwrite")
	}
}

func TestCodexStatusMissing(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "codex")

	cfg := makeCodexConfig([]config.Account{makeCodexAccount("work", accountDir)}, nil)

	results, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if results[0].Status != SyncMissing {
		t.Errorf("expected missing, got %q", results[0].Status)
	}
}

func TestCodexStatusInSync(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "codex")

	cfg := makeCodexConfig([]config.Account{makeCodexAccount("work", accountDir)}, nil)
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

func TestCodexStatusAllEventsDisabledInSync(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "codex")
	allCodexEvents := []string{
		"SessionStart",
		"UserPromptSubmit",
		"PreToolUse",
		"PermissionRequest",
		"PostToolUse",
		"PreCompact",
		"PostCompact",
		"SubagentStart",
		"SubagentStop",
		"Stop",
	}

	cfg := makeCodexConfig(
		[]config.Account{makeCodexAccount("work", accountDir)},
		map[string]config.AccountHookOverride{
			"work": {DisabledEvents: allCodexEvents},
		},
	)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	results, err := StatusAll(cfg, testBin)
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	if results[0].Status != SyncInSync {
		t.Errorf("expected in_sync when every managed event is intentionally disabled, got %q", results[0].Status)
	}
}

func TestCodexStatusDrifted(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "codex")

	cfg := makeCodexConfig([]config.Account{makeCodexAccount("work", accountDir)}, nil)
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
