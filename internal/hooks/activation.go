package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
)

const defaultLastSeenWindow = 4 * time.Hour

var (
	activationVersionProbe      = probeHarnessVersion
	activationLastSeenProbe     = probeLastSeen
	activationClaudeManagedRoot = claudeManagedSettingsRoot
)

type versionEvidence struct {
	version string
	err     error
}

type activationEvaluator struct {
	cfg      *config.Config
	now      time.Time
	versions map[string]versionEvidence
}

type activationContext struct {
	name string
	cwd  string
	args []string
}

func newActivationEvaluator(cfg *config.Config) *activationEvaluator {
	return &activationEvaluator{
		cfg:      cfg,
		now:      time.Now().UTC(),
		versions: make(map[string]versionEvidence),
	}
}

func (e *activationEvaluator) evaluate(account config.Account, override config.AccountHookOverride, status *AccountStatus) {
	if status == nil {
		return
	}
	status.Activation = ActivationHealthy
	if status.Err != nil {
		addActivationIssue(status, ActivationFailure, "status_error", status.Err.Error(), status.Path)
		return
	}
	if status.Status != SyncInSync {
		addActivationIssue(status, ActivationFailure, "hook_file_"+string(status.Status), "installed hook file is "+string(status.Status), status.Path)
	}
	if len(override.DisabledEvents) > 0 {
		addActivationIssue(status, ActivationFailure, "events_disabled", "Agent Mux events disabled by profile: "+strings.Join(override.DisabledEvents, ", "), "")
	}

	verified := verifiedCLIVersion(account.EffectiveHarness())
	status.VerifiedCLIVersion = verified
	version := e.versionFor(account.EffectiveHarness())
	if version.err != nil {
		addActivationIssue(status, ActivationFailure, "cli_unavailable", version.err.Error(), "")
	} else {
		status.CLIVersion = version.version
		switch comparison, ok := compareVersions(version.version, verified); {
		case !ok:
			addActivationIssue(status, ActivationUnverified, "cli_version_unparsed", fmt.Sprintf("cannot compare CLI version %q with verified contract %q", version.version, verified), "")
		case comparison > 0:
			addActivationIssue(status, ActivationUnverified, "contract_stale", fmt.Sprintf("CLI %s is newer than verified hook contract %s", version.version, verified), "")
		case comparison < 0:
			addActivationIssue(status, ActivationUnverified, "cli_older_than_contract", fmt.Sprintf("CLI %s is older than verified hook contract %s", version.version, verified), "")
		}
	}

	contexts := activationContexts(e.cfg, account)
	switch account.EffectiveHarness() {
	case harness.Codex:
		e.inspectCodex(account, override, contexts, status)
	case harness.Claude:
		e.inspectClaude(account, contexts, status)
	case harness.Copilot:
		e.inspectCopilot(account, contexts, status)
	}

	lastSeen, err := activationLastSeenProbe(context.Background(), e.cfg, account.EffectiveHarness(), account.Name)
	if err != nil {
		addActivationIssue(status, ActivationUnverified, "last_seen_unavailable", "recent hook evidence unavailable: "+err.Error(), "")
	} else if lastSeen.IsZero() {
		addActivationIssue(status, ActivationUnverified, "last_seen_missing", "no hook event has been observed in the diagnostic journal", "")
	} else {
		status.LastSeen = lastSeen
		if age := e.now.Sub(lastSeen); age > lastSeenWindow(e.cfg, account) {
			addActivationIssue(status, ActivationUnverified, "last_seen_stale", fmt.Sprintf("last hook evidence is stale (%s ago)", age.Round(time.Second)), "")
		}
	}
}

func (e *activationEvaluator) versionFor(harnessName string) versionEvidence {
	harnessName = harness.Normalize(harnessName)
	if evidence, ok := e.versions[harnessName]; ok {
		return evidence
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	version, err := activationVersionProbe(ctx, harness.Lookup(harnessName).Executable())
	evidence := versionEvidence{version: version, err: err}
	e.versions[harnessName] = evidence
	return evidence
}

func addActivationIssue(status *AccountStatus, verdict ActivationVerdict, code, message, path string) {
	for _, issue := range status.ActivationIssues {
		if issue.Verdict == verdict && issue.Code == code && issue.Message == message && issue.Path == path {
			return
		}
	}
	status.ActivationIssues = append(status.ActivationIssues, ActivationIssue{
		Verdict: verdict,
		Code:    code,
		Message: message,
		Path:    path,
	})
	if verdict == ActivationFailure || status.Activation == ActivationHealthy {
		status.Activation = verdict
	}
}

func verifiedCLIVersion(harnessName string) string {
	matrix := harness.HookEventMatrix(harnessName)
	if len(matrix) == 0 {
		return ""
	}
	return matrix[0].VerifiedCLIVersion
}

func activationContexts(cfg *config.Config, account config.Account) []activationContext {
	var contexts []activationContext
	if cfg != nil {
		for _, project := range cfg.Projects {
			if !strings.EqualFold(project.EffectiveProfile(), account.Name) {
				continue
			}
			contexts = append(contexts, activationContext{
				name: project.Title,
				cwd:  config.ExpandTilde(project.Directory),
				args: effectiveLaunchArgs(cfg.Defaults, account, project),
			})
		}
	}
	if len(contexts) == 0 {
		var defaults config.Defaults
		if cfg != nil {
			defaults = cfg.Defaults
		}
		contexts = append(contexts, activationContext{name: account.Name, args: effectiveLaunchArgs(defaults, account, config.Session{})})
	}
	return contexts
}

func effectiveLaunchArgs(defaults config.Defaults, account config.Account, project config.Session) []string {
	if args := project.EffectiveArgs(); len(args) > 0 {
		return append([]string(nil), args...)
	}
	if len(account.Args) > 0 {
		return append([]string(nil), account.Args...)
	}
	return append([]string(nil), defaults.EffectiveArgs()...)
}

func lastSeenWindow(cfg *config.Config, account config.Account) time.Duration {
	seconds := account.Watchdog.HookSilence
	if seconds <= 0 && cfg != nil {
		seconds = cfg.Defaults.Watchdog.HookSilence
	}
	if seconds <= 0 {
		return defaultLastSeenWindow
	}
	return time.Duration(seconds) * time.Second
}

func probeHarnessVersion(ctx context.Context, executable string) (string, error) {
	path, err := exec.LookPath(executable)
	if err != nil {
		return "", fmt.Errorf("%s CLI is not on PATH", executable)
	}
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("%s --version timed out", executable)
		}
		return "", fmt.Errorf("%s --version failed: %w", executable, err)
	}
	version := extractVersion(string(out))
	if version == "" {
		return "", fmt.Errorf("%s --version returned no numeric version", executable)
	}
	return version, nil
}

func extractVersion(output string) string {
	for _, field := range strings.Fields(output) {
		candidate := strings.Trim(field, "vV.,;()[]")
		parts := strings.Split(candidate, ".")
		if len(parts) < 2 {
			continue
		}
		valid := true
		for _, part := range parts {
			if _, err := strconv.Atoi(part); err != nil {
				valid = false
				break
			}
		}
		if valid {
			return candidate
		}
	}
	return ""
}

func compareVersions(left, right string) (int, bool) {
	if left == "" || right == "" {
		return 0, false
	}
	lparts := strings.Split(left, ".")
	rparts := strings.Split(right, ".")
	count := len(lparts)
	if len(rparts) > count {
		count = len(rparts)
	}
	for i := 0; i < count; i++ {
		var lvalue, rvalue int
		var err error
		if i < len(lparts) {
			lvalue, err = strconv.Atoi(lparts[i])
			if err != nil {
				return 0, false
			}
		}
		if i < len(rparts) {
			rvalue, err = strconv.Atoi(rparts[i])
			if err != nil {
				return 0, false
			}
		}
		if lvalue < rvalue {
			return -1, true
		}
		if lvalue > rvalue {
			return 1, true
		}
	}
	return 0, true
}

func probeLastSeen(ctx context.Context, cfg *config.Config, harnessName, profile string) (time.Time, error) {
	if cfg != nil && !cfg.Defaults.Diagnostics.JournalEnabledValue() {
		return time.Time{}, errors.New("diagnostic journal is disabled")
	}
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		return time.Time{}, err
	}
	if _, err := os.Stat(paths.JournalDir); os.IsNotExist(err) {
		return time.Time{}, nil
	} else if err != nil {
		return time.Time{}, err
	}
	options := diagnostics.JournalOptions{}
	if cfg != nil {
		options.KeepDays = cfg.Defaults.Diagnostics.JournalKeepDays
		options.MaxBytes = int64(cfg.Defaults.Diagnostics.JournalMaxMiB) << 20
	}
	journal, err := diagnostics.NewJournal(paths, options)
	if err != nil {
		return time.Time{}, err
	}
	records, err := journal.Read(ctx, diagnostics.Selector{Harness: harnessName, Profile: profile, Limit: 256})
	if err != nil {
		return time.Time{}, err
	}
	var latest time.Time
	for _, record := range records {
		if !strings.HasPrefix(record.Stage, "hook.") {
			continue
		}
		if record.ObservedAt.After(latest) {
			latest = record.ObservedAt
		}
	}
	return latest, nil
}

// Codex activation ----------------------------------------------------------

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
