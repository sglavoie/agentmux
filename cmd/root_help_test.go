package cmd

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
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
// the help width in display columns, and no line of a paragraph ends early
// when the next line's first word would have fit on it, which is the short
// orphan a line broken on its own leaves. Indented lines (lists, commands)
// are the family's to lay out.
func TestHelpDescriptionsAreWrapped(t *testing.T) {
	visibleCommands(func(command *cobra.Command) {
		lines := strings.Split(command.Long, "\n")
		for i, line := range lines {
			if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				continue
			}
			columns := runewidth.StringWidth(line)
			if columns > helpWidth {
				t.Errorf("%s: description line of %d columns:\n%s", command.CommandPath(), columns, line)
			}
			if i+1 == len(lines) {
				continue
			}
			next := lines[i+1]
			if strings.TrimSpace(next) == "" || strings.HasPrefix(next, " ") || strings.HasPrefix(next, "\t") {
				continue
			}
			if first := helpWords(next)[0]; columns+1+runewidth.StringWidth(first) <= helpWidth {
				t.Errorf("%s: description line ends early; %q would have fit after:\n%s\n%s", command.CommandPath(), first, line, next)
			}
		}
	})
}

// pflag reads the first `quoted` word of a flag's usage as its value name,
// so a usage quoting a command in backticks shows `--command asl` in help
// instead of `--command string`. Quote with words, not backticks.
func TestFlagUsagesQuoteNoValueName(t *testing.T) {
	visibleCommands(func(command *cobra.Command) {
		command.LocalFlags().VisitAll(func(flag *pflag.Flag) {
			if strings.Contains(flag.Usage, "`") && !flag.Hidden {
				t.Errorf("%s --%s: usage has backticks: %q", command.CommandPath(), flag.Name, flag.Usage)
			}
		})
	})
}

// A paragraph is refilled as a whole, by display width, keeping code spans
// and indented lines intact.
func TestWrapHelpTextRefillsParagraphs(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"orphan", "one two three four five six\nseven", "one two three\nfour five six\nseven"},
		{"short lines join", "one\ntwo\nthree", "one two three"},
		{"paragraphs and indentation stay", "one two\n\n  keep   this\nfour five", "one two\n\n  keep   this\nfour five"},
		{"code span stays whole", "aaaa `amx session list` b", "aaaa\n`amx session list`\nb"},
		{"display width", "🔔🔔🔔🔔🔔 a b", "🔔🔔🔔🔔🔔 a\nb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := wrapHelpText(tc.in, 13); got != tc.want {
				t.Fatalf("wrapHelpText(%q) =\n%s\nwant\n%s", tc.in, got, tc.want)
			}
		})
	}
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
