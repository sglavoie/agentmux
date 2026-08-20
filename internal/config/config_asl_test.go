package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestASLTriStateDistinguishesOmittedFromExplicitFalse(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string
	}{
		{"omitted section", "", "auto"},
		{"omitted key", "[defaults.asl]\ncommand = \"asl\"\n", "auto"},
		{"explicit true", "[defaults.asl]\nenabled = true\n", "required"},
		{"explicit false", "[defaults.asl]\nenabled = false\n", "disabled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseConfig([]byte(tc.toml))
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Defaults.ASL.ASLModeValue(); got != tc.want {
				t.Fatalf("mode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestASLCommandDefaultsAndOverride(t *testing.T) {
	cfg, err := parseConfig([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.ASL.Command != "asl" {
		t.Fatalf("command default = %q, want asl", cfg.Defaults.ASL.Command)
	}

	cfg, err = parseConfig([]byte("[defaults.asl]\ncommand = \"/opt/asl/bin/asl\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.ASL.Command != "/opt/asl/bin/asl" {
		t.Fatalf("command = %q", cfg.Defaults.ASL.Command)
	}
}

func TestASLCommandRejectsShellSyntax(t *testing.T) {
	for _, command := range []string{
		"asl && rm -rf /",
		"asl; echo hi",
		"asl | tee log",
		"$(which asl)",
		"asl `id`",
		"asl bridge serve",
	} {
		cfg := &Config{Defaults: Defaults{ASL: ASLConfig{Command: command}}}
		err := Validate(cfg)
		if err == nil {
			t.Errorf("command %q was accepted", command)
			continue
		}
		if !strings.Contains(err.Error(), "defaults.asl.command") {
			t.Errorf("command %q: validation error missing the key name: %v", command, err)
		}
	}
}

func TestASLCommandAcceptsPlainExecutables(t *testing.T) {
	for _, command := range []string{"asl", "/opt/asl/bin/asl", "~/bin/asl"} {
		cfg := &Config{Defaults: Defaults{ASL: ASLConfig{Command: command}}}
		if err := Validate(cfg); err != nil {
			t.Errorf("command %q was rejected: %v", command, err)
		}
	}
}

func TestASLCommandAcceptsExistingPathWithSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Agent Tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(dir, "asl")
	if err := os.WriteFile(command, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Defaults: Defaults{ASL: ASLConfig{Command: command}}}
	if err := Validate(cfg); err != nil {
		t.Fatalf("command %q was rejected: %v", command, err)
	}
}

func TestASLCommandRejectsArgumentsAndNonExecutableSpacedPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Agent Tools")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	notExecutable := filepath.Join(dir, "asl.txt")
	if err := os.WriteFile(notExecutable, []byte("not a program\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, command := range []string{
		"asl --stdio",
		"/opt/asl/bin/asl --protocol 1",
		filepath.Join(dir, "missing asl"),
		notExecutable + " --stdio",
		dir,
	} {
		cfg := &Config{Defaults: Defaults{ASL: ASLConfig{Command: command}}}
		err := Validate(cfg)
		if err == nil {
			t.Errorf("command %q was accepted", command)
			continue
		}
		if !strings.Contains(err.Error(), "defaults.asl.command") {
			t.Errorf("command %q: validation error missing the key name: %v", command, err)
		}
	}
}
