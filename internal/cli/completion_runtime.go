package cli

import (
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// completionRegistryPath is the runtime-session registry completion reads,
// swapped by tests.
var completionRegistryPath = runtimesession.DefaultRegistryPath

// RuntimeSessionFlag names the flag every command that acts on one runtime
// session takes.
const RuntimeSessionFlag = "runtime-session"

// RuntimeSessionHint is where a runtime session ID comes from, for the end of
// an error about a missing, malformed, or unknown one.
const RuntimeSessionHint = "a runtime session ID looks like rs- followed by 32 hex digits; <Tab> completes the running ones, and `amx session list --live --json` reports each one as live.runtime_session_id"

// CompleteRuntimeSessions suggests the runtime sessions that are running, the
// most recently launched first, each described by its project and harness. It
// is the completion for every flag and argument that names a runtime session.
// It reads the registry file only: no daemon, no tmux, and nothing written,
// so a <Tab> stays instant, and an unreadable registry offers nothing.
func CompleteRuntimeSessions(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	const directive = cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
	store, err := runtimesession.NewStore(completionRegistryPath())
	if err != nil {
		return nil, directive
	}
	registry, err := store.Load()
	if err != nil {
		return nil, directive
	}
	var live []runtimesession.Record
	for _, record := range registry.List() {
		if record.State == runtimesession.StateLive && strings.HasPrefix(string(record.ID), toComplete) {
			live = append(live, record)
		}
	}
	slices.SortStableFunc(live, func(a, b runtimesession.Record) int { return b.CreatedAt.Compare(a.CreatedAt) })
	suggestions := make([]string, 0, len(live))
	for _, record := range live {
		suggestion := string(record.ID)
		if description := runtimeSessionDescription(record); description != "" {
			suggestion += "\t" + description
		}
		suggestions = append(suggestions, suggestion)
	}
	return suggestions, directive
}

// CompleteRuntimeSessionFirstArg is CompleteRuntimeSessions for a command
// whose first positional argument is a runtime session.
func CompleteRuntimeSessionFirstArg(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return CompleteRuntimeSessions(cmd, args, toComplete)
}

// runtimeSessionDescription is what a shell shows beside a runtime session's
// ID: its project, and the harness once its conversation is known.
func runtimeSessionDescription(record runtimesession.Record) string {
	description := record.Project
	if record.Conversation != nil && record.Conversation.Harness != "" {
		if description == "" {
			return string(record.Conversation.Harness)
		}
		description += " (" + string(record.Conversation.Harness) + ")"
	}
	return description
}

// CompleteBackends offers the launch backends, for every --backend flag.
func CompleteBackends(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return []string{string(runtimesession.BackendKitty), string(runtimesession.BackendTmux)}, cobra.ShellCompDirectiveNoFileComp
}

// CompleteProjects suggests configured, unarchived project titles, each
// described by the profile it launches with. When the command's --profile is
// already given, only that profile's projects are offered. What was typed is
// matched past a title's leading emoji, as config.CompleteProjectTitle reads
// it.
func CompleteProjects(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cfg, err := completionLoadConfig()
	if err != nil || cfg == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	profile := ""
	if cmd != nil && cmd.Flags().Lookup("profile") != nil {
		profile, _ = cmd.Flags().GetString("profile")
	}
	seen := make(map[string]bool)
	var titles []string
	for _, project := range config.ActiveProjects(cfg.Projects) {
		title, ok := config.CompleteProjectTitle(project.Title, toComplete)
		if !ok || seen[title] {
			continue
		}
		if profile != "" && !strings.EqualFold(project.EffectiveProfile(), profile) {
			continue
		}
		seen[title] = true
		titles = append(titles, title+"\t"+project.EffectiveProfile())
	}
	return titles, cobra.ShellCompDirectiveNoFileComp
}

// FixedCompletions offers exactly values, with no file completion.
func FixedCompletions(values ...string) cobra.CompletionFunc {
	return cobra.FixedCompletions(values, cobra.ShellCompDirectiveNoFileComp)
}
