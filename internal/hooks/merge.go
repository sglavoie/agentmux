package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
)

// hookEntry is a single hook command inside a matcher group.
type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Async   bool   `json:"async"`
}

// hookGroup is a matcher group with one or more hook entries.
type hookGroup struct {
	Hooks []hookEntry `json:"hooks"`
}

// hookEvents lists all Claude Code events that ccm installs hooks for.
var hookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"SubagentStart",
	"SubagentStop",
	"PostToolUse",
	"PostToolUseFailure",
	"PermissionRequest",
	"PermissionDenied",
	"Stop",
	"StopFailure",
	"TaskCompleted",
	"SessionEnd",
	"Notification",
	"PreCompact",
	"PostCompact",
	"Elicitation",
	"ElicitationResult",
}

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

// MergeAll merges ccm hooks into each account's Claude settings.json.
// filter restricts to a single account name (empty = all accounts).
// ccmBin is the absolute path to the ccm binary used in hook commands.
func MergeAll(cfg *config.Config, filter, ccmBin string) ([]MergeResult, error) {
	var results []MergeResult
	matched := false
	for _, account := range cfg.Accounts {
		if filter != "" && account.Name != filter {
			continue
		}
		matched = true
		override := accountOverride(cfg, account.Name)
		result, err := mergeAccount(account, override, ccmBin)
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

func mergeAccount(account config.Account, override config.AccountHookOverride, ccmBin string) (MergeResult, error) {
	dir := config.ExpandTilde(account.ConfigDir)
	settingsPath := filepath.Join(dir, "settings.json")

	expected := buildExpectedHooks(override.DisabledEvents, ccmBin)

	// Load existing settings as raw JSON map to preserve all keys.
	existingRaw := make(map[string]json.RawMessage)
	originalData, err := os.ReadFile(settingsPath)
	fileExisted := true
	if err != nil {
		if !os.IsNotExist(err) {
			return MergeResult{}, fmt.Errorf("reading %s: %w", settingsPath, err)
		}
		fileExisted = false
	} else {
		if err := json.Unmarshal(originalData, &existingRaw); err != nil {
			return MergeResult{}, fmt.Errorf("parsing %s: %w", settingsPath, err)
		}
	}

	// Decode existing hooks section.
	existing := make(map[string][]hookGroup)
	if raw, ok := existingRaw["hooks"]; ok {
		if err := json.Unmarshal(raw, &existing); err != nil {
			existing = make(map[string][]hookGroup)
		}
	}

	// Strip existing ccm hook groups from all events, then append expected.
	merged := stripCcmGroups(existing)
	for event, groups := range expected {
		merged[event] = append(merged[event], groups...)
	}

	// Remove events with empty group arrays to keep settings.json clean.
	for event, groups := range merged {
		if len(groups) == 0 {
			delete(merged, event)
		}
	}

	// Encode merged hooks back into the raw map.
	hooksRaw, err := json.Marshal(merged)
	if err != nil {
		return MergeResult{}, fmt.Errorf("encoding hooks: %w", err)
	}
	existingRaw["hooks"] = json.RawMessage(hooksRaw)

	newData, err := json.MarshalIndent(existingRaw, "", "  ")
	if err != nil {
		return MergeResult{}, fmt.Errorf("encoding settings: %w", err)
	}
	newData = append(newData, '\n')

	if fileExisted && bytes.Equal(originalData, newData) {
		return MergeResult{Account: account.Name, Path: settingsPath, Status: MergeSkipped}, nil
	}

	if fileExisted {
		if err := os.WriteFile(settingsPath+".bak", originalData, 0o644); err != nil {
			return MergeResult{}, fmt.Errorf("creating backup %s.bak: %w", settingsPath, err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return MergeResult{}, fmt.Errorf("creating directory %s: %w", dir, err)
	}
	if err := os.WriteFile(settingsPath, newData, 0o644); err != nil {
		return MergeResult{}, fmt.Errorf("writing %s: %w", settingsPath, err)
	}

	status := MergeUpdated
	if !fileExisted {
		status = MergeCreated
	}
	return MergeResult{Account: account.Name, Path: settingsPath, Status: status}, nil
}

// buildExpectedHooks returns the hook groups ccm should install,
// excluding events in disabledEvents.
func buildExpectedHooks(disabledEvents []string, ccmBin string) map[string][]hookGroup {
	disabled := make(map[string]bool, len(disabledEvents))
	for _, e := range disabledEvents {
		disabled[e] = true
	}
	hooks := make(map[string][]hookGroup)
	for _, event := range hookEvents {
		if disabled[event] {
			continue
		}
		hooks[event] = []hookGroup{
			{Hooks: []hookEntry{{
				Type:    "command",
				Command: ccmBin + " hook-indicator " + event,
				Async:   true,
			}}},
		}
	}
	return hooks
}

// stripCcmGroups returns a copy of hooks with all ccm-managed matcher groups removed.
// A group is ccm-managed if any of its entries has a command containing "hook-indicator".
func stripCcmGroups(hooks map[string][]hookGroup) map[string][]hookGroup {
	out := make(map[string][]hookGroup, len(hooks))
	for event, groups := range hooks {
		var kept []hookGroup
		for _, g := range groups {
			if !isCcmGroup(g) {
				kept = append(kept, g)
			}
		}
		if len(kept) > 0 {
			out[event] = kept
		}
	}
	return out
}

func isCcmGroup(g hookGroup) bool {
	for _, e := range g.Hooks {
		if strings.Contains(e.Command, "hook-indicator") {
			return true
		}
	}
	return false
}
