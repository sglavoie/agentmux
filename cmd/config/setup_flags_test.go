package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/spf13/cobra"
)

func setupTestEnvironment(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("amx_CONFIG", filepath.Join(home, "config.toml"))
	t.Setenv("PATH", t.TempDir())
	oldStatus := setupBackendStatus
	setupBackendStatus = func(*config.Config) cli.BackendStatus {
		return cli.BackendStatus{Tmux: cli.TmuxStatus{Available: true}}
	}
	t.Cleanup(func() { setupBackendStatus = oldStatus })
	return home
}

func setupCommandOutput(t *testing.T, input string, args ...string) (string, string, error) {
	t.Helper()
	cmd := &cobra.Command{RunE: runConfigSetup}
	addSetupFlags(cmd)
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(input))
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	err := cmd.RunE(cmd, nil)
	return out.String(), stderr.String(), err
}

func setupFixtureFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func setupHomeSnapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data := []byte{}
		if !entry.IsDir() {
			data, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		snapshot[path] = fmt.Sprintf("%s:%d:%s", info.Mode(), info.ModTime().UnixNano(), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestSetupDetectionHomesAndReadOnly(t *testing.T) {
	for _, names := range [][]string{nil, {"codex"}, {"claude", "copilot", "codex"}} {
		t.Run(fmt.Sprint(names), func(t *testing.T) {
			home := setupTestEnvironment(t)
			for _, name := range names {
				if err := os.MkdirAll(harness.DefaultHomeDir(name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			// A CLI without a home is independently reported. Environment overrides
			// must not replace default homes, and malformed config must not block detection.
			bin := filepath.Join(os.Getenv("PATH"), "copilot")
			if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CODEX_HOME", filepath.Join(home, "custom"))
			setupFixtureFile(t, config.DefaultConfigPath(), "invalid TOML [[")
			before := setupHomeSnapshot(t, home)
			out, _, err := setupCommandOutput(t, "", "--detect", "--json", "--sync-hooks")
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				SchemaVersion int            `json:"schema_version"`
				Data          setupDetection `json:"data"`
			}
			if err := json.Unmarshal([]byte(out), &envelope); err != nil {
				t.Fatal(err, out)
			}
			if envelope.SchemaVersion != 1 || len(envelope.Data.Harnesses) != 3 || envelope.Data.RecentProjects == nil || len(envelope.Data.RecentProjects) != 0 {
				t.Fatalf("bad detection: %s", out)
			}
			for _, h := range envelope.Data.Harnesses {
				wantHome := false
				for _, name := range names {
					if name == h.Harness {
						wantHome = true
					}
				}
				if h.HomeExists != wantHome || h.CLIAvailable != (h.Harness == "copilot") || h.Home != filepath.Join(home, "."+h.Harness) {
					t.Fatalf("bad harness: %+v", h)
				}
			}
			text, _, err := setupCommandOutput(t, "", "--detect")
			if err != nil || !strings.Contains(text, "codex: CLI on PATH=false") {
				t.Fatalf("text detection: %s %v", text, err)
			}
			if !reflect.DeepEqual(before, setupHomeSnapshot(t, home)) {
				t.Fatal("detection mutated HOME")
			}
		})
	}
}

func setupTranscriptFixture(t *testing.T, name, id, directory string, when time.Time) {
	t.Helper()
	root := harness.Lookup(name).TranscriptsDir(harness.DefaultHomeDir(name))
	var path, body string
	switch name {
	case "claude":
		path = filepath.Join(root, "fixture", id+".jsonl")
		body = fmt.Sprintf(`{"cwd":%q,"sessionId":%q}`, directory, id)
	case "codex":
		path = filepath.Join(root, "rollout-"+id+".jsonl")
		body = fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q,"id":%q}}`, directory, id)
	case "copilot":
		path = filepath.Join(root, id, "events.jsonl")
		body = fmt.Sprintf(`{"type":"session.start","data":{"sessionId":%q,"context":{"cwd":%q}}}`, id, directory)
	}
	setupFixtureFile(t, path, body+"\n"+`{"type":"response_item","message":"PRIVATE MESSAGE MUST NOT APPEAR"}`+"\n")
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestSetupDetectionRecentDirectories(t *testing.T) {
	home := setupTestEnvironment(t)
	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dirs := []string{}
	for n := 0; n < 10; n++ {
		dir := filepath.Join(home, fmt.Sprintf("project-%02d", n))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
		setupTranscriptFixture(t, harness.Names()[n%3], fmt.Sprint(n), dir, when.Add(time.Duration(n)*time.Hour))
	}
	setupTranscriptFixture(t, "codex", "duplicate", dirs[0], when.Add(20*time.Hour))
	setupTranscriptFixture(t, "claude", "missing", filepath.Join(home, "deleted"), when.Add(30*time.Hour))
	file := filepath.Join(home, "file")
	setupFixtureFile(t, file, "not a directory")
	setupTranscriptFixture(t, "copilot", "file", file, when.Add(40*time.Hour))
	before := setupHomeSnapshot(t, home)
	detection := detectSetup()
	if len(detection.RecentProjects) != 8 {
		t.Fatalf("projects: %+v", detection)
	}
	first := detection.RecentProjects[0]
	if first.Directory != dirs[0] || first.Harness != "codex" || !first.LastUsed.Equal(when.Add(20*time.Hour)) {
		t.Fatalf("first: %+v", first)
	}
	for n, p := range detection.RecentProjects[1:] {
		if p.Directory != dirs[9-n] {
			t.Fatalf("order: %+v", detection.RecentProjects)
		}
	}
	if !reflect.DeepEqual(before, setupHomeSnapshot(t, home)) {
		t.Fatal("discovery wrote data")
	}
}

func TestSetupFlagsMatchWizard(t *testing.T) {
	profile := "# keep\n[harness_emojis]\ncodex = \"📟\"\n[[profiles]]\nname = \"work\"\nharness = \"codex\"\nhome_dir = \"~/.codex\"\ncolor = \"purple\"\n"
	other := "[[profiles]]\nname = \"other\"\nharness = \"codex\"\nhome_dir = \"~/other\"\ncolor = \"purple\"\n"
	for _, body := range []string{"", profile, profile + other} {
		for _, backend := range []string{"keep", "tmux"} {
			t.Run(fmt.Sprint(len(body))+backend, func(t *testing.T) {
				home := setupTestEnvironment(t)
				project := t.TempDir()
				args := []string{"--harness", "codex", "--directory", project, "--title", "My project", "--backend", backend, "--json"}
				input := project + "\nMy project\n" + backend + "\nno\n"
				if strings.Count(body, "[[profiles]]") > 1 {
					input = "work\n" + input
					args = append(args, "--profile-name", "work")
				}
				if body == "" {
					input = "codex\n\n\n" + input
				} else {
					setupFixtureFile(t, config.DefaultConfigPath(), body)
				}
				if _, _, err := setupCommandOutput(t, input); err != nil {
					t.Fatal(err)
				}
				wizard, err := os.ReadFile(config.DefaultConfigPath())
				if err != nil {
					t.Fatal(err)
				}
				scriptedPath := filepath.Join(home, "scripted.toml")
				t.Setenv("amx_CONFIG", scriptedPath)
				if body != "" {
					setupFixtureFile(t, scriptedPath, body)
				}
				out, _, err := setupCommandOutput(t, "", args...)
				if err != nil {
					t.Fatal(err)
				}
				scripted, err := os.ReadFile(scriptedPath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(wizard, scripted) {
					t.Fatalf("wizard/flags differ:\n%s\n%s", wizard, scripted)
				}
				var envelope struct {
					Data setupResult `json:"data"`
				}
				if err := json.Unmarshal([]byte(out), &envelope); err != nil {
					t.Fatal(err, out)
				}
				r := envelope.Data
				if !r.Saved || r.Directory != project || r.Harness != "codex" || r.Home != filepath.Join(home, ".codex") || r.HooksStatus != "not_synced" || r.Backend != "tmux" {
					t.Fatalf("result: %+v", r)
				}
			})
		}
	}
}

func TestSetupDetectionWizardDefaultsAndNumber(t *testing.T) {
	setupTestEnvironment(t)
	project := t.TempDir()
	setupTranscriptFixture(t, "codex", "recent", project, time.Now())
	out, _, err := setupCommandOutput(t, "\n\n\n1\n\n\nno\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[codex]") || !strings.Contains(out, "1. "+project) {
		t.Fatalf("missing detected choices: %s", out)
	}
	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Projects[0].Directory != project || cfg.Profiles[0].Harness != "codex" {
		t.Fatalf("wrong defaults: %+v", cfg)
	}
}

func TestSetupJSONHookPartialOutcome(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(fmt.Sprint(fails), func(t *testing.T) {
			setupTestEnvironment(t)
			old := setupSyncHooks
			t.Cleanup(func() { setupSyncHooks = old })
			calls := 0
			setupSyncHooks = func(cmd *cobra.Command, profile string) error {
				calls++
				cfg, err := config.Load(config.DefaultConfigPath())
				if err != nil || len(cfg.Projects) != 1 || profile != "named" {
					t.Fatalf("sync before saving: %v", err)
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), "hook progress"); err != nil {
					return err
				}
				if fails {
					return errors.New("fixture failure")
				}
				return nil
			}
			out, stderr, err := setupCommandOutput(t, "", "--harness", "claude", "--directory", t.TempDir(), "--home", "~/custom", "--profile-name", "named", "--sync-hooks", "--json")
			if (err != nil) != fails || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			var envelope struct {
				Data setupResult `json:"data"`
			}
			if err := json.Unmarshal([]byte(out), &envelope); err != nil {
				t.Fatal(err, out)
			}
			r := envelope.Data
			if !r.Saved || r.Profile != "named" || (r.HooksStatus == "failed") != fails || !strings.Contains(stderr, "hook progress") {
				t.Fatalf("bad outcome: %s / %s", out, stderr)
			}
			if fails && r.HooksError != "fixture failure" {
				t.Fatal(out)
			}
		})
	}
}

func TestSetupFlagRefusalsDoNotWrite(t *testing.T) {
	profile := "[harness_emojis]\nclaude = \"🦊\"\ncodex = \"📟\"\n[[profiles]]\nname = \"work\"\nharness = \"codex\"\nhome_dir = \"~/.codex\"\ncolor = \"purple\"\n"
	for _, tc := range []struct {
		name, body, want string
		args             []string
	}{
		{name: "bad harness", args: []string{"--harness", "unknown"}, want: "--harness"},
		{name: "empty harness", args: []string{"--harness", ""}, want: "nonempty"},
		{name: "bad backend", args: []string{"--backend", "unknown"}, want: "--backend"},
		{name: "missing directory", args: []string{"--directory", "~/missing"}, want: "project directory"},
		{name: "control character", args: []string{"--title", "bad\ntitle"}, want: "control characters"},
		{name: "invalid config", body: "[[invalid", want: "existing config is invalid"},
		{name: "existing projects", body: profile + "[[projects]]\ntitle = \"existing\"\ndirectory = \"/existing\"\nprofile = \"work\"\n", want: "projects already exist"},
		{name: "legacy sessions", body: profile + "[[sessions]]\ntitle = \"existing\"\ndirectory = \"/existing\"\nprofile = \"work\"\n", want: "projects already exist"},
		{name: "wrong harness", body: profile, args: []string{"--harness", "claude"}, want: "no existing profile"},
		{name: "wrong named harness", body: profile, args: []string{"--harness", "claude", "--profile-name", "work"}, want: "does not match --harness"},
		{name: "wrong home", body: profile, args: []string{"--home", "~/different"}, want: "does not match --home"},
		{name: "unknown profile", body: profile, args: []string{"--profile-name", "unknown"}, want: "unknown profile"},
		{name: "ambiguous profile", body: profile + "[[profiles]]\nname = \"other\"\nharness = \"codex\"\nhome_dir = \"~/other\"\ncolor = \"purple\"\n", want: "several profiles"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := setupTestEnvironment(t)
			if tc.body != "" {
				setupFixtureFile(t, config.DefaultConfigPath(), tc.body)
			}
			old := setupSyncHooks
			t.Cleanup(func() { setupSyncHooks = old })
			setupSyncHooks = func(*cobra.Command, string) error { t.Fatal("synced after refusal"); return nil }
			before := setupHomeSnapshot(t, home)
			args := append([]string{"--harness", "codex", "--directory", t.TempDir(), "--json", "--sync-hooks"}, tc.args...)
			out, _, err := setupCommandOutput(t, "", args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q: %v %s", tc.want, err, out)
			}
			var envelope cli.JSONErrorEnvelope
			if err := json.Unmarshal([]byte(out), &envelope); err != nil || envelope.Error.Code != "setup_failed" {
				t.Fatalf("error envelope: %s %v", out, err)
			}
			if !reflect.DeepEqual(before, setupHomeSnapshot(t, home)) {
				t.Fatal("refusal wrote data")
			}
		})
	}
}

func TestSetupExplicitExistingProfileAndPartialFlags(t *testing.T) {
	setupTestEnvironment(t)
	body := "# retain both profiles\n[harness_emojis]\ncodex = \"📟\"\n[[profiles]]\nname = \"work\"\nharness = \"codex\"\nhome_dir = \"~/.codex\"\ncolor = \"purple\"\n[[profiles]]\nname = \"other\"\nharness = \"codex\"\nhome_dir = \"~/other\"\ncolor = \"purple\"\n"
	setupFixtureFile(t, config.DefaultConfigPath(), body)
	project := t.TempDir()
	// Missing --directory keeps setup interactive; explicit flags answer the rest.
	out, _, err := setupCommandOutput(t, project+"\n", "--harness", "codex", "--profile-name", "OTHER", "--home", "~/other", "--title", "Chosen", "--backend", "tmux", "--sync-hooks=false")
	if err != nil || !strings.Contains(out, "Project directory [") {
		t.Fatalf("partial flags: %s %v", out, err)
	}
	data, err := os.ReadFile(config.DefaultConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), body) {
		t.Fatal("existing profile configuration changed")
	}
	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Projects[0].Profile != "other" || cfg.Projects[0].Title != "Chosen" {
		t.Fatalf("wrong selection: %+v", cfg.Projects)
	}
}

func TestSetupDetectionDefaultHarness(t *testing.T) {
	for _, tc := range []struct {
		names []string
		want  string
	}{{nil, "claude"}, {[]string{"copilot"}, "copilot"}, {[]string{"codex"}, "codex"}, {[]string{"codex", "copilot"}, "claude"}} {
		t.Run(fmt.Sprint(tc.names), func(t *testing.T) {
			setupTestEnvironment(t)
			for _, name := range tc.names {
				if err := os.MkdirAll(harness.DefaultHomeDir(name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if got := detectSetup().defaultHarness(); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

// The embedded workspace invokes RunE with its own command, which has none of
// config setup's flags. Preserve that entry point and its interactive prompts.
func TestSetupEmbeddedCommandWithoutFlags(t *testing.T) {
	setupTestEnvironment(t)
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader("codex\n\n\n" + t.TempDir() + "\n\n\nno\n"))
	if err := SetupCommand().RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Agent CLI (") || !strings.Contains(out.String(), "Configured") {
		t.Fatal(out.String())
	}
}

type setupMutatingReader struct {
	*strings.Reader
	mutate func()
}

func (r *setupMutatingReader) Read(p []byte) (int, error) {
	if r.mutate != nil {
		r.mutate()
		r.mutate = nil
	}
	return r.Reader.Read(p)
}

func TestSetupRefusesConcurrentConfigChanges(t *testing.T) {
	for _, change := range []string{"created", "modified", "removed"} {
		t.Run(change, func(t *testing.T) {
			setupTestEnvironment(t)
			path := config.DefaultConfigPath()
			if change != "created" {
				setupFixtureFile(t, path, config.ExampleConfig)
			}
			cmd := &cobra.Command{}
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetIn(&setupMutatingReader{Reader: strings.NewReader("codex\n\n\n" + t.TempDir() + "\n\n\nno\n"), mutate: func() {
				switch change {
				case "created":
					setupFixtureFile(t, path, config.ExampleConfig)
				case "modified":
					setupFixtureFile(t, path, "# concurrent edit\n"+config.ExampleConfig)
				case "removed":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
			}})
			if err := runConfigSetup(cmd, nil); err == nil || !strings.Contains(err.Error(), "config changed during setup") {
				t.Fatalf("concurrent %s: %v", change, err)
			}
			data, err := os.ReadFile(path)
			if change == "removed" {
				if !os.IsNotExist(err) {
					t.Fatal("deleted config recreated")
				}
				return
			}
			if err != nil || bytes.Contains(data, []byte("First profile and project added")) {
				t.Fatalf("overwrote concurrent config: %v", err)
			}
		})
	}
}
