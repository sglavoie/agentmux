package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func runSetupWithInput(t *testing.T, input string) (string, error) {
	t.Helper()
	oldIn, oldOut := configSetupCmd.InOrStdin(), configSetupCmd.OutOrStdout()
	t.Cleanup(func() { configSetupCmd.SetIn(oldIn); configSetupCmd.SetOut(oldOut) })
	var out bytes.Buffer
	configSetupCmd.SetIn(strings.NewReader(input))
	configSetupCmd.SetOut(&out)
	err := configSetupCmd.RunE(configSetupCmd, nil)
	return out.String(), err
}

func TestSetupCreatesFirstProfileAndProjectWithoutLosingStarter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)
	project := t.TempDir()
	out, err := runSetupWithInput(t, "codex\n\n\n"+project+"\nMy project\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Next: amx config hooks-sync; amx backend status; amx") ||
		!strings.Contains(out, "If launching on tmux, run amx backend status --check first.") {
		t.Fatalf("next step missing: %q", out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("# Agent Mux starter configuration file.")) ||
		!bytes.Contains(data, []byte("# ── Profiles")) {
		t.Fatal("setup lost starter documentation")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Profiles) != 1 || cfg.Profiles[0].Name != "codex-local" ||
		len(cfg.Projects) != 1 || cfg.Projects[0].Directory != project || cfg.Projects[0].Profile != "codex-local" {
		t.Fatalf("configured profiles/projects = %+v / %+v", cfg.Profiles, cfg.Projects)
	}
	if cfg.Defaults.LaunchBackend != "" || cfg.LaunchBackendOrDefault() != config.LaunchBackendKitty {
		t.Fatalf("new setup pinned a launch destination: %q", cfg.Defaults.LaunchBackend)
	}
}

func TestSetupUsesExistingProfileAndPreservesFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)
	body := "# Keep this comment\n[harness_emojis]\nclaude = \"🦊\"\n[[profiles]]\nname = \"work\"\nharness = \"claude\"\nhome_dir = \"~/.claude\"\ncolor = \"yellow\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if _, err := runSetupWithInput(t, project+"\n\n\n"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), body) || !strings.Contains(string(data), `profile = "work"`) {
		t.Fatalf("existing config was not preserved: %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestSetupRejectsMissingProjectWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)
	_, err := runSetupWithInput(t, "\n\n\n"+filepath.Join(t.TempDir(), "missing")+"\n")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "project directory") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config written after refusal: %v", err)
	}
}

func TestSetupLetsOperatorCorrectProjectDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)
	missing := filepath.Join(t.TempDir(), "missing")
	project := t.TempDir()
	out, err := runSetupWithInput(t, "\n\n\n"+missing+"\n"+project+"\n\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Enter an existing directory") {
		t.Fatalf("setup did not explain the path problem: %s", out)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 1 || cfg.Projects[0].Directory != project {
		t.Fatalf("corrected project directory was not saved: %+v", cfg.Projects)
	}
}

func TestSetupLetsOperatorCorrectExistingProfileName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)
	body := "[harness_emojis]\nclaude = \"🦊\"\ncodex = \"📟\"\n" +
		"[[profiles]]\nname = \"first\"\nharness = \"claude\"\nhome_dir = \"~/.claude\"\ncolor = \"yellow\"\n" +
		"[[profiles]]\nname = \"second\"\nharness = \"codex\"\nhome_dir = \"~/.codex\"\ncolor = \"purple\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	out, err := runSetupWithInput(t, "unknown\nSECOND\n"+project+"\n\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Unknown profile \"unknown\". Choose first, second.") {
		t.Fatalf("setup did not explain available profiles: %s", out)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 1 || cfg.Projects[0].Profile != "second" {
		t.Fatalf("corrected profile was not saved: %+v", cfg.Projects)
	}
}

func TestSetupAddsRequiredMarkerToMinimalConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)
	if err := os.WriteFile(path, []byte("# minimal\n[defaults]\nsort = \"alpha\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if _, err := runSetupWithInput(t, "\n\n\n"+project+"\n\n\n"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[harness_emojis]\nclaude = \"🦊\"") {
		t.Fatalf("missing required harness marker: %s", data)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestSetupUpdatesEmptyBackendAndTablesWithHeaderComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)
	body := "# keep this guidance\n[defaults] # launch destination\nlaunch_backend = \"\"  # set during setup\n" +
		"[harness_emojis] # agent markers\nclaude = \"🦊\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if _, err := runSetupWithInput(t, "codex\n\n\n"+project+"\n\ntmux\n"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"# keep this guidance", "[defaults] # launch destination", "launch_backend = \"tmux\"  # set during setup",
		"[harness_emojis] # agent markers", "codex = \"📟\"",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("setup lost %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "[defaults]") != 1 || strings.Count(got, "launch_backend =") != 1 ||
		strings.Count(got, "[harness_emojis]") != 1 {
		t.Fatalf("setup duplicated a table or key:\n%s", got)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(cfg); err != nil || cfg.Defaults.LaunchBackend != config.LaunchBackendTmux {
		t.Fatalf("setup config invalid or wrong backend: %+v, %v", cfg.Defaults, err)
	}
}

func TestSetupAcceptsExplicitKittyDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)
	project := t.TempDir()
	if _, err := runSetupWithInput(t, "\n\n\n"+project+"\n\nkitty\n"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.LaunchBackendOrDefault(); got != config.LaunchBackendKitty {
		t.Fatalf("launch backend = %q, want kitty", got)
	}
}

func TestSetupAcceptsExplicitTmuxDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)
	project := t.TempDir()
	if _, err := runSetupWithInput(t, "\n\n\n"+project+"\n\ntmux\n"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.LaunchBackendOrDefault(); got != config.LaunchBackendTmux {
		t.Fatalf("launch backend = %q, want tmux", got)
	}
}

func TestSetupAllowsCorrectingAChoice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)
	project := t.TempDir()
	out, err := runSetupWithInput(t, "wrong\nCODEX\n\n\n"+project+"\n\ninvalid\nTMUX\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Choose claude, copilot, codex") || !strings.Contains(out, "Choose keep, tmux, kitty") {
		t.Fatalf("setup did not explain the choices: %s", out)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profiles[0].Harness != "codex" || cfg.Defaults.LaunchBackend != "tmux" {
		t.Fatalf("corrected choices were not saved: %+v", cfg)
	}
}
