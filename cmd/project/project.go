// Package project holds the `amx project` command family: the command surface
// over configured projects, the launch templates `[[projects]]` declares.
//
// `list` reports each configured project as {profile, title, harness,
// directory, groups}, plus a directory_problem when its directory is unusable.
// --missing keeps only those, by the same rule `config validate` applies;
// --profile, --harness, and --group narrow the list, refusing a name the
// configuration does not know. `launch` starts a new session from one project
// through actions.Launch in actions.DefaultRegistry(), the same definition the
// dashboard's launcher runs, so the two cannot disagree about what launching a
// project means.
//
// A project is identified by its profile and title. An exact title wins;
// failing one, a title matching exactly one project once the leading emoji or
// symbols are set aside and case is ignored selects it, which is how an
// operator types it (so `amx` reaches `🐱amx`). Selection never guesses: a
// title shared by several projects without a --profile to tell them apart,
// and a selector that matches nothing, are both refused with the configured
// projects it could have meant as ordered profile/title candidates. Such a
// candidate passed back as the title launches that project, once the whole
// string has failed to match as a title itself. A launch reports the new
// tab's identity only when exactly one new tab carrying the title appeared.
//
// Neither leaf rewrites config.toml.
package project

import (
	"context"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/spf13/cobra"
)

// The seams tests replace for configuration and project actions.
var (
	projectLoadConfig = func() (*config.Config, error) { return cli.LoadRunnableConfig() }
	projectExecute    = func(ctx context.Context, deps actions.Dependencies, request actions.Request) (actions.Result, error) {
		return actions.DefaultRegistry().Execute(ctx, deps, actions.Launch, request)
	}
	// projectCompletionLoadConfig loads without validating, like the profile
	// completion: a <Tab> on a broken config suggests nothing rather than
	// reporting an error at a shell prompt.
	projectCompletionLoadConfig = config.LoadDefault
	// projectDirectoryProblem stats a project directory on the operator's disk.
	projectDirectoryProblem = config.ProjectDirectoryProblem
	// projectListIsTerminal and projectListTerminalWidth decide between the
	// table for a person and the tab-separated rows for a pipe.
	projectListIsTerminal    = cli.IsTerminal
	projectListTerminalWidth = cli.TerminalWidth
)

// Command returns the `project` command tree so the root command can register
// it. Root assembly is the only consumer.
func Command() *cobra.Command { return projectCmd }

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "List configured projects and launch sessions from them",
	Long: `A project is a launch template from config.toml: a title, a directory, and
the profile it launches with. ` + "`amx config setup`" + ` sets up the first one, and
` + "`amx config edit`" + ` opens the file to add more.`,
}

var (
	projectListJSON      bool
	projectListMissing   bool
	projectListArchived  bool
	projectListProfiles  []string
	projectListHarnesses []string
	projectListGroups    []string
	projectLaunchJSON    bool
	projectLaunchProfile string
	projectLaunchTitle   string
	projectLaunchDir     string
	projectLaunchAs      string
)

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured projects",
	Long: `List the projects config.toml configures: profile, title, harness, directory,
and groups, as a table in a terminal and as tab-separated rows in a pipe.
Archived projects (archived = true) are left out unless --archived asks for
them alone. --missing lists only projects whose directory is unusable, and
--profile, --harness, and --group narrow the list; each is repeatable.`,
	Example: `  amx project list
  amx project list --missing
  amx project list --harness codex --group work --json`,
	Args: cobra.NoArgs,
	RunE: runProjectList,
}

var projectLaunchCmd = &cobra.Command{
	Use:   "launch TITLE [--profile PROFILE] [--directory DIR]",
	Short: "Launch a new session from a configured project",
	Long: `Launches a new session from the configured project named by TITLE (or
--title), through the same launch action the dashboard uses. It starts on
Agent Mux's private tmux server. The agent keeps running when no terminal
shows it, and the JSON payload carries
the runtime_session_id that ` + "`amx attach`" + ` takes.

A project is identified by its profile and title. A title that matches no
project exactly still selects one when it is the only project whose title reads
the same without its leading emoji or symbols, ignoring case: "amx" launches
"🐱amx". --profile is needed only when several projects share the title; an
ambiguous or unmatched selector lists the configured projects it could have
meant and launches nothing.

--directory starts this one session in DIR instead of the project's configured
directory; a relative DIR is read from the current directory. The project, its
profile, and its configuration are unchanged.

--launch-profile uses another configured profile (and its harness) for this
launch. --profile still identifies the source project; neither override is saved.`,
	Example: `  amx project launch api
  amx project launch amx --profile claude-work --backend tmux --json
  amx project launch api --directory ../api-hotfix`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeProjectTitleArg,
	RunE:              runProjectLaunch,
}

func init() {
	cli.AddJSONFlag(projectListCmd, &projectListJSON)
	projectListCmd.Flags().BoolVar(&projectListMissing, "missing", false, "list only projects whose directory is unusable")
	projectListCmd.Flags().BoolVar(&projectListArchived, "archived", false, "list only projects marked archived = true, which are otherwise left out")
	projectListCmd.Flags().StringSliceVar(&projectListProfiles, "profile", nil, "only projects that launch with this profile (repeatable)")
	projectListCmd.Flags().StringSliceVar(&projectListHarnesses, "harness", nil, "only projects whose profile runs this harness (repeatable)")
	projectListCmd.Flags().StringSliceVar(&projectListGroups, "group", nil, "only projects in this group (repeatable)")
	_ = projectListCmd.RegisterFlagCompletionFunc("profile", cli.CompleteProfiles)
	_ = projectListCmd.RegisterFlagCompletionFunc("harness", cli.CompleteHarnesses)
	_ = projectListCmd.RegisterFlagCompletionFunc("group", completeProjectGroups)
	cli.AddJSONFlag(projectLaunchCmd, &projectLaunchJSON)
	projectLaunchCmd.Flags().StringVar(&projectLaunchTitle, "title", "", "configured project title")
	projectLaunchCmd.Flags().StringVar(&projectLaunchProfile, "profile", "", "configured profile the project launches with")
	projectLaunchCmd.Flags().StringVar(&projectLaunchDir, "directory", "", "working directory for this launch, instead of the project's configured one")
	projectLaunchCmd.Flags().StringVar(&projectLaunchAs, "launch-profile", "", "profile and harness to use for this launch, instead of the project's own")
	_ = projectLaunchCmd.MarkFlagDirname("directory")
	_ = projectLaunchCmd.RegisterFlagCompletionFunc("profile", cli.CompleteProfiles)
	_ = projectLaunchCmd.RegisterFlagCompletionFunc("launch-profile", cli.CompleteProfiles)
	_ = projectLaunchCmd.RegisterFlagCompletionFunc("title", completeProjectTitles)
	cli.AddBackendFlag(projectLaunchCmd)
	projectCmd.AddCommand(projectListCmd, projectLaunchCmd)
}

// completeProjectTitles suggests configured project titles for --title, each
// described by the profile it launches with. When --profile is already given,
// only that profile's projects are offered, so the completion never proposes a
// pair the launch would refuse as unmatched. A title shared by several
// profiles is offered once. What was typed is matched past a title's leading
// emoji, as config.CompleteProjectTitle reads it, so "ras" reaches
// "🥧Raspberry Pi".
func completeProjectTitles(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cfg, err := projectCompletionLoadConfig()
	if err != nil || cfg == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	profile := ""
	if cmd != nil {
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

// completeProjectGroups offers the groups configured projects declare.
func completeProjectGroups(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cfg, err := projectCompletionLoadConfig()
	if err != nil || cfg == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var groups []string
	for _, group := range projectGroups(cfg) {
		if strings.HasPrefix(group, toComplete) {
			groups = append(groups, group)
		}
	}
	return groups, cobra.ShellCompDirectiveNoFileComp
}

// completeProjectTitleArg offers the same titles for the positional TITLE,
// and nothing once it is given.
func completeProjectTitleArg(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return completeProjectTitles(cmd, args, toComplete)
}
