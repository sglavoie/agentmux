package config_test

import (
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
