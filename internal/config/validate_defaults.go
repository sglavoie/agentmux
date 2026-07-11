package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/palette"
)

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
	if d.SessionArchive.KeepDays < 0 {
		errs = append(errs, "defaults.session_archive.keep_days must be >= 0")
	}
	if d.SessionArchive.ScanMaxAgeDays < -1 {
		errs = append(errs, "defaults.session_archive.scan_max_age_days must be >= -1")
	}
	if d.Activity.KeepDays < 0 {
		errs = append(errs, "defaults.activity.keep_days must be >= 0")
	}
	if d.Handoff.MaxBytes < 0 {
		errs = append(errs, "defaults.handoff.max_bytes must be >= 0")
	}
	if d.Handoff.RecentMessages < 0 {
		errs = append(errs, "defaults.handoff.recent_messages must be >= 0")
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

func validateJump(j *JumpConfig) []string {
	if len(j.AttentionStates) == 0 {
		return nil
	}
	known := palette.StateOrder()
	knownSet := make(map[string]bool, len(known))
	for _, s := range known {
		knownSet[s] = true
	}
	sortedKnown := make([]string, len(known))
	copy(sortedKnown, known)
	sort.Strings(sortedKnown)
	validList := strings.Join(sortedKnown, ", ")

	var errs []string
	for _, s := range j.AttentionStates {
		if !knownSet[s] {
			errs = append(errs, fmt.Sprintf(
				"defaults.jump.attention_states: unknown state %q\n  → valid states: %s",
				s, validList,
			))
		}
	}
	return errs
}
