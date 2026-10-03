package config_test

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestLoad_UserCanDisableHookSilenceNotification(t *testing.T) {
	content := `
[defaults.notifications]
hook_silence = false

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Defaults.Notifications.HookSilence
	if got == nil {
		t.Fatal("HookSilence: got nil, want non-nil pointer to false")
	}
	if *got {
		t.Fatalf("HookSilence user-disabled: got true, want false")
	}
}

func TestLoad_UserCanDisableStaleActiveNotification(t *testing.T) {
	content := `
[defaults.notifications]
stale_active = false

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Defaults.Notifications.StaleActive
	if got == nil {
		t.Fatal("StaleActive: got nil, want non-nil pointer to false")
	}
	if *got {
		t.Fatalf("StaleActive user-disabled: got true, want false (stale_active = false in config should be respected)")
	}
}

func TestLoad_StopFailureDefaultsTrueWhenAbsent(t *testing.T) {
	content := `
[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Defaults.Notifications.StopFailure
	if got == nil {
		t.Fatal("StopFailure: got nil, want non-nil pointer to true")
	}
	if !*got {
		t.Fatalf("StopFailure default: got %v, want true", *got)
	}
}

func TestLoad_UserCanDisableStopFailureNotification(t *testing.T) {
	content := `
[defaults.notifications]
stop_failure = false

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Defaults.Notifications.StopFailure
	if got == nil {
		t.Fatal("StopFailure: got nil, want non-nil pointer to false")
	}
	if *got {
		t.Fatalf("StopFailure user-disabled: got true, want false (stop_failure = false in config should be respected)")
	}
}

func TestLoad_NotificationAlerterPath(t *testing.T) {
	content := `
[defaults.notifications]
alerter_path = "/custom/bin/alerter"

[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	path := writeTempConfig(t, content)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.Defaults.Notifications.AlerterPath, "/custom/bin/alerter"; got != want {
		t.Fatalf("AlerterPath: got %q, want %q", got, want)
	}
}

func TestLoad_ElicitationFollowsPermissionUntilSet(t *testing.T) {
	base := `
[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	cases := []struct {
		name  string
		block string
		want  bool
	}{
		{"omitted follows permission on", "[defaults.notifications]\npermission = true\n", true},
		{"omitted follows permission off", "[defaults.notifications]\npermission = false\n", false},
		{"explicit off overrides permission", "[defaults.notifications]\npermission = true\nelicitation = false\n", false},
		{"explicit on without permission", "[defaults.notifications]\npermission = false\nelicitation = true\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Load(writeTempConfig(t, tc.block+base))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.Defaults.Notifications.ElicitationEnabled(); got != tc.want {
				t.Fatalf("ElicitationEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoad_NotificationCooldown(t *testing.T) {
	base := `
[[profiles]]
name = "work"
home_dir = "~/.claude-work"
color = "blue"
`
	cfg, err := config.Load(writeTempConfig(t, base))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Defaults.Notifications.CooldownSeconds; got != 10 {
		t.Fatalf("default CooldownSeconds = %d, want 10", got)
	}

	cfg, err = config.Load(writeTempConfig(t, "[defaults.notifications]\ncooldown_seconds = 45\n"+base))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Defaults.Notifications.CooldownSeconds; got != 45 {
		t.Fatalf("CooldownSeconds = %d, want 45", got)
	}

	cfg, err = config.Load(writeTempConfig(t, "[defaults.notifications]\ncooldown_seconds = -1\n"+base))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := config.Validate(cfg); err == nil || !strings.Contains(err.Error(), "cooldown_seconds") {
		t.Fatalf("Validate = %v, want a cooldown_seconds error", err)
	}
}
