package config

import (
	"strings"
	"testing"
)

func TestHandoffDefaultsAndOverrides(t *testing.T) {
	cfg, err := parseConfig([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.Handoff.MaxBytes != 65536 || cfg.Defaults.Handoff.RecentMessages != 12 {
		t.Fatalf("handoff defaults = %#v", cfg.Defaults.Handoff)
	}
	cfg, err = parseConfig([]byte("[defaults.handoff]\nmax_bytes = 8192\nrecent_messages = 5\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.Handoff.MaxBytes != 8192 || cfg.Defaults.Handoff.RecentMessages != 5 {
		t.Fatalf("handoff overrides = %#v", cfg.Defaults.Handoff)
	}
}

func TestHandoffRejectsNegativeValues(t *testing.T) {
	cfg := &Config{Defaults: Defaults{Handoff: HandoffConfig{MaxBytes: -1, RecentMessages: -2}}}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("negative handoff values were accepted")
	}
	for _, want := range []string{"defaults.handoff.max_bytes", "defaults.handoff.recent_messages"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("validation error missing %q: %v", want, err)
		}
	}
}
