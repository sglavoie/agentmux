package config_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestLoadDiagnosticDefaults(t *testing.T) {
	path := writeTempConfig(t, "[defaults]\n")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Defaults.Diagnostics
	if !d.JournalEnabledValue() || !d.NativeLoggingValue() {
		t.Fatalf("diagnostic enabled defaults = journal:%v native:%v, want true/true", d.JournalEnabledValue(), d.NativeLoggingValue())
	}
	if d.JournalKeepDays != 14 || d.JournalMaxMiB != 256 || d.NativeKeepDays != 14 || d.NativeMaxMiB != 1024 || d.NativeSessionMaxMiB != 128 {
		t.Fatalf("diagnostic defaults = %+v", d)
	}
}

func TestLoadDiagnosticsPreservesExplicitDisabledValues(t *testing.T) {
	path := writeTempConfig(t, `[defaults.diagnostics]
journal_enabled = false
native_logging = false
journal_keep_days = 3
journal_max_mib = 9
native_keep_days = 4
native_max_mib = 20
native_session_max_mib = 10
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Defaults.Diagnostics
	if d.JournalEnabledValue() || d.NativeLoggingValue() {
		t.Fatalf("explicit false values were overwritten: %+v", d)
	}
	if d.JournalKeepDays != 3 || d.JournalMaxMiB != 9 || d.NativeKeepDays != 4 || d.NativeMaxMiB != 20 || d.NativeSessionMaxMiB != 10 {
		t.Fatalf("explicit diagnostic values changed: %+v", d)
	}
}

func TestValidateDiagnosticsRejectsNegativeAndOversizedSessionQuota(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
		JournalKeepDays:     -1,
		JournalMaxMiB:       -2,
		NativeKeepDays:      -3,
		NativeMaxMiB:        10,
		NativeSessionMaxMiB: 11,
	}}}
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("Validate accepted invalid diagnostic retention")
	}
	for _, field := range []string{"journal_keep_days", "journal_max_mib", "native_keep_days", "native_session_max_mib"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("validation error missing %s: %v", field, err)
		}
	}
}

func TestValidateDiagnosticsRejectsMiBValuesThatOverflowByteCounts(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("overflowing MiB values do not fit in a 32-bit int")
	}
	large := int64(1) << 43
	tooLarge := int(large)
	for _, field := range []struct {
		name string
		set  func(*config.DiagnosticsConfig)
	}{
		{"journal_max_mib", func(d *config.DiagnosticsConfig) { d.JournalMaxMiB = tooLarge }},
		{"native_max_mib", func(d *config.DiagnosticsConfig) { d.NativeMaxMiB = tooLarge }},
		{"native_session_max_mib", func(d *config.DiagnosticsConfig) { d.NativeSessionMaxMiB = tooLarge }},
	} {
		t.Run(field.name, func(t *testing.T) {
			cfg := &config.Config{}
			field.set(&cfg.Defaults.Diagnostics)
			if err := config.Validate(cfg); err == nil || !strings.Contains(err.Error(), field.name+" must be <=") {
				t.Fatalf("Validate accepted overflowing %s: %v", field.name, err)
			}
		})
	}
}
