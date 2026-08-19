package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

type codexSettings struct {
	AllowManagedHooksOnly *bool `toml:"allow_managed_hooks_only"`
	Features              struct {
		Hooks      *bool `toml:"hooks"`
		CodexHooks *bool `toml:"codex_hooks"`
	} `toml:"features"`
	Hooks struct {
		State map[string]codexHookState `toml:"state"`
	} `toml:"hooks"`
}

type codexHookState struct {
	Enabled     *bool  `toml:"enabled"`
	TrustedHash string `toml:"trusted_hash"`
}

type codexRawHookEntry struct {
	Type          string  `json:"type"`
	Command       string  `json:"command"`
	Timeout       *uint64 `json:"timeout"`
	Async         bool    `json:"async"`
	StatusMessage *string `json:"statusMessage"`
}

type codexRawHookGroup struct {
	Matcher *string             `json:"matcher"`
	Hooks   []codexRawHookEntry `json:"hooks"`
}

func (e *activationEvaluator) inspectCodex(account config.Account, override config.AccountHookOverride, contexts []activationContext, status *AccountStatus) {
	homeDir := config.ExpandTilde(account.EffectiveHomeDir())
	basePath := filepath.Join(homeDir, "config.toml")
	base, baseErr := readCodexSettings(basePath)
	if baseErr != nil {
		addActivationIssue(status, ActivationUnverified, "codex_config_unavailable", baseErr.Error(), basePath)
	}

	for _, launch := range contexts {
		settings := cloneCodexSettings(base)
		settingsPath := basePath
		if profile := codexProfileArg(launch.args); profile != "" {
			profilePath := filepath.Join(homeDir, profile+".config.toml")
			profileSettings, err := readCodexSettings(profilePath)
			if err != nil {
				addActivationIssue(status, ActivationUnverified, "codex_profile_config_unavailable", fmt.Sprintf("%s: %s", launch.name, err), profilePath)
			} else {
				mergeCodexSettings(&settings, profileSettings)
				settingsPath = profilePath
			}
		}
		if settings.AllowManagedHooksOnly != nil && *settings.AllowManagedHooksOnly {
			addActivationIssue(status, ActivationFailure, "managed_hooks_only", launch.name+" resolves allow_managed_hooks_only=true, excluding the Agent Mux user hook file", settingsPath)
		}
		enabled := codexHooksEnabled(settings)
		if cliEnabled, ok := codexHooksCLIOverride(launch.args); ok {
			enabled = cliEnabled
		}
		if !enabled {
			addActivationIssue(status, ActivationFailure, "codex_hooks_disabled", fmt.Sprintf("%s launches with features.hooks disabled", launch.name), settingsPath)
		}
		if baseErr == nil && status.Status == SyncInSync {
			e.inspectCodexTrust(
				settings.Hooks.State,
				override.DisabledEvents,
				hasArg(launch.args, "--dangerously-bypass-hook-trust"),
				launch.name,
				settingsPath,
				status,
			)
		}
	}
}

func cloneCodexSettings(settings codexSettings) codexSettings {
	clone := settings
	if settings.Hooks.State != nil {
		clone.Hooks.State = make(map[string]codexHookState, len(settings.Hooks.State))
		for key, state := range settings.Hooks.State {
			clone.Hooks.State[key] = state
		}
	}
	return clone
}

func readCodexSettings(path string) (codexSettings, error) {
	var settings codexSettings
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("reading Codex config: %w", err)
	}
	if _, err := toml.Decode(string(data), &settings); err != nil {
		return codexSettings{}, fmt.Errorf("parsing Codex config: %w", err)
	}
	return settings, nil
}

func mergeCodexSettings(dst *codexSettings, src codexSettings) {
	if src.AllowManagedHooksOnly != nil {
		dst.AllowManagedHooksOnly = src.AllowManagedHooksOnly
	}
	if src.Features.Hooks != nil {
		dst.Features.Hooks = src.Features.Hooks
	}
	if src.Features.CodexHooks != nil {
		dst.Features.CodexHooks = src.Features.CodexHooks
	}
	if len(src.Hooks.State) > 0 {
		if dst.Hooks.State == nil {
			dst.Hooks.State = make(map[string]codexHookState)
		}
		for key, state := range src.Hooks.State {
			dst.Hooks.State[key] = state
		}
	}
}

func codexHooksEnabled(settings codexSettings) bool {
	if settings.Features.Hooks != nil {
		return *settings.Features.Hooks
	}
	if settings.Features.CodexHooks != nil {
		return *settings.Features.CodexHooks
	}
	return true
}

func codexProfileArg(args []string) string {
	var profile string
	for i := 0; i < len(args); i++ {
		switch {
		case (args[i] == "-p" || args[i] == "--profile") && i+1 < len(args):
			i++
			profile = args[i]
		case strings.HasPrefix(args[i], "--profile="):
			profile = strings.TrimPrefix(args[i], "--profile=")
		}
	}
	return profile
}

func codexHooksCLIOverride(args []string) (bool, bool) {
	var value bool
	var found bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case (arg == "--enable" || arg == "--disable") && i+1 < len(args):
			i++
			if args[i] == "hooks" || args[i] == "codex_hooks" {
				value, found = arg == "--enable", true
			}
		case strings.HasPrefix(arg, "--enable=") || strings.HasPrefix(arg, "--disable="):
			parts := strings.SplitN(arg, "=", 2)
			if parts[1] == "hooks" || parts[1] == "codex_hooks" {
				value, found = parts[0] == "--enable", true
			}
		case (arg == "-c" || arg == "--config") && i+1 < len(args):
			i++
			if parsed, ok := parseCodexFeatureOverride(args[i]); ok {
				value, found = parsed, true
			}
		case strings.HasPrefix(arg, "--config="):
			if parsed, ok := parseCodexFeatureOverride(strings.TrimPrefix(arg, "--config=")); ok {
				value, found = parsed, true
			}
		}
	}
	return value, found
}

func parseCodexFeatureOverride(value string) (bool, bool) {
	parts := strings.SplitN(strings.TrimSpace(value), "=", 2)
	if len(parts) != 2 || (parts[0] != "features.hooks" && parts[0] != "features.codex_hooks") {
		return false, false
	}
	parsed, err := strconv.ParseBool(strings.Trim(parts[1], " \t\"'"))
	return parsed, err == nil
}

func (e *activationEvaluator) inspectCodexTrust(states map[string]codexHookState, disabledEvents []string, bypass bool, launchName, settingsPath string, status *AccountStatus) {
	disabled := stringSet(disabledEvents)
	data, err := os.ReadFile(status.Path)
	if err != nil {
		addActivationIssue(status, ActivationUnverified, "codex_trust_unavailable", err.Error(), status.Path)
		return
	}
	var file struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		addActivationIssue(status, ActivationUnverified, "codex_trust_unavailable", err.Error(), status.Path)
		return
	}
	found := 0
	for _, contract := range harness.HookEventMatrix(harness.Codex) {
		if contract.Installation != harness.HookInstalled || disabled[contract.CanonicalName] {
			continue
		}
		var groups []codexRawHookGroup
		if err := json.Unmarshal(file.Hooks[contract.CanonicalName], &groups); err != nil {
			addActivationIssue(status, ActivationUnverified, "codex_trust_unavailable", fmt.Sprintf("cannot parse %s trust definition: %s", contract.CanonicalName, err), status.Path)
			continue
		}
		for groupIndex, group := range groups {
			for handlerIndex, handler := range group.Hooks {
				if handler.Type != "command" || !strings.Contains(handler.Command, " hook-indicator "+contract.CanonicalName+" --harness=codex") {
					continue
				}
				found++
				key := fmt.Sprintf("%s:%s:%d:%d", status.Path, eventSnakeCase(contract.CanonicalName), groupIndex, handlerIndex)
				state, ok := states[key]
				if ok && state.Enabled != nil && !*state.Enabled {
					addActivationIssue(status, ActivationFailure, "codex_hook_disabled", launchName+": "+contract.CanonicalName+" is disabled in hooks.state", settingsPath)
					continue
				}
				if bypass {
					continue
				}
				currentHash := codexDefinitionHash(contract.CanonicalName, group.Matcher, handler)
				switch {
				case !ok || state.TrustedHash == "":
					addActivationIssue(status, ActivationFailure, "codex_hook_untrusted", launchName+": "+contract.CanonicalName+" has no exact-definition trust record", settingsPath)
				case state.TrustedHash != currentHash:
					addActivationIssue(status, ActivationFailure, "codex_hook_modified", launchName+": "+contract.CanonicalName+" trust hash does not match the installed definition", settingsPath)
				}
			}
		}
	}
	if found == 0 {
		addActivationIssue(status, ActivationUnverified, "codex_trust_unavailable", "no Agent Mux hook definitions were available for exact trust verification", status.Path)
	}
}

func codexDefinitionHash(event string, matcher *string, handler codexRawHookEntry) string {
	timeout := uint64(600)
	if handler.Timeout != nil {
		timeout = *handler.Timeout
	}
	if timeout < 1 {
		timeout = 1
	}
	handlerValue := map[string]any{
		"async":   handler.Async,
		"command": handler.Command,
		"timeout": timeout,
		"type":    "command",
	}
	if handler.StatusMessage != nil {
		handlerValue["statusMessage"] = *handler.StatusMessage
	}
	identity := map[string]any{
		"event_name": eventSnakeCase(event),
		"hooks":      []any{handlerValue},
	}
	if matcher != nil {
		identity["matcher"] = *matcher
	}
	encoded, _ := json.Marshal(identity)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func eventSnakeCase(value string) string {
	var out strings.Builder
	for index, r := range value {
		if unicode.IsUpper(r) {
			if index > 0 {
				out.WriteByte('_')
			}
			out.WriteRune(unicode.ToLower(r))
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// Claude activation ---------------------------------------------------------
