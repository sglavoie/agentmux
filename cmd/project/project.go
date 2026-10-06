// Package project holds the `amx project` command family: the command surface
// over configured projects, the launch templates `[[projects]]` declares.
//
// `list` reports every configured project with the profile, harness, and
// directory it launches with, and `launch` starts a new session from one of
// them through the shared actions.Launch definition, exactly as the dashboard's
// launcher does. A project is identified by its profile and title. Selection
// never guesses: a title shared by several projects without a --profile to
// tell them apart, and a selector that matches nothing, both return the
// configured projects it could have meant as ordered candidates.
//
// Neither leaf rewrites config.toml.
package project

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/spf13/cobra"
)

// The seams the tests replace: a launch writes to the operator's real Kitty,
// and both leaves read the operator's real configuration.
var (
	projectLoadConfig = func() (*config.Config, error) { return cli.LoadRunnableConfig() }
	projectNewKitty   = func() kitty.KittyClient { return cli.NewKittyClient() }
	projectExecute    = func(ctx context.Context, deps actions.Dependencies, request actions.Request) (actions.Result, error) {
		return actions.DefaultRegistry().Execute(ctx, deps, actions.Launch, request)
	}
	// projectCompletionLoadConfig loads without validating, like the profile
	// completion: a <Tab> on a broken config suggests nothing rather than
	// reporting an error at a shell prompt.
	projectCompletionLoadConfig = config.LoadDefault
)

// Command returns the `project` command tree so the root command can register
// it. Root assembly is the only consumer.
func Command() *cobra.Command { return projectCmd }

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "List configured projects and launch sessions from them",
}

var (
	projectListJSON      bool
	projectLaunchJSON    bool
	projectLaunchProfile string
	projectLaunchTitle   string
)

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured projects",
	Args:  cobra.NoArgs,
	RunE:  runProjectList,
}

var projectLaunchCmd = &cobra.Command{
	Use:   "launch --title TITLE [--profile PROFILE]",
	Short: "Launch a new session from a configured project",
	Long: `Launches a new session from the configured project named by --title, in a
new Kitty tab, through the same launch action the dashboard uses.

A project is identified by its profile and title. --profile is needed only when
several projects share the title; an ambiguous or unmatched selector lists the
configured projects it could have meant and launches nothing.

Outside Kitty, set KITTY_LISTEN_ON to the instance's remote-control socket.`,
	Args: cobra.NoArgs,
	RunE: runProjectLaunch,
}

func init() {
	projectListCmd.Flags().BoolVar(&projectListJSON, "json", false, "Emit the schema-v1 JSON envelope")
	projectLaunchCmd.Flags().BoolVar(&projectLaunchJSON, "json", false, "Emit the schema-v1 JSON envelope")
	projectLaunchCmd.Flags().StringVar(&projectLaunchTitle, "title", "", "configured project title")
	projectLaunchCmd.Flags().StringVar(&projectLaunchProfile, "profile", "", "configured profile the project launches with")
	_ = projectLaunchCmd.RegisterFlagCompletionFunc("profile", cli.CompleteProfiles)
	_ = projectLaunchCmd.RegisterFlagCompletionFunc("title", completeProjectTitles)
	projectCmd.AddCommand(projectListCmd, projectLaunchCmd)
}

// projectView is one configured project as both leaves report it. Groups is
// always an array, empty rather than null, so a JSON caller can iterate it
// without a nil check.
type projectView struct {
	Profile   string   `json:"profile"`
	Title     string   `json:"title"`
	Harness   string   `json:"harness"`
	Directory string   `json:"directory"`
	Groups    []string `json:"groups"`
}

// projectLaunchData is the `project launch --json` payload. TabID and WindowID
// name the tab the launch opened when exactly one new tab carrying the
// project's title appeared; they are omitted rather than guessed otherwise.
type projectLaunchData struct {
	Action   actions.ID  `json:"action"`
	Changed  bool        `json:"changed"`
	Message  string      `json:"message,omitempty"`
	Project  projectView `json:"project"`
	TabID    int         `json:"tab_id,omitempty"`
	WindowID int         `json:"window_id,omitempty"`
}

func runProjectList(cmd *cobra.Command, _ []string) error {
	cfg, err := projectLoadConfig()
	if err != nil {
		return cli.CommandFailure(cmd, projectListJSON, "config_invalid", err)
	}
	views := projectViews(cfg)
	if projectListJSON {
		return cli.WriteJSON(cmd, views)
	}
	if len(views) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No projects are configured.")
		return err
	}
	rows := make([][]string, 0, len(views))
	for _, view := range views {
		rows = append(rows, []string{view.Profile, view.Title, view.Harness, cli.DisplayPath(view.Directory), strings.Join(view.Groups, ",")})
	}
	return cli.WriteTable(cmd.OutOrStdout(), []string{"PROFILE", "TITLE", "HARNESS", "DIRECTORY", "GROUPS"}, rows)
}

func runProjectLaunch(cmd *cobra.Command, _ []string) error {
	title := strings.TrimSpace(projectLaunchTitle)
	if title == "" {
		return cli.MissingParameter(cmd, projectLaunchJSON, "title", "configured project title (see `amx project list`)")
	}
	cfg, err := projectLoadConfig()
	if err != nil {
		return cli.CommandFailure(cmd, projectLaunchJSON, "config_invalid", err)
	}
	project, err := selectProject(cfg, strings.TrimSpace(projectLaunchProfile), title)
	if err != nil {
		return projectActionError(cmd, err)
	}

	client := projectNewKitty()
	before := tabIDs(client)
	result, err := projectExecute(cli.CommandContext(cmd), actions.Dependencies{Kitty: client, Config: cfg}, actions.Request{Project: &project})
	if err != nil {
		return projectActionError(cmd, err)
	}
	data := projectLaunchData{Action: result.Action, Changed: result.Changed, Message: result.Message, Project: projectViewOf(cfg, project)}
	if before != nil {
		data.TabID, data.WindowID = launchedTab(client, before, project.Title)
	}
	if projectLaunchJSON {
		return cli.WriteJSON(cmd, data)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Launched %s (%s).\n", project.Title, project.EffectiveProfile())
	return err
}

// projectActionError maps a selection or launch failure onto the caller's
// output mode, carrying any candidates into the JSON error envelope.
func projectActionError(cmd *cobra.Command, err error) error {
	var actionErr *actions.Error
	if !errors.As(err, &actionErr) {
		return cli.CommandFailure(cmd, projectLaunchJSON, "action_failed", err)
	}
	if !projectLaunchJSON {
		return cli.ActionErrorText(actionErr)
	}
	payload := cli.JSONError{Code: actionErr.Code, Message: actionErr.Message}
	for _, candidate := range actionErr.Candidates {
		payload.Candidates = append(payload.Candidates, cli.Candidate(candidate))
	}
	for _, parameter := range actionErr.MissingParameters {
		payload.Parameters = append(payload.Parameters, cli.ParameterHelp{Name: parameter.Name, Description: parameter.Description, Required: parameter.Required})
	}
	return cli.WriteJSONError(cmd, payload)
}

// selectProject resolves the configured project a (profile, title) selector
// names. Titles compare exactly, because they are operator-authored and are
// the launch identity; an empty profile matches any profile. It returns a
// typed action error, never a guess, when zero or several projects match.
func selectProject(cfg *config.Config, profile, title string) (config.Session, error) {
	var matches []config.Session
	for _, project := range cfg.Projects {
		if project.Title != title {
			continue
		}
		if profile != "" && !strings.EqualFold(project.EffectiveProfile(), profile) {
			continue
		}
		matches = append(matches, project)
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		selector := fmt.Sprintf("title %q", title)
		if profile != "" {
			selector += fmt.Sprintf(" and profile %q", profile)
		}
		// The near misses — the same title under another profile, or a
		// differently cased title — are the projects the caller most likely
		// meant; with none, every configured project is a candidate.
		var near []config.Session
		for _, project := range cfg.Projects {
			if strings.EqualFold(project.Title, title) {
				near = append(near, project)
			}
		}
		if len(near) == 0 {
			near = cfg.Projects
		}
		return config.Session{}, &actions.Error{Code: "not_found", Action: actions.Launch, Message: "no configured project matches " + selector, Candidates: projectCandidates(near)}
	default:
		return config.Session{}, &actions.Error{
			Code: "ambiguous_project", Action: actions.Launch,
			Message:           fmt.Sprintf("%d configured projects are titled %q; choose one with --profile", len(matches), title),
			MissingParameters: []actions.ParameterSchema{{Name: "profile", Type: "string", Description: "configured profile the project launches with", Required: true}},
			Candidates:        projectCandidates(matches),
		}
	}
}

// projectCandidates orders candidates by profile then title, so the same
// configuration always produces the same list. The ID is the selector pair a
// caller passes back as --profile and --title.
func projectCandidates(projects []config.Session) []actions.Candidate {
	candidates := make([]actions.Candidate, 0, len(projects))
	for _, project := range projects {
		candidates = append(candidates, actions.Candidate{
			ID:            project.EffectiveProfile() + "/" + project.Title,
			DisplayName:   project.Title,
			CanonicalPath: project.Directory,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	return candidates
}

// completeProjectTitles suggests configured project titles for --title, each
// described by the profile it launches with. When --profile is already given,
// only that profile's projects are offered, so the completion never proposes a
// pair the launch would refuse as unmatched. A title shared by several
// profiles is offered once.
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
	for _, project := range cfg.Projects {
		if !strings.HasPrefix(project.Title, toComplete) || seen[project.Title] {
			continue
		}
		if profile != "" && !strings.EqualFold(project.EffectiveProfile(), profile) {
			continue
		}
		seen[project.Title] = true
		titles = append(titles, project.Title+"\t"+project.EffectiveProfile())
	}
	return titles, cobra.ShellCompDirectiveNoFileComp
}

func projectViews(cfg *config.Config) []projectView {
	views := make([]projectView, 0, len(cfg.Projects))
	for _, project := range cfg.Projects {
		views = append(views, projectViewOf(cfg, project))
	}
	return views
}

func projectViewOf(cfg *config.Config, project config.Session) projectView {
	view := projectView{
		Profile:   project.EffectiveProfile(),
		Title:     project.Title,
		Directory: project.Directory,
		Groups:    append([]string{}, project.Groups...),
	}
	for _, profile := range cfg.Profiles {
		if strings.EqualFold(profile.Name, view.Profile) {
			view.Harness = profile.EffectiveHarness()
			break
		}
	}
	return view
}

// tabIDs records every tab before a launch, so the tab the launch adds can be
// told apart from one that already carried the same title. A failed listing
// returns nil and the launch is reported without a tab identity.
func tabIDs(client kitty.KittyClient) map[int]bool {
	osWindows, err := client.ListWindows()
	if err != nil {
		return nil
	}
	ids := make(map[int]bool)
	for _, osWindow := range osWindows {
		for _, tab := range osWindow.Tabs {
			ids[tab.ID] = true
		}
	}
	return ids
}

// launchedTab names the tab a launch opened: the one new tab whose title
// contains the project title. Zero, or more than one, is reported as unknown.
func launchedTab(client kitty.KittyClient, before map[int]bool, title string) (tabID, windowID int) {
	osWindows, err := client.ListWindows()
	if err != nil {
		return 0, 0
	}
	found := 0
	for _, osWindow := range osWindows {
		for _, tab := range osWindow.Tabs {
			if before[tab.ID] || !strings.Contains(tab.Title, title) {
				continue
			}
			found++
			tabID = tab.ID
			windowID = 0
			if len(tab.Windows) > 0 {
				windowID = tab.Windows[0].ID
			}
		}
	}
	if found != 1 {
		return 0, 0
	}
	return tabID, windowID
}
