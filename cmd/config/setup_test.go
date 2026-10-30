package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/spf13/cobra"
)

func runSetupWithInput(t *testing.T, input string) (string, error) {
	t.Helper()
	oldLook, oldStatus := setupLookPath, setupBackendStatus
	setupLookPath = func(name string) (string, error) { return "/fixture/bin/" + name, nil }
	setupBackendStatus = func(*config.Config) cli.BackendStatus {
		return cli.BackendStatus{Tmux: cli.TmuxStatus{Available: true, Version: "3.5", Path: "/fixture/tmux"}}
	}
	t.Cleanup(func() { setupLookPath, setupBackendStatus = oldLook, oldStatus })
	oldIn, oldOut := configSetupCmd.InOrStdin(), configSetupCmd.OutOrStdout()
	t.Cleanup(func() { configSetupCmd.SetIn(oldIn); configSetupCmd.SetOut(oldOut) })
	var out bytes.Buffer
	configSetupCmd.SetIn(strings.NewReader(input))
	configSetupCmd.SetOut(&out)
	err := configSetupCmd.RunE(configSetupCmd, nil)
	return out.String(), err
}

func TestSetupSyncsOnlyChosenProfileAfterSavingAndReportsFailure(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fails], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			t.Setenv("amx_CONFIG", path)
			old := setupSyncHooks
			t.Cleanup(func() { setupSyncHooks = old })
			calls := 0
			setupSyncHooks = func(_ *cobra.Command, profile string) error {
				calls++
				cfg, err := config.Load(path)
				if err != nil || len(cfg.Projects) != 1 || profile != "codex-local" {
					t.Fatalf("sync before saving or wrong profile: %s %v", profile, err)
				}
				if fails {
					return errors.New("fixture hook write failed")
				}
				return nil
			}
			out, err := runSetupWithInput(t, "codex\n\n\n"+t.TempDir()+"\n\ntmux\nyes\n")
			if calls != 1 || (err != nil) != fails {
				t.Fatalf("calls=%d, err=%v", calls, err)
			}
			want := "Hooks: synced for this profile"
			if fails {
				want = "Retry: amx config hooks-sync -- codex-local"
			}
			if !strings.Contains(out, want) || !strings.Contains(out, "Dashboard in this terminal: tmux") {
				t.Fatalf("missing outcome: %s", out)
			}
		})
	}
}

func TestSetupUnansweredHookChoiceDoesNotSaveOrSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)
	_, err := runSetupWithInput(t, "codex\n\n\n"+t.TempDir()+"\n\ntmux\n")
	if err == nil || !strings.Contains(err.Error(), "Sync Agent Mux hooks") {
		t.Fatalf("unanswered choice: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("saved before answer: %v", err)
	}
}

func TestSetupSummaryNamesMissingRequirementsAndActualDestinations(t *testing.T) {
	oldLook, oldStatus := setupLookPath, setupBackendStatus
	t.Cleanup(func() { setupLookPath, setupBackendStatus = oldLook, oldStatus })
	setupLookPath = func(string) (string, error) { return "", errors.New("not installed") }
	setupBackendStatus = func(*config.Config) cli.BackendStatus {
		return cli.BackendStatus{Problems: []cli.BackendProblem{{Severity: "warning", Message: "tmux unavailable", Hint: "Install tmux 3.5+."}}}
	}
	for _, kitty := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain terminal", true: "Kitty socket"}[kitty], func(t *testing.T) {
			t.Setenv("KITTY_WINDOW_ID", "")
			t.Setenv("KITTY_LISTEN_ON", "")
			if kitty {
				t.Setenv("KITTY_LISTEN_ON", "unix:/fixture")
			}
			cfg := &config.Config{Profiles: []config.Account{{Name: "work", Harness: "codex", HomeDir: "/isolated/codex"}}}
			if kitty {
				cfg.Defaults.LaunchBackend = "  "
			}
			var out bytes.Buffer
			if err := setupSummary(&out, cfg, "work", false, nil); err != nil {
				t.Fatal(err)
			}
			destination := "tmux"
			if !strings.Contains(out.String(), "Dashboard launch blocked") {
				t.Fatalf("missing tmux must block launches in every terminal: %s", out.String())
			}
			for _, want := range []string{"CLI: tmux (default", "Dashboard in this terminal: " + destination,
				"Agent CLI missing", "Install tmux 3.5+", "Authentication: not checked", "/isolated/codex", "Hooks: not synced"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %q in %s", want, out.String())
				}
			}
		})
	}
}

func TestSetupCreatesFirstProfileAndProjectWithoutLosingStarter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("amx_CONFIG", path)
	project := t.TempDir()
	out, err := runSetupWithInput(t, "codex\n\n\n"+project+"\nMy project\n\nno\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Next: amx config hooks-sync -- codex-local") ||
		!strings.Contains(out, "CLI: tmux (default") || !strings.Contains(out, "Launch checks") {
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
	if cfg.Defaults.LaunchBackend != "" || cfg.LaunchBackendOrDefault() != config.LaunchBackendTmux {
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
	if _, err := runSetupWithInput(t, project+"\n\n\nno\n"); err != nil {
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
	out, err := runSetupWithInput(t, "\n\n\n"+missing+"\n"+project+"\n\n\nno\n")
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
	out, err := runSetupWithInput(t, "unknown\nSECOND\n"+project+"\n\n\nno\n")
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
	if _, err := runSetupWithInput(t, "\n\n\n"+project+"\n\n\nno\n"); err != nil {
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
	if _, err := runSetupWithInput(t, "codex\n\n\n"+project+"\n\ntmux\nno\n"); err != nil {
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
	if _, err := runSetupWithInput(t, "\n\n\n"+project+"\n\nkitty\nno\n"); err != nil {
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
	if _, err := runSetupWithInput(t, "\n\n\n"+project+"\n\ntmux\nno\n"); err != nil {
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
	out, err := runSetupWithInput(t, "wrong\nCODEX\n\n\n"+project+"\n\ninvalid\nTMUX\nno\n")
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

// Keep leaves the configuration unpinned and selects tmux regardless of the
// terminal or whether Kitty is installed. Explicit choices remain available.
func TestSetupKeepUsesTmuxInEveryTerminal(t *testing.T) {
	for _, tc := range []struct {
		name, windowID, answer string
		noKitten               bool
		pinned                 string
	}{
		{name: "plain terminal, keep", answer: ""},
		{name: "no Kitty installed", windowID: "5", answer: "", noKitten: true},
		{name: "inside Kitty", windowID: "5", answer: ""},
		{name: "plain terminal, tmux chosen", answer: "tmux", pinned: config.LaunchBackendTmux},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			t.Setenv("amx_CONFIG", path)
			t.Setenv("KITTY_WINDOW_ID", tc.windowID)
			t.Setenv("KITTY_LISTEN_ON", "")
			input := "codex\n\n\n" + t.TempDir() + "\n\n" + tc.answer + "\nno\n"
			var out string
			var err error
			if tc.noKitten {
				out, err = runSetupWithLookPath(t, input, func(name string) (string, error) {
					if name == "kitten" {
						return "", errors.New("not installed")
					}
					return "/fixture/bin/" + name, nil
				})
			} else {
				out, err = runSetupWithInput(t, input)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"Keep uses the tmux default in every terminal", "CLI: tmux", "Dashboard in this terminal: tmux"} {
				if !strings.Contains(out, want) {
					t.Fatalf("missing %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "Kitty client missing") {
				t.Fatalf("default tmux launch requires Kitty: %s", out)
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Defaults.LaunchBackend != tc.pinned {
				t.Fatalf("launch_backend = %q, want %q", cfg.Defaults.LaunchBackend, tc.pinned)
			}
		})
	}
}

// runSetupWithLookPath is runSetupWithInput with the PATH lookup replaced
// after its own stub is installed.
func runSetupWithLookPath(t *testing.T, input string, look func(string) (string, error)) (string, error) {
	t.Helper()
	oldStatus := setupBackendStatus
	setupBackendStatus = func(*config.Config) cli.BackendStatus {
		return cli.BackendStatus{Tmux: cli.TmuxStatus{Available: true, Version: "3.5", Path: "/fixture/tmux"}}
	}
	oldLook := setupLookPath
	setupLookPath = look
	t.Cleanup(func() { setupLookPath, setupBackendStatus = oldLook, oldStatus })
	oldIn, oldOut := configSetupCmd.InOrStdin(), configSetupCmd.OutOrStdout()
	t.Cleanup(func() { configSetupCmd.SetIn(oldIn); configSetupCmd.SetOut(oldOut) })
	var out bytes.Buffer
	configSetupCmd.SetIn(strings.NewReader(input))
	configSetupCmd.SetOut(&out)
	err := configSetupCmd.RunE(configSetupCmd, nil)
	return out.String(), err
}
