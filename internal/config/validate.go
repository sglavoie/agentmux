package config

import (
	"fmt"
	"strings"
)

// Validate performs cross-section validation on a loaded Config.
// Returns a combined error if any rules are violated.
func Validate(cfg *Config) error {
	var errs []string

	errs = append(errs, validateDefaults(&cfg.Defaults)...)
	errs = append(errs, validateAccounts(cfg.Accounts)...)
	errs = append(errs, validateSessions(cfg.Sessions, cfg.Accounts)...)

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
	return errs
}

func validateAccounts(accounts []Account) []string {
	var errs []string
	seen := map[string]bool{}
	for i, a := range accounts {
		if a.Name == "" {
			errs = append(errs, fmt.Sprintf("account[%d]: name is required", i))
		}
		if a.ConfigDir == "" {
			errs = append(errs, fmt.Sprintf("account %q: config_dir is required", a.Name))
		}
		if a.Color == "" {
			errs = append(errs, fmt.Sprintf("account %q: color is required", a.Name))
		} else if !ValidColor(a.Color) {
			errs = append(errs, fmt.Sprintf(
				"account %q: invalid color %q\n  → valid colors: green, blue, red, orange, purple, teal, pink, yellow, gray, cyan",
				a.Name, a.Color,
			))
		}
		if seen[a.Name] {
			errs = append(errs, fmt.Sprintf("duplicate account name %q", a.Name))
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
	for i, s := range sessions {
		if s.Title == "" {
			errs = append(errs, fmt.Sprintf("session[%d]: title is required", i))
		}
		if s.Directory == "" {
			errs = append(errs, fmt.Sprintf("session %q: directory is required", s.Title))
		}
		if s.Account == "" {
			errs = append(errs, fmt.Sprintf("session %q: account is required", s.Title))
		} else if !accountNames[s.Account] {
			errs = append(errs, fmt.Sprintf(
				"session %q references unknown account %q\n  → check config.toml for valid account names",
				s.Title, s.Account,
			))
		}
	}
	return errs
}
