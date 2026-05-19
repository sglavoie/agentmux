package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// amxHookCommandIdentifier matches the executable basenames Agent Mux uses for
// its hook command. The merge logic detects amx-owned hook groups so they can
// be replaced atomically; anything else is preserved verbatim.
const amxHookCommandIdentifier = "amx"

// hookEntry is a single hook command inside a matcher group.
type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Async   bool   `json:"async"`
}

// hookGroup is a matcher group with one or more hook entries.
type hookGroup struct {
	Matcher string      `json:"matcher,omitempty"`
	Hooks   []hookEntry `json:"hooks"`
}

// claudeHookEvents lists the Claude Code events Agent Mux installs hooks for.
var claudeHookEvents = []string{
	"SessionStart",
	"Setup",
	"UserPromptSubmit",
	"UserPromptExpansion",
	"PreToolUse",
	"SubagentStart",
	"SubagentStop",
	"PostToolUse",
	"PostToolUseFailure",
	"PostToolBatch",
	"PermissionRequest",
	"PermissionDenied",
	"Stop",
	"StopFailure",
	"TaskCreated",
	"TaskCompleted",
	"TeammateIdle",
	"SessionEnd",
	"Notification",
	"ConfigChange",
	"WorktreeCreate",
	"WorktreeRemove",
	"PreCompact",
	"PostCompact",
	"Elicitation",
	"ElicitationResult",
}

type claudeHookContract struct{}

func (claudeHookContract) InstallPath(homeDir string) string {
	return filepath.Join(homeDir, "settings.json")
}

func (claudeHookContract) MergeData(existingData []byte, disabledEvents []string, amxBin string) ([]byte, error) {
	expected := buildClaudeHooks(disabledEvents, amxBin)

	existingRaw := make(map[string]json.RawMessage)
	if len(existingData) > 0 {
		if err := json.Unmarshal(existingData, &existingRaw); err != nil {
			return nil, fmt.Errorf("parsing settings.json: %w", err)
		}
	}

	existing := make(map[string][]hookGroup)
	if raw, ok := existingRaw["hooks"]; ok {
		// Bail out rather than overwrite a malformed `hooks` value: silently
		// resetting would destroy the user's pre-existing entries (the
		// in-flight merge writes a fresh hooks map back over the top), which
		// is unrecoverable for anyone whose .bak rotation missed the change.
		if err := json.Unmarshal(raw, &existing); err != nil {
			return nil, fmt.Errorf("parsing settings.json hooks section: %w", err)
		}
	}

	merged := stripAmxGroups(existing)
	for event, groups := range expected {
		merged[event] = append(merged[event], groups...)
	}
	for event, groups := range merged {
		if len(groups) == 0 {
			delete(merged, event)
		}
	}

	hooksRaw, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("encoding hooks: %w", err)
	}
	existingRaw["hooks"] = json.RawMessage(hooksRaw)

	out, err := json.MarshalIndent(existingRaw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding settings: %w", err)
	}
	return append(out, '\n'), nil
}

func (claudeHookContract) ParseInstalledEvents(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil
	}

	var hooks map[string][]hookGroup
	if hooksRaw, ok := raw["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &hooks); err != nil {
			return nil, nil
		}
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
	return events, nil
}

func buildClaudeHooks(disabledEvents []string, amxBin string) map[string][]hookGroup {
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

func stripAmxGroups(hooks map[string][]hookGroup) map[string][]hookGroup {
	out := make(map[string][]hookGroup, len(hooks))
	for event, groups := range hooks {
		var kept []hookGroup
		for _, g := range groups {
			// Drop only the amx-installed entries within the group so any
			// user-added hooks colocated by hand-editing survive the sync.
			// A pure-amx group leaves nothing behind and is dropped entirely.
			var keptHooks []hookEntry
			for _, e := range g.Hooks {
				if !isAmxHookEntry(e) {
					keptHooks = append(keptHooks, e)
				}
			}
			if len(keptHooks) == 0 {
				continue
			}
			g.Hooks = keptHooks
			kept = append(kept, g)
		}
		if len(kept) > 0 {
			out[event] = kept
		}
	}
	return out
}

// isAmxGroup reports whether g was installed by Agent Mux. The check inspects
// the command's first token (the executable) and requires its basename to be
// "amx" with a "hook-indicator" subcommand argument. Substring matching on
// "hook-indicator" alone would misclassify unrelated user tools that happen to
// share that token (e.g. a path like /opt/hook-indicator-helper/run) and
// silently wipe them on every sync.
func isAmxGroup(g hookGroup) bool {
	for _, e := range g.Hooks {
		if isAmxHookEntry(e) {
			return true
		}
	}
	return false
}

// isAmxHookEntry reports whether a single hookEntry was installed by Agent Mux.
func isAmxHookEntry(e hookEntry) bool {
	fields := strings.Fields(e.Command)
	if len(fields) < 2 {
		return false
	}
	if filepath.Base(fields[0]) != amxHookCommandIdentifier {
		return false
	}
	return fields[1] == "hook-indicator"
}
