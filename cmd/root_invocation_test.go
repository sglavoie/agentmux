package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

// commandGroups lists every visible command with subcommands under root.
func commandGroups() []*cobra.Command {
	var groups []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		for _, child := range command.Commands() {
			if !child.HasSubCommands() || child.Name() == "help" || child.Name() == cobra.ShellCompRequestCmd {
				continue
			}
			groups = append(groups, child)
			walk(child)
		}
	}
	walk(rootCmd)
	return groups
}

func decodeErrorEnvelope(t *testing.T, stdout string) cli.JSONError {
	t.Helper()
	var envelope cli.JSONErrorEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("stdout is not an error envelope: %v\n%s", err, stdout)
	}
	return envelope.Error
}

func TestEveryCommandGroupRejectsAnUnknownCommand(t *testing.T) {
	groups := commandGroups()
	if len(groups) < 8 {
		t.Fatalf("found only %d command groups", len(groups))
	}
	for _, group := range groups {
		path := strings.Fields(group.CommandPath())[1:]
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			_, stderr, code := executeRootArgs(t, nil, append(append([]string{}, path...), "no-such-subcommand")...)
			if code == 0 {
				t.Fatalf("exit = 0, want nonzero; stderr:\n%s", stderr)
			}
			want := `unknown command "no-such-subcommand" for "` + group.CommandPath() + `"`
			if !strings.Contains(stderr, want) {
				t.Fatalf("stderr lacks %q:\n%s", want, stderr)
			}

			stdout, stderr, code := executeRootArgs(t, nil, append(append([]string{}, path...), "no-such-subcommand", "--json")...)
			if code == 0 || stderr != "" {
				t.Fatalf("--json: exit = %d, stderr = %q", code, stderr)
			}
			if got := decodeErrorEnvelope(t, stdout); got.Code != "unknown_command" {
				t.Fatalf("code = %q, want unknown_command", got.Code)
			}
		})
	}
}

func TestBareCommandGroupsPrintTheirHelp(t *testing.T) {
	for _, group := range commandGroups() {
		if group.Name() == "notifications" {
			continue // a bare notifications reports delivery status
		}
		path := strings.Fields(group.CommandPath())[1:]
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			stdout, stderr, code := executeRootArgs(t, nil, path...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0; stderr:\n%s", code, stderr)
			}
			if !strings.Contains(stdout, "Available Commands:") {
				t.Fatalf("bare %s printed no help:\n%s", group.CommandPath(), stdout)
			}
		})
	}
}

func TestUnknownCommandBeforeAFlagIsStillAnUnknownCommand(t *testing.T) {
	_, stderr, code := executeRootArgs(t, nil, "session", "lst", "--limit", "5")
	if code != 1 || !strings.Contains(stderr, `unknown command "lst" for "amx session"`) || !strings.Contains(stderr, "\tlist\n") {
		t.Fatalf("exit = %d, stderr:\n%s", code, stderr)
	}
	stdout, _, _ := executeRootArgs(t, nil, "session", "lst", "--json")
	got := decodeErrorEnvelope(t, stdout)
	if got.Code != "unknown_command" || len(got.Candidates) == 0 || got.Candidates[0].ID != "amx session list" {
		t.Fatalf("envelope = %+v", got)
	}
}

func TestFlagErrorsHonorJSONAndReadPlainly(t *testing.T) {
	stdout, stderr, code := executeRootArgs(t, nil, "session", "list", "--bogus", "--json")
	if code != 1 || stderr != "" {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if got := decodeErrorEnvelope(t, stdout); got.Code != "invalid_arguments" || !strings.Contains(got.Message, "--bogus") {
		t.Fatalf("envelope = %+v", got)
	}

	_, stderr, code = executeRootArgs(t, nil, "session", "show", "--last", "x")
	if code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(stderr, "strconv") || !strings.Contains(stderr, `invalid value "x" for --last: want a whole number`) {
		t.Fatalf("stderr:\n%s", stderr)
	}
}

// config validate --strict prints its failure itself (in red, on stderr) and
// returns it quiet, so the root does not print the same line a second time.
func TestConfigValidateStrictPrintsItsFailureOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "[harness_emojis]\nclaude = \"🦊\"\n\n[[profiles]]\nname = \"work\"\nharness = \"claude\"\nhome_dir = \"" + dir + "\"\ncolor = \"blue\"\n\n" +
		"[[projects]]\ntitle = \"gone\"\ndirectory = \"" + filepath.Join(dir, "gone") + "\"\nprofile = \"work\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMX_CONFIG", path)
	command, _, _ := rootCmd.Find([]string{"config", "validate"})
	t.Cleanup(func() { _ = command.Flags().Set("strict", "false") })
	_, stderr, code := executeRootArgs(t, nil, "config", "validate", "--strict")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if stderr != "" {
		t.Fatalf("the root printed the failure again:\n%s", stderr)
	}
}

func TestVersionCommandMatchesTheVersionFlag(t *testing.T) {
	// A --help or --version an earlier test parsed stays set on rootCmd.
	reset := func() {
		_ = rootCmd.Flags().Set("help", "false")
		_ = rootCmd.Flags().Set("version", "false")
	}
	reset()
	t.Cleanup(reset)
	flagOut, _, code := executeRootArgs(t, nil, "--version")
	if code != 0 {
		t.Fatalf("--version exit = %d", code)
	}
	commandOut, _, code := executeRootArgs(t, nil, "version")
	if code != 0 || commandOut != flagOut {
		t.Fatalf("version = %q (exit %d), --version = %q", commandOut, code, flagOut)
	}
}
