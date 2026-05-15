package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

// SyncStatus describes whether an account's hooks are in sync with the config.
type SyncStatus string

const (
	// SyncInSync means settings.json has the expected amx hooks installed.
	SyncInSync SyncStatus = "in_sync"
	// SyncDrifted means amx hooks exist but differ from what amx would write.
	SyncDrifted SyncStatus = "drifted"
	// SyncMissing means settings.json is absent or has no amx hooks.
	SyncMissing SyncStatus = "missing"
)

// AccountStatus holds the sync status for one account.
type AccountStatus struct {
	Account string
	Path    string
	Status  SyncStatus
}

// StatusAll returns the hooks sync status for every configured profile.
// amxBin is the absolute path to the amx binary (used to determine expected commands).
func StatusAll(cfg *config.Config, amxBin string) ([]AccountStatus, error) {
	results := make([]AccountStatus, 0, len(cfg.Accounts))
	for _, account := range cfg.Accounts {
		override := accountOverride(cfg, account.Name)
		status, err := checkStatus(account, override, amxBin)
		if err != nil {
			return nil, err
		}
		results = append(results, status)
	}
	return results, nil
}

func checkStatus(account config.Account, override config.AccountHookOverride, amxBin string) (AccountStatus, error) {
	switch account.EffectiveHarness() {
	case harness.Copilot:
		return checkCopilotStatus(account, override, amxBin)
	default:
		return checkClaudeStatus(account, override, amxBin)
	}
}

func checkClaudeStatus(account config.Account, override config.AccountHookOverride, amxBin string) (AccountStatus, error) {
	dir := config.ExpandTilde(account.EffectiveHomeDir())
	settingsPath := filepath.Join(dir, "settings.json")

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return AccountStatus{Account: account.Name, Path: settingsPath, Status: SyncMissing}, nil
		}
		return AccountStatus{}, err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return AccountStatus{Account: account.Name, Path: settingsPath, Status: SyncMissing}, nil
	}

	var existingHooks map[string][]hookGroup
	if hooksRaw, ok := raw["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &existingHooks); err != nil {
			existingHooks = nil
		}
	}

	existing := extractAmxCommands(existingHooks)
	if len(existing) == 0 {
		return AccountStatus{Account: account.Name, Path: settingsPath, Status: SyncMissing}, nil
	}

	expected := expectedClaudeCommands(override.DisabledEvents, amxBin)
	if commandMapsEqual(existing, expected) {
		return AccountStatus{Account: account.Name, Path: settingsPath, Status: SyncInSync}, nil
	}
	return AccountStatus{Account: account.Name, Path: settingsPath, Status: SyncDrifted}, nil
}

func checkCopilotStatus(account config.Account, override config.AccountHookOverride, amxBin string) (AccountStatus, error) {
	hooksPath := filepath.Join(config.ExpandTilde(account.EffectiveHomeDir()), "hooks", harness.HookFileName())
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		if os.IsNotExist(err) {
			return AccountStatus{Account: account.Name, Path: hooksPath, Status: SyncMissing}, nil
		}
		return AccountStatus{}, err
	}

	var existing copilotHooksFile
	if err := json.Unmarshal(data, &existing); err != nil {
		return AccountStatus{Account: account.Name, Path: hooksPath, Status: SyncMissing}, nil
	}

	expected := buildExpectedCopilotHooks(override.DisabledEvents, amxBin)
	if copilotHooksEqual(existing, expected) {
		return AccountStatus{Account: account.Name, Path: hooksPath, Status: SyncInSync}, nil
	}
	return AccountStatus{Account: account.Name, Path: hooksPath, Status: SyncDrifted}, nil
}

// extractAmxCommands returns a map of event -> command for all amx-managed hook entries.
func extractAmxCommands(hooks map[string][]hookGroup) map[string]string {
	out := make(map[string]string)
	for event, groups := range hooks {
		for _, g := range groups {
			if isAmxGroup(g) {
				for _, e := range g.Hooks {
					out[event] = e.Command
					break
				}
			}
		}
	}
	return out
}

// expectedClaudeCommands returns { event: command } for all non-disabled Claude hook events.
func expectedClaudeCommands(disabledEvents []string, amxBin string) map[string]string {
	disabled := make(map[string]bool, len(disabledEvents))
	for _, e := range disabledEvents {
		disabled[e] = true
	}
	out := make(map[string]string)
	for _, event := range claudeHookEvents {
		if !disabled[event] {
			out[event] = amxBin + " hook-indicator " + event
		}
	}
	return out
}

func commandMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func copilotHooksEqual(a, b copilotHooksFile) bool {
	aj, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(aj, bj)
}
