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

// Copilot hook tests

func makeCopilotConfig(accounts []config.Account, overrides map[string]config.AccountHookOverride) *config.Config {
	cfg := &config.Config{Accounts: accounts}
	if overrides != nil {
		cfg.Hooks.Overrides = overrides
	}
	return cfg
}

func makeCopilotAccount(name, dir string) config.Account {
	return config.Account{Name: name, ConfigDir: dir, Harness: "copilot"}
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

// TestCopilotHookContract_HookEntrySchema pins the Copilot CLI hook entry
// schema: each entry must carry the command under "bash" (Unix) and
// "powershell" (Windows), never under the undocumented "command" fallback. The
// regression scenario this guards against is older Copilot CLI versions that
// reject any entry missing both "bash" and "powershell" with
// `Hook execution failed: Neither 'bash' nor 'powershell' specified`, which
// blocks every event from reaching the daemon's event log.
func TestCopilotHookContract_HookEntrySchema(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "copilot")

	cfg := makeCopilotConfig([]config.Account{makeCopilotAccount("work", accountDir)}, nil)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	hooksPath := filepath.Join(accountDir, "hooks", "agent-mux.json")
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("reading hooks file: %v", err)
	}

	var f struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parsing hooks file: %v", err)
	}
	if len(f.Hooks) == 0 {
		t.Fatal("expected non-empty hooks map")
	}

	for event, entries := range f.Hooks {
		if len(entries) == 0 {
			t.Errorf("event %q has no entries", event)
			continue
		}
		entry := entries[0]

		bash, _ := entry["bash"].(string)
		if bash == "" {
			t.Errorf("event %q first entry: expected non-empty bash field, got %v", event, entry["bash"])
		}
		ps, _ := entry["powershell"].(string)
		if ps == "" {
			t.Errorf("event %q first entry: expected non-empty powershell field, got %v", event, entry["powershell"])
		}
		if _, present := entry["command"]; present {
			t.Errorf("event %q first entry: legacy command field must be absent, got %v", event, entry["command"])
		}
		if typ, _ := entry["type"].(string); typ != "command" {
			t.Errorf("event %q first entry: type = %q, want %q", event, typ, "command")
		}
		if !strings.Contains(bash, "hook-indicator "+event) {
			t.Errorf("event %q first entry: bash = %q, expected to invoke hook-indicator %s", event, bash, event)
		}
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
