package project

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/spf13/cobra"
)

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
		return cli.ConfigFailure(cmd, projectLaunchJSON, err)
	}
	if title == "" {
		return projectMissingTitle(cmd, cfg)
	}
	project, err := selectProjectOrPair(cfg, strings.TrimSpace(projectLaunchProfile), title)
	if err != nil {
		return projectActionError(cmd, err)
	}

	if name := strings.TrimSpace(projectLaunchAs); name != "" {
		index := slices.IndexFunc(cfg.Profiles, func(profile config.Account) bool { return strings.EqualFold(profile.Name, name) })
		if index < 0 {
			return cli.CommandFailure(cmd, projectLaunchJSON, "not_found", cli.UnknownProfile(cfg, name))
		}
		project.Profile, project.Account = cfg.Profiles[index].Name, cfg.Profiles[index].Name
	}

	deps := actions.Dependencies{Config: cfg}
	if err := cli.SelectBackend(cmd, &deps); err != nil {
		return projectActionError(cmd, err)
	}
	request := actions.Request{Project: &project}
	if directory := strings.TrimSpace(projectLaunchDir); directory != "" {
		// Made absolute here, where "relative" still means the caller's
		// directory; the launch itself runs wherever the backend does.
		absolute, err := filepath.Abs(config.ExpandTilde(directory))
		if err != nil {
			return cli.CommandFailure(cmd, projectLaunchJSON, "invalid_arguments", fmt.Errorf("--directory %q: %w", directory, err))
		}
		project.Directory = absolute
		request.Parameters = map[string]string{"directory": absolute}
	}
	result, err := projectExecute(cli.CommandContext(cmd), deps, request)
	if err != nil {
		return projectActionError(cmd, err)
	}
	data := projectLaunchData{Action: result.Action, Changed: result.Changed, Message: result.Message, Project: projectViewOf(cfg, project), Backend: string(deps.Backend)}
	if id, ok := result.Data["runtime_session_id"].(string); ok {
		data.RuntimeSessionID = id
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
	active := config.ActiveProjects(cfg.Projects)
	launchable := make([]config.Session, 0, len(active))
	for _, project := range active {
		if projectDirectoryProblem(project.Directory) == "" {
			launchable = append(launchable, project)
		}
	}
	message := "a project title is required; `amx project list` shows every project"
	if unusable := len(active) - len(launchable); unusable > 0 {
		noun := "projects whose directory is unusable are"
		if unusable == 1 {
			noun = "project whose directory is unusable is"
		}
		message += fmt.Sprintf(" (%d %s left out; `amx project list --missing` names them, and `archived = true` keeps one without offering it)", unusable, noun)
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
	// Archived projects are not offered, so they are neither matches nor
	// candidates; one the selector names is reported as archived instead.
	projects := config.ActiveProjects(cfg.Projects)
	var matches []config.Session
	for _, project := range projects {
		if project.Title == title && inProfile(project) {
			matches = append(matches, project)
		}
	}
	if len(matches) == 0 {
		key := config.ProjectTitleKey(title)
		for _, project := range projects {
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
		if archived, ok := archivedProject(cfg, profile, title); ok {
			return config.Session{}, &actions.Error{
				Code: "project_archived", Action: actions.Launch,
				Message: fmt.Sprintf("project %s/%s is archived; remove `archived = true` from its [[projects]] entry to launch it", archived.EffectiveProfile(), archived.Title),
			}
		}
		// The near misses — any project, under any profile, whose title
		// contains what was typed once both are read by their ProjectTitleKey — are
		// the projects the caller most likely meant; with none, every
		// configured project is a candidate.
		var near []config.Session
		key := config.ProjectTitleKey(title)
		for _, project := range projects {
			if strings.Contains(config.ProjectTitleKey(project.Title), key) {
				near = append(near, project)
			}
		}
		candidates := projectCandidates(near)
		// A title no project contains is most often a typo of one, so the
		// projects within a few edits of it come first, closest first.
		if len(near) == 0 {
			candidates = typoCandidates(projects, key)
		}
		message := "no configured project matches " + selector
		if len(candidates) == 0 {
			candidates = projectCandidates(projects)
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

// selectProjectOrPair is selectProject that also accepts the "profile/title"
// form the candidate lists print, so a candidate copied out of an error can be
// passed straight back. The whole string is tried as a title first, because a
// title may itself contain a slash; only when that matches nothing, no
// --profile was given, and the part before the first slash names a configured
// profile is it read as the pair. Otherwise the first attempt's error stands,
// with its near-miss candidates.
func selectProjectOrPair(cfg *config.Config, profile, title string) (config.Session, error) {
	project, err := selectProject(cfg, profile, title)
	var actionErr *actions.Error
	if err == nil || profile != "" || !errors.As(err, &actionErr) || actionErr.Code != "not_found" {
		return project, err
	}
	pairProfile, pairTitle, ok := strings.Cut(title, "/")
	if !ok || strings.TrimSpace(pairTitle) == "" || !hasProfile(cfg, pairProfile) {
		return project, err
	}
	paired, pairErr := selectProject(cfg, pairProfile, strings.TrimSpace(pairTitle))
	if pairErr == nil {
		return paired, nil
	}
	if errors.As(pairErr, &actionErr) && actionErr.Code == "project_archived" {
		return paired, pairErr
	}
	return project, err
}

// archivedProject finds the archived project a selector names, by exact title
// or, failing that, by ProjectTitleKey, under the same profile rule
// selectProject applies. It is consulted only once nothing active matched.
func archivedProject(cfg *config.Config, profile, title string) (config.Session, bool) {
	key := config.ProjectTitleKey(title)
	var byKey []config.Session
	for _, project := range cfg.Projects {
		if !project.Archived || (profile != "" && !strings.EqualFold(project.EffectiveProfile(), profile)) {
			continue
		}
		if project.Title == title {
			return project, true
		}
		if key != "" && config.ProjectTitleKey(project.Title) == key {
			byKey = append(byKey, project)
		}
	}
	if len(byKey) == 1 {
		return byKey[0], true
	}
	return config.Session{}, false
}

func hasProfile(cfg *config.Config, name string) bool {
	for _, project := range cfg.Projects {
		if strings.EqualFold(project.EffectiveProfile(), name) {
			return true
		}
	}
	return false
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
// and --title, or as the single "profile/title" argument.
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

// typoCandidates returns the projects whose title key is a
// config.ProjectTitleTypo of key, ordered by distance and then as
// projectCandidates orders them, so "amxx" offers "🐱amx" first and
// "rasberry" offers "🥧Raspberry Pi".
func typoCandidates(projects []config.Session, key string) []actions.Candidate {
	distance := map[string]int{}
	var near []config.Session
	for _, project := range projects {
		if d, ok := config.ProjectTitleTypo(key, project.Title); ok {
			near = append(near, project)
			distance[project.EffectiveProfile()+"/"+project.Title] = d
		}
	}
	candidates := projectCandidates(near)
	sort.SliceStable(candidates, func(i, j int) bool { return distance[candidates[i].ID] < distance[candidates[j].ID] })
	return candidates
}
