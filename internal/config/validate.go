package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
)

// Validate performs cross-section validation on a loaded Config.
// Returns a combined error if any rules are violated.
func Validate(cfg *Config) error {
	var errs []string
	normalizeAliases(cfg)

	errs = append(errs, validateDefaults(&cfg.Defaults)...)
	errs = append(errs, validateAccounts(cfg.Profiles)...)
	errs = append(errs, validateSessions(cfg.Projects, cfg.Profiles)...)
	errs = append(errs, validateHookOverrides(cfg.Hooks, cfg.Profiles)...)

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
		errs = append(errs, "defaults.preview.min_cols must be > 0")
	}
	if d.Preview.MinRows < 0 {
		errs = append(errs, "defaults.preview.min_rows must be > 0")
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
		if seen[a.Name] {
			errs = append(errs, fmt.Sprintf("duplicate profile name %q", a.Name))
		}
		seen[a.Name] = true
	}
	return errs
}

func validateSessions(sessions []Session, accounts []Account) []string {
	var errs []string
	accountNames := map[string]bool{}
	for _, a := range accounts {
		accountNames[a.Name] = true
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
		if s.Profile == "" {
			errs = append(errs, fmt.Sprintf("project %q: profile is required", s.Title))
		} else if !accountNames[s.Profile] {
			errs = append(errs, fmt.Sprintf(
				"project %q references unknown profile %q\n  → check config.toml for valid profile names",
				s.Title, s.Profile,
			))
		}
		if s.Title != "" && s.Profile != "" {
			k := projectKey{profile: s.Profile, title: s.Title}
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
	profileHarness := make(map[string]string, len(accounts))
	for _, a := range accounts {
		profileHarness[a.Name] = a.EffectiveHarness()
	}
	var errs []string
	// Sort keys so the error order is deterministic for tests and users.
	names := make([]string, 0, len(hooks.Overrides))
	for name := range hooks.Overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		override := hooks.Overrides[name]
		harnessName, ok := profileHarness[name]
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
