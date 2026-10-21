package cmd

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
)

// Every flag that names a runtime session or a backend completes, wherever it
// is registered.
func TestSharedFlagsCompleteEverywhere(t *testing.T) {
	var walk func(*cobra.Command)
	seen := map[string]int{}
	walk = func(command *cobra.Command) {
		for _, name := range []string{cli.RuntimeSessionFlag, cli.BackendFlag} {
			if command.NonInheritedFlags().Lookup(name) == nil {
				continue
			}
			seen[name]++
			if _, ok := command.GetFlagCompletionFunc(name); !ok {
				t.Errorf("%s --%s has no completion", command.CommandPath(), name)
			}
		}
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(rootCmd)
	// The root's own --backend (the dashboard's) is checked directly: a
	// repeated in-process __complete of a root flag answers nothing, which
	// a real shell, one process per <Tab>, never meets.
	if complete, ok := rootCmd.GetFlagCompletionFunc(cli.BackendFlag); !ok {
		t.Error("amx --backend has no completion")
	} else if got, _ := complete(rootCmd, nil, ""); len(got) != 2 {
		t.Errorf("amx --backend completes %q", got)
	}
	if seen[cli.RuntimeSessionFlag] < 5 || seen[cli.BackendFlag] < 5 {
		t.Fatalf("found too few shared flags: %v", seen)
	}
}

func TestStaticAndRuntimeSessionCompletions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AMX_CONFIG", writeCompletionConfig(t))
	runtimeDirective := cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
	for _, tc := range []struct {
		args      []string
		directive cobra.ShellCompDirective
		contains  []string
	}{
		{[]string{"attach", ""}, runtimeDirective, nil},
		{[]string{"close", ""}, runtimeDirective, nil},
		{[]string{"send", ""}, runtimeDirective, nil},
		{[]string{"name-session", "--runtime-session", ""}, runtimeDirective, nil},
		{[]string{"debug", "probe", "--runtime-session", ""}, runtimeDirective, nil},
		{[]string{"send", "rs-x", "--key", ""}, cobra.ShellCompDirectiveNoFileComp, []string{"enter", "escape"}},
		{[]string{"overseer", "policy", "--access", ""}, cobra.ShellCompDirectiveNoFileComp, nil},
		{[]string{"overview", "--backend", ""}, cobra.ShellCompDirectiveNoFileComp, []string{"kitty", "tmux"}},
		{[]string{"worktree", "shell", "--backend", ""}, cobra.ShellCompDirectiveNoFileComp, []string{"kitty", "tmux"}},
		{[]string{"plans", "run", "--project", ""}, cobra.ShellCompDirectiveNoFileComp, nil},
		{[]string{"debug", "tail", "--stage", "hook."}, cobra.ShellCompDirectiveNoFileComp, []string{"hook.delivery"}},
		{[]string{"debug", "tail", "--outcome", ""}, cobra.ShellCompDirectiveNoFileComp, []string{"accepted"}},
		{[]string{"daemon", "export-events", "--schema", ""}, cobra.ShellCompDirectiveNoFileComp, []string{"13", "8"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stdout, _, err := runRootArgs(t, append([]string{cobra.ShellCompNoDescRequestCmd}, tc.args...)...)
			if err != nil {
				t.Fatalf("completion: %v", err)
			}
			lines := strings.Split(strings.TrimSpace(stdout), "\n")
			if want := fmt.Sprintf(":%d", tc.directive); lines[len(lines)-1] != want {
				t.Fatalf("directive = %s, want %s\n%s", lines[len(lines)-1], want, stdout)
			}
			for _, want := range tc.contains {
				if !slices.Contains(lines, want) {
					t.Errorf("candidates %q lack %q", lines, want)
				}
			}
		})
	}
	stdout, _, _ := runRootArgs(t, cobra.ShellCompNoDescRequestCmd, "overseer", "policy", "--access", "")
	if len(strings.Split(strings.TrimSpace(stdout), "\n")) < 2 {
		t.Fatalf("overseer policy --access offers nothing:\n%s", stdout)
	}
}
