package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
)

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
