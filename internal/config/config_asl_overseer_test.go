package config

import (
	"strings"
	"testing"
)

// The overseer acts on a project only when its operator said so in their own
// config file, so the load path has to keep three answers apart: never asked,
// asked and declined, and consent. A bool would fold the first two together and
// make "we never asked" indistinguishable from "they said no".
func TestProjectASLOverseerTriStateRoundTrips(t *testing.T) {
	cases := []struct {
		name           string
		toml           string
		wantConfigured bool
		wantValue      bool
	}{
		{
			name: "omitted",
			toml: `[[projects]]
title = "api"
directory = "~/projects/api"
profile = "work"
`,
			wantConfigured: false,
			wantValue:      false,
		},
		{
			name: "explicit false",
			toml: `[[projects]]
title = "api"
directory = "~/projects/api"
profile = "work"
asl_overseer = false
`,
			wantConfigured: true,
			wantValue:      false,
		},
		{
			name: "explicit true",
			toml: `[[projects]]
title = "api"
directory = "~/projects/api"
profile = "work"
asl_overseer = true
`,
			wantConfigured: true,
			wantValue:      true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseConfig([]byte(tc.toml))
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Projects) != 1 || len(cfg.Sessions) != 1 {
				t.Fatalf("projects/sessions = %d/%d, want 1/1", len(cfg.Projects), len(cfg.Sessions))
			}
			// Both views must answer identically: a caller reading the
			// deprecated alias must not see a different consent state than one
			// reading the canonical table.
			for view, project := range map[string]Session{
				"projects": cfg.Projects[0],
				"sessions": cfg.Sessions[0],
			} {
				if got := project.ASLOverseerConfigured(); got != tc.wantConfigured {
					t.Errorf("%s: ASLOverseerConfigured() = %v, want %v", view, got, tc.wantConfigured)
				}
				if got := project.ASLOverseerValue(); got != tc.wantValue {
					t.Errorf("%s: ASLOverseerValue() = %v, want %v", view, got, tc.wantValue)
				}
			}
		})
	}
}

// A project opted in through the deprecated [[sessions]] table is still a
// project. normalizeAliases falls back to Sessions when Projects is empty, and
// the opt-in has to survive that copy or a legacy config would silently read as
// "never asked".
func TestProjectASLOverseerSurvivesLegacySessionsTable(t *testing.T) {
	cfg, err := parseConfig([]byte(`[[sessions]]
title = "api"
directory = "~/projects/api"
account = "work"
asl_overseer = true

[[sessions]]
title = "web"
directory = "~/projects/web"
account = "work"
asl_overseer = false
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 2 {
		t.Fatalf("projects = %d, want 2", len(cfg.Projects))
	}
	if !cfg.Projects[0].ASLOverseerValue() {
		t.Error("legacy [[sessions]] opt-in did not reach Projects[0]")
	}
	if cfg.Projects[1].ASLOverseer == nil {
		t.Fatal("legacy [[sessions]] explicit false reached Projects[1] as omitted")
	}
	if cfg.Projects[1].ASLOverseerValue() {
		t.Error("Projects[1] opted in despite asl_overseer = false")
	}
}

// Canonical [[projects]] wins over the deprecated table, and it wins whole: a
// project the operator moved to [[projects]] without the opt-in must not
// inherit consent from a stale [[sessions]] entry of the same name.
func TestProjectASLOverseerCanonicalTableWinsOverLegacy(t *testing.T) {
	cfg, err := parseConfig([]byte(`[[projects]]
title = "api"
directory = "~/projects/api"
profile = "work"

[[sessions]]
title = "api"
directory = "~/projects/api"
account = "work"
asl_overseer = true
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 1 {
		t.Fatalf("projects = %d, want 1", len(cfg.Projects))
	}
	if cfg.Projects[0].ASLOverseer != nil {
		t.Fatalf("canonical project inherited opt-in from the deprecated table: %v", *cfg.Projects[0].ASLOverseer)
	}
	if cfg.Sessions[0].ASLOverseer != nil {
		t.Fatal("mirrored alias reports an opt-in the canonical table never made")
	}
}

// Projects and Sessions are two views of the same data, so a shared pointer
// would let a write through one view flip a consent bit read through the other.
// The alias mirror must be a deep copy.
func TestProjectASLOverseerAliasPointersAreIndependent(t *testing.T) {
	cfg, err := parseConfig([]byte(`[[projects]]
title = "api"
directory = "~/projects/api"
profile = "work"
asl_overseer = false
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Projects[0].ASLOverseer == nil || cfg.Sessions[0].ASLOverseer == nil {
		t.Fatal("explicit false decoded as omitted on one of the two views")
	}
	if cfg.Projects[0].ASLOverseer == cfg.Sessions[0].ASLOverseer {
		t.Fatal("Projects and Sessions share one *bool; a write through either would rewrite the other")
	}

	*cfg.Sessions[0].ASLOverseer = true
	if cfg.Projects[0].ASLOverseerValue() {
		t.Error("mutating the deprecated alias opted the canonical project in")
	}
}

// normalizeProjects rewrites profile/account and args in place. The opt-in must
// pass through it untouched rather than being flattened to a plain bool, which
// would erase the omitted state at load time.
func TestProjectASLOverseerSurvivesNormalization(t *testing.T) {
	in := []Session{
		{Title: "opted-in", Account: "work", ASLOverseer: boolPtr(true)},
		{Title: "declined", Account: "work", ASLOverseer: boolPtr(false)},
		{Title: "unanswered", Account: "work"},
	}
	out := normalizeProjects(in)
	if len(out) != len(in) {
		t.Fatalf("normalizeProjects returned %d projects, want %d", len(out), len(in))
	}
	if !out[0].ASLOverseerValue() {
		t.Error("normalization dropped an explicit opt-in")
	}
	if out[1].ASLOverseer == nil || out[1].ASLOverseerValue() {
		t.Errorf("normalization mangled an explicit decline: %v", out[1].ASLOverseer)
	}
	if out[2].ASLOverseer != nil {
		t.Error("normalization invented an answer for a project that never stated one")
	}
	if out[0].ASLOverseer == in[0].ASLOverseer {
		t.Error("normalizeProjects aliases the caller's *bool instead of cloning it")
	}
}

// A misspelled value must fail loudly at load. Silently coercing "true" or 1
// into consent would grant supervision the operator never expressed in the
// type the schema asks for.
func TestProjectASLOverseerRejectsNonBooleanValues(t *testing.T) {
	for _, value := range []string{`"true"`, `"yes"`, `1`, `0`, `[true]`, `{ enabled = true }`} {
		toml := `[[projects]]
title = "api"
directory = "~/projects/api"
profile = "work"
asl_overseer = ` + value + "\n"
		cfg, err := parseConfig([]byte(toml))
		if err == nil {
			t.Errorf("asl_overseer = %s was accepted (parsed as %v)", value, cfg.Projects[0].ASLOverseer)
			continue
		}
		if !strings.Contains(err.Error(), "asl_overseer") {
			t.Errorf("asl_overseer = %s: error does not name the key: %v", value, err)
		}
	}
}

// The field is optional and adds no validation rule of its own: an existing
// config that never mentions it, and a new one that opts in, must both stay
// valid.
func TestProjectASLOverseerDoesNotAffectValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		optIn *bool
	}{
		{"omitted", nil},
		{"declined", boolPtr(false)},
		{"opted in", boolPtr(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				HarnessEmojis: map[string]string{"claude": "🦊"},
				Profiles: []Account{
					{Name: "work", Harness: "claude", HomeDir: "/tmp/h", Color: "blue"},
				},
				Projects: []Session{
					{Title: "api", Directory: "/tmp/a", Profile: "work", ASLOverseer: tc.optIn},
				},
			}
			if err := Validate(cfg); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

// The opt-in is per project, not per profile or global: two projects under one
// profile must be able to disagree.
func TestProjectASLOverseerIsPerProject(t *testing.T) {
	cfg, err := parseConfig([]byte(`[[projects]]
title = "api"
directory = "~/projects/api"
profile = "work"
asl_overseer = true

[[projects]]
title = "web"
directory = "~/projects/web"
profile = "work"
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 2 {
		t.Fatalf("projects = %d, want 2", len(cfg.Projects))
	}
	if !cfg.Projects[0].ASLOverseerValue() {
		t.Error("api did not opt in")
	}
	if cfg.Projects[1].ASLOverseerConfigured() {
		t.Error("web inherited an answer from its sibling project")
	}
}

func boolPtr(v bool) *bool { return &v }
