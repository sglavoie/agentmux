package config

import (
	"fmt"
	"strings"

	runewidth "github.com/mattn/go-runewidth"
	"github.com/sglavoie/agentmux/internal/palette"
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
	errs = append(errs, validateJump(&cfg.Defaults.Jump)...)
	errs = append(errs, validateHarnessColors(cfg)...)
	errs = append(errs, validateHarnessEmojis(cfg)...)
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

// paletteStateIconSet returns a map from state-icon glyph to the state
// name it represents, used to reject harness emojis that would collide
// with the title parser's state-icon prefix.
func paletteStateIconSet() map[string]string {
	out := map[string]string{}
	for _, name := range palette.StateOrder() {
		icon := palette.StateIcon(name)
		if icon == "" {
			continue
		}
		out[icon] = name
	}
	return out
}

// stateIconList renders the state-icon glyphs from set in a deterministic
// order (matching palette.StateOrder so the error message stays stable).
func stateIconList(set map[string]string) string {
	names := palette.StateOrder()
	parts := make([]string, 0, len(names))
	for _, name := range names {
		icon := palette.StateIcon(name)
		if icon == "" {
			continue
		}
		if _, ok := set[icon]; ok {
			parts = append(parts, icon)
		}
	}
	return strings.Join(parts, " ")
}

// validHarnessEmojiValue reports whether v looks like a single emoji
// glyph suitable for the tab-title harness badge. The check is approximate
// (no full grapheme clustering): the display width must be 1 or 2, and the
// value must contain a non-ASCII rune so that text like "ab" — which has
// width 2 from two width-1 ASCII chars — is rejected.
func validHarnessEmojiValue(v string) bool {
	w := runewidth.StringWidth(v)
	if w < 1 || w > 2 {
		return false
	}
	for _, r := range v {
		if r > 127 {
			return true
		}
	}
	return false
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
