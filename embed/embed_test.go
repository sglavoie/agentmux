package embed

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

type exampleConfig struct {
	Defaults struct {
		Sort               string `toml:"sort"`
		RefreshInterval    int    `toml:"refresh_interval"`
		GuardDuration      int    `toml:"guard_duration"`
		IdleThreshold      int    `toml:"idle_threshold"`
		Args               []string
		ConfirmDestructive bool `toml:"confirm_destructive"`
		Editor             string
		PlansDirectory     string `toml:"plans_directory"`
		Watchdog           struct {
			Active                  int
			Subagent                int
			Compact                 int
			HookSilence             int   `toml:"hook_silence"`
			ClaudeContextLimitProbe *bool `toml:"claude_context_limit_probe"`
		}
		SessionArchive struct {
			KeepDays       int `toml:"keep_days"`
			ScanMaxAgeDays int `toml:"scan_max_age_days"`
		} `toml:"session_archive"`
		Notifications struct {
			Permission           bool
			Stop                 bool
			StopFailure          *bool `toml:"stop_failure"`
			StaleActive          *bool `toml:"stale_active"`
			HookSilence          *bool `toml:"hook_silence"`
			PermissionDebounceMs int   `toml:"permission_debounce_ms"`
		}
	}
	HarnessColors map[string]string `toml:"harness_colors"`
}

func TestExampleConfigIsImmutableString(t *testing.T) {
	if ExampleConfig == "" {
		t.Fatal("ExampleConfig is empty")
	}
}

func TestExampleConfigIsValidTOML(t *testing.T) {
	var decoded map[string]any
	if _, err := toml.Decode(ExampleConfig, &decoded); err != nil {
		t.Fatalf("ExampleConfig is not valid TOML: %v", err)
	}

	if _, ok := decoded["defaults"]; !ok {
		t.Fatal("ExampleConfig is missing [defaults]")
	}
}

func TestExampleConfigActiveKeysMatchConfigShape(t *testing.T) {
	var decoded exampleConfig
	md, err := toml.Decode(ExampleConfig, &decoded)
	if err != nil {
		t.Fatalf("ExampleConfig is not valid TOML: %v", err)
	}

	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, key := range undecoded {
			keys[i] = key.String()
		}
		t.Fatalf("ExampleConfig has active keys outside the config shape: %s", strings.Join(keys, ", "))
	}
	if decoded.Defaults.Args == nil {
		t.Fatal("defaults.args was not decoded; it may be scoped under a nested table")
	}
	if _, ok := decoded.HarnessColors["claude"]; !ok {
		t.Fatal("harness_colors.claude was not decoded")
	}
}

func TestExampleConfigIncludesStopFailureNotificationToggle(t *testing.T) {
	var decoded struct {
		Defaults struct {
			Notifications struct {
				StopFailure *bool `toml:"stop_failure"`
			} `toml:"notifications"`
		} `toml:"defaults"`
	}
	if _, err := toml.Decode(ExampleConfig, &decoded); err != nil {
		t.Fatalf("ExampleConfig is not valid TOML: %v", err)
	}
	if decoded.Defaults.Notifications.StopFailure == nil {
		t.Fatal("ExampleConfig is missing defaults.notifications.stop_failure")
	}
	if !*decoded.Defaults.Notifications.StopFailure {
		t.Fatal("defaults.notifications.stop_failure = false, want true")
	}
}
