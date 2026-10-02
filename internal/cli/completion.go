package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
)

// completionLoadConfig is the loader shell completion reads profiles through,
// swapped by tests. Completion runs on every <Tab>, so it loads without
// validating and degrades to no suggestions rather than an error: a shell
// prompt is no place to report a broken config.
var completionLoadConfig = config.LoadDefault

// CompleteProfiles suggests configured profile names. It is the completion
// for every argument or flag that names a launch profile.
func CompleteProfiles(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cfg, err := completionLoadConfig()
	if err != nil || cfg == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, profile := range cfg.Profiles {
		if strings.HasPrefix(profile.Name, toComplete) {
			names = append(names, profile.Name)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// CompleteHarnessFirstArg suggests harness names for the first positional
// argument of a conversation command and nothing after it: the session ID is
// not something completion can list without walking every transcript.
func CompleteHarnessFirstArg(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, name := range harness.Names() {
		if strings.HasPrefix(name, toComplete) {
			names = append(names, name)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// CompleteProfileFirstArg is CompleteProfiles for a command whose only
// positional argument is a profile name.
func CompleteProfileFirstArg(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return CompleteProfiles(cmd, args, toComplete)
}
