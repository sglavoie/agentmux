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
		StopConfirm struct {
			Delay   int
			Timeout int
			Enabled *bool
		} `toml:"stop_confirm"`
		SessionArchive struct {
			KeepDays       int `toml:"keep_days"`
			ScanMaxAgeDays int `toml:"scan_max_age_days"`
		} `toml:"session_archive"`
		Handoff struct {
			MaxBytes       int `toml:"max_bytes"`
			RecentMessages int `toml:"recent_messages"`
		}
		Diagnostics struct {
			JournalEnabled      *bool  `toml:"journal_enabled"`
			JournalKeepDays     int    `toml:"journal_keep_days"`
			JournalMaxMiB       int    `toml:"journal_max_mib"`
			JournalDetail       string `toml:"journal_detail"`
			NativeLogging       *bool  `toml:"native_logging"`
			NativeKeepDays      int    `toml:"native_keep_days"`
			NativeMaxMiB        int    `toml:"native_max_mib"`
			NativeSessionMaxMiB int    `toml:"native_session_max_mib"`
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
		Overview struct {
			Compact *bool `toml:"compact"`
		}
		ASL struct {
			Enabled *bool  `toml:"enabled"`
			Command string `toml:"command"`
		} `toml:"asl"`
		Release struct {
			EvidenceDir string `toml:"evidence_dir"`
		} `toml:"release"`
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

func TestExampleConfigOmitsRetiredActivityDefaults(t *testing.T) {
	if strings.Contains(ExampleConfig, "[defaults.activity]") {
		t.Fatal("ExampleConfig still advertises retired defaults.activity settings")
	}
}

func TestExampleConfigOmitsRetiredProjectID(t *testing.T) {
	if strings.Contains(ExampleConfig, "# "+"id =") || strings.Contains(ExampleConfig, "project "+"ID") {
		t.Fatal("ExampleConfig still advertises retired project id settings")
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

func TestExampleConfigIncludesDiagnosticDefaults(t *testing.T) {
	var decoded exampleConfig
	if _, err := toml.Decode(ExampleConfig, &decoded); err != nil {
		t.Fatal(err)
	}
	d := decoded.Defaults.Diagnostics
	if d.JournalEnabled == nil || !*d.JournalEnabled || d.NativeLogging == nil || !*d.NativeLogging {
		t.Fatalf("diagnostic enable defaults = %+v", d)
	}
	if d.JournalKeepDays != 14 || d.JournalMaxMiB != 256 || d.NativeKeepDays != 14 || d.NativeMaxMiB != 1024 || d.NativeSessionMaxMiB != 128 {
		t.Fatalf("diagnostic retention defaults = %+v", d)
	}
}

func TestExampleConfigIncludesStopConfirmDefaults(t *testing.T) {
	var decoded exampleConfig
	if _, err := toml.Decode(ExampleConfig, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Defaults.StopConfirm.Delay != 6 || decoded.Defaults.StopConfirm.Timeout != 15 {
		t.Fatalf("stop-confirm defaults = %+v", decoded.Defaults.StopConfirm)
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

func TestExampleConfigAdvertisesTheCompactOverviewDefault(t *testing.T) {
	var decoded exampleConfig
	if _, err := toml.Decode(ExampleConfig, &decoded); err != nil {
		t.Fatalf("ExampleConfig is not valid TOML: %v", err)
	}
	if c := decoded.Defaults.Overview.Compact; c == nil || !*c {
		t.Fatal("defaults.overview.compact must be advertised as true, the built-in default")
	}
}

func TestExampleConfigDoesNotAdvertiseEmptyProjectArgsAsOverride(t *testing.T) {
	if strings.Contains(ExampleConfig, "set to [] to pass none") {
		t.Fatal("ExampleConfig claims project args = [] disables inherited args, but empty args inherit")
	}
}
