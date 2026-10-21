package cmd

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// visibleCommands walks every command a person can find in help.
func visibleCommands(visit func(*cobra.Command)) {
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		if command.Hidden || command.Name() == "help" || command.Name() == "completion" {
			return
		}
		visit(command)
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(rootCmd)
}

// A description reads in an ordinary terminal: no line of prose runs past
// the help width. Indented lines (lists, commands) are the family's to lay out.
func TestHelpDescriptionsAreWrapped(t *testing.T) {
	visibleCommands(func(command *cobra.Command) {
		for _, line := range strings.Split(command.Long, "\n") {
			if len(line) > helpWidth && !strings.HasPrefix(line, " ") {
				t.Errorf("%s: description line of %d characters:\n%s", command.CommandPath(), len(line), line)
			}
		}
	})
}

// The commands an operator reaches for first say what they do at length and
// show how they are used.
func TestKeyCommandsHaveDescriptionsAndExamples(t *testing.T) {
	for _, path := range [][]string{
		{"project", "launch"}, {"project", "list"}, {"doctor"}, {"plans", "status"}, {"keybindings"},
		{"debug"}, {"debug", "status"}, {"debug", "tail"}, {"debug", "probe"}, {"debug", "explain"},
		{"debug", "capture"}, {"debug", "capture", "start"}, {"debug", "capture", "stop"}, {"debug", "capture", "status"},
		{"debug", "bundle"}, {"debug", "replay"}, {"debug", "subscribe"},
		{"worktree", "diff"}, {"worktree", "land"}, {"worktree", "prune"},
		{"backend"}, {"backend", "status"}, {"backend", "stop"},
	} {
		command, _, err := rootCmd.Find(path)
		if err != nil || command == rootCmd {
			t.Errorf("amx %s not found: %v", strings.Join(path, " "), err)
			continue
		}
		if strings.TrimSpace(command.Long) == "" || strings.TrimSpace(command.Example) == "" {
			t.Errorf("%s lacks a description or an example", command.CommandPath())
		}
	}
	stop, _, _ := rootCmd.Find([]string{"backend", "stop"})
	if !strings.Contains(stop.Example, "amx backend stop --yes") {
		t.Errorf("backend stop's example does not confirm with --yes:\n%s", stop.Example)
	}
}

// --json says the same thing everywhere it means the same thing: the common
// envelope is "emit schema-v1 JSON", and a report with its own schema says so.
func TestJSONFlagsShareOneDescription(t *testing.T) {
	visibleCommands(func(command *cobra.Command) {
		flag := command.LocalFlags().Lookup("json")
		if flag == nil || flag.Hidden {
			return
		}
		if !strings.HasPrefix(flag.Usage, cli.JSONFlagUsage) && !strings.HasSuffix(flag.Usage, "as schema-versioned JSON") && !strings.Contains(flag.Usage, "emit schema-v1 JSON") {
			t.Errorf("%s --json: %q", command.CommandPath(), flag.Usage)
		}
	})
}

// The dashboard's own flags still work on the bare command but are not listed
// in `amx --help`, and either spelling of the Kitty window flag works.
func TestRootHelpLeavesOutTheDashboardFlags(t *testing.T) {
	stdout, _, err := runRootArgs(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"--backend", "--window-id", "--select", "--client", "--popup"} {
		if strings.Contains(stdout, name+" ") {
			t.Errorf("root help lists %s:\n%s", name, stdout)
		}
		if rootCmd.Flags().Lookup(strings.TrimPrefix(name, "--")) == nil {
			t.Errorf("root no longer accepts %s", name)
		}
	}
	for _, path := range [][]string{{"mute-toggle"}, {"open-dashboard"}, {"debug", "tail"}, {"daemon", "export-events"}} {
		command, _, _ := rootCmd.Find(path)
		for _, name := range []string{"window", "window-id"} {
			if command.Flags().Lookup(name) == nil {
				t.Errorf("%s does not accept --%s", command.CommandPath(), name)
			}
		}
	}
	var hidden []string
	visibleCommands(func(command *cobra.Command) {
		command.LocalFlags().VisitAll(func(flag *pflag.Flag) {
			if flag.Name == "window" && flag.Hidden {
				hidden = append(hidden, command.CommandPath())
			}
		})
	})
	if len(hidden) == 0 {
		t.Error("no command carries the hidden --window alias")
	}
}

func TestSessionShowFromPrintsNoDefault(t *testing.T) {
	show, _, _ := rootCmd.Find([]string{"session", "show"})
	if usage := show.Flags().FlagUsages(); strings.Contains(usage, "(default -1)") {
		t.Fatalf("session show help prints a default for --from:\n%s", usage)
	}
}
