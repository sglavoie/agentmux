package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/palette"
)

// validateHarnessEmojis checks the [harness_emojis] table: every used
// harness must have an entry, values must be a single emoji glyph that
// does not collide with the palette state icons, and case-conflict
// duplicates are rejected.
func validateHarnessEmojis(cfg *Config) []string {
	var errs []string
	for _, conflict := range cfg.harnessEmojiKeyConflicts {
		errs = append(errs, fmt.Sprintf(
			"[harness_emojis] duplicate keys normalize to the same harness (%s)\n  → keep only one spelling for each harness",
			conflict,
		))
	}
	stateIcons := paletteStateIconSet()
	keys := make([]string, 0, len(cfg.HarnessEmojis))
	for k := range cfg.HarnessEmojis {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := cfg.HarnessEmojis[k]
		if !harness.Valid(k) {
			errs = append(errs, fmt.Sprintf(
				"[harness_emojis] unknown harness %q\n  → valid harnesses: %s",
				k, strings.Join(harness.Names(), ", "),
			))
			continue
		}
		if !validHarnessEmojiValue(v) {
			errs = append(errs, fmt.Sprintf(
				"[harness_emojis].%s: invalid emoji %q\n  → value must be a single emoji glyph (display width 1 or 2)",
				k, v,
			))
			continue
		}
		if marker, ok := titleMarkerCollision(v); ok {
			errs = append(errs, fmt.Sprintf(
				"[harness_emojis].%s: %q collides with the %s — tab titles parse leading marker glyphs before the project name\n  → pick a glyph not in {%s} and not one of the %s/%s/%s star/mute/loop markers",
				k, v, marker, stateIconList(stateIcons), palette.StarIcon(), palette.MuteIcon(), palette.LoopIcon(),
			))
		}
	}
	used := map[string][]string{}
	for _, p := range cfg.Profiles {
		h := p.EffectiveHarness()
		if !harness.Valid(h) {
			continue
		}
		used[h] = append(used[h], p.Name)
	}
	harnessKeys := make([]string, 0, len(used))
	for h := range used {
		harnessKeys = append(harnessKeys, h)
	}
	sort.Strings(harnessKeys)
	for _, h := range harnessKeys {
		if _, ok := cfg.HarnessEmojis[h]; ok {
			continue
		}
		sort.Strings(used[h])
		errs = append(errs, fmt.Sprintf(
			"[harness_emojis] is missing an entry for harness %q (used by profile(s): %s)",
			h, strings.Join(used[h], ", "),
		))
	}
	return errs
}

// titleMarkerCollision probes v through the real title parser: if
// ParseTitleParts would consume v as a leading marker — a state icon,
// one of its U+FE0F-stripped fallbacks, or the star/mute/loop markers —
// it returns a human-readable name for whichever marker consumed it.
// Round-tripping through the parser keeps validation in lockstep with the
// fallback rules in palette instead of duplicating them here.
func titleMarkerCollision(v string) (marker string, collides bool) {
	probe := v + "x"
	parts := palette.ParseTitleParts(probe)
	if parts.Base == probe {
		return "", false
	}
	switch {
	case parts.State != "":
		return fmt.Sprintf("%q state icon", parts.State), true
	case parts.Starred:
		return fmt.Sprintf("star marker (%s)", palette.StarIcon()), true
	case parts.Muted:
		return fmt.Sprintf("mute marker (%s)", palette.MuteIcon()), true
	case parts.Looping:
		return fmt.Sprintf("loop marker (%s)", palette.LoopIcon()), true
	}
	return "title parser prefix", true
}

// validateHarnessColors checks the optional [harness_colors] table. When the
// table is present, every harness referenced by a profile must have a color so
// the dashboard and picker do not mix styled and unstyled harness cells.
func validateHarnessColors(cfg *Config) []string {
	var errs []string
	for _, conflict := range cfg.harnessColorKeyConflicts {
		errs = append(errs, fmt.Sprintf(
			"[harness_colors] duplicate keys normalize to the same harness (%s)\n  → keep only one spelling for each harness",
			conflict,
		))
	}
	keys := make([]string, 0, len(cfg.HarnessColors))
	for k := range cfg.HarnessColors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := cfg.HarnessColors[k]
		if !harness.Valid(k) {
			errs = append(errs, fmt.Sprintf(
				"[harness_colors] unknown harness %q\n  → valid harnesses: %s",
				k, strings.Join(harness.Names(), ", "),
			))
			continue
		}
		if !ValidColor(v) {
			errs = append(errs, fmt.Sprintf(
				"[harness_colors].%s: invalid color %q\n  → valid colors: green, blue, red, orange, purple, teal, pink, yellow, gray, cyan",
				k, v,
			))
		}
	}
	if len(cfg.HarnessColors) == 0 {
		return errs
	}

	used := map[string][]string{}
	for _, p := range cfg.Profiles {
		h := p.EffectiveHarness()
		if !harness.Valid(h) {
			continue
		}
		used[h] = append(used[h], p.Name)
	}
	harnessKeys := make([]string, 0, len(used))
	for h := range used {
		harnessKeys = append(harnessKeys, h)
	}
	sort.Strings(harnessKeys)
	for _, h := range harnessKeys {
		if _, ok := cfg.HarnessColors[h]; ok {
			continue
		}
		sort.Strings(used[h])
		errs = append(errs, fmt.Sprintf(
			"[harness_colors] is missing color for harness %q (used by profile(s): %s)",
			h, strings.Join(used[h], ", "),
		))
	}
	return errs
}
