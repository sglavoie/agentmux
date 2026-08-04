package config

import (
	"strings"
	"testing"
)

func TestStopConfirmDefaultsAndOverrides(t *testing.T) {
	cfg, err := parseConfig([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.StopConfirm.Delay != 6 || cfg.Defaults.StopConfirm.Timeout != 15 {
		t.Fatalf("stop_confirm defaults = %#v", cfg.Defaults.StopConfirm)
	}
	if cfg.Defaults.StopConfirm.Enabled == nil || !*cfg.Defaults.StopConfirm.Enabled {
		t.Fatalf("stop_confirm.enabled default = %v, want explicit true", cfg.Defaults.StopConfirm.Enabled)
	}

	cfg, err = parseConfig([]byte("[defaults.stop_confirm]\ndelay = 3\ntimeout = 9\nenabled = false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.StopConfirm.Delay != 3 || cfg.Defaults.StopConfirm.Timeout != 9 {
		t.Fatalf("stop_confirm overrides = %#v", cfg.Defaults.StopConfirm)
	}
	if cfg.Defaults.StopConfirm.Enabled == nil || *cfg.Defaults.StopConfirm.Enabled {
		t.Fatalf("stop_confirm.enabled = %v, want explicit false", cfg.Defaults.StopConfirm.Enabled)
	}
}

func TestStopConfirmRejectsNegativeValues(t *testing.T) {
	cfg := &Config{Defaults: Defaults{StopConfirm: StopConfirmConfig{Delay: -1, Timeout: -2}}}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("negative stop_confirm values were accepted")
	}
	for _, want := range []string{"defaults.stop_confirm.delay", "defaults.stop_confirm.timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("validation error missing %q: %v", want, err)
		}
	}
}

func TestStopConfirmRejectsTimeoutBelowDelay(t *testing.T) {
	cfg := &Config{Defaults: Defaults{StopConfirm: StopConfirmConfig{Delay: 20, Timeout: 5}}}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("stop_confirm timeout below delay was accepted")
	}
	if !strings.Contains(err.Error(), "defaults.stop_confirm.timeout (5) must be >= defaults.stop_confirm.delay (20)") {
		t.Errorf("validation error is not specific about the ordering: %v", err)
	}
}
