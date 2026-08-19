package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
)

type jsonHookSettings struct {
	DisableAllHooks       *bool `json:"disableAllHooks"`
	AllowManagedHooksOnly *bool `json:"allowManagedHooksOnly"`
}

func (e *activationEvaluator) inspectClaude(account config.Account, contexts []activationContext, status *AccountStatus) {
	managed, managedPaths := readManagedJSONSettings(activationClaudeManagedRoot())
	for _, path := range managedPaths.errors {
		addActivationIssue(status, ActivationUnverified, "claude_managed_settings_unavailable", path.err.Error(), path.path)
	}
	if managed.AllowManagedHooksOnly != nil && *managed.AllowManagedHooksOnly {
		addActivationIssue(status, ActivationFailure, "managed_hooks_only", "Claude allowManagedHooksOnly excludes Agent Mux user hooks", managedPaths.last)
	}
	if managed.DisableAllHooks != nil && *managed.DisableAllHooks {
		addActivationIssue(status, ActivationFailure, "claude_hooks_disabled", "Claude managed settings disable all hooks", managedPaths.last)
	}

	userSettings, err := readJSONHookSettings(status.Path)
	if err != nil {
		addActivationIssue(status, ActivationUnverified, "claude_settings_unavailable", err.Error(), status.Path)
	}
	for _, launch := range contexts {
		if hasArg(launch.args, "--bare") {
			addActivationIssue(status, ActivationFailure, "claude_bare", launch.name+" launches with --bare which skips hooks", "")
		}
		if hasArg(launch.args, "--safe-mode") {
			addActivationIssue(status, ActivationFailure, "claude_safe_mode", launch.name+" launches with --safe-mode which disables user hooks", "")
		}
		sources := claudeSettingSources(launch.args)
		if len(sources) > 0 && !sources["user"] {
			addActivationIssue(status, ActivationFailure, "claude_user_settings_excluded", launch.name+" excludes the user settings source containing Agent Mux hooks", "")
		}
		effective := jsonHookSettings{}
		if len(sources) == 0 || sources["user"] {
			mergeJSONHookSettings(&effective, userSettings)
		}
		if launch.cwd != "" {
			if len(sources) == 0 || sources["project"] {
				readAndMergeJSONSettings(&effective, filepath.Join(launch.cwd, ".claude", "settings.json"), status, "claude_project_settings_unavailable")
			}
			if len(sources) == 0 || sources["local"] {
				readAndMergeJSONSettings(&effective, filepath.Join(launch.cwd, ".claude", "settings.local.json"), status, "claude_local_settings_unavailable")
			}
		}
		for _, raw := range flagValues(launch.args, "--settings") {
			settings, path, err := readInlineOrFileJSONSettings(raw, launch.cwd)
			if err != nil {
				addActivationIssue(status, ActivationUnverified, "claude_cli_settings_unavailable", err.Error(), path)
				continue
			}
			mergeJSONHookSettings(&effective, settings)
		}
		if effective.DisableAllHooks != nil && *effective.DisableAllHooks {
			addActivationIssue(status, ActivationFailure, "claude_hooks_disabled", launch.name+" resolves disableAllHooks=true", status.Path)
		}
	}
}

func claudeSettingSources(args []string) map[string]bool {
	values := flagValues(args, "--setting-sources")
	if len(values) == 0 {
		return nil
	}
	sources := make(map[string]bool)
	for _, item := range strings.Split(values[len(values)-1], ",") {
		sources[strings.TrimSpace(item)] = true
	}
	return sources
}

// Copilot activation --------------------------------------------------------

func (e *activationEvaluator) inspectCopilot(account config.Account, contexts []activationContext, status *AccountStatus) {
	homeDir := config.ExpandTilde(account.EffectiveHomeDir())
	userPath := filepath.Join(homeDir, "settings.json")
	user, err := readJSONHookSettings(userPath)
	if err != nil {
		addActivationIssue(status, ActivationUnverified, "copilot_settings_unavailable", err.Error(), userPath)
	}
	hookFileSettings, err := readJSONHookSettings(status.Path)
	if err != nil {
		addActivationIssue(status, ActivationUnverified, "copilot_hook_settings_unavailable", err.Error(), status.Path)
	}
	if hookFileSettings.DisableAllHooks != nil && *hookFileSettings.DisableAllHooks {
		addActivationIssue(status, ActivationFailure, "copilot_hook_file_disabled", "Agent Mux's Copilot hook file sets disableAllHooks=true", status.Path)
	}

	for _, launch := range contexts {
		effective := user
		cwd := copilotWorkingDirectory(launch.cwd, launch.args)
		if cwd != "" {
			for _, path := range []string{
				filepath.Join(cwd, ".claude", "settings.json"),
				filepath.Join(cwd, ".github", "copilot", "settings.json"),
				filepath.Join(cwd, ".claude", "settings.local.json"),
				filepath.Join(cwd, ".github", "copilot", "settings.local.json"),
			} {
				readAndMergeJSONSettings(&effective, path, status, "copilot_project_settings_unavailable")
			}
		}
		if effective.DisableAllHooks != nil && *effective.DisableAllHooks {
			addActivationIssue(status, ActivationFailure, "copilot_hooks_disabled", launch.name+" resolves disableAllHooks=true", userPath)
		}
	}
}

func copilotWorkingDirectory(fallback string, args []string) string {
	cwd := fallback
	overridden := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-C" && i+1 < len(args):
			i++
			cwd = config.ExpandTilde(args[i])
			overridden = true
		case strings.HasPrefix(args[i], "-C="):
			cwd = config.ExpandTilde(strings.TrimPrefix(args[i], "-C="))
			overridden = true
		}
	}
	if overridden && cwd != "" && !filepath.IsAbs(cwd) && fallback != "" {
		cwd = filepath.Join(fallback, cwd)
	}
	return cwd
}

// Shared settings helpers ---------------------------------------------------

type settingsReadError struct {
	path string
	err  error
}

type managedSettingsEvidence struct {
	last   string
	errors []settingsReadError
}

func readJSONHookSettings(path string) (jsonHookSettings, error) {
	var settings jsonHookSettings
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("reading settings: %w", err)
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return jsonHookSettings{}, fmt.Errorf("parsing settings: %w", err)
	}
	return settings, nil
}

func mergeJSONHookSettings(dst *jsonHookSettings, src jsonHookSettings) {
	if src.DisableAllHooks != nil {
		dst.DisableAllHooks = src.DisableAllHooks
	}
	if src.AllowManagedHooksOnly != nil {
		dst.AllowManagedHooksOnly = src.AllowManagedHooksOnly
	}
}

func readAndMergeJSONSettings(dst *jsonHookSettings, path string, status *AccountStatus, code string) {
	settings, err := readJSONHookSettings(path)
	if err != nil {
		addActivationIssue(status, ActivationUnverified, code, err.Error(), path)
		return
	}
	mergeJSONHookSettings(dst, settings)
}

func readInlineOrFileJSONSettings(raw, cwd string) (jsonHookSettings, string, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") {
		var settings jsonHookSettings
		if err := json.Unmarshal([]byte(trimmed), &settings); err != nil {
			return settings, "", fmt.Errorf("parsing inline --settings JSON: %w", err)
		}
		return settings, "", nil
	}
	path := config.ExpandTilde(trimmed)
	if !filepath.IsAbs(path) && cwd != "" {
		path = filepath.Join(cwd, path)
	}
	settings, err := readJSONHookSettings(path)
	return settings, path, err
}

func readManagedJSONSettings(root string) (jsonHookSettings, managedSettingsEvidence) {
	var settings jsonHookSettings
	evidence := managedSettingsEvidence{}
	if root == "" {
		return settings, evidence
	}
	paths := []string{filepath.Join(root, "managed-settings.json")}
	dropins, err := filepath.Glob(filepath.Join(root, "managed-settings.d", "*.json"))
	if err != nil {
		evidence.errors = append(evidence.errors, settingsReadError{path: filepath.Join(root, "managed-settings.d"), err: err})
	}
	sort.Strings(dropins)
	paths = append(paths, dropins...)
	for _, path := range paths {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			evidence.errors = append(evidence.errors, settingsReadError{path: path, err: err})
			continue
		}
		layer, err := readJSONHookSettings(path)
		if err != nil {
			evidence.errors = append(evidence.errors, settingsReadError{path: path, err: err})
			continue
		}
		mergeJSONHookSettings(&settings, layer)
		evidence.last = path
	}
	return settings, evidence
}

func claudeManagedSettingsRoot() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode"
	case "linux":
		return "/etc/claude-code"
	default:
		return ""
	}
}

func hasArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}

func flagValues(args []string, flag string) []string {
	var values []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == flag && i+1 < len(args):
			i++
			values = append(values, args[i])
		case strings.HasPrefix(args[i], flag+"="):
			values = append(values, strings.TrimPrefix(args[i], flag+"="))
		}
	}
	return values
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}
