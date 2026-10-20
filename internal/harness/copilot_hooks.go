package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sglavoie/agentmux/internal/shellquote"
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

const (
	copilotHooksDocumentation = "https://docs.github.com/en/copilot/reference/hooks-reference"
	copilotHooksVerifiedCLI   = "1.0.70"
)

var copilotHookMatrix = []HookEventContract{
	{CanonicalName: "sessionStart", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: copilotHooksDocumentation, VerifiedCLIVersion: copilotHooksVerifiedCLI},
	{CanonicalName: "sessionEnd", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: copilotHooksDocumentation, VerifiedCLIVersion: copilotHooksVerifiedCLI},
	{CanonicalName: "userPromptSubmitted", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: copilotHooksDocumentation, VerifiedCLIVersion: copilotHooksVerifiedCLI},
	{CanonicalName: "preToolUse", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: copilotHooksDocumentation, VerifiedCLIVersion: copilotHooksVerifiedCLI},
	{CanonicalName: "postToolUse", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: copilotHooksDocumentation, VerifiedCLIVersion: copilotHooksVerifiedCLI},
	{CanonicalName: "postToolUseFailure", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: copilotHooksDocumentation, VerifiedCLIVersion: copilotHooksVerifiedCLI},
	{CanonicalName: "permissionRequest", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, SpecialSemantics: "fires before Copilot permission resolution and may be non-blocking until an explicit prompt", DocumentationSource: copilotHooksDocumentation, VerifiedCLIVersion: copilotHooksVerifiedCLI},
	{CanonicalName: "agentStop", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: copilotHooksDocumentation, VerifiedCLIVersion: copilotHooksVerifiedCLI},
	{CanonicalName: "subagentStart", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: copilotHooksDocumentation, VerifiedCLIVersion: copilotHooksVerifiedCLI},
	{CanonicalName: "subagentStop", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: copilotHooksDocumentation, VerifiedCLIVersion: copilotHooksVerifiedCLI},
	{CanonicalName: "errorOccurred", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: copilotHooksDocumentation, VerifiedCLIVersion: copilotHooksVerifiedCLI},
	{CanonicalName: "preCompact", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: copilotHooksDocumentation, VerifiedCLIVersion: copilotHooksVerifiedCLI},
	{CanonicalName: "notification", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, SpecialSemantics: "notification_type decides whether the event changes state or remains observational", DocumentationSource: copilotHooksDocumentation, VerifiedCLIVersion: copilotHooksVerifiedCLI},
}

// copilotHookEvents lists the GitHub Copilot CLI hook events Agent Mux installs.
var copilotHookEvents = installedHookEventNames(copilotHookMatrix)

type copilotHookContract struct{}

func (copilotHookContract) EventMatrix() []HookEventContract {
	return cloneHookEventMatrix(copilotHookMatrix)
}

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
		if err := json.Unmarshal(existingData, &existing); err != nil {
			return nil, fmt.Errorf("parsing agent-mux.json: %w", err)
		}
		if copilotHooksEqual(existing, expected) {
			return existingData, nil
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
		return nil, fmt.Errorf("parsing agent-mux.json: %w", err)
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
	sort.Strings(events)
	return events, nil
}

func isAmxCopilotHookDef(def copilotHookDef) bool {
	return isAmxHookEntry(hookEntry{
		Type:           def.Type,
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
	bashCommand := shellquote.Quote(amxBin)
	powershellCommand := powershellQuoteArg(amxBin)
	for _, event := range copilotHookEvents {
		if disabled[event] {
			continue
		}
		hooks[event] = []copilotHookDef{{
			Type:       "command",
			Bash:       bashCommand + " hook-indicator " + event + " --harness=" + Copilot,
			Powershell: powershellCommand + " hook-indicator " + event + " --harness=" + Copilot,
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
