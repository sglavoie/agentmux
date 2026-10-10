package config

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, content string) *Config {
	t.Helper()
	cfg, err := parseConfig([]byte(content))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	return cfg
}

func TestLaunchBackendDefaultsToKitty(t *testing.T) {
	cfg := mustParse(t, "[defaults]\n")
	if got := cfg.LaunchBackendOrDefault(); got != LaunchBackendKitty {
		t.Fatalf("an unset launch_backend launches on %q, want kitty: existing configurations must keep launching into Kitty", got)
	}
	var none *Config
	if got := none.LaunchBackendOrDefault(); got != LaunchBackendKitty {
		t.Fatalf("no configuration launches on %q, want kitty", got)
	}
}

func TestLaunchBackendAcceptsTmuxInAnyCase(t *testing.T) {
	cfg := mustParse(t, "[defaults]\nlaunch_backend = \" TMUX \"\n")
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := cfg.LaunchBackendOrDefault(); got != LaunchBackendTmux {
		t.Fatalf("launch backend = %q, want tmux", got)
	}
}

func TestLaunchBackendRejectsAnUnknownBackend(t *testing.T) {
	cfg := mustParse(t, "[defaults]\nlaunch_backend = \"ghostty\"\n")
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), `defaults.launch_backend "ghostty"`) {
		t.Fatalf("Validate = %v, want a launch_backend error naming the value", err)
	}
	if got := cfg.LaunchBackendOrDefault(); got != LaunchBackendKitty {
		t.Fatalf("an invalid launch_backend resolved to %q, want kitty", got)
	}
}

func TestTmuxPathExpandsTildeAndMustBeAbsolute(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	cfg := mustParse(t, "[defaults.tmux]\npath = \"~/bin/tmux\"\n")
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := cfg.TmuxPath(); got != "/home/someone/bin/tmux" {
		t.Fatalf("TmuxPath = %q", got)
	}

	relative := mustParse(t, "[defaults.tmux]\npath = \"tmux\"\n")
	err := Validate(relative)
	if err == nil || !strings.Contains(err.Error(), "defaults.tmux.path") || !strings.Contains(err.Error(), "leave it unset") {
		t.Fatalf("Validate = %v, want a tmux.path error that says how to fix it", err)
	}
}

func TestTmuxPathIsEmptyWhenUnset(t *testing.T) {
	cfg := mustParse(t, "[defaults]\n")
	if got := cfg.TmuxPath(); got != "" {
		t.Fatalf("TmuxPath = %q, want empty", got)
	}
	if len(cfg.UnknownKeys) != 0 {
		t.Fatalf("unknown keys: %v", cfg.UnknownKeys)
	}
}

func TestBackendKeysAreKnownSettings(t *testing.T) {
	cfg := mustParse(t, "[defaults]\nlaunch_backend = \"tmux\"\n[defaults.tmux]\npath = \"/opt/homebrew/bin/tmux\"\n")
	if len(cfg.UnknownKeys) != 0 {
		t.Fatalf("launch_backend and tmux.path were reported as unknown keys: %v", cfg.UnknownKeys)
	}
}
