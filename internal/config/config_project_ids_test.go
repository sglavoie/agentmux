package config_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/sglavoie/agentmux/internal/config"
)

func TestValidate_ProjectIDsAreCanonicalAndGloballyUnique(t *testing.T) {
	cfg := &config.Config{
		Profiles: []config.Account{{Name: "work", Harness: "claude", HomeDir: "/tmp/work", Color: "blue"}},
		Projects: []config.Session{
			{ID: "api", Title: "api", Directory: "/tmp/api", Profile: "work"},
			{ID: "API", Title: "web", Directory: "/tmp/web", Profile: "work"},
			{ID: "not valid", Title: "cli", Directory: "/tmp/cli", Profile: "work"},
		},
	}

	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("Validate returned nil, want project ID errors")
	}
	if got := err.Error(); !strings.Contains(got, `id "API" must match`) || !strings.Contains(got, `id "not valid" must match`) {
		t.Fatalf("Validate error = %q, want canonical ID diagnostics", got)
	}

	// A lowercase duplicate remains invalid even when both IDs match the
	// syntax, which pins global rather than per-profile uniqueness.
	cfg.Projects[1].ID = "api"
	cfg.Projects[2].ID = "cli"
	err = config.Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "duplicate project id") {
		t.Fatalf("Validate duplicate IDs = %v, want global duplicate error", err)
	}
}

func TestProjectIDRenderingOmitsLegacyEmptyValue(t *testing.T) {
	var out bytes.Buffer
	if err := toml.NewEncoder(&out).Encode(config.Session{Title: "api", Directory: "/tmp/api", Profile: "work"}); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if strings.Contains(out.String(), "id =") {
		t.Fatalf("encoded legacy session unexpectedly contains ID: %s", out.String())
	}

	out.Reset()
	if err := toml.NewEncoder(&out).Encode(config.Session{ID: "api", Title: "api", Directory: "/tmp/api", Profile: "work"}); err != nil {
		t.Fatalf("Encode configured ID: %v", err)
	}
	if !strings.Contains(out.String(), `id = "api"`) {
		t.Fatalf("encoded configured session = %s, want id", out.String())
	}
}

func TestLoad_ProjectID(t *testing.T) {
	path := writeTempConfig(t, `
[[profiles]]
name = "work"
harness = "claude"
home_dir = "/tmp/work"
color = "blue"

[harness_emojis]
claude = "🦊"

[[projects]]
id = "agent-mux"
title = "Agent Mux"
directory = "/tmp/agentmux"
profile = "work"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Projects) != 1 || cfg.Projects[0].ID != "agent-mux" {
		t.Fatalf("loaded project IDs = %#v, want agent-mux", cfg.Projects)
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("Validate loaded project ID: %v", err)
	}
}
