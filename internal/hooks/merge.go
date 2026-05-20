package hooks

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

// MergeStatus describes the outcome of a merge operation for one account.
type MergeStatus string

const (
	MergeCreated MergeStatus = "created"
	MergeUpdated MergeStatus = "updated"
	MergeSkipped MergeStatus = "skipped"
)

// MergeResult holds the outcome of merging hooks for one account.
type MergeResult struct {
	Account string
	Path    string
	Status  MergeStatus
	Err     error
}

// MergeAll syncs Agent Mux hooks for each configured profile.
// filter restricts to a single profile name (empty = all profiles).
// amxBin is the absolute path to the amx binary used in hook commands.
func MergeAll(cfg *config.Config, filter, amxBin string) ([]MergeResult, error) {
	var results []MergeResult
	matched := false
	for _, account := range cfg.Accounts {
		// Case-insensitive compare: SessionKey lowercases profile names at
		// runtime and config validation rejects case-only duplicates, so the
		// CLI filter (`amx config hooks-sync NAME`) should accept any casing
		// the user types — mirroring session.findAccount and
		// session.RestartWithAccount.
		if filter != "" && !strings.EqualFold(account.Name, filter) {
			continue
		}
		matched = true
		override := accountOverride(cfg, account.Name)
		result, err := mergeAccount(account, override, amxBin)
		if err != nil {
			result = MergeResult{Account: account.Name, Err: err}
		}
		results = append(results, result)
	}
	if filter != "" && !matched {
		return nil, fmt.Errorf("account %q not found in config", filter)
	}
	return results, nil
}

func accountOverride(cfg *config.Config, name string) config.AccountHookOverride {
	if cfg.Hooks.Overrides != nil {
		if o, ok := cfg.Hooks.Overrides[name]; ok {
			return o
		}
	}
	return config.AccountHookOverride{}
}

func mergeAccount(account config.Account, override config.AccountHookOverride, amxBin string) (MergeResult, error) {
	h := harness.Lookup(account.EffectiveHarness())
	contract := h.HookContract()
	homeDir := config.ExpandTilde(account.EffectiveHomeDir())
	return mergeViaContract(contract, homeDir, account, override, amxBin)
}

func mergeViaContract(contract harness.HookContract, homeDir string, account config.Account, override config.AccountHookOverride, amxBin string) (MergeResult, error) {
	path := contract.InstallPath(homeDir)

	existingData, err := os.ReadFile(path)
	fileExisted := true
	if err != nil {
		if !os.IsNotExist(err) {
			return MergeResult{}, fmt.Errorf("reading %s: %w", path, err)
		}
		fileExisted = false
		existingData = nil
	}

	newData, err := contract.MergeData(existingData, override.DisabledEvents, amxBin)
	if err != nil {
		return MergeResult{}, fmt.Errorf("computing hooks for %s: %w", path, err)
	}

	if fileExisted && bytes.Equal(existingData, newData) {
		return MergeResult{Account: account.Name, Path: path, Status: MergeSkipped}, nil
	}

	if fileExisted {
		if err := os.WriteFile(path+".bak", existingData, 0o644); err != nil {
			return MergeResult{}, fmt.Errorf("creating backup %s.bak: %w", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return MergeResult{}, fmt.Errorf("creating directory %s: %w", filepath.Dir(path), err)
	}
	if err := atomicWriteFile(path, newData, 0o644); err != nil {
		return MergeResult{}, fmt.Errorf("writing %s: %w", path, err)
	}

	status := MergeUpdated
	if !fileExisted {
		status = MergeCreated
	}
	return MergeResult{Account: account.Name, Path: path, Status: status}, nil
}
