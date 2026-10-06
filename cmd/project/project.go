// Package project holds the `amx project` command family: the command surface
// over configured projects, the launch templates `[[projects]]` declares.
//
// `list` reports every configured project with the profile, harness, and
// directory it launches with, and `launch` starts a new session from one of
// them through the shared actions.Launch definition, exactly as the dashboard's
// launcher does. A project is identified by its profile and title. A title
// that matches no project exactly may still name exactly one project once the
// leading emoji or symbols are set aside and case is ignored, which is how an
// operator types it. Selection never guesses: a title shared by several
// projects without a --profile to tell them apart, and a selector that matches
// nothing, both return the configured projects it could have meant as ordered
// candidates.
//
// Neither leaf rewrites config.toml.
package project

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

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
	// projectDirectoryProblem stats a project directory on the operator's disk.
	projectDirectoryProblem = config.ProjectDirectoryProblem
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
	projectListMissing   bool
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
	Use:   "launch TITLE [--profile PROFILE]",
	Short: "Launch a new session from a configured project",
	Long: `Launches a new session from the configured project named by TITLE (or
--title), in a new Kitty tab, through the same launch action the dashboard uses.

A project is identified by its profile and title. A title that matches no
project exactly still selects one when it is the only project whose title reads
the same without its leading emoji or symbols, ignoring case: "amx" launches
"🐱amx". --profile is needed only when several projects share the title; an
ambiguous or unmatched selector lists the configured projects it could have
meant and launches nothing.

Outside Kitty, set KITTY_LISTEN_ON to the instance's remote-control socket.`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeProjectTitleArg,
	RunE:              runProjectLaunch,
}

func init() {
	projectListCmd.Flags().BoolVar(&projectListJSON, "json", false, "Emit the schema-v1 JSON envelope")
	projectListCmd.Flags().BoolVar(&projectListMissing, "missing", false, "List only projects whose directory is unusable")
	projectLaunchCmd.Flags().BoolVar(&projectLaunchJSON, "json", false, "Emit the schema-v1 JSON envelope")
	projectLaunchCmd.Flags().StringVar(&projectLaunchTitle, "title", "", "configured project title")
	projectLaunchCmd.Flags().StringVar(&projectLaunchProfile, "profile", "", "configured profile the project launches with")
	_ = projectLaunchCmd.RegisterFlagCompletionFunc("profile", cli.CompleteProfiles)
	_ = projectLaunchCmd.RegisterFlagCompletionFunc("title", completeProjectTitles)
	projectCmd.AddCommand(projectListCmd, projectLaunchCmd)
}

// projectView is one configured project as both leaves report it. Groups is
// always an array, empty rather than null, so a JSON caller can iterate it
// without a nil check. DirectoryProblem is filled by `list` only, and only
// when the directory is unusable; a launch from such a project fails on its
// own terms.
type projectView struct {
	Profile          string   `json:"profile"`
	Title            string   `json:"title"`
	Harness          string   `json:"harness"`
	Directory        string   `json:"directory"`
	DirectoryProblem string   `json:"directory_problem,omitempty"`
	Groups           []string `json:"groups"`
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
	kept := views[:0]
	for _, view := range views {
		view.DirectoryProblem = projectDirectoryProblem(view.Directory)
		if !projectListMissing || view.DirectoryProblem != "" {
			kept = append(kept, view)
		}
	}
	views = kept
	if projectListJSON {
		return cli.WriteJSON(cmd, views)
	}
	if len(views) == 0 {
		message := "No projects are configured."
		if projectListMissing {
			message = "Every configured project directory is usable."
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), message)
		return err
	}
	rows := make([][]string, 0, len(views))
	for _, view := range views {
		directory := cli.DisplayPath(view.Directory)
		if view.DirectoryProblem != "" {
			directory += " (" + projectProblemLabel(view.DirectoryProblem) + ")"
		}
		rows = append(rows, []string{view.Profile, view.Title, view.Harness, directory, strings.Join(view.Groups, ",")})
	}
	return cli.WriteTable(cmd.OutOrStdout(), []string{"PROFILE", "TITLE", "HARNESS", "DIRECTORY", "GROUPS"}, rows)
}

// projectProblemLabel shortens a directory problem to fit a table cell; the
// detail after the colon of "cannot be inspected: ..." is in --json and in
// `amx config validate`.
func projectProblemLabel(problem string) string {
	switch {
	case problem == "does not exist":
		return "missing"
	case strings.HasPrefix(problem, "cannot be inspected"):
		return "unreadable"
	default:
		return problem
	}
}

func runProjectLaunch(cmd *cobra.Command, args []string) error {
	title := strings.TrimSpace(projectLaunchTitle)
	if len(args) == 1 {
		positional := strings.TrimSpace(args[0])
		if title != "" && title != positional {
			return cli.CommandFailure(cmd, projectLaunchJSON, "invalid_arguments", fmt.Errorf("title given twice: %q and --title %q", positional, title))
		}
		title = positional
	}
	if title == "" && projectLaunchJSON {
		return cli.MissingParameter(cmd, projectLaunchJSON, "title", "configured project title (see `amx project list`)")
	}
	cfg, err := projectLoadConfig()
	if err != nil {
		return cli.CommandFailure(cmd, projectLaunchJSON, "config_invalid", err)
	}
	if title == "" {
		// A person who typed the bare command is asking what there is to
		// launch, so the answer lists it, the way an unmatched title does.
		return cli.ActionErrorText(&actions.Error{
			Code: "missing_parameter", Action: actions.Launch,
			Message:    "a project title is required; `amx project list` shows every project",
			Candidates: projectCandidates(cfg.Projects),
		})
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
// names; an empty profile matches any profile. An exact title wins, because
// titles are operator-authored and are the launch identity. Only when nothing
// matches exactly is the title compared by its titleKey, so "amx" reaches
// "🐱amx" but can never shadow a project actually titled "amx". It returns a
// typed action error, never a guess, when zero or several projects match.
func selectProject(cfg *config.Config, profile, title string) (config.Session, error) {
	inProfile := func(project config.Session) bool {
		return profile == "" || strings.EqualFold(project.EffectiveProfile(), profile)
	}
	var matches []config.Session
	for _, project := range cfg.Projects {
		if project.Title == title && inProfile(project) {
			matches = append(matches, project)
		}
	}
	if len(matches) == 0 {
		key := titleKey(title)
		for _, project := range cfg.Projects {
			if key != "" && titleKey(project.Title) == key && inProfile(project) {
				matches = append(matches, project)
			}
		}
		if len(matches) > 1 && !sameTitle(matches) {
			return config.Session{}, &actions.Error{
				Code: "ambiguous_project", Action: actions.Launch,
				Message:    fmt.Sprintf("%d configured projects match title %q; give the exact title", len(matches), title),
				Candidates: projectCandidates(matches),
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		selector := fmt.Sprintf("title %q", title)
		if profile != "" {
			selector += fmt.Sprintf(" and profile %q", profile)
		}
		// The near misses — any project, under any profile, whose title
		// contains what was typed once both are read by their titleKey — are
		// the projects the caller most likely meant; with none, every
		// configured project is a candidate.
		var near []config.Session
		key := titleKey(title)
		for _, project := range cfg.Projects {
			if strings.Contains(titleKey(project.Title), key) {
				near = append(near, project)
			}
		}
		candidates := projectCandidates(near)
		// A title no project contains is most often a typo of one, so the
		// projects within a few edits of it come first, closest first.
		if len(near) == 0 {
			candidates = typoCandidates(cfg.Projects, key)
		}
		message := "no configured project matches " + selector
		if len(candidates) == 0 {
			candidates = projectCandidates(cfg.Projects)
			message += "; `amx project list` shows every project"
		}
		return config.Session{}, &actions.Error{Code: "not_found", Action: actions.Launch, Message: message, Candidates: candidates}
	default:
		return config.Session{}, &actions.Error{
			Code: "ambiguous_project", Action: actions.Launch,
			Message:           fmt.Sprintf("%d configured projects are titled %q; choose one with --profile", len(matches), matches[0].Title),
			MissingParameters: []actions.ParameterSchema{{Name: "profile", Type: "string", Description: "configured profile the project launches with", Required: true}},
			Candidates:        projectCandidates(matches),
		}
	}
}

// titleKey is how an operator types a title: without the emoji, bullets, and
// other symbols titles often lead with, and in lower case. "🐱amx", "•amx",
// and "AMX" all read as "amx".
func titleKey(title string) string {
	trimmed := strings.TrimLeftFunc(strings.TrimSpace(title), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return strings.ToLower(trimmed)
}

// sameTitle reports whether every project carries one title, so several of
// them differ only by profile and --profile is what tells them apart.
func sameTitle(projects []config.Session) bool {
	for _, project := range projects[1:] {
		if project.Title != projects[0].Title {
			return false
		}
	}
	return true
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

// typoCandidates returns the projects whose titleKey is within a few edits of
// key — two, or a third of what was typed when that is more — ordered by
// distance and then as projectCandidates orders them, so "amxx" offers "🐱amx"
// first. An empty key is no typo of anything.
func typoCandidates(projects []config.Session, key string) []actions.Candidate {
	if key == "" {
		return nil
	}
	limit := max(2, len([]rune(key))/3)
	distance := map[string]int{}
	var near []config.Session
	for _, project := range projects {
		d := editDistance(key, titleKey(project.Title))
		if d <= limit {
			near = append(near, project)
			distance[project.EffectiveProfile()+"/"+project.Title] = d
		}
	}
	candidates := projectCandidates(near)
	sort.SliceStable(candidates, func(i, j int) bool { return distance[candidates[i].ID] < distance[candidates[j].ID] })
	return candidates
}

// editDistance is the Levenshtein distance between a and b, counted in runes.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	row := make([]int, len(br)+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		diagonal := row[0]
		row[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			diagonal, row[j] = row[j], min(row[j]+1, row[j-1]+1, diagonal+cost)
		}
	}
	return row[len(br)]
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

// completeProjectTitleArg offers the same titles for the positional TITLE,
// and nothing once it is given.
func completeProjectTitleArg(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return completeProjectTitles(cmd, args, toComplete)
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
