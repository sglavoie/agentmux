package config

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
)

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
			errs = append(errs, fmt.Sprintf(
				"profile %q: 'color' is required (e.g. color=\"yellow\")"+validColorsHint(),
				a.Name,
			))
		} else if !ValidColor(a.Color) {
			errs = append(errs, fmt.Sprintf(
				"profile %q: invalid color %q"+validColorsHint(),
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
				errs = append(errs, fmt.Sprintf(
					"duplicate profile name %q\n  → profile.name is the unique launch ID; use distinct IDs per harness, e.g. \"vta-claude\" and \"vta-codex\"",
					a.Name,
				))
			}
			seen[key] = true
		}
		if err := durationSecondsError("watchdog.active", a.Watchdog.Active); err != "" {
			errs = append(errs, fmt.Sprintf("profile %q: %s", a.Name, err))
		}
		if err := durationSecondsError("watchdog.subagent", a.Watchdog.Subagent); err != "" {
			errs = append(errs, fmt.Sprintf("profile %q: %s", a.Name, err))
		}
		if err := durationSecondsError("watchdog.compact", a.Watchdog.Compact); err != "" {
			errs = append(errs, fmt.Sprintf("profile %q: %s", a.Name, err))
		}
		if err := durationSecondsError("watchdog.hook_silence", a.Watchdog.HookSilence); err != "" {
			errs = append(errs, fmt.Sprintf("profile %q: %s", a.Name, err))
		}
	}
	return errs
}
