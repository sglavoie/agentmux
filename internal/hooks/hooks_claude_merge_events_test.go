package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// TestMergePreservesNonHooksTopLevelKeys ensures that a settings.json
// carrying user-defined top-level keys (model, env, permissions, etc.) is
// rewritten without losing those keys when the merge updates only the
// hooks section.
func TestMergePreservesNonHooksTopLevelKeys(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(accountDir, "settings.json")

	original := map[string]any{
		"model":       "claude-opus",
		"env":         map[string]any{"CUSTOM": "value"},
		"permissions": map[string]any{"allow": []any{"npm"}},
	}
	writeJSON(t, settingsPath, original)

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
	}, nil)

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got := s["model"]; got != "claude-opus" {
		t.Errorf("model lost across merge: got %v", got)
	}
	envMap, ok := s["env"].(map[string]any)
	if !ok || envMap["CUSTOM"] != "value" {
		t.Errorf("env lost across merge: got %v", s["env"])
	}
	permMap, ok := s["permissions"].(map[string]any)
	if !ok {
		t.Errorf("permissions lost across merge: got %v", s["permissions"])
	} else if allow, _ := permMap["allow"].([]any); len(allow) != 1 || allow[0] != "npm" {
		t.Errorf("permissions.allow lost: got %v", permMap["allow"])
	}
	if _, ok := s["hooks"]; !ok {
		t.Errorf("expected hooks key to be added by merge")
	}
}

func TestMergeInstallsNewEvents(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir},
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
		[]config.Account{{Name: "work", ConfigDir: accountDir}},
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
		[]config.Account{{Name: "work", ConfigDir: accountDir}},
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
		[]config.Account{{Name: "work", ConfigDir: accountDir}},
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
