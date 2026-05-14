package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
)

// SyncStatus describes whether an account's hooks are in sync with the config.
type SyncStatus string

const (
	// SyncInSync means settings.json has the expected ccm hooks installed.
	SyncInSync SyncStatus = "in_sync"
	// SyncDrifted means ccm hooks exist but differ from what ccm would write.
	SyncDrifted SyncStatus = "drifted"
	// SyncMissing means settings.json is absent or has no ccm hooks.
	SyncMissing SyncStatus = "missing"
)

// AccountStatus holds the sync status for one account.
type AccountStatus struct {
	Account string
	Path    string
	Status  SyncStatus
}

// StatusAll returns the hooks sync status for every account in cfg.
// ccmBin is the absolute path to the ccm binary (used to determine expected commands).
func StatusAll(cfg *config.Config, ccmBin string) ([]AccountStatus, error) {
	results := make([]AccountStatus, 0, len(cfg.Accounts))
	for _, account := range cfg.Accounts {
		override := accountOverride(cfg, account.Name)
		status, err := checkStatus(account, override, ccmBin)
		if err != nil {
			return nil, err
		}
		results = append(results, status)
	}
	return results, nil
}

func checkStatus(account config.Account, override config.AccountHookOverride, ccmBin string) (AccountStatus, error) {
	dir := config.ExpandTilde(account.ConfigDir)
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

	existing := extractCcmCommands(existingHooks)
	if len(existing) == 0 {
		return AccountStatus{Account: account.Name, Path: settingsPath, Status: SyncMissing}, nil
	}

	expected := expectedCommands(override.DisabledEvents, ccmBin)
	if commandMapsEqual(existing, expected) {
		return AccountStatus{Account: account.Name, Path: settingsPath, Status: SyncInSync}, nil
	}
	return AccountStatus{Account: account.Name, Path: settingsPath, Status: SyncDrifted}, nil
}

// extractCcmCommands returns a map of event → command for all ccm-managed hook entries.
func extractCcmCommands(hooks map[string][]hookGroup) map[string]string {
	out := make(map[string]string)
	for event, groups := range hooks {
		for _, g := range groups {
			if isCcmGroup(g) {
				for _, e := range g.Hooks {
					out[event] = e.Command
					break
				}
			}
		}
	}
	return out
}

// expectedCommands returns { event: command } for all non-disabled ccm hook events.
func expectedCommands(disabledEvents []string, ccmBin string) map[string]string {
	disabled := make(map[string]bool, len(disabledEvents))
	for _, e := range disabledEvents {
		disabled[e] = true
	}
	out := make(map[string]string)
	for _, event := range hookEvents {
		if !disabled[event] {
			out[event] = ccmBin + " hook-indicator " + event
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
