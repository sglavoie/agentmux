package hooks

import (
	"bytes"
	"os"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

// SyncStatus describes whether an account's hooks are in sync with the config.
type SyncStatus string

const (
	// SyncInSync means the hooks file has the expected amx hooks installed.
	SyncInSync SyncStatus = "in_sync"
	// SyncDrifted means amx hooks exist but differ from what amx would write.
	SyncDrifted SyncStatus = "drifted"
	// SyncMissing means the hooks file is absent or has no amx hooks.
	SyncMissing SyncStatus = "missing"
)

// AccountStatus holds the sync status for one account.
type AccountStatus struct {
	Account string
	Harness string
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
	h := harness.Lookup(account.EffectiveHarness())
	contract := h.HookContract()
	homeDir := config.ExpandTilde(account.EffectiveHomeDir())
	path := contract.InstallPath(homeDir)

	harnessName := account.EffectiveHarness()
	if harnessName == "" {
		harnessName = "claude"
	}

	existingData, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return AccountStatus{Account: account.Name, Harness: harnessName, Path: path, Status: SyncMissing}, nil
		}
		return AccountStatus{}, err
	}

	installed, err := contract.ParseInstalledEvents(path)
	if err != nil {
		return AccountStatus{}, err
	}
	if len(installed) == 0 {
		return AccountStatus{Account: account.Name, Harness: harnessName, Path: path, Status: SyncMissing}, nil
	}

	newData, err := contract.MergeData(existingData, override.DisabledEvents, amxBin)
	if err != nil {
		return AccountStatus{}, err
	}
	if bytes.Equal(existingData, newData) {
		return AccountStatus{Account: account.Name, Harness: harnessName, Path: path, Status: SyncInSync}, nil
	}
	return AccountStatus{Account: account.Name, Harness: harnessName, Path: path, Status: SyncDrifted}, nil
}
