// Package project holds the `amx project` command family: the command surface
// over configured projects, the launch templates `[[projects]]` declares.
//
// `list` reports every configured project with the profile, harness, and
// directory it launches with, narrowed on request by profile, harness, or
// group, and `launch` starts a new session from one of them through the shared
// actions.Launch definition, exactly as the dashboard's launcher does. A project is identified by its profile and title. A title
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
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui/layout"
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
}

var (
	projectListJSON      bool
	projectListMissing   bool
	projectListProfiles  []string
	projectListHarnesses []string
	projectListGroups    []string
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
	projectListCmd.Flags().BoolVar(&projectListJSON, "json", false, "emit schema-v1 JSON")
	projectListCmd.Flags().BoolVar(&projectListMissing, "missing", false, "list only projects whose directory is unusable")
	projectListCmd.Flags().StringSliceVar(&projectListProfiles, "profile", nil, "only projects that launch with this profile (repeatable)")
	projectListCmd.Flags().StringSliceVar(&projectListHarnesses, "harness", nil, "only projects whose profile runs this harness (repeatable)")
	projectListCmd.Flags().StringSliceVar(&projectListGroups, "group", nil, "only projects in this group (repeatable)")
	_ = projectListCmd.RegisterFlagCompletionFunc("profile", cli.CompleteProfiles)
	_ = projectListCmd.RegisterFlagCompletionFunc("harness", cli.CompleteHarnessFirstArg)
	_ = projectListCmd.RegisterFlagCompletionFunc("group", completeProjectGroups)
	projectLaunchCmd.Flags().BoolVar(&projectLaunchJSON, "json", false, "emit schema-v1 JSON")
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
	filter, err := newProjectListFilter(cfg)
	if err != nil {
		return cli.CommandFailure(cmd, projectListJSON, "invalid_arguments", err)
	}
	views := projectViews(cfg)
	kept := views[:0]
	for _, view := range views {
		if !filter.keeps(view) {
			continue
		}
		view.DirectoryProblem = projectDirectoryProblem(view.Directory)
		if !projectListMissing || view.DirectoryProblem != "" {
			kept = append(kept, view)
		}
	}
	views = kept
	if projectListJSON {
		return cli.WriteJSON(cmd, views)
	}
	out := cmd.OutOrStdout()
	if !projectListIsTerminal(out) {
		writeProjectLines(out, views)
		return nil
	}
	if len(views) == 0 {
		message := "No projects are configured."
		switch {
		case filter.active() && projectListMissing:
			message = "Every matching project directory is usable."
		case filter.active():
			message = "No configured project matches these filters."
		case projectListMissing:
			message = "Every configured project directory is usable."
		}
		_, err := fmt.Fprintln(out, message)
		return err
	}
	header := []string{"PROFILE", "TITLE", "HARNESS", "DIRECTORY", "GROUPS"}
	rows := make([][]string, 0, len(views))
	for _, view := range views {
		directory := cli.DisplayPath(view.Directory)
		if view.DirectoryProblem != "" {
			directory += " (" + projectProblemLabel(view.DirectoryProblem) + ")"
		}
		rows = append(rows, []string{view.Profile, view.Title, view.Harness, directory, groupsField(view.Groups)})
	}
	fitProjectDirectories(header, rows, projectListTerminalWidth(out))
	return cli.WriteTable(out, header, rows)
}

// writeProjectLines is `project list` off a terminal: headerless and
// tab-separated, as `session list` and `worktree list` are for pipes, with the
// directory cleaned of a configured trailing slash, so it compares equal to
// $PWD in the project, and its problem in a field of its own rather than
// appended to the path, so `cut -f4` yields a path. An empty list stays empty,
// since a script reading rows must not mistake a sentence for a project.
func writeProjectLines(out io.Writer, views []projectView) {
	for _, view := range views {
		problem := ""
		if view.DirectoryProblem != "" {
			problem = projectProblemLabel(view.DirectoryProblem)
		}
		directory := view.Directory
		if directory != "" {
			directory = filepath.Clean(directory)
		}
		_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\t%s\n", view.Profile, view.Title, view.Harness, directory, groupsField(view.Groups), problem)
	}
}

// groupsField is a project's groups as one text field, sorted, so two projects
// declaring the same groups in a different order read, grep, and sort alike.
// JSON keeps them in configured order.
func groupsField(groups []string) string {
	return strings.Join(slices.Sorted(slices.Values(groups)), ",")
}

// projectDirectoryColumn is the DIRECTORY column's index in the table, and
// projectDirectoryMinWidth the narrowest it is cut to: past that the row wraps
// instead, because a path cut to a few cells names nothing.
const (
	projectDirectoryColumn   = 3
	projectDirectoryMinWidth = 24
)

// fitProjectDirectories cuts DIRECTORY cells from the left when the table is
// wider than a terminal width cells wide, keeping the end of each path, which
// is the part that tells projects apart, and any "(missing)" mark after it.
// A width of zero is a terminal that would not say, and nothing is cut.
func fitProjectDirectories(header []string, rows [][]string, width int) {
	if width <= 0 {
		return
	}
	total := 0
	directory := 0
	for i := range header {
		column := layout.Width(header[i])
		for _, row := range rows {
			column = max(column, layout.Width(row[i]))
		}
		if i == projectDirectoryColumn {
			directory = column
		}
		total += column
		if i < len(header)-1 {
			total += 2
		}
	}
	if total <= width {
		return
	}
	limit := max(projectDirectoryMinWidth, directory-(total-width))
	for _, row := range rows {
		row[projectDirectoryColumn] = layout.TruncateLeft(row[projectDirectoryColumn], limit, "…")
	}
}

// projectListFilter is what --profile, --harness, and --group keep. Values of
// one flag are alternatives and the flags combine, so `--profile a --profile b
// --group web` keeps the web projects of either profile. Names compare
// without regard to case, as a profile lookup does.
type projectListFilter struct {
	profiles, harnesses, groups []string
}

// newProjectListFilter refuses a name the configuration does not know rather
// than matching it against nothing, since an empty table would read as "no
// such projects" instead of "no such name" — the same rule `session list`
// applies to its selectors.
func newProjectListFilter(cfg *config.Config) (projectListFilter, error) {
	var filter projectListFilter
	for _, name := range projectListProfiles {
		if !slices.ContainsFunc(cfg.Profiles, func(profile config.Account) bool { return strings.EqualFold(profile.Name, name) }) {
			return filter, fmt.Errorf("--profile %q: not a configured profile", name)
		}
		filter.profiles = append(filter.profiles, name)
	}
	for _, name := range projectListHarnesses {
		known := harness.NormalizeKnown(name)
		if known == "" {
			return filter, fmt.Errorf("--harness %q: want one of %s", name, strings.Join(harness.Names(), ", "))
		}
		filter.harnesses = append(filter.harnesses, known)
	}
	configured := projectGroups(cfg)
	for _, name := range projectListGroups {
		if !slices.ContainsFunc(configured, func(group string) bool { return strings.EqualFold(group, name) }) {
			if len(configured) == 0 {
				return filter, fmt.Errorf("--group %q: no project declares a group", name)
			}
			return filter, fmt.Errorf("--group %q: want one of %s", name, strings.Join(configured, ", "))
		}
		filter.groups = append(filter.groups, name)
	}
	return filter, nil
}

func (f projectListFilter) active() bool {
	return len(f.profiles)+len(f.harnesses)+len(f.groups) > 0
}

func (f projectListFilter) keeps(view projectView) bool {
	matches := func(wanted []string, have ...string) bool {
		if len(wanted) == 0 {
			return true
		}
		for _, w := range wanted {
			if slices.ContainsFunc(have, func(h string) bool { return strings.EqualFold(h, w) }) {
				return true
			}
		}
		return false
	}
	return matches(f.profiles, view.Profile) && matches(f.harnesses, view.Harness) && matches(f.groups, view.Groups...)
}

// projectGroups is every group a configured project declares, sorted and
// without duplicates.
func projectGroups(cfg *config.Config) []string {
	var groups []string
	for _, project := range cfg.Projects {
		for _, group := range project.Groups {
			if !slices.Contains(groups, group) {
				groups = append(groups, group)
			}
		}
	}
	sort.Strings(groups)
	return groups
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
	cfg, err := projectLoadConfig()
	if err != nil {
		return cli.CommandFailure(cmd, projectLaunchJSON, "config_invalid", err)
	}
	if title == "" {
		return projectMissingTitle(cmd, cfg)
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

// projectMissingTitle answers the bare command. A person who typed it is
// asking what there is to launch, so the answer lists it, the way an unmatched
// title does — but only the projects a launch would accept, since one whose
// directory is unusable is refused, and offering it would be offering a
// failure. Those are counted instead and left to `project list --missing`.
func projectMissingTitle(cmd *cobra.Command, cfg *config.Config) error {
	launchable := make([]config.Session, 0, len(cfg.Projects))
	for _, project := range cfg.Projects {
		if projectDirectoryProblem(project.Directory) == "" {
			launchable = append(launchable, project)
		}
	}
	message := "a project title is required; `amx project list` shows every project"
	if unusable := len(cfg.Projects) - len(launchable); unusable > 0 {
		noun := "projects whose directory is unusable are"
		if unusable == 1 {
			noun = "project whose directory is unusable is"
		}
		message += fmt.Sprintf(" (%d %s left out; `amx project list --missing` names them)", unusable, noun)
	}
	actionErr := &actions.Error{
		Code: "missing_parameter", Action: actions.Launch,
		Message: message, Candidates: projectCandidates(launchable),
	}
	if projectLaunchJSON {
		// The text form's positional title needs no flag spelled out; a JSON
		// caller is told the parameter by name, as every missing_parameter is.
		actionErr.MissingParameters = []actions.ParameterSchema{{
			Name: "title", Description: "configured project title (see `amx project list`)", Required: true,
		}}
	}
	return projectActionError(cmd, actionErr)
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
// matches exactly is the title compared by its ProjectTitleKey, so "amx" reaches
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
		key := config.ProjectTitleKey(title)
		for _, project := range cfg.Projects {
			if key != "" && config.ProjectTitleKey(project.Title) == key && inProfile(project) {
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
		// contains what was typed once both are read by their ProjectTitleKey — are
		// the projects the caller most likely meant; with none, every
		// configured project is a candidate.
		var near []config.Session
		key := config.ProjectTitleKey(title)
		for _, project := range cfg.Projects {
			if strings.Contains(config.ProjectTitleKey(project.Title), key) {
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
// configuration always produces the same list. Titles are compared by their
// ProjectTitleKey, so a leading emoji or bullet does not move a title away
// from its alphabetical place ("🐱amx" sorts with "amp", not after every
// plain title). The ID is the selector pair a caller passes back as --profile
// and --title.
func projectCandidates(projects []config.Session) []actions.Candidate {
	sorted := append([]config.Session(nil), projects...)
	sort.SliceStable(sorted, func(i, j int) bool {
		pi, pj := strings.ToLower(sorted[i].EffectiveProfile()), strings.ToLower(sorted[j].EffectiveProfile())
		if pi != pj {
			return pi < pj
		}
		if ki, kj := config.ProjectTitleKey(sorted[i].Title), config.ProjectTitleKey(sorted[j].Title); ki != kj {
			return ki < kj
		}
		return sorted[i].EffectiveProfile()+"/"+sorted[i].Title < sorted[j].EffectiveProfile()+"/"+sorted[j].Title
	})
	candidates := make([]actions.Candidate, 0, len(sorted))
	for _, project := range sorted {
		candidates = append(candidates, actions.Candidate{
			ID:            project.EffectiveProfile() + "/" + project.Title,
			DisplayName:   project.Title,
			CanonicalPath: project.Directory,
			Problem:       candidateProblem(project.Directory),
		})
	}
	return candidates
}

// candidateProblem names a directory a launch would refuse, the way
// `project list` labels it, so a near miss that cannot launch says so.
func candidateProblem(directory string) string {
	if problem := projectDirectoryProblem(directory); problem != "" {
		return "directory " + projectProblemLabel(problem)
	}
	return ""
}

// typoCandidates returns the projects whose ProjectTitleKey is within a few
// edits of key — two, or a third of what was typed when that is more —
// ordered by distance and then as projectCandidates orders them, so "amxx"
// offers "🐱amx" first. An empty key is no typo of anything.
func typoCandidates(projects []config.Session, key string) []actions.Candidate {
	if key == "" {
		return nil
	}
	limit := max(2, len([]rune(key))/3)
	distance := map[string]int{}
	var near []config.Session
	for _, project := range projects {
		d := editDistance(key, config.ProjectTitleKey(project.Title))
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
