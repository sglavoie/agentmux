package config_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestValidateRejectsDurationValuesThatOverflow(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("overflowing seconds and milliseconds do not fit in a 32-bit int")
	}
	maxSeconds := int64(1<<63-1) / int64(time.Second)
	maxMillis := int64(1<<63-1) / int64(time.Millisecond)
	cases := []struct {
		name string
		max  int64
		set  func(*config.Config, int)
	}{
		{"defaults.refresh_interval", maxSeconds, func(c *config.Config, v int) { c.Defaults.RefreshInterval = v }},
		{"defaults.guard_duration", maxSeconds, func(c *config.Config, v int) { c.Defaults.GuardDuration = v }},
		{"defaults.idle_threshold", maxSeconds, func(c *config.Config, v int) { c.Defaults.IdleThreshold = v }},
		{"defaults.stale_active_threshold", maxSeconds, func(c *config.Config, v int) { c.Defaults.StaleActiveThreshold = v }},
		{"defaults.watchdog.active", maxSeconds, func(c *config.Config, v int) { c.Defaults.Watchdog.Active = v }},
		{"defaults.watchdog.subagent", maxSeconds, func(c *config.Config, v int) { c.Defaults.Watchdog.Subagent = v }},
		{"defaults.watchdog.compact", maxSeconds, func(c *config.Config, v int) { c.Defaults.Watchdog.Compact = v }},
		{"defaults.watchdog.hook_silence", maxSeconds, func(c *config.Config, v int) { c.Defaults.Watchdog.HookSilence = v }},
		{"defaults.stop_confirm.delay", maxSeconds, func(c *config.Config, v int) {
			c.Defaults.StopConfirm.Delay, c.Defaults.StopConfirm.Timeout = v, v
		}},
		{"defaults.stop_confirm.timeout", maxSeconds, func(c *config.Config, v int) { c.Defaults.StopConfirm.Timeout = v }},
		{"defaults.git_probe_ttl", maxSeconds, func(c *config.Config, v int) { c.Defaults.GitProbeTTL = v }},
		{"defaults.git_probe_timeout", maxSeconds, func(c *config.Config, v int) { c.Defaults.GitProbeTimeout = v }},
		{"defaults.notifications.cooldown_seconds", maxSeconds, func(c *config.Config, v int) { c.Defaults.Notifications.CooldownSeconds = v }},
		{"defaults.notifications.permission_debounce_ms", maxMillis, func(c *config.Config, v int) { c.Defaults.Notifications.PermissionDebounceMs = v }},
		{"defaults.preview.refresh_ms", maxMillis, func(c *config.Config, v int) { c.Defaults.Preview.RefreshMs = v }},
		{"profile \"work\": watchdog.active", maxSeconds, func(c *config.Config, v int) { c.Profiles[0].Watchdog.Active = v }},
		{"profile \"work\": watchdog.subagent", maxSeconds, func(c *config.Config, v int) { c.Profiles[0].Watchdog.Subagent = v }},
		{"profile \"work\": watchdog.compact", maxSeconds, func(c *config.Config, v int) { c.Profiles[0].Watchdog.Compact = v }},
		{"profile \"work\": watchdog.hook_silence", maxSeconds, func(c *config.Config, v int) { c.Profiles[0].Watchdog.HookSilence = v }},
	}
	newConfig := func() *config.Config {
		return &config.Config{
			HarnessEmojis: map[string]string{"claude": "🦊"},
			Profiles:      []config.Account{{Name: "work", Harness: "claude", HomeDir: "~/.claude", Color: "yellow"}},
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			valid := newConfig()
			tc.set(valid, int(tc.max))
			if err := config.Validate(valid); err != nil {
				t.Fatalf("largest representable value rejected: %v", err)
			}
			invalid := newConfig()
			tc.set(invalid, int(tc.max+1))
			if err := config.Validate(invalid); err == nil || !strings.Contains(err.Error(), tc.name+" must be <=") {
				t.Fatalf("overflowing %s accepted: %v", tc.name, err)
			}
		})
	}
}

func TestValidateRejectsNaNPreviewRatioFromTOML(t *testing.T) {
	path := writeTempConfig(t, "[defaults.preview]\nratio = nan\n")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(cfg); err == nil || !strings.Contains(err.Error(), "defaults.preview.ratio must be > 0 and < 1") {
		t.Fatalf("NaN preview ratio accepted: %v", err)
	}
}
