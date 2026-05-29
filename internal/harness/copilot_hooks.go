package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type copilotHooksFile struct {
	Version int                         `json:"version"`
	Hooks   map[string][]copilotHookDef `json:"hooks"`
}

type copilotHookDef struct {
	Type       string `json:"type"`
	Bash       string `json:"bash,omitempty"`
	Powershell string `json:"powershell,omitempty"`
	TimeoutSec int    `json:"timeoutSec,omitempty"`
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

type copilotHookContract struct{}

func (copilotHookContract) InstallPath(homeDir string) string {
	return filepath.Join(homeDir, "hooks", HookFileName())
}

func (copilotHookContract) MergeData(existingData []byte, disabledEvents []string, amxBin string) ([]byte, error) {
	expected := buildCopilotHooks(disabledEvents, amxBin)

	newData, err := json.MarshalIndent(expected, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding copilot hooks file: %w", err)
	}
	newData = append(newData, '\n')

	// If there's existing data that's semantically equal, return original bytes
	// so the caller's byte comparison detects "no change".
	if len(existingData) > 0 {
		var existing copilotHooksFile
		if err := json.Unmarshal(existingData, &existing); err == nil {
			if copilotHooksEqual(existing, expected) {
				return existingData, nil
			}
		}
	}

	return newData, nil
}

func (copilotHookContract) ParseInstalledEvents(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var f copilotHooksFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, nil
	}

	var events []string
	for event, defs := range f.Hooks {
		for _, def := range defs {
			if isAmxCopilotHookDef(def) {
				events = append(events, event)
				break
			}
		}
	}
	return events, nil
}

func isAmxCopilotHookDef(def copilotHookDef) bool {
	return isAmxHookEntry(hookEntry{
		Command:        def.Bash,
		CommandWindows: def.Powershell,
	})
}

func buildCopilotHooks(disabledEvents []string, amxBin string) copilotHooksFile {
	disabled := make(map[string]bool, len(disabledEvents))
	for _, e := range disabledEvents {
		disabled[e] = true
	}
	hooks := make(map[string][]copilotHookDef)
	for _, event := range copilotHookEvents {
		if disabled[event] {
			continue
		}
		cmd := amxBin + " hook-indicator " + event
		hooks[event] = []copilotHookDef{{
			Type:       "command",
			Bash:       cmd,
			Powershell: cmd,
			TimeoutSec: 5,
		}}
	}
	return copilotHooksFile{Version: 1, Hooks: hooks}
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
