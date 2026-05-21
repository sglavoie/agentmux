package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

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

func TestMergePreservesUserMatcher(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}

	existing := map[string]any{
		"hooks": map[string]any{
			"Notification": []any{
				map[string]any{
					"matcher": "permission_prompt",
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "my-notification-hook",
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

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(accountDir, "settings.json"))
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	notifs := s["hooks"].(map[string]any)["Notification"].([]any)
	if len(notifs) != 2 {
		t.Fatalf("expected 2 hook groups for Notification, got %d", len(notifs))
	}
	if matcher := notifs[0].(map[string]any)["matcher"]; matcher != "permission_prompt" {
		t.Fatalf("expected matcher permission_prompt to be preserved, got %v", matcher)
	}
}

// TestMergePreservesThirdPartyHookIndicatorSubstring asserts that a non-amx
// hook whose command merely contains the substring "hook-indicator" is left
// in place by MergeData. The previous (loose) detection wiped any command
// containing that substring, including tools whose path happened to include
// it (e.g. a third-party "hook-indicator-helper").
func TestMergePreservesThirdPartyHookIndicatorSubstring(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}

	thirdPartyCmd := "/opt/hook-indicator-helper/run --notify"
	existing := map[string]any{
		"hooks": map[string]any{
			"Notification": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": thirdPartyCmd,
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

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(accountDir, "settings.json"))
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	notifs := s["hooks"].(map[string]any)["Notification"].([]any)
	foundThirdParty := false
	for _, g := range notifs {
		group := g.(map[string]any)
		inner := group["hooks"].([]any)
		for _, h := range inner {
			entry := h.(map[string]any)
			if entry["command"] == thirdPartyCmd {
				foundThirdParty = true
			}
		}
	}
	if !foundThirdParty {
		t.Fatalf("third-party hook with 'hook-indicator' substring was wiped: %v", notifs)
	}
}

// TestMergePreservesUserHookColocatedInAmxGroup verifies that a user-added
// hook entry placed (by hand-edit) inside the same matcher group as the
// amx-installed hook is preserved across a sync. Previously the group was
// dropped wholesale if any entry was amx-owned, silently wiping the user's
// command.
func TestMergePreservesUserHookColocatedInAmxGroup(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}

	userCmd := "/usr/local/bin/notify-user-stop"
	existing := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": testBin + " hook-indicator Stop",
							"async":   true,
						},
						map[string]any{
							"type":    "command",
							"command": userCmd,
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

	if _, err := MergeAll(cfg, "", testBin); err != nil {
		t.Fatalf("MergeAll error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(accountDir, "settings.json"))
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	stops := s["hooks"].(map[string]any)["Stop"].([]any)
	foundUser := false
	for _, g := range stops {
		group := g.(map[string]any)
		inner := group["hooks"].([]any)
		for _, h := range inner {
			entry := h.(map[string]any)
			if entry["command"] == userCmd {
				foundUser = true
			}
		}
	}
	if !foundUser {
		t.Fatalf("user hook colocated in amx group was wiped: %v", stops)
	}
}

// TestMergeRefusesMalformedHooksValue locks in the guarantee that a Claude
// settings.json whose top-level "hooks" field is not an object of hookGroup
// arrays surfaces a parse error rather than being silently overwritten.
// Previously MergeData reset `existing = make(...)` on unmarshal failure,
// which discarded the user's malformed (but recoverable) entries on the
// next sync and replaced them with only amx-managed groups.
func TestMergeRefusesMalformedHooksValue(t *testing.T) {
	dir := t.TempDir()
	accountDir := filepath.Join(dir, "claude")
	if err := os.MkdirAll(accountDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(accountDir, "settings.json")

	// Top-level JSON is valid, but `hooks` is a string instead of the
	// expected object — the kind of mistake a hand-edit can introduce.
	bad := []byte(`{"hooks": "broken"}`)
	if err := os.WriteFile(settingsPath, bad, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := makeConfig([]config.Account{
		{Name: "personal", ConfigDir: accountDir, Color: "green"},
	}, nil)

	results, err := MergeAll(cfg, "", testBin)
	if err != nil {
		t.Fatalf("MergeAll outer error: %v", err)
	}
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("expected per-account error for malformed hooks, got: %+v", results)
	}

	// The on-disk file must be untouched so the user can recover by hand.
	got, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if string(got) != string(bad) {
		t.Fatalf("settings.json was overwritten despite parse error\n got: %q\nwant: %q", got, bad)
	}
}
