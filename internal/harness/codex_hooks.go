package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const (
	codexHooksDocumentation = "https://learn.chatgpt.com/docs/hooks"
	codexHooksVerifiedCLI   = "0.144.4"
)

var codexHookMatrix = []HookEventContract{
	{CanonicalName: "SessionStart", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: codexHooksDocumentation, VerifiedCLIVersion: codexHooksVerifiedCLI},
	{CanonicalName: "UserPromptSubmit", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: codexHooksDocumentation, VerifiedCLIVersion: codexHooksVerifiedCLI},
	{CanonicalName: "PreToolUse", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: codexHooksDocumentation, VerifiedCLIVersion: codexHooksVerifiedCLI},
	{CanonicalName: "PermissionRequest", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: codexHooksDocumentation, VerifiedCLIVersion: codexHooksVerifiedCLI},
	{CanonicalName: "PostToolUse", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: codexHooksDocumentation, VerifiedCLIVersion: codexHooksVerifiedCLI},
	{CanonicalName: "PreCompact", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, SpecialSemantics: "matcher distinguishes manual and automatic compaction", DocumentationSource: codexHooksDocumentation, VerifiedCLIVersion: codexHooksVerifiedCLI},
	{CanonicalName: "PostCompact", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, SpecialSemantics: "payload trigger distinguishes manual stop from automatic active continuation", DocumentationSource: codexHooksDocumentation, VerifiedCLIVersion: codexHooksVerifiedCLI},
	{CanonicalName: "SubagentStart", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: codexHooksDocumentation, VerifiedCLIVersion: codexHooksVerifiedCLI},
	{CanonicalName: "SubagentStop", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: codexHooksDocumentation, VerifiedCLIVersion: codexHooksVerifiedCLI},
	{CanonicalName: "Stop", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: codexHooksDocumentation, VerifiedCLIVersion: codexHooksVerifiedCLI},
}

// codexHookEvents lists the Codex hook events Agent Mux installs.
var codexHookEvents = installedHookEventNames(codexHookMatrix)

type codexHookContract struct{}

func (codexHookContract) EventMatrix() []HookEventContract {
	return cloneHookEventMatrix(codexHookMatrix)
}

func (codexHookContract) InstallPath(homeDir string) string {
	return filepath.Join(homeDir, "hooks.json")
}

func (codexHookContract) MergeData(existingData []byte, disabledEvents []string, amxBin string) ([]byte, error) {
	expected := buildCodexHooks(disabledEvents, amxBin)

	existingRaw := make(map[string]json.RawMessage)
	if len(existingData) > 0 {
		if err := json.Unmarshal(existingData, &existingRaw); err != nil {
			return nil, fmt.Errorf("parsing hooks.json: %w", err)
		}
		// Unmarshaling JSON `null` into a map sets it to nil rather than
		// erroring. Re-initialize so the later `existingRaw["hooks"] = ...`
		// assignment does not panic on a nil map.
		if existingRaw == nil {
			existingRaw = make(map[string]json.RawMessage)
		}
	}

	hooksRaw := make(map[string]json.RawMessage)
	if raw, ok := existingRaw["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooksRaw); err != nil {
			return nil, fmt.Errorf("parsing hooks.json hooks section: %w", err)
		}
	}

	existingHooks, passthrough, err := parseCodexHooksSection(hooksRaw)
	if err != nil {
		return nil, err
	}

	merged := stripAmxGroups(existingHooks)
	for event, groups := range expected {
		merged[event] = append(merged[event], groups...)
	}
	for event, groups := range merged {
		if len(groups) == 0 {
			delete(merged, event)
		}
	}

	mergedRaw := make(map[string]json.RawMessage, len(passthrough)+len(merged))
	for key, raw := range passthrough {
		mergedRaw[key] = raw
	}
	for key, groups := range merged {
		raw, err := json.Marshal(groups)
		if err != nil {
			return nil, fmt.Errorf("encoding hooks.json hooks.%s: %w", key, err)
		}
		mergedRaw[key] = json.RawMessage(raw)
	}

	hooksJSON, err := json.Marshal(mergedRaw)
	if err != nil {
		return nil, fmt.Errorf("encoding hooks.json hooks section: %w", err)
	}
	existingRaw["hooks"] = json.RawMessage(hooksJSON)

	out, err := json.MarshalIndent(existingRaw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding hooks.json: %w", err)
	}
	return append(out, '\n'), nil
}

func (codexHookContract) ParseInstalledEvents(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing hooks.json: %w", err)
	}

	var hooksRaw map[string]json.RawMessage
	if rawHooks, ok := raw["hooks"]; ok {
		if err := json.Unmarshal(rawHooks, &hooksRaw); err != nil {
			return nil, fmt.Errorf("parsing hooks.json hooks section: %w", err)
		}
	}

	hooks, _, err := parseCodexHooksSection(hooksRaw)
	if err != nil {
		return nil, err
	}

	var events []string
	for event, groups := range hooks {
		for _, g := range groups {
			if isAmxGroup(g) {
				events = append(events, event)
				break
			}
		}
	}
	sort.Strings(events)
	return events, nil
}

func buildCodexHooks(disabledEvents []string, amxBin string) map[string][]hookGroup {
	disabled := make(map[string]bool, len(disabledEvents))
	for _, e := range disabledEvents {
		disabled[e] = true
	}

	amxCommand := shellQuoteArg(amxBin)
	hooks := make(map[string][]hookGroup)
	for _, event := range codexHookEvents {
		if disabled[event] {
			continue
		}
		hooks[event] = []hookGroup{
			{Hooks: []hookEntry{{
				Type:    "command",
				Command: amxCommand + " hook-indicator " + event + " --harness=" + Codex,
				Timeout: 5,
			}}},
		}
	}
	return hooks
}

func parseCodexHooksSection(raw map[string]json.RawMessage) (map[string][]hookGroup, map[string]json.RawMessage, error) {
	hookGroups := make(map[string][]hookGroup)
	passthrough := make(map[string]json.RawMessage)
	managed := make(map[string]bool, len(codexHookEvents))
	for _, event := range codexHookEvents {
		managed[event] = true
	}

	for key, value := range raw {
		if !managed[key] && isJSONNull(value) {
			passthrough[key] = value
			continue
		}

		var groups []hookGroup
		if err := json.Unmarshal(value, &groups); err != nil {
			if managed[key] {
				return nil, nil, fmt.Errorf("parsing hooks.json hooks.%s: %w", key, err)
			}
			passthrough[key] = value
			continue
		}
		hookGroups[key] = groups
	}

	return hookGroups, passthrough, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
