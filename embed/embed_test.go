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
		Activity struct {
			KeepDays int `toml:"keep_days"`
		}
		Handoff struct {
			MaxBytes       int `toml:"max_bytes"`
			RecentMessages int `toml:"recent_messages"`
		}
		Notifications struct {
			Permission           bool
			Stop                 bool
			StopFailure          *bool `toml:"stop_failure"`
			StaleActive          *bool `toml:"stale_active"`
			HookSilence          *bool `toml:"hook_silence"`
			PermissionDebounceMs int   `toml:"permission_debounce_ms"`
		}
		Preview struct {
			Direction string
			Ratio     float64
			RefreshMs int `toml:"refresh_ms"`
			MinCols   int `toml:"min_cols"`
			MinRows   int `toml:"min_rows"`
		}
	}
	HarnessEmojis map[string]string `toml:"harness_emojis"`
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
	if _, ok := decoded.HarnessEmojis["claude"]; !ok {
		t.Fatal("harness_emojis.claude was not decoded")
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

func TestExampleConfigIncludesPreviewDefaults(t *testing.T) {
	var decoded struct {
		Defaults struct {
			Preview struct {
				Direction string
				Ratio     float64
				RefreshMs int `toml:"refresh_ms"`
				MinCols   int `toml:"min_cols"`
				MinRows   int `toml:"min_rows"`
			}
		}
	}
	if _, err := toml.Decode(ExampleConfig, &decoded); err != nil {
		t.Fatalf("ExampleConfig is not valid TOML: %v", err)
	}
	if decoded.Defaults.Preview.Direction != "horizontal" {
		t.Fatalf("defaults.preview.direction = %q, want horizontal", decoded.Defaults.Preview.Direction)
	}
	if decoded.Defaults.Preview.Ratio != 0.5 {
		t.Fatalf("defaults.preview.ratio = %v, want 0.5", decoded.Defaults.Preview.Ratio)
	}
	if decoded.Defaults.Preview.RefreshMs != 500 {
		t.Fatalf("defaults.preview.refresh_ms = %d, want 500", decoded.Defaults.Preview.RefreshMs)
	}
	if decoded.Defaults.Preview.MinCols != 100 {
		t.Fatalf("defaults.preview.min_cols = %d, want 100", decoded.Defaults.Preview.MinCols)
	}
	if decoded.Defaults.Preview.MinRows != 30 {
		t.Fatalf("defaults.preview.min_rows = %d, want 30", decoded.Defaults.Preview.MinRows)
	}
}

func TestExampleConfigDoesNotAdvertiseEmptyProjectArgsAsOverride(t *testing.T) {
	if strings.Contains(ExampleConfig, "set to [] to pass none") {
		t.Fatal("ExampleConfig claims project args = [] disables inherited args, but empty args inherit")
	}
}
