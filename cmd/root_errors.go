package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Invocation errors are the failures cobra raises before a command runs: a
// name that is not a command, a flag that does not parse, or positional
// arguments a command does not take. They are classified here rather than in
// each family so every command reports them the same way, in text and with
// --json alike.

// unknownCommandError is an argument that names no command under a command
// group. Suggestions are cobra's typo matches at that level.
type unknownCommandError struct {
	group       *cobra.Command
	name        string
	suggestions []string
}

// summary is the error's first line, without the suggestions block.
func (e *unknownCommandError) summary() string {
	return fmt.Sprintf("unknown command %q for %q", e.name, e.group.CommandPath())
}

func (e *unknownCommandError) Error() string {
	message := e.summary()
	if len(e.suggestions) > 0 {
		message += "\n\nDid you mean this?\n\t" + strings.Join(e.suggestions, "\n\t") + "\n"
	}
	return message
}

// invalidArgumentsError is a flag or positional argument a command cannot
// accept. Its message is already written for a person.
type invalidArgumentsError struct{ err error }

func (e *invalidArgumentsError) Error() string { return e.err.Error() }
func (e *invalidArgumentsError) Unwrap() error { return e.err }

func newUnknownCommand(group *cobra.Command, name string) *unknownCommandError {
	if group.SuggestionsMinimumDistance <= 0 {
		group.SuggestionsMinimumDistance = 2
	}
	var suggestions []string
	if !group.DisableSuggestions {
		suggestions = group.SuggestionsFor(name)
	}
	return &unknownCommandError{group: group, name: name, suggestions: suggestions}
}

// guardInvocations walks the command tree once, after every family is
// registered, so that:
//
//   - a command group (a command with subcommands) given an argument that is
//     none of them fails with an unknown-command error instead of printing its
//     help and exiting 0, while the bare group still prints its help;
//   - positional-argument errors are classified, so --json callers get the
//     structured envelope.
//
// A group has no --json flag of its own, so `amx session lst --json` fails
// flag parsing first; flagError recognizes the stray command there.
func guardInvocations(command *cobra.Command) {
	if command.HasSubCommands() {
		if command.Args == nil {
			command.Args = groupArgs
		}
		if !command.Runnable() {
			command.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
		}
	}
	if validate := command.Args; validate != nil {
		command.Args = func(cmd *cobra.Command, args []string) error {
			err := validate(cmd, args)
			if err == nil {
				return nil
			}
			var unknown *unknownCommandError
			if errors.As(err, &unknown) {
				return err
			}
			if cmd.HasSubCommands() && len(args) > 0 && strings.HasPrefix(err.Error(), "unknown command") {
				return newUnknownCommand(cmd, args[0])
			}
			if len(args) > 0 && strings.HasPrefix(err.Error(), "unknown command") {
				// cobra.NoArgs says "unknown command" of a leaf, which has
				// no commands to be unknown among.
				err = fmt.Errorf("%s accepts no arguments, got %q", cmd.CommandPath(), args[0])
			}
			return &invalidArgumentsError{err: err}
		}
	}
	for _, child := range command.Commands() {
		guardInvocations(child)
	}
}

// groupArgs is the Args of a command group that takes no positional argument
// of its own: none is fine (the group runs, usually printing its help), and
// anything else is a command that does not exist.
func groupArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return newUnknownCommand(cmd, args[0])
}

// flagError is rootCmd's FlagErrorFunc, which cobra calls for every command
// whose flags do not parse. A misspelled command under a group followed by a
// flag (`amx session lst --limit 5`) is reported as the unknown command it
// is; anything else becomes a plain invalid-arguments message.
func flagError(cmd *cobra.Command, err error) error {
	if cmd.HasSubCommands() {
		if name := strayArgument(cmd, invocationArgs()); name != "" {
			return newUnknownCommand(cmd, name)
		}
	}
	return &invalidArgumentsError{err: plainFlagError(err)}
}

// plainFlagError rewrites pflag's parse failures in the words a person reads,
// without the strconv detail pflag passes through.
func plainFlagError(err error) error {
	var invalid *pflag.InvalidValueError
	if errors.As(err, &invalid) {
		flag := invalid.GetFlag()
		if want := expectedValue(flag.Value.Type()); want != "" {
			return fmt.Errorf("invalid value %q for --%s: want %s", invalid.GetValue(), flag.Name, want)
		}
		if cause := errors.Unwrap(invalid); cause != nil {
			return fmt.Errorf("invalid value %q for --%s: %w", invalid.GetValue(), flag.Name, cause)
		}
	}
	return err
}

// expectedValue names, for a person, what a flag of a built-in pflag type
// takes. Custom value types write their own errors and return "".
func expectedValue(kind string) string {
	switch kind {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return "a whole number"
	case "float32", "float64":
		return "a number"
	case "bool":
		return "true or false"
	case "duration":
		return "a duration such as 30s, 5m, or 1h"
	case "intSlice", "int64Slice", "uintSlice":
		return "comma-separated whole numbers"
	}
	return ""
}

// strayArgument is the first positional argument after cmd's own path in
// args, which for a command group is a command that was not found. Flags are
// skipped, with the value of any flag cmd knows that takes one.
func strayArgument(cmd *cobra.Command, args []string) string {
	path := strings.Fields(cmd.CommandPath())[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return ""
		}
		if strings.HasPrefix(arg, "-") {
			if takesValue(cmd, arg) {
				i++
			}
			continue
		}
		if len(path) > 0 {
			if arg != path[0] {
				// An alias, or the value of a flag this command does not
				// know: give up rather than guess.
				return ""
			}
			path = path[1:]
			continue
		}
		for _, child := range cmd.Commands() {
			if child.Name() == arg || child.HasAlias(arg) {
				return ""
			}
		}
		return arg
	}
	return ""
}

// takesValue reports whether arg is a flag of cmd, given without "=", whose
// value is therefore the next argument.
func takesValue(cmd *cobra.Command, arg string) bool {
	if strings.Contains(arg, "=") {
		return false
	}
	var flag *pflag.Flag
	if name, ok := strings.CutPrefix(arg, "--"); ok {
		flag = cmd.Flags().Lookup(name)
	} else if len(arg) == 2 {
		flag = cmd.Flags().ShorthandLookup(arg[1:])
	}
	return flag != nil && flag.NoOptDefVal == ""
}

// jsonRequested reports whether the invocation asked for --json. It reads the
// arguments rather than the parsed flag because a flag error stops parsing
// before --json is reached, and a group's own flags may not include it.
func jsonRequested(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--json" || arg == "--json=true" || arg == "--json=1" {
			return true
		}
	}
	return false
}

// invocationFailure classifies an error from rootCmd as an invocation error,
// returning its JSON code; ok is false for any other failure.
func invocationFailure(err error) (code string, ok bool) {
	var unknown *unknownCommandError
	if errors.As(err, &unknown) {
		return "unknown_command", true
	}
	var invalid *invalidArgumentsError
	if errors.As(err, &invalid) {
		return "invalid_arguments", true
	}
	return "", false
}

// unknownCommandCandidates lists an unknown command's suggestions for the JSON
// envelope, each by its full command path.
func unknownCommandCandidates(err error) []cli.Candidate {
	var unknown *unknownCommandError
	if !errors.As(err, &unknown) {
		return nil
	}
	var candidates []cli.Candidate
	for _, name := range unknown.suggestions {
		path := unknown.group.CommandPath() + " " + name
		candidates = append(candidates, cli.Candidate{ID: path, DisplayName: path})
	}
	for _, path := range nestedCommandPaths(unknown.group, unknown.name) {
		candidates = append(candidates, cli.Candidate{ID: path, DisplayName: path})
	}
	return candidates
}
