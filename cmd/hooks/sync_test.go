package hooks

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/hooks"
)

func TestConfigHooksStatusCommandWritesToCommandOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	home := filepath.Join(t.TempDir(), "missing-home")
	if err := os.WriteFile(path, []byte(validConfigBody(home)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)
	asTerminal(t, true)

	oldOut := configHooksStatusCmd.OutOrStdout()
	oldErr := configHooksStatusCmd.ErrOrStderr()
	t.Cleanup(func() {
		configHooksStatusCmd.SetOut(oldOut)
		configHooksStatusCmd.SetErr(oldErr)
	})

	var out, stderr bytes.Buffer
	configHooksStatusCmd.SetOut(&out)
	configHooksStatusCmd.SetErr(&stderr)

	if err := configHooksStatusCmd.RunE(configHooksStatusCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "ACCOUNT") {
		t.Fatalf("hooks-status still heads the column with the deprecated ACCOUNT:\n%s", got)
	}
	for _, want := range []string{
		"PROFILE",
		"work",
		"missing",
		"hint: run `amx config hooks-sync`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("hooks-status output missing %q:\n%s", want, got)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q; want empty", stderr.String())
	}
}

func TestConfigHooksStatusCommandWritesErrorsToCommandErrorOutput(t *testing.T) {
	tempHome := t.TempDir()
	badHomeParent := filepath.Join(tempHome, "bad-as-file")
	if err := os.WriteFile(badHomeParent, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write badHomeParent: %v", err)
	}

	path := filepath.Join(tempHome, "config.toml")
	if err := os.WriteFile(path, []byte(validConfigBody(filepath.Join(badHomeParent, "nested"))), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)

	oldOut := configHooksStatusCmd.OutOrStdout()
	oldErr := configHooksStatusCmd.ErrOrStderr()
	t.Cleanup(func() {
		configHooksStatusCmd.SetOut(oldOut)
		configHooksStatusCmd.SetErr(oldErr)
	})

	var out, stderr bytes.Buffer
	configHooksStatusCmd.SetOut(&out)
	configHooksStatusCmd.SetErr(&stderr)

	err := configHooksStatusCmd.RunE(configHooksStatusCmd, nil)
	if err == nil {
		t.Fatal("RunE error = nil; want status error")
	}
	if got := out.String(); !strings.Contains(got, "error") || !strings.Contains(got, "work") {
		t.Fatalf("stdout should include account error row, got:\n%s", got)
	}
	if got := stderr.String(); !strings.Contains(got, "error:") || !strings.Contains(got, "work") {
		t.Fatalf("stderr should include status error, got:\n%s", got)
	}
}

// Off a terminal hooks-status prints what project, session, and worktree list
// print for a pipe: headerless tab-separated rows, so `cut -f3` yields the
// sync state. The hint goes to stderr, where no script reads it as a row.
func TestConfigHooksStatusCommandPrintsTabSeparatedRowsForAPipe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	home := filepath.Join(t.TempDir(), "missing-home")
	if err := os.WriteFile(path, []byte(validConfigBody(home)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)
	asTerminal(t, false)

	oldOut := configHooksStatusCmd.OutOrStdout()
	oldErr := configHooksStatusCmd.ErrOrStderr()
	t.Cleanup(func() {
		configHooksStatusCmd.SetOut(oldOut)
		configHooksStatusCmd.SetErr(oldErr)
	})
	var out, stderr bytes.Buffer
	configHooksStatusCmd.SetOut(&out)
	configHooksStatusCmd.SetErr(&stderr)

	if err := configHooksStatusCmd.RunE(configHooksStatusCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("piped hooks-status = %q, want exactly one row and no header", out.String())
	}
	fields := strings.Split(lines[0], "\t")
	if len(fields) != 5 || fields[0] != "work" || fields[1] != "claude" || fields[2] != "missing" {
		t.Fatalf("piped row fields = %q, want work, claude, missing, activation, detail", fields)
	}
	if !strings.Contains(stderr.String(), "hint: run `amx config hooks-sync`") {
		t.Fatalf("stderr = %q, want the hint", stderr.String())
	}
}

func asTerminal(t *testing.T, terminal bool) {
	t.Helper()
	old := hooksStatusIsTerminal
	t.Cleanup(func() { hooksStatusIsTerminal = old })
	hooksStatusIsTerminal = func(io.Writer) bool { return terminal }
}

func validConfigBody(homeDir string) string {
	return `
[harness_emojis]
claude = "🦊"

[[profiles]]
name = "work"
harness = "claude"
home_dir = "` + homeDir + `"
color = "blue"
`
}

// Hook files embed the binary that ran hooks-sync, and the hook indicator fails
// open, so a `go run` build — deleted when the command exits — would silently
// break every hook. The sync is refused before any file is written.
func TestConfigHooksSyncRefusesAGoRunBuild(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "claude-home")
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(validConfigBody(home)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)
	oldBinary := hooksBinary
	t.Cleanup(func() { hooksBinary = oldBinary })
	hooksBinary = func() (string, error) { return "/tmp/go-build1/b001/exe/agentmux", nil }

	err := configHooksSyncCmd.RunE(configHooksSyncCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to write hook commands") {
		t.Fatalf("RunE error = %v; want a go run refusal", err)
	}
	if _, statErr := os.Stat(home); !os.IsNotExist(statErr) {
		t.Fatalf("hook home %s was touched (stat err %v); want nothing written", home, statErr)
	}
}

// Each hint names the remedy for a state a row is in: a table whose files are
// all in sync is not told to run hooks-sync, which would change nothing.
func TestHooksStatusHintsNameOnlyTheRemediesTheRowsNeed(t *testing.T) {
	healthy := debugger.HookProfile{Sync: hooks.SyncInSync, Activation: hooks.ActivationHealthy, Verdict: debugger.VerdictHealthy}
	unused := debugger.HookProfile{Sync: hooks.SyncInSync, Activation: hooks.ActivationUnverified, Unused: true, Verdict: debugger.VerdictHealthy}
	unverified := debugger.HookProfile{Sync: hooks.SyncInSync, Activation: hooks.ActivationUnverified, Verdict: debugger.VerdictWarning}
	inactive := debugger.HookProfile{Sync: hooks.SyncInSync, Activation: hooks.ActivationFailure, Verdict: debugger.VerdictFailure}
	missing := debugger.HookProfile{Sync: hooks.SyncMissing, Activation: hooks.ActivationFailure, Verdict: debugger.VerdictFailure}
	broken := debugger.HookProfile{CheckError: "unreadable", Verdict: debugger.VerdictFailure}

	for _, tc := range []struct {
		name     string
		profiles []debugger.HookProfile
		want     []string
	}{
		{"all healthy", []debugger.HookProfile{healthy, unused}, nil},
		{"check error only", []debugger.HookProfile{broken}, nil},
		{"unverified only", []debugger.HookProfile{healthy, unverified}, []string{"a session started with that profile"}},
		{"inactive", []debugger.HookProfile{inactive}, []string{"switched off"}},
		{"missing and unverified", []debugger.HookProfile{missing, unverified}, []string{"amx config hooks-sync", "a session started with that profile"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := hooksStatusHints(tc.profiles)
			if len(got) != len(tc.want) {
				t.Fatalf("hints = %q, want %d matching %q", got, len(tc.want), tc.want)
			}
			for i, want := range tc.want {
				if !strings.Contains(got[i], want) {
					t.Fatalf("hint %d = %q, want it to contain %q", i, got[i], want)
				}
			}
		})
	}
}

// A build that is not the amx on PATH — a ./amx built in a checkout — would
// point every hook at a file the next build replaces, so hooks-sync refuses
// it unless --this-binary says that is the intent.
func TestConfigHooksSyncRefusesABuildOtherThanTheInstalledAmx(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "claude-home")
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(validConfigBody(home)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)
	installed := filepath.Join(dir, "bin", "amx")
	dev := filepath.Join(dir, "checkout", "amx")
	oldBinary, oldLookPath, oldThis := hooksBinary, hooksLookPath, hooksSyncThisBinary
	t.Cleanup(func() { hooksBinary, hooksLookPath, hooksSyncThisBinary = oldBinary, oldLookPath, oldThis })
	hooksBinary = func() (string, error) { return dev, nil }
	hooksLookPath = func(string) (string, error) { return installed, nil }
	configHooksSyncCmd.SetOut(io.Discard)

	hooksSyncThisBinary = false
	err := configHooksSyncCmd.RunE(configHooksSyncCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "not the amx on PATH") || !strings.Contains(err.Error(), "--this-binary") {
		t.Fatalf("RunE error = %v; want a foreign-build refusal naming --this-binary", err)
	}
	if _, statErr := os.Stat(home); !os.IsNotExist(statErr) {
		t.Fatalf("hook home %s was touched (stat err %v); want nothing written", home, statErr)
	}

	hooksSyncThisBinary = true
	if err := configHooksSyncCmd.RunE(configHooksSyncCmd, nil); err != nil {
		t.Fatalf("RunE with --this-binary: %v", err)
	}
	statuses, err := hooks.StatusAll(mustLoadTestConfig(t), dev)
	if err != nil || len(statuses) != 1 || statuses[0].Status != hooks.SyncInSync {
		t.Fatalf("after --this-binary, status against the dev build = %+v, %v; want in sync", statuses, err)
	}
}

// hooks-status run from a build other than the installed amx judges the hook
// files against the installed one, so files the installed amx wrote read as in
// sync, and it says which binary it judged.
func TestConfigHooksStatusJudgesTheInstalledAmxFromAnotherBuild(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "claude-home")
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(validConfigBody(home)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("amx_CONFIG", path)
	asTerminal(t, true)
	installed := filepath.Join(dir, "bin", "amx")
	dev := filepath.Join(dir, "checkout", "amx")
	if _, err := hooks.MergeAll(mustLoadTestConfig(t), "", installed); err != nil {
		t.Fatalf("install hooks for the installed amx: %v", err)
	}
	oldBinary, oldLookPath := hooksBinary, hooksLookPath
	oldOut, oldErr := configHooksStatusCmd.OutOrStdout(), configHooksStatusCmd.ErrOrStderr()
	t.Cleanup(func() {
		hooksBinary, hooksLookPath = oldBinary, oldLookPath
		configHooksStatusCmd.SetOut(oldOut)
		configHooksStatusCmd.SetErr(oldErr)
	})
	hooksBinary = func() (string, error) { return dev, nil }
	hooksLookPath = func(string) (string, error) { return installed, nil }
	var out bytes.Buffer
	configHooksStatusCmd.SetOut(&out)
	configHooksStatusCmd.SetErr(io.Discard)

	_ = configHooksStatusCmd.RunE(configHooksStatusCmd, nil)
	got := out.String()
	if strings.Contains(got, "drifted") || !strings.Contains(got, "in_sync") {
		t.Fatalf("hooks-status from another build reads the installed amx's hooks as drifted:\n%s", got)
	}
	if !strings.Contains(got, "judged against the installed amx at "+installed) {
		t.Fatalf("hooks-status does not say which amx it judged:\n%s", got)
	}
}

func mustLoadTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := cli.LoadRunnableConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}
