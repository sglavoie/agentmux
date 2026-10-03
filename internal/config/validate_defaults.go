package config

import (
	"fmt"
	"os"
	"path/filepath"
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
	if d.StopConfirm.Delay < 0 {
		errs = append(errs, "defaults.stop_confirm.delay must be >= 0")
	}
	if d.StopConfirm.Timeout < 0 {
		errs = append(errs, "defaults.stop_confirm.timeout must be >= 0")
	}
	if d.StopConfirm.Delay >= 0 && d.StopConfirm.Timeout >= 0 && d.StopConfirm.Timeout < d.StopConfirm.Delay {
		errs = append(errs, fmt.Sprintf(
			"defaults.stop_confirm.timeout (%d) must be >= defaults.stop_confirm.delay (%d): the hard deadline cannot expire before the quiet period",
			d.StopConfirm.Timeout, d.StopConfirm.Delay,
		))
	}
	if d.SessionArchive.KeepDays < 0 {
		errs = append(errs, "defaults.session_archive.keep_days must be >= 0")
	}
	if d.SessionArchive.ScanMaxAgeDays < -1 {
		errs = append(errs, "defaults.session_archive.scan_max_age_days must be >= -1")
	}
	if d.Handoff.MaxBytes < 0 {
		errs = append(errs, "defaults.handoff.max_bytes must be >= 0")
	}
	if d.Handoff.RecentMessages < 0 {
		errs = append(errs, "defaults.handoff.recent_messages must be >= 0")
	}
	if d.Diagnostics.JournalKeepDays < 0 {
		errs = append(errs, "defaults.diagnostics.journal_keep_days must be >= 0")
	}
	if d.Diagnostics.JournalMaxMiB < 0 {
		errs = append(errs, "defaults.diagnostics.journal_max_mib must be >= 0")
	}
	if detail := strings.TrimSpace(d.Diagnostics.JournalDetail); detail != "" &&
		!strings.EqualFold(detail, "standard") && !strings.EqualFold(detail, "full") {
		errs = append(errs, fmt.Sprintf(
			"invalid defaults.diagnostics.journal_detail %q: must be one of standard, full",
			d.Diagnostics.JournalDetail,
		))
	}
	if d.Diagnostics.NativeKeepDays < 0 {
		errs = append(errs, "defaults.diagnostics.native_keep_days must be >= 0")
	}
	if d.Diagnostics.NativeMaxMiB < 0 {
		errs = append(errs, "defaults.diagnostics.native_max_mib must be >= 0")
	}
	if d.Diagnostics.NativeSessionMaxMiB < 0 {
		errs = append(errs, "defaults.diagnostics.native_session_max_mib must be >= 0")
	}
	if d.Diagnostics.NativeMaxMiB > 0 && d.Diagnostics.NativeSessionMaxMiB > d.Diagnostics.NativeMaxMiB {
		errs = append(errs, "defaults.diagnostics.native_session_max_mib must be <= native_max_mib")
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
	if d.Notifications.CooldownSeconds < 0 {
		errs = append(errs, "defaults.notifications.cooldown_seconds must be >= 0")
	}
	errs = append(errs, validateASL(&d.ASL)...)
	return errs
}

// aslShellMetacharacters are rejected in defaults.asl.command because the
// daemon starts the bridge directly with a fixed argv. Accepting shell syntax
// here would promise an interpretation that never happens.
const aslShellMetacharacters = "|&;<>()$`\\\"'\n\t*?[]#"

func validateASL(a *ASLConfig) []string {
	var errs []string
	// An empty state root is the supported default (derive it from XDG). A
	// relative one is not: the daemon's working directory is not the operator's,
	// so it would resolve somewhere neither side intended and simply report no
	// runs. Reject it here rather than let it degrade silently.
	if root := strings.TrimSpace(a.StateRoot); root != "" {
		if !filepath.IsAbs(root) && !strings.HasPrefix(root, "~") {
			errs = append(errs, "defaults.asl.state_root must be an absolute path or start with ~")
		}
	}

	command := strings.TrimSpace(a.Command)
	if command == "" {
		return errs
	}
	if strings.ContainsAny(command, aslShellMetacharacters) {
		return append(errs, "defaults.asl.command must be a plain executable name or path, without shell syntax")
	}
	if strings.Contains(command, " ") && !isExecutableFile(command) {
		return append(errs, "defaults.asl.command must not contain arguments; the daemon supplies them")
	}
	return errs
}

// isExecutableFile reports whether command is an existing absolute path to an
// executable file. The daemon passes the command as a single argv element, so a
// spaced string that names such a file is a legitimate path; anything else with
// a space can only be an attempt to pass arguments.
func isExecutableFile(command string) bool {
	if !filepath.IsAbs(command) {
		return false
	}
	info, err := os.Stat(command)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
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
