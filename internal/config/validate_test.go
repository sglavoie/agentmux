package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestValidate_InvalidHarness(t *testing.T) {
	path := filepath.Join(testdataDir, "invalid_harness_config.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for invalid harness, got nil")
	}
	if !strings.Contains(err.Error(), "harness") {
		t.Errorf("error %q does not mention harness", err.Error())
	}
}

func TestValidate_MissingHomeDirAfterNormalization(t *testing.T) {
	// normalizeProfiles always fills HomeDir (from ConfigDir, then DefaultHomeDir),
	// so a profile with no home_dir or config_dir specified does not fail validation
	// for home_dir. This test documents that invariant.
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "belt", Harness: "claude"},
		},
	}
	err := config.Validate(cfg)
	if err != nil {
		if strings.Contains(err.Error(), "home_dir") {
			t.Errorf("unexpected home_dir error — normalization should have filled HomeDir: %v", err)
		}
	}
}

func TestValidate_SessionArchiveNegativeKeepDays(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	cfg.Defaults.SessionArchive.KeepDays = -1
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for negative session_archive.keep_days")
	}
	if !strings.Contains(err.Error(), "session_archive.keep_days must be >= 0") {
		t.Errorf("expected session_archive.keep_days error, got: %v", err)
	}
}

// TestValidate_PreviewMinDimensions exercises the boundary cases for the
// preview.min_cols / preview.min_rows checks. The previous error message
// claimed "must be > 0" but the predicate only rejected negative values, so
// zero (which applyDefaults rewrites to the package default) was allowed.
// Validation must accept zero, reject negative values, and report the lower
// bound that actually matches the predicate.
func TestValidate_PreviewMinDimensions(t *testing.T) {
	zero := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
	}
	zero.Defaults.Preview.MinCols = 0
	zero.Defaults.Preview.MinRows = 0
	if err := config.Validate(zero); err != nil && (strings.Contains(err.Error(), "min_cols") || strings.Contains(err.Error(), "min_rows")) {
		t.Fatalf("zero min dimensions must be accepted (filled by applyDefaults later), got: %v", err)
	}

	neg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
	}
	neg.Defaults.Preview.MinCols = -1
	neg.Defaults.Preview.MinRows = -2
	err := config.Validate(neg)
	if err == nil {
		t.Fatal("expected validation error for negative min dimensions")
	}
	msg := err.Error()
	if !strings.Contains(msg, "min_cols must be >= 0") {
		t.Errorf("expected min_cols error to say '>= 0' (matches predicate), got: %s", msg)
	}
	if !strings.Contains(msg, "min_rows must be >= 0") {
		t.Errorf("expected min_rows error to say '>= 0' (matches predicate), got: %s", msg)
	}
}

func TestValidate_DuplicateProjectTitlePerProfile(t *testing.T) {
	// Two projects with the same (profile, title) pair are ambiguous: launch dedup
	// keys on this pair, so duplicates would mask one of them and silently confuse
	// the user. Validation must surface this rather than wait for runtime confusion.
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work"},
			{Title: "api", Directory: "/tmp/b", Profile: "work"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for duplicate project title under same profile, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") || !strings.Contains(err.Error(), "api") {
		t.Errorf("expected duplicate/api in error, got %q", err.Error())
	}
}

// TestValidate_DuplicateProjectTitleCaseInsensitive pins the dedup contract:
// launch-time SessionKey lowercases both the profile name and project title
// before deduping, so validation must use the same case-folding. Two projects
// "api" and "API" under profile "work" would silently collide at runtime if
// validation only checked exact-case equality.
func TestValidate_DuplicateProjectTitleCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work"},
			{Title: "API", Directory: "/tmp/b", Profile: "work"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for case-insensitive duplicate title under same profile, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate in error, got %q", err.Error())
	}
}

// TestValidate_DuplicateProjectTitleProfileCaseInsensitive pins the same dedup
// contract on the profile side: "Work" and "work" lowercase to the same key
// in SessionKey, so they share a runtime dedup namespace and validation must
// surface the collision rather than wait for confusing launch behavior.
func TestValidate_DuplicateProjectTitleProfileCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
			{Name: "Work", Harness: "claude", HomeDir: "/tmp/h2"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work"},
			{Title: "api", Directory: "/tmp/b", Profile: "Work"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for case-insensitive profile collision, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate in error, got %q", err.Error())
	}
}

func TestValidate_DuplicateProjectTitleAcrossProfilesAllowed(t *testing.T) {
	// Same title under different profiles is allowed: launch dedup keys on
	// (account, title), so they are distinct sessions.
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/w", Color: "blue"},
			{Name: "personal", Harness: "claude", HomeDir: "/tmp/p", Color: "blue"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work"},
			{Title: "api", Directory: "/tmp/b", Profile: "personal"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("unexpected error for same-title-across-profiles: %v", err)
	}
}

func TestValidate_UnknownHookDisabledEvent(t *testing.T) {
	// A typo in hooks.overrides.<account>.disabled_events silently turns into a
	// no-op: the named event isn't in the harness's known set, so nothing is
	// disabled and the user wonders why their hook still fires. Validation must
	// catch this and report the typo with a hint to the valid set.
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
		Hooks: config.HooksConfig{
			Overrides: map[string]config.AccountHookOverride{
				"work": {DisabledEvents: []string{"Stop", "Stopp"}},
			},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unknown disabled hook event, got nil")
	}
	if !strings.Contains(err.Error(), "Stopp") {
		t.Errorf("expected error to mention the offending event %q, got %q", "Stopp", err.Error())
	}
	if strings.Contains(err.Error(), "\"Stop\"") {
		// The known event "Stop" must NOT be flagged.
		t.Errorf("validation should not complain about known event %q; got %q", "Stop", err.Error())
	}
}

// TestValidate_EmptyNamesNotReportedAsDuplicate pins the contract that
// multiple unnamed profiles surface as "name is required" only, not as a
// misleading 'duplicate profile name ""' on top. The duplicate check must
// skip the empty-name case so the user sees the real fix (name the
// profiles) instead of a phantom duplicate report.
func TestValidate_EmptyNamesNotReportedAsDuplicate(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Harness: "claude", HomeDir: "/tmp/a"},
			{Harness: "claude", HomeDir: "/tmp/b"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unnamed profiles, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "name is required") {
		t.Errorf("expected per-entry 'name is required' error, got %q", msg)
	}
	if strings.Contains(msg, "duplicate profile name") {
		t.Errorf("validation must not report duplicates for empty names; got %q", msg)
	}
}

// TestValidate_DuplicateProfileNameCaseInsensitive pins the dedup contract on
// profile names: SessionKey lowercases the profile name when building dedup
// keys, so two profiles whose names differ only in case ("work" and "Work")
// share a runtime namespace. Without overlapping project titles the project-
// side dedup check wouldn't fire, so validation must catch the profile-level
// collision directly.
func TestValidate_DuplicateProfileNameCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/a"},
			{Name: "Work", Harness: "claude", HomeDir: "/tmp/b"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for case-insensitive duplicate profile name, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate profile name") {
		t.Errorf("expected 'duplicate profile name' in error, got %q", err.Error())
	}
}

// TestValidate_ProjectProfileReferenceCaseInsensitive pins the contract that
// project.profile references resolve case-insensitively: runtime findAccount
// uses strings.EqualFold, so a project listing profile = "Work" against a
// configured profile name "work" works at launch time and validation must not
// reject it as unknown.
func TestValidate_ProjectProfileReferenceCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "Work"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("unexpected error for case-insensitive profile reference: %v", err)
	}
}

// TestValidate_HookOverrideKeyCaseInsensitive pins the contract that
// [hooks.overrides.NAME] keys resolve to profiles case-insensitively, matching
// hooks.accountOverride at merge time. Before the fix, validation rejected a
// [hooks.overrides.Work] entry against profile "work" as "unknown profile",
// even though the override would be applied correctly at merge time.
func TestValidate_HookOverrideKeyCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
		Hooks: config.HooksConfig{
			Overrides: map[string]config.AccountHookOverride{
				"Work": {DisabledEvents: []string{"Stop"}},
			},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("unexpected error for case-insensitive override key: %v", err)
	}
}

// TestValidate_DuplicateHookOverrideKeyCaseInsensitive pins the contract that
// two [hooks.overrides.NAME] entries differing only in case are reported as a
// config error. At merge time, hooks.accountOverride iterates the map and
// returns whichever case-folded entry the Go map iterator visits first — a
// random choice — so the user would silently get one set of disabled events or
// the other on a per-run basis. Surface it instead.
func TestValidate_DuplicateHookOverrideKeyCaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
		Hooks: config.HooksConfig{
			Overrides: map[string]config.AccountHookOverride{
				"work": {DisabledEvents: []string{"Stop"}},
				"Work": {DisabledEvents: []string{"PreToolUse"}},
			},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for case-only duplicate override key, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate hooks.overrides key") {
		t.Errorf("expected 'duplicate hooks.overrides key' in error, got %q", err.Error())
	}
}

// TestValidate_NegativeWatchdogThresholds pins that each watchdog sub-field
// rejects negative values with a per-field error message, matching the
// existing `stale_active_threshold` pattern. A negative threshold is always a
// typo — there is no sensible interpretation.
func TestValidate_NegativeWatchdogThresholds(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*config.Defaults)
		want string
	}{
		{"active", func(d *config.Defaults) { d.Watchdog.Active = -1 }, "defaults.watchdog.active must be >= 0"},
		{"subagent", func(d *config.Defaults) { d.Watchdog.Subagent = -1 }, "defaults.watchdog.subagent must be >= 0"},
		{"compact", func(d *config.Defaults) { d.Watchdog.Compact = -1 }, "defaults.watchdog.compact must be >= 0"},
		{"hook_silence", func(d *config.Defaults) { d.Watchdog.HookSilence = -1 }, "defaults.watchdog.hook_silence must be >= 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				HarnessColors: map[string]string{"claude": "blue"},
				Profiles:      []config.Account{{Name: "work", Harness: "claude", HomeDir: "/tmp/h"}},
			}
			tc.mut(&cfg.Defaults)
			err := config.Validate(cfg)
			if err == nil {
				t.Fatalf("expected validation error for negative watchdog.%s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestValidate_PerProfileNegativeWatchdog pins that per-profile overrides
// are validated with the same rules as the defaults — a negative value
// inside [[profiles]].watchdog must be rejected with a profile-qualified
// error so the user can locate it.
func TestValidate_PerProfileNegativeWatchdog(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Watchdog: config.WatchdogConfig{Active: -5}},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for negative per-profile watchdog.active, got nil")
	}
	if !strings.Contains(err.Error(), "profile \"work\"") || !strings.Contains(err.Error(), "watchdog.active") {
		t.Errorf("error %q must qualify profile and field", err.Error())
	}
}

// TestValidate_ZeroWatchdogAccepted pins that 0 means "use default" — it must
// not trigger an error or a sub-30s warning. After applyDefaults the field is
// always populated to a sane value, so 0 at validate time would actually be
// the zero-value Defaults struct used by tests; either way 0 is benign.
func TestValidate_ZeroWatchdogAccepted(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Watchdog: config.WatchdogConfig{Active: 0, Subagent: 0, Compact: 0, HookSilence: 0}},
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles:      []config.Account{{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"}},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("zero watchdog values must validate cleanly: %v", err)
	}
	for _, d := range cfg.Deprecations {
		if strings.HasPrefix(d.Key, "defaults.watchdog.") {
			t.Errorf("zero values must not emit a watchdog warning, got %+v", d)
		}
	}
}

// TestValidate_SubThirtyWatchdogWarns pins the soft-bound rule: a value in
// (0, 30) is accepted (no error) but appended to cfg.Deprecations as an
// advisory so the TUI banner surfaces the suspicious value. The check covers
// both default-scope and per-profile keys.
func TestValidate_SubThirtyWatchdogWarns(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Watchdog: config.WatchdogConfig{Active: 5}},
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue", Watchdog: config.WatchdogConfig{Subagent: 10}},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("sub-30s watchdog values must validate (only warn): %v", err)
	}
	keys := make(map[string]config.Deprecation, len(cfg.Deprecations))
	for _, d := range cfg.Deprecations {
		keys[d.Key] = d
	}
	if _, ok := keys["defaults.watchdog.active"]; !ok {
		t.Errorf("missing warning for defaults.watchdog.active=5 (got %+v)", cfg.Deprecations)
	}
	if _, ok := keys["profiles.work.watchdog.subagent"]; !ok {
		t.Errorf("missing warning for per-profile watchdog.subagent=10 (got %+v)", cfg.Deprecations)
	}
}

// TestValidate_WatchdogWarningsIdempotent pins that calling Validate multiple
// times on the same *Config doesn't duplicate the sub-30s advisory entries in
// cfg.Deprecations. Validate appends watchdog warnings on every call, so
// without dedup the TUI banner would show the same key 2x, 3x, ... each time
// the user revalidates after an edit.
func TestValidate_WatchdogWarningsIdempotent(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Watchdog: config.WatchdogConfig{Active: 5}},
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue", Watchdog: config.WatchdogConfig{Subagent: 10}},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("first Validate: %v", err)
	}
	first := append([]config.Deprecation(nil), cfg.Deprecations...)
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("second Validate: %v", err)
	}
	if len(cfg.Deprecations) != len(first) {
		t.Fatalf("Deprecations grew across Validate calls: got %d, want %d (first=%+v, after=%+v)",
			len(cfg.Deprecations), len(first), first, cfg.Deprecations)
	}
	// Count occurrences of each key — none should appear more than once.
	counts := map[string]int{}
	for _, d := range cfg.Deprecations {
		counts[d.Key]++
	}
	for k, n := range counts {
		if n > 1 {
			t.Errorf("Deprecation key %q appears %d times after re-validate; want 1", k, n)
		}
	}
}

// TestValidate_StripsStaleWatchdogWarningsOnRevalidate pins that when the user
// fixes a sub-30s watchdog value and revalidates, the prior advisory for that
// key is dropped — the banner reflects the current config, not historical
// state. Before the fix, stripWatchdogWarnings cleared only when collecting
// fresh entries, so a fixed key would still surface a stale warning from the
// previous Validate call.
func TestValidate_StripsStaleWatchdogWarningsOnRevalidate(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Watchdog: config.WatchdogConfig{Active: 5}},
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("first Validate: %v", err)
	}
	found := false
	for _, d := range cfg.Deprecations {
		if d.Key == "defaults.watchdog.active" {
			found = true
		}
	}
	if !found {
		t.Fatalf("first Validate did not emit watchdog warning (got %+v)", cfg.Deprecations)
	}
	// User edits the config to a sane threshold and revalidates.
	cfg.Defaults.Watchdog.Active = 600
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("second Validate: %v", err)
	}
	for _, d := range cfg.Deprecations {
		if d.Key == "defaults.watchdog.active" {
			t.Errorf("stale watchdog warning persisted after threshold was fixed: %+v", d)
		}
	}
}

// TestValidate_ProjectProfileAccountConflict pins that a project specifying
// both profile and account with non-matching values is reported as an error.
// normalizeProjects only mirrors profile↔account when one of them is empty,
// so two different values would be silently retained — runtime paths read
// Profile (via EffectiveProfile) for dashboard display and Account for sort/
// launch context, so a mismatch would split the same project across two
// profiles. The user must collapse them to one value or use only profile.
func TestValidate_ProjectProfileAccountConflict(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "X", Harness: "claude", HomeDir: "/tmp/x"},
			{Name: "Y", Harness: "claude", HomeDir: "/tmp/y"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "X", Account: "Y"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for conflicting profile/account on project, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "profile") || !strings.Contains(msg, "account") {
		t.Errorf("error %q must mention both profile and account", msg)
	}
	if !strings.Contains(msg, "api") {
		t.Errorf("error %q must mention the offending project title", msg)
	}
}

// TestValidate_ProjectProfileAccountMatchingAccepted pins the matching case:
// when profile and account are equal (case-insensitively), validation must
// accept the duplication — it's redundant but unambiguous, and many legacy
// configs were written with both keys set to the same value.
func TestValidate_ProjectProfileAccountMatchingAccepted(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/w", Color: "blue"},
		},
		Projects: []config.Session{
			{Title: "api", Directory: "/tmp/a", Profile: "work", Account: "Work"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("matching profile/account (case-insensitively) must validate: %v", err)
	}
}

// TestValidate_NegativePermissionDebounce pins that a negative
// permission_debounce_ms is rejected at validate time. Without this check the
// value flows through unchanged and is later interpreted as a duration in the
// daemon's debounce path, where a negative debounce yields confusing
// instant-fire behavior with no signal to the user.
func TestValidate_NegativePermissionDebounce(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Notifications: config.NotificationConfig{PermissionDebounceMs: -1},
		},
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for negative permission_debounce_ms, got nil")
	}
	if !strings.Contains(err.Error(), "permission_debounce_ms must be >= 0") {
		t.Errorf("error %q does not mention permission_debounce_ms bound", err.Error())
	}
}

func TestValidate_UnknownHookOverrideAccount(t *testing.T) {
	// An override keyed by a profile that doesn't exist is dead weight; the user
	// renamed the profile or made a typo. Surface it.
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
		Hooks: config.HooksConfig{
			Overrides: map[string]config.AccountHookOverride{
				"worrk": {DisabledEvents: []string{"Stop"}},
			},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unknown hooks.overrides profile, got nil")
	}
	if !strings.Contains(err.Error(), "worrk") {
		t.Errorf("expected error to mention the unknown profile %q, got %q", "worrk", err.Error())
	}
}

// TestValidate_MissingHarnessColorListsReferringProfiles pins that the
// validator reports a missing [harness_colors] entry for any harness used by
// at least one profile, and the message names every profile referencing the
// harness in sorted order so the user can locate them.
func TestValidate_MissingHarnessColorListsReferringProfiles(t *testing.T) {
	cfg := &config.Config{
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/w"},
			{Name: "personal", Harness: "claude", HomeDir: "/tmp/p"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for missing harness color, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[harness_colors] is missing color for harness \"claude\"") {
		t.Errorf("expected missing-harness error, got %q", msg)
	}
	if !strings.Contains(msg, "personal, work") {
		t.Errorf("expected sorted profile list in error, got %q", msg)
	}
}

// TestValidate_HarnessColorsUnknownHarnessRejected pins that an entry in
// [harness_colors] keyed by an unknown harness name is reported with the set
// of valid harnesses appended for guidance.
func TestValidate_HarnessColorsUnknownHarnessRejected(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue", "bogus": "red"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for unknown harness key, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[harness_colors] unknown harness \"bogus\"") {
		t.Errorf("expected unknown-harness error, got %q", msg)
	}
	if !strings.Contains(msg, "valid harnesses:") {
		t.Errorf("expected message to list valid harnesses, got %q", msg)
	}
}

// TestValidate_HarnessColorsInvalidColorRejected pins that a [harness_colors]
// value that is not in the palette is rejected with a per-harness location and
// the palette list appended.
func TestValidate_HarnessColorsInvalidColorRejected(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "magenta"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h"},
		},
	}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for invalid harness color, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[harness_colors].claude") {
		t.Errorf("expected per-harness location in error, got %q", msg)
	}
	if !strings.Contains(msg, "magenta") {
		t.Errorf("expected offending color in error, got %q", msg)
	}
}

// TestValidate_HarnessColorsExtraEntryForUnusedHarnessAllowed pins that
// supplying a [harness_colors] entry for a harness no profile references is
// allowed — the user may be staging a future profile, and the entry is inert
// until referenced.
func TestValidate_HarnessColorsExtraEntryForUnusedHarnessAllowed(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue", "copilot": "purple"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("extra harness_colors entry for unused harness must validate cleanly: %v", err)
	}
}

// TestValidate_HarnessColorsTwoProfilesShareOneEntry pins that two profiles
// backed by the same harness require only one [harness_colors] entry — the
// whole point of the migration is that color is keyed by harness, not by
// profile.
func TestValidate_HarnessColorsTwoProfilesShareOneEntry(t *testing.T) {
	cfg := &config.Config{
		HarnessColors: map[string]string{"claude": "blue"},
		Profiles: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "/tmp/w", Color: "blue"},
			{Name: "personal", Harness: "claude", HomeDir: "/tmp/p", Color: "blue"},
		},
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("two profiles sharing a harness must validate with one entry: %v", err)
	}
}
