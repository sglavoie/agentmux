package config

import (
	"fmt"
	"sort"
	"strings"
)

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
		envKeys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			envKeys = append(envKeys, k)
		}
		sort.Strings(envKeys)
		for _, k := range envKeys {
			if k == "" {
				errs = append(errs, fmt.Sprintf("project %q: env key is required", s.Title))
				continue
			}
			if strings.Contains(k, "=") {
				errs = append(errs, fmt.Sprintf(
					"project %q: env key %q must not contain \"=\"",
					s.Title, k,
				))
			}
			// Session metadata joins KEY=VALUE entries with this byte. If it
			// appears inside either part, restart can silently change the env.
			if strings.ContainsRune(k, '\x1f') {
				errs = append(errs, fmt.Sprintf("project %q: env key %q must not contain the session metadata separator (U+001F)", s.Title, k))
			}
			if strings.ContainsRune(k, '\x00') {
				errs = append(errs, fmt.Sprintf("project %q: env key %q must not contain NUL", s.Title, k))
			}
			if strings.ContainsRune(s.Env[k], '\x1f') {
				errs = append(errs, fmt.Sprintf("project %q: env value for %q must not contain the session metadata separator (U+001F)", s.Title, k))
			}
			if strings.ContainsRune(s.Env[k], '\x00') {
				errs = append(errs, fmt.Sprintf("project %q: env value for %q must not contain NUL", s.Title, k))
			}
		}
		for _, group := range s.Groups {
			if strings.ContainsRune(group, '\x1f') {
				errs = append(errs, fmt.Sprintf("project %q: group %q must not contain the session metadata separator (U+001F)", s.Title, group))
			}
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
