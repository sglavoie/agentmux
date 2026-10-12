package actions

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/planrun"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/projectresolve"
	"github.com/sglavoie/agentmux/internal/session"
)

// Data keys of a PlanRun result. Readiness is present whenever ASL answered,
// including beside the error of a plan that may not be started.
const (
	DataPlanReadiness = "readiness"
	DataPlanCommand   = "command"
	DataPlanProject   = "project"
	DataPlanProfile   = "profile"
	DataPlanDirectory = "directory"
)

func planCommitModes() []string {
	modes := planrun.CommitModes()
	names := make([]string, 0, len(modes))
	for _, mode := range modes {
		names = append(names, string(mode))
	}
	return names
}

// PlanLaunchProjects are the configured projects a run may launch from:
// every project that is not archived. A configuration built in memory may
// carry its projects only under the deprecated alias, so both are read.
func PlanLaunchProjects(cfg *config.Config) []config.Session {
	if cfg == nil {
		return nil
	}
	projects := cfg.Projects
	if len(projects) == 0 {
		projects = cfg.Sessions
	}
	return config.ActiveProjects(projects)
}

// PlanProjectID is the identifier a PlanRun "project" parameter takes for
// exactly this project, the one its candidates carry.
func PlanProjectID(project config.Session) string {
	return project.EffectiveProfile() + "/" + project.Title
}

// PlanProjects lists the configured projects a run of plan could launch
// from: the ones whose directory covers the plan's working directory, in a
// stable order. It is empty for a plan that names no working directory.
func PlanProjects(cfg *config.Config, plan plans.PlanMeta) []config.Session {
	if cfg == nil || plan.CWD == "" {
		return nil
	}
	matches := projectresolve.NewWithGit(PlanLaunchProjects(cfg)).Matches(plan.CWD)
	projects := make([]config.Session, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, match := range matches {
		key := projectKey(match.Project)
		if seen[key] {
			continue
		}
		seen[key] = true
		projects = append(projects, match.Project)
	}
	sort.SliceStable(projects, func(i, j int) bool { return projectKey(projects[i]) < projectKey(projects[j]) })
	return projects
}

func projectKey(project config.Session) string {
	return strings.ToLower(project.EffectiveProfile()) + "/" + project.Title
}

func planProjectCandidates(projects []config.Session) []Candidate {
	candidates := make([]Candidate, 0, len(projects))
	for _, project := range projects {
		candidates = append(candidates, Candidate{
			ID:            PlanProjectID(project),
			DisplayName:   project.Title,
			CanonicalPath: project.Directory,
		})
	}
	return candidates
}

// planProject is the project a run launches from. A title names one among
// every configured project, and so does a candidate's own "profile/title"
// identifier, which names exactly one; with neither, the plan's working
// directory has to name exactly one. A profile narrows a choice and never
// widens it, so several projects that still match are returned as candidates.
func planProject(cfg *config.Config, plan plans.PlanMeta, title, profile string) (config.Session, error) {
	var pool []config.Session
	if title != "" {
		for _, project := range PlanLaunchProjects(cfg) {
			if project.EffectiveProfile()+"/"+project.Title == title {
				return project, nil
			}
		}
		for _, project := range PlanLaunchProjects(cfg) {
			if project.Title == title || config.ProjectTitleKey(project.Title) == config.ProjectTitleKey(title) {
				pool = append(pool, project)
			}
		}
		if len(pool) == 0 {
			return config.Session{}, &Error{Code: "not_found", Action: PlanRun, Message: fmt.Sprintf("no configured project is titled %q", title)}
		}
	} else {
		pool = PlanProjects(cfg, plan)
		if len(pool) == 0 {
			message := "the plan names no working directory, so the project to launch from has to be named"
			if plan.CWD != "" {
				message = fmt.Sprintf("no configured project covers the plan's working directory %s, so the project to launch from has to be named", plan.CWD)
			}
			return config.Session{}, &Error{
				Code: "missing_parameter", Action: PlanRun, Message: message,
				MissingParameters: []ParameterSchema{{Name: "project", Type: "string", Description: "configured project title (see `amx project list`)", Required: true}},
			}
		}
	}
	if len(pool) > 1 && profile != "" {
		var narrowed []config.Session
		for _, project := range pool {
			if strings.EqualFold(project.EffectiveProfile(), profile) {
				narrowed = append(narrowed, project)
			}
		}
		if len(narrowed) > 0 {
			pool = narrowed
		}
	}
	if len(pool) > 1 {
		return config.Session{}, &Error{
			Code: "ambiguous_project", Action: PlanRun,
			Message:    "several configured projects could run this plan; name one by title and profile",
			Candidates: planProjectCandidates(pool),
		}
	}
	return pool[0], nil
}

func runPlan(ctx context.Context, deps Dependencies, request Request) (Result, error) {
	if deps.Config == nil {
		return Result{}, dependencyError(PlanRun, "configuration")
	}
	plan := *request.Plan
	opts := planrun.Options{
		Harness:       strings.TrimSpace(request.Parameters["harness"]),
		ReviewHarness: strings.TrimSpace(request.Parameters["review_harness"]),
		CommitMode:    planrun.CommitMode(strings.TrimSpace(request.Parameters["commit_mode"])),
	}
	if err := opts.Validate(); err != nil {
		return Result{}, &Error{Code: "invalid_arguments", Action: PlanRun, Message: err.Error()}
	}
	check := parameterBool(request.Parameters, "check")
	profile := strings.TrimSpace(request.Parameters["profile"])
	project, err := planProject(deps.Config, plan, strings.TrimSpace(request.Parameters["project"]), profile)
	var unresolved *Error
	if errors.As(err, &unresolved) && check && unresolved.Code != "not_found" && plan.CWD != "" {
		// Whether a plan is ready does not depend on which project would
		// launch it, so a check answers without one.
		project, err = config.Session{Directory: plan.CWD}, nil
	}
	if err != nil {
		return Result{}, err
	}
	var account config.Account
	if project.Title != "" {
		if profile != "" {
			project.Profile, project.Account = profile, profile
		}
		found, ok := accountByName(deps.Config, project.EffectiveProfile())
		if !ok {
			return Result{}, &Error{Code: "not_found", Action: PlanRun, Message: fmt.Sprintf("no matching profile %q", project.EffectiveProfile())}
		}
		account = found
	}
	// The run works in the plan's own directory, which a project can cover
	// from above; `asl run` reads its workspace from where the agent starts.
	if plan.CWD != "" {
		project.Directory = plan.CWD
	}
	if problem := ProjectDirectoryProblem(project.Directory); problem != "" {
		issue := config.ProjectDirectoryIssue{Project: project.EffectiveProfile() + "/" + project.Title, Directory: project.Directory, Problem: problem}
		return Result{}, &Error{Code: "directory_unusable", Action: PlanRun, Message: issue.String()}
	}

	asl := deps.ASL
	if asl == nil {
		asl = planrun.Exec{Command: deps.Config.Defaults.ASL.Command}
	}
	readiness, err := planrun.Check(ctx, asl, plan.DirPath, project.Directory, opts)
	if err != nil {
		return Result{}, &Error{Code: "asl_unavailable", Action: PlanRun, Message: err.Error(), Cause: err}
	}
	data := map[string]any{
		DataPlanReadiness: readiness,
		DataPlanCommand:   planrun.Command(plan.DirPath, opts),
		DataPlanProject:   project.Title,
		DataPlanProfile:   project.EffectiveProfile(),
		DataPlanDirectory: project.Directory,
	}
	if !readiness.Ready() {
		return Result{Data: data}, &Error{Code: "plan_" + string(readiness.State), Action: PlanRun, Message: readiness.Summary()}
	}
	if check {
		return Result{Message: readiness.Summary(), Data: data}, nil
	}

	prompt, err := planrun.Prompt(ctx, asl, account.EffectiveHarness(), plan.DirPath, opts)
	if err != nil {
		return Result{Data: data}, &Error{Code: "asl_unavailable", Action: PlanRun, Message: err.Error(), Cause: err}
	}
	rt, err := deps.launchRuntime(PlanRun)
	if err != nil {
		return Result{Data: data}, err
	}
	launched, err := session.LaunchFreshOn(rt, project, account, deps.Config, prompt)
	if err != nil {
		return Result{Data: data}, err
	}
	return Result{Changed: true, Message: "started run", Data: deps.launchData(data, launched)}, nil
}
