package hooks

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

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
	// Match the case-insensitive contract used everywhere else (session.findAccount,
	// the hooks-sync filter above, project profile references). A user writing
	// [hooks.overrides.Work] against profile "work" should get the override applied,
	// not silently dropped.
	for key, override := range cfg.Hooks.Overrides {
		if strings.EqualFold(key, name) {
			return override
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
	writePath, err := resolveHookWritePath(path)
	if err != nil {
		return MergeResult{}, err
	}

	existingData, err := os.ReadFile(writePath)
	fileExisted := true
	if err != nil {
		if !os.IsNotExist(err) {
			return MergeResult{}, fmt.Errorf("reading %s: %w", path, err)
		}
		fileExisted = false
		existingData = nil
	}

	// Preserve the existing file's permission bits across the rewrite. Hooks
	// files may legitimately be tightened to 0o600 by users (the harness
	// settings can carry tokens or environment secrets); a hardcoded 0o644
	// silently widens the mode and leaks contents to other local users. When
	// the file does not yet exist, fall back to the standard 0o644 default
	// and let the umask narrow it.
	perm := os.FileMode(0o644)
	if fileExisted {
		if info, statErr := os.Stat(writePath); statErr == nil {
			perm = info.Mode().Perm()
		}
	} else {
		perm &^= processUmask()
	}

	newData, err := contract.MergeData(existingData, override.DisabledEvents, amxBin)
	if err != nil {
		return MergeResult{}, fmt.Errorf("computing hooks for %s: %w", path, err)
	}

	if fileExisted && bytes.Equal(existingData, newData) {
		return MergeResult{Account: account.Name, Path: path, Status: MergeSkipped}, nil
	}

	if fileExisted {
		if err := writeBackup(writePath+".bak", existingData, perm); err != nil {
			return MergeResult{}, fmt.Errorf("creating backup %s.bak: %w", writePath, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
		return MergeResult{}, fmt.Errorf("creating directory %s: %w", filepath.Dir(writePath), err)
	}
	if err := writeSettings(writePath, newData, perm); err != nil {
		return MergeResult{}, fmt.Errorf("writing %s: %w", path, err)
	}

	status := MergeUpdated
	if !fileExisted {
		status = MergeCreated
	}
	return MergeResult{Account: account.Name, Path: path, Status: status}, nil
}

func resolveHookWritePath(path string) (string, error) {
	current := path
	for range 255 {
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return current, nil
			}
			return "", fmt.Errorf("checking %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return current, nil
		}

		target, err := os.Readlink(current)
		if err != nil {
			return "", fmt.Errorf("reading symlink %s: %w", current, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(current), target)
		}
		current = filepath.Clean(target)
	}
	return "", fmt.Errorf("resolving symlink chain for %s: too many links", path)
}

func processUmask() os.FileMode {
	u := syscall.Umask(0)
	syscall.Umask(u)
	return os.FileMode(u)
}
