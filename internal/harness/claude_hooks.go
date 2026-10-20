package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/shellquote"
)

// amxHookCommandIdentifier matches the executable basenames Agent Mux uses for
// its hook command. The merge logic detects amx-owned hook groups so they can
// be replaced atomically; anything else is preserved verbatim.
const amxHookCommandIdentifier = "amx"

// hookEntry is a single hook handler inside a matcher group.
type hookEntry struct {
	Type           string `json:"type"`
	Command        string `json:"command,omitempty"`
	CommandWindows string `json:"commandWindows,omitempty"`
	Async          *bool  `json:"async,omitempty"`
	Timeout        int    `json:"timeout,omitempty"`
	StatusMessage  string `json:"statusMessage,omitempty"`
}

// hookGroup is a matcher group with one or more hook entries.
type hookGroup struct {
	Matcher string      `json:"matcher,omitempty"`
	Hooks   []hookEntry `json:"hooks"`
}

const (
	claudeHooksDocumentation = "https://code.claude.com/docs/en/hooks"
	claudeHooksVerifiedCLI   = "2.1.195"
)

var claudeHookMatrix = []HookEventContract{
	{CanonicalName: "SessionStart", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "Setup", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "InstructionsLoaded", Behavior: HookObserverOnly, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "UserPromptSubmit", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "UserPromptExpansion", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "MessageDisplay", Behavior: HookObserverOnly, Installation: HookInstalled, DisableSupported: true, SpecialSemantics: "display-only stream batches are recorded without returning displayContent", DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "PreToolUse", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "PermissionRequest", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "PostToolUse", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "PostToolUseFailure", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "PostToolBatch", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "PermissionDenied", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "Notification", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, SpecialSemantics: "notification_type decides permission stop or observer behavior", DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "SubagentStart", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "SubagentStop", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "TaskCreated", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "TaskCompleted", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "Stop", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "StopFailure", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "TeammateIdle", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "ConfigChange", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "CwdChanged", Behavior: HookObserverOnly, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "FileChanged", Behavior: HookObserverOnly, Installation: HookUnsupported, DisableSupported: false, SpecialSemantics: "requires an explicit bounded literal watch-path matcher policy", DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "WorktreeCreate", Behavior: HookStateChanging, Installation: HookUnsupported, DisableSupported: false, SpecialSemantics: "replaces Claude's default worktree creation and must return a valid worktree path", DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "WorktreeRemove", Behavior: HookObserverOnly, Installation: HookInstalled, DisableSupported: true, SpecialSemantics: "cleanup notification is recorded but Agent Mux never removes the worktree", DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "PreCompact", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "PostCompact", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, SpecialSemantics: "manual and automatic matcher groups dispatch distinct Agent Mux pseudo-events", DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "SessionEnd", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "Elicitation", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
	{CanonicalName: "ElicitationResult", Behavior: HookStateChanging, Installation: HookInstalled, DisableSupported: true, DocumentationSource: claudeHooksDocumentation, VerifiedCLIVersion: claudeHooksVerifiedCLI},
}

// claudeHookEvents lists the Claude Code events Agent Mux installs hooks for.
var claudeHookEvents = installedHookEventNames(claudeHookMatrix)

type claudeHookContract struct{}

func (claudeHookContract) EventMatrix() []HookEventContract {
	return cloneHookEventMatrix(claudeHookMatrix)
}

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
		// Unmarshaling JSON `null` into a map sets it to nil rather than
		// erroring. Re-initialize so the later `existingRaw["hooks"] = ...`
		// assignment does not panic on a nil map.
		if existingRaw == nil {
			existingRaw = make(map[string]json.RawMessage)
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
		return nil, fmt.Errorf("parsing settings.json: %w", err)
	}

	var hooks map[string][]hookGroup
	if hooksRaw, ok := raw["hooks"]; ok {
		if err := json.Unmarshal(hooksRaw, &hooks); err != nil {
			return nil, fmt.Errorf("parsing settings.json hooks section: %w", err)
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
	sort.Strings(events)
	return events, nil
}

func buildClaudeHooks(disabledEvents []string, amxBin string) map[string][]hookGroup {
	disabled := make(map[string]bool, len(disabledEvents))
	for _, e := range disabledEvents {
		disabled[e] = true
	}
	amxCommand := shellquote.Quote(amxBin)
	hooks := make(map[string][]hookGroup)
	for _, event := range claudeHookEvents {
		if disabled[event] {
			continue
		}
		// Claude Code fires PostCompact for both manual /compact and automatic
		// mid-turn compaction; the payload carries no trigger field, so only
		// hook-config matchers can tell them apart. Install two matcher groups
		// dispatching distinct pseudo-events: manual compaction is a turn end
		// (stop), auto compaction happens mid-turn while Claude keeps working
		// (active). The map key stays "PostCompact" so disabled_events and
		// ParseInstalledEvents keep operating on the real event name — and
		// disabling PostCompact drops both groups.
		if event == "PostCompact" {
			hooks[event] = []hookGroup{
				{Matcher: "manual", Hooks: []hookEntry{{
					Type:    "command",
					Command: amxCommand + " hook-indicator PostCompactManual --harness=" + Claude,
					Async:   boolPtr(true),
				}}},
				{Matcher: "auto", Hooks: []hookEntry{{
					Type:    "command",
					Command: amxCommand + " hook-indicator PostCompactAuto --harness=" + Claude,
					Async:   boolPtr(true),
				}}},
			}
			continue
		}
		hooks[event] = []hookGroup{
			{Hooks: []hookEntry{{
				Type:    "command",
				Command: amxCommand + " hook-indicator " + event + " --harness=" + Claude,
				Async:   boolPtr(true),
			}}},
		}
	}
	return hooks
}

func boolPtr(v bool) *bool { return &v }

func powershellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if !strings.ContainsAny(arg, " \t\n'\"`$&;()<>|*?[]{}") {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "''") + "'"
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
// "amx" or "amx.exe" with a "hook-indicator" subcommand argument. Substring
// matching on "hook-indicator" alone would misclassify unrelated user tools
// that happen to share that token (e.g. a path like
// /opt/hook-indicator-helper/run) and silently wipe them on every sync.
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
	if e.Type != "command" {
		return false
	}

	const marker = " hook-indicator "
	for _, candidate := range []string{e.Command, e.CommandWindows} {
		idx := strings.LastIndex(candidate, marker)
		if idx <= 0 {
			continue
		}
		if isAmxExecutable(commandExecutableBase(candidate[:idx])) {
			return true
		}
	}
	return false
}

func commandExecutableBase(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if strings.HasPrefix(prefix, "&") {
		prefix = strings.TrimSpace(strings.TrimPrefix(prefix, "&"))
	}
	prefix = strings.Trim(prefix, `"'`)
	prefix = strings.TrimRight(prefix, `/\`)

	i := strings.LastIndexAny(prefix, `/\`)
	if i >= 0 {
		return prefix[i+1:]
	}
	return filepath.Base(prefix)
}

func isAmxExecutable(base string) bool {
	base = strings.ToLower(base)
	return base == amxHookCommandIdentifier || base == amxHookCommandIdentifier+".exe"
}
