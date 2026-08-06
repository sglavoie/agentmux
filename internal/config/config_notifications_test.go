package config_test

import (
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
