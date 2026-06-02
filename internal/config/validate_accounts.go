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
				"profile %q: 'color' is required (e.g. color=\"yellow\")\n  → valid colors: green, blue, red, orange, purple, teal, pink, yellow, gray, cyan",
				a.Name,
			))
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
				errs = append(errs, fmt.Sprintf(
					"duplicate profile name %q\n  → profile.name is the unique launch ID; use distinct IDs per harness, e.g. \"vta-claude\" and \"vta-codex\"",
					a.Name,
				))
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
