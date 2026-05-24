package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
)

// Validate performs cross-section validation on a loaded Config.
// Returns a combined error if any rules are violated.
//
// Validate also appends advisory entries to cfg.Deprecations for watchdog
// thresholds that are set to a sub-30s value: those are accepted (no error)
// but very likely a misconfiguration, so the TUI banner surfaces them next
// to the legacy-key deprecations.
func Validate(cfg *Config) error {
	var errs []string
	normalizeAliases(cfg)

	errs = append(errs, validateDefaults(&cfg.Defaults)...)
	errs = append(errs, validateAccounts(cfg.Profiles)...)
	errs = append(errs, validateSessions(cfg.Projects, cfg.Profiles)...)
	errs = append(errs, validateHookOverrides(cfg.Hooks, cfg.Profiles)...)

	// Strip any watchdog warnings appended by a prior Validate call before
	// re-appending the current set: Validate may be called more than once on
	// the same *Config (e.g. revalidating after an edit), and without this
	// step every advisory would surface N times in cfg.Deprecations.
	cfg.Deprecations = stripWatchdogWarnings(cfg.Deprecations)
	cfg.Deprecations = append(cfg.Deprecations, collectWatchdogWarnings(cfg)...)

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return nil
}

func validateDefaults(d *Defaults) []string {
	var errs []string
	if d.Sort != "" && !ValidSort(d.Sort) {
		errs = append(errs, fmt.Sprintf(
			"invalid sort %q: must be one of alpha, account",
			d.Sort,
		))
	}
	if d.RefreshInterval < 0 {
		errs = append(errs, "defaults.refresh_interval must be >= 0")
	}
	if d.GuardDuration < 0 {
		errs = append(errs, "defaults.guard_duration must be >= 0")
	}
	if d.IdleThreshold < 0 {
		errs = append(errs, "defaults.idle_threshold must be >= 0")
	}
	if d.StaleActiveThreshold < 0 {
		errs = append(errs, "defaults.stale_active_threshold must be >= 0")
	}
	if d.Watchdog.Active < 0 {
		errs = append(errs, "defaults.watchdog.active must be >= 0")
	}
	if d.Watchdog.Subagent < 0 {
		errs = append(errs, "defaults.watchdog.subagent must be >= 0")
	}
	if d.Watchdog.Compact < 0 {
		errs = append(errs, "defaults.watchdog.compact must be >= 0")
	}
	if d.Watchdog.HookSilence < 0 {
		errs = append(errs, "defaults.watchdog.hook_silence must be >= 0")
	}
	if d.Preview.Direction != "" && !ValidPreviewDirection(d.Preview.Direction) {
		errs = append(errs, fmt.Sprintf(
			"invalid defaults.preview.direction %q: must be one of horizontal, vertical",
			d.Preview.Direction,
		))
	}
	if d.Preview.Ratio != 0 && (d.Preview.Ratio <= 0 || d.Preview.Ratio >= 1) {
		errs = append(errs, fmt.Sprintf(
			"defaults.preview.ratio must be > 0 and < 1 (got %v)",
			d.Preview.Ratio,
		))
	}
	if d.Preview.RefreshMs != 0 && d.Preview.RefreshMs < 50 {
		errs = append(errs, "defaults.preview.refresh_ms must be >= 50")
	}
	if d.Preview.MinCols < 0 {
		errs = append(errs, "defaults.preview.min_cols must be >= 0")
	}
	if d.Preview.MinRows < 0 {
		errs = append(errs, "defaults.preview.min_rows must be >= 0")
	}
	if d.Notifications.PermissionDebounceMs < 0 {
		errs = append(errs, "defaults.notifications.permission_debounce_ms must be >= 0")
	}
	return errs
}

func validateAccounts(accounts []Account) []string {
	var errs []string
	seen := map[string]bool{}
	for i, a := range accounts {
		if a.Name == "" {
			errs = append(errs, fmt.Sprintf("profile[%d]: name is required", i))
		}
		if !harness.Valid(a.Harness) {
			errs = append(errs, fmt.Sprintf(
				"profile %q: invalid harness %q\n  → valid harnesses: %s",
				a.Name, a.Harness, strings.Join(harness.Names(), ", "),
			))
		}
		if a.HomeDir == "" {
			errs = append(errs, fmt.Sprintf("profile %q: home_dir is required", a.Name))
		}
		if a.Color == "" {
			errs = append(errs, fmt.Sprintf("profile %q: color is required", a.Name))
		} else if !ValidColor(a.Color) {
			errs = append(errs, fmt.Sprintf(
				"profile %q: invalid color %q\n  → valid colors: green, blue, red, orange, purple, teal, pink, yellow, gray, cyan",
				a.Name, a.Color,
			))
		}
		// Only check duplicates for named profiles. Otherwise a config with
		// two unnamed entries reports a misleading 'duplicate profile name ""'
		// on top of the per-entry "name is required" error.
		// SessionKey lowercases the profile name, so the duplicate check must
		// match that contract — otherwise two profiles differing only in case
		// would pass validation here and then collide silently at launch-time
		// dedup.
		if a.Name != "" {
			key := strings.ToLower(a.Name)
			if seen[key] {
				errs = append(errs, fmt.Sprintf("duplicate profile name %q", a.Name))
			}
			seen[key] = true
		}
		if a.Watchdog.Active < 0 {
			errs = append(errs, fmt.Sprintf("profile %q: watchdog.active must be >= 0", a.Name))
		}
		if a.Watchdog.Subagent < 0 {
			errs = append(errs, fmt.Sprintf("profile %q: watchdog.subagent must be >= 0", a.Name))
		}
		if a.Watchdog.Compact < 0 {
			errs = append(errs, fmt.Sprintf("profile %q: watchdog.compact must be >= 0", a.Name))
		}
		if a.Watchdog.HookSilence < 0 {
			errs = append(errs, fmt.Sprintf("profile %q: watchdog.hook_silence must be >= 0", a.Name))
		}
	}
	return errs
}

// watchdogWarningReplacement is the marker placed on advisory entries
// emitted by collectWatchdogWarnings; stripWatchdogWarnings keys off the
// exact text to filter them out on re-validation.
const watchdogWarningReplacement = "a value >= 30 seconds (or 0 to inherit the default)"

// stripWatchdogWarnings returns deps with all sub-30s watchdog advisory
// entries removed. Used by Validate to keep the warning set idempotent
// across multiple invocations.
func stripWatchdogWarnings(deps []Deprecation) []Deprecation {
	if len(deps) == 0 {
		return deps
	}
	out := make([]Deprecation, 0, len(deps))
	for _, d := range deps {
		if d.Replacement == watchdogWarningReplacement {
			continue
		}
		out = append(out, d)
	}
	return out
}

// collectWatchdogWarnings flags any watchdog threshold that is set to a
// non-zero value below 30s. The daemon accepts the value (no error), but a
// 5s active threshold will fire false stales constantly — so the user gets
// a banner pointing them at the suspicious key without blocking the load.
func collectWatchdogWarnings(cfg *Config) []Deprecation {
	var ws []Deprecation
	check := func(key, location string, value int) {
		if value > 0 && value < 30 {
			ws = append(ws, Deprecation{
				Key:         key,
				Replacement: watchdogWarningReplacement,
				Location:    location,
			})
		}
	}
	check("defaults.watchdog.active", "[defaults.watchdog]", cfg.Defaults.Watchdog.Active)
	check("defaults.watchdog.subagent", "[defaults.watchdog]", cfg.Defaults.Watchdog.Subagent)
	check("defaults.watchdog.compact", "[defaults.watchdog]", cfg.Defaults.Watchdog.Compact)
	check("defaults.watchdog.hook_silence", "[defaults.watchdog]", cfg.Defaults.Watchdog.HookSilence)
	for _, p := range cfg.Profiles {
		loc := fmt.Sprintf("[[profiles]] %s", p.Name)
		check(fmt.Sprintf("profiles.%s.watchdog.active", p.Name), loc, p.Watchdog.Active)
		check(fmt.Sprintf("profiles.%s.watchdog.subagent", p.Name), loc, p.Watchdog.Subagent)
		check(fmt.Sprintf("profiles.%s.watchdog.compact", p.Name), loc, p.Watchdog.Compact)
		check(fmt.Sprintf("profiles.%s.watchdog.hook_silence", p.Name), loc, p.Watchdog.HookSilence)
	}
	return ws
}

func validateSessions(sessions []Session, accounts []Account) []string {
	var errs []string
	// Key by lowercased profile name: runtime findAccount uses
	// strings.EqualFold, so validation must accept the same set of references
	// or it will reject configs that work fine at launch.
	accountNames := map[string]bool{}
	for _, a := range accounts {
		accountNames[strings.ToLower(a.Name)] = true
	}
	// Track (profile, title) pairs to surface duplicates: launch dedup keys on
	// SessionKey(account, title), so two projects sharing both fields would
	// silently overwrite one another at runtime.
	type projectKey struct{ profile, title string }
	seenProject := map[projectKey]bool{}
	for i, s := range sessions {
		if s.Title == "" {
			errs = append(errs, fmt.Sprintf("project[%d]: title is required", i))
		}
		if s.Directory == "" {
			errs = append(errs, fmt.Sprintf("project %q: directory is required", s.Title))
		}
		// normalizeProjects mirrors profile↔account when one is empty, but
		// silently keeps both when the user wrote two different values. That
		// would route launch (Account) and dashboard (Profile via
		// EffectiveProfile) to different profiles for the same project —
		// surface the conflict here instead of letting the user wonder why
		// their session ends up under the wrong profile.
		if s.Profile != "" && s.Account != "" && !strings.EqualFold(s.Profile, s.Account) {
			errs = append(errs, fmt.Sprintf(
				"project %q sets both profile=%q and account=%q with different values\n  → account is the deprecated alias for profile; remove one or make them match",
				s.Title, s.Profile, s.Account,
			))
		}
		if s.Profile == "" {
			errs = append(errs, fmt.Sprintf("project %q: profile is required", s.Title))
		} else if !accountNames[strings.ToLower(s.Profile)] {
			errs = append(errs, fmt.Sprintf(
				"project %q references unknown profile %q\n  → check config.toml for valid profile names",
				s.Title, s.Profile,
			))
		}
		if s.Title != "" && s.Profile != "" {
			// SessionKey lowercases both fields, so the duplicate check must
			// match that contract — otherwise two projects whose titles or
			// profiles differ only in case would pass validation here and then
			// collide silently at launch-time dedup.
			k := projectKey{profile: strings.ToLower(s.Profile), title: strings.ToLower(s.Title)}
			if seenProject[k] {
				errs = append(errs, fmt.Sprintf(
					"duplicate project title %q under profile %q\n  → titles must be unique per profile (launch dedup keys on this pair)",
					s.Title, s.Profile,
				))
			}
			seenProject[k] = true
		}
	}
	return errs
}

func validateHookOverrides(hooks HooksConfig, accounts []Account) []string {
	if len(hooks.Overrides) == 0 {
		return nil
	}
	// Build name → harness lookup so each override can be validated against
	// the events its specific harness installs.
	// Key by lowercased profile name to match the case-insensitive lookup
	// performed by hooks.accountOverride at merge time.
	profileHarness := make(map[string]string, len(accounts))
	for _, a := range accounts {
		profileHarness[strings.ToLower(a.Name)] = a.EffectiveHarness()
	}
	var errs []string
	// Sort keys so the error order is deterministic for tests and users.
	names := make([]string, 0, len(hooks.Overrides))
	for name := range hooks.Overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	// Detect case-only duplicate override keys. accountOverride at merge time
	// returns whichever case-folded entry the Go map iterator visits first,
	// which is non-deterministic — so two overrides like [hooks.overrides.work]
	// and [hooks.overrides.Work] silently apply one or the other on a per-run
	// basis. Surface this as a config error rather than wait for the user to
	// notice that disabled_events appears to come and go between syncs.
	seenLower := make(map[string]string, len(names))
	for _, name := range names {
		key := strings.ToLower(name)
		if prior, ok := seenLower[key]; ok {
			errs = append(errs, fmt.Sprintf(
				"duplicate hooks.overrides key %q and %q\n  → keys differ only in case; merge-time lookup is case-insensitive, so pick one",
				prior, name,
			))
			continue
		}
		seenLower[key] = name
	}
	for _, name := range names {
		override := hooks.Overrides[name]
		harnessName, ok := profileHarness[strings.ToLower(name)]
		if !ok {
			errs = append(errs, fmt.Sprintf(
				"hooks.overrides references unknown profile %q\n  → no profile by that name is defined; rename or remove the override",
				name,
			))
			continue
		}
		known := harness.KnownEvents(harnessName)
		if len(known) == 0 {
			continue
		}
		knownSet := make(map[string]bool, len(known))
		for _, e := range known {
			knownSet[e] = true
		}
		for _, e := range override.DisabledEvents {
			if e == "" {
				errs = append(errs, fmt.Sprintf(
					"hooks.overrides.%s.disabled_events contains an empty entry",
					name,
				))
				continue
			}
			if !knownSet[e] {
				errs = append(errs, fmt.Sprintf(
					"hooks.overrides.%s.disabled_events: unknown %s event %q\n  → valid events: %s",
					name, harnessName, e, strings.Join(known, ", "),
				))
			}
		}
	}
	return errs
}
