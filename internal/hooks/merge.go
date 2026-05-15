package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
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

// claudeHookEvents lists the Claude Code events Agent Mux installs hooks for.
var claudeHookEvents = []string{
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

// copilotHookEvents lists the GitHub Copilot CLI hook events Agent Mux installs.
var copilotHookEvents = []string{
	"sessionStart",
	"sessionEnd",
	"userPromptSubmitted",
	"preToolUse",
	"postToolUse",
	"postToolUseFailure",
	"permissionRequest",
	"agentStop",
	"subagentStart",
	"subagentStop",
	"errorOccurred",
	"preCompact",
	"notification",
}

type copilotHooksFile struct {
	Version int                         `json:"version"`
	Hooks   map[string][]copilotHookDef `json:"hooks"`
}

type copilotHookDef struct {
	Type       string `json:"type"`
	Command    string `json:"command,omitempty"`
	TimeoutSec int    `json:"timeoutSec,omitempty"`
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

// MergeAll syncs Agent Mux hooks for each configured profile.
// filter restricts to a single profile name (empty = all profiles).
// amxBin is the absolute path to the amx binary used in hook commands.
func MergeAll(cfg *config.Config, filter, amxBin string) ([]MergeResult, error) {
	var results []MergeResult
	matched := false
	for _, account := range cfg.Accounts {
		if filter != "" && account.Name != filter {
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
	switch account.EffectiveHarness() {
	case harness.Copilot:
		return mergeCopilotProfile(account, override, amxBin)
	default:
		return mergeClaudeProfile(account, override, amxBin)
	}
}

func mergeClaudeProfile(account config.Account, override config.AccountHookOverride, amxBin string) (MergeResult, error) {
	dir := config.ExpandTilde(account.EffectiveHomeDir())
	settingsPath := filepath.Join(dir, "settings.json")

	expected := buildExpectedClaudeHooks(override.DisabledEvents, amxBin)

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

	// Strip existing amx hook groups from all events, then append expected.
	merged := stripAmxGroups(existing)
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

func mergeCopilotProfile(account config.Account, override config.AccountHookOverride, amxBin string) (MergeResult, error) {
	dir := config.ExpandTilde(account.EffectiveHomeDir())
	hooksPath := filepath.Join(dir, "hooks", harness.HookFileName())
	expected := buildExpectedCopilotHooks(override.DisabledEvents, amxBin)

	newData, err := json.MarshalIndent(expected, "", "  ")
	if err != nil {
		return MergeResult{}, fmt.Errorf("encoding hooks file: %w", err)
	}
	newData = append(newData, '\n')

	originalData, err := os.ReadFile(hooksPath)
	fileExisted := true
	if err != nil {
		if !os.IsNotExist(err) {
			return MergeResult{}, fmt.Errorf("reading %s: %w", hooksPath, err)
		}
		fileExisted = false
	}

	if fileExisted && bytes.Equal(originalData, newData) {
		return MergeResult{Account: account.Name, Path: hooksPath, Status: MergeSkipped}, nil
	}

	if fileExisted {
		if err := os.WriteFile(hooksPath+".bak", originalData, 0o644); err != nil {
			return MergeResult{}, fmt.Errorf("creating backup %s.bak: %w", hooksPath, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		return MergeResult{}, fmt.Errorf("creating directory %s: %w", filepath.Dir(hooksPath), err)
	}
	if err := os.WriteFile(hooksPath, newData, 0o644); err != nil {
		return MergeResult{}, fmt.Errorf("writing %s: %w", hooksPath, err)
	}

	status := MergeUpdated
	if !fileExisted {
		status = MergeCreated
	}
	return MergeResult{Account: account.Name, Path: hooksPath, Status: status}, nil
}

// buildExpectedClaudeHooks returns the hook groups Agent Mux should install,
// excluding events in disabledEvents.
func buildExpectedClaudeHooks(disabledEvents []string, amxBin string) map[string][]hookGroup {
	disabled := make(map[string]bool, len(disabledEvents))
	for _, e := range disabledEvents {
		disabled[e] = true
	}
	hooks := make(map[string][]hookGroup)
	for _, event := range claudeHookEvents {
		if disabled[event] {
			continue
		}
		hooks[event] = []hookGroup{
			{Hooks: []hookEntry{{
				Type:    "command",
				Command: amxBin + " hook-indicator " + event,
				Async:   true,
			}}},
		}
	}
	return hooks
}

func buildExpectedCopilotHooks(disabledEvents []string, amxBin string) copilotHooksFile {
	disabled := make(map[string]bool, len(disabledEvents))
	for _, e := range disabledEvents {
		disabled[e] = true
	}
	hooks := make(map[string][]copilotHookDef)
	for _, event := range copilotHookEvents {
		if disabled[event] {
			continue
		}
		hooks[event] = []copilotHookDef{{
			Type:       "command",
			Command:    amxBin + " hook-indicator " + event,
			TimeoutSec: 5,
		}}
	}
	return copilotHooksFile{
		Version: 1,
		Hooks:   hooks,
	}
}

// stripAmxGroups returns a copy of hooks with all amx-managed matcher groups removed.
// A group is amx-managed if any of its entries has a command containing "hook-indicator".
func stripAmxGroups(hooks map[string][]hookGroup) map[string][]hookGroup {
	out := make(map[string][]hookGroup, len(hooks))
	for event, groups := range hooks {
		var kept []hookGroup
		for _, g := range groups {
			if !isAmxGroup(g) {
				kept = append(kept, g)
			}
		}
		if len(kept) > 0 {
			out[event] = kept
		}
	}
	return out
}

func isAmxGroup(g hookGroup) bool {
	for _, e := range g.Hooks {
		if strings.Contains(e.Command, "hook-indicator") {
			return true
		}
	}
	return false
}
