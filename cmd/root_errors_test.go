package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

// executeRootArgs drives execute, the path the real binary takes, with a
// temporary leaf registered under root so the stderr contract can be checked
// without any configuration, daemon, or Kitty.
func executeRootArgs(t *testing.T, leaf *cobra.Command, args ...string) (string, string, int) {
	t.Helper()
	if leaf != nil {
		rootCmd.AddCommand(leaf)
		t.Cleanup(func() { rootCmd.RemoveCommand(leaf) })
	}
	var stdout, stderr bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	code := execute(&stderr)
	return stdout.String(), stderr.String(), code
}

func TestJSONFailureWritesNothingToStderr(t *testing.T) {
	leaf := &cobra.Command{
		Use: "json-failure-probe",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cli.WriteJSONError(cmd, cli.JSONError{Code: "not_found", Message: "probe was not found"})
		},
	}
	stdout, stderr, code := executeRootArgs(t, leaf, "json-failure-probe")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stdout, `"code":"not_found"`) {
		t.Fatalf("stdout lacks the error envelope:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("a JSON failure must leave stderr empty, got %q", stderr)
	}
}

func TestFailurePrintsItsErrorExactlyOnce(t *testing.T) {
	for name, failure := range map[string]error{
		"plain":      errors.New("probe failed"),
		"exit error": &cli.ExitError{Code: 1, Err: errors.New("probe failed")},
	} {
		t.Run(name, func(t *testing.T) {
			leaf := &cobra.Command{
				Use:  "text-failure-probe",
				RunE: func(*cobra.Command, []string) error { return failure },
			}
			_, stderr, code := executeRootArgs(t, leaf, "text-failure-probe")
			if code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			if got := strings.Count(stderr, "probe failed"); got != 1 {
				t.Fatalf("stderr names the failure %d times, want once:\n%s", got, stderr)
			}
		})
	}
}

func TestUnknownCommandStillPointsAtHelp(t *testing.T) {
	_, stderr, code := executeRootArgs(t, nil, "no-such-command")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	for _, want := range []string{`Error: unknown command "no-such-command" for "amx"`, "Run 'amx --help' for usage."} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}
