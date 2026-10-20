// Package plans holds the `amx plans` command family. Its one leaf, `run`,
// starts an agent-session-loop run of a split plan the way the Plans view
// does: through the shared plan.run action, which asks ASL whether the plan
// may be started and, when it may, launches a managed session whose first
// prompt is ASL's own supervision skill.
//
// The package decides nothing about readiness and names no asl flag beyond
// the three run options it forwards; internal/planrun owns both. What it
// owns is the command surface: which plan a selector names, and how a plan
// that may not be started is reported. That report is a decision and not a
// failure to decide, so under --json it is the ordinary data envelope with
// the readiness in it and a quiet nonzero exit, never an error envelope.
//
// The family is `plans`, as the Plans view and `plans_directory` are, and not
// `plan`: that name belonged to a command of the retired database and must
// not come back (TestRemovedCommandsAreNotRegistered).
//
// A file in this package may name the imported internal/plans package
// unqualified as `plans`: a package's own name is not in scope inside its
// files.
package plans

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/planrun"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/spf13/cobra"
)

var (
	planLoadConfig = cli.LoadRunnableConfig
	planNewKitty   = func() kitty.KittyClient { return cli.NewKittyClient() }
	planDescribe   = plans.Describe
	// planASL replaces the installed agent-session-loop in tests; nil is the
	// configured command.
	planASL planrun.ASL
)

// Command returns the `plans` command tree so the root command can register
// it. Root assembly is the only consumer.
func Command() *cobra.Command { return planCmd }

var planCmd = &cobra.Command{
	Use:   "plans",
	Short: "Start agent-session-loop runs of split plans",
}

var planRunCmd = &cobra.Command{
	Use:   "run PLAN",
	Short: "Start a run of a ready plan under a supervising agent",
	Long: `Starts an agent-session-loop run of the split plan PLAN, the way the Plans
view's run form does.

PLAN is a plan directory, or the name of one under the configured plans
directory: its exact name, or the start of it when only one plan begins that
way. A name several plans begin with lists them and starts nothing.

The plan is checked first: ` + "`asl preflight`" + ` has to pass, and the plan must be
neither running nor finished. A plan that fails any of that is reported with
what agent-session-loop said about it, and nothing is started; preparing a
plan stays with ` + "`asl prepare`" + `. --check stops after the check either way.

A ready plan is started by launching an ordinary managed session in the plan's
working directory whose first prompt is agent-session-loop's own supervision
skill, so the run is supervised by an agent you can attach to and shows up in
the dashboard, the overview, and the Runs view like any other. The session
launches from the configured project that covers the plan's working directory;
--project names one when none or several do, and --profile picks the profile
the supervising agent runs under.

--harness, --review-harness, and the commit mode are passed to ` + "`asl run`" + `;
each one left out is agent-session-loop's own default. They choose what the
run's sessions are implemented and reviewed with, which is independent of the
profile supervising it.`,
	Example: `  amx plans run 2e774ec64570 --check
  amx plans run 2e774ec64570 --harness codex --review-harness claude
  amx plans run ~/.local/share/agent-plans/2e774ec64570-1a09b1daef1c --project api --profile claude-work --backend tmux`,
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completePlans,
	RunE:              runPlanRun,
}

func init() {
	flags := planRunCmd.Flags()
	cli.AddJSONFlag(planRunCmd, nil)
	flags.Bool("check", false, "report whether the plan may be started and start nothing")
	flags.String("project", "", "configured project the supervising session launches from")
	_ = planRunCmd.RegisterFlagCompletionFunc("project", cli.CompleteProjects)
	flags.String("profile", "", "configured profile the supervising session launches under")
	flags.String("harness", "", "harness that implements the plan's sessions (default: agent-session-loop's)")
	flags.String("review-harness", "", "harness for a final review of the run (default: none)")
	flags.Bool("commit-per-session", false, "commit after each session")
	flags.Bool("commit-at-end", false, "commit once, when the run ends")
	planRunCmd.MarkFlagsMutuallyExclusive("commit-per-session", "commit-at-end")
	_ = planRunCmd.RegisterFlagCompletionFunc("profile", cli.CompleteProfiles)
	for _, name := range []string{"harness", "review-harness"} {
		_ = planRunCmd.RegisterFlagCompletionFunc(name, func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return harness.Names(), cobra.ShellCompDirectiveNoFileComp
		})
	}
	cli.AddBackendFlag(planRunCmd)
	planCmd.AddCommand(planRunCmd)
}

// planView names the plan a run is about.
type planView struct {
	Name      string `json:"name"`
	Directory string `json:"directory"`
	// WorkingDirectory is the directory the plan records; empty when it
	// records none.
	WorkingDirectory string `json:"working_directory,omitempty"`
}

// planRunData is the `plans run --json` payload, for a started run, a check,
// and a plan that may not be started alike. Started says which it was.
type planRunData struct {
	Action  actions.ID `json:"action"`
	Changed bool       `json:"changed"`
	Started bool       `json:"started"`
	Message string     `json:"message,omitempty"`
	Plan    planView   `json:"plan"`
	// Readiness is what agent-session-loop said about the plan.
	Readiness planrun.Readiness `json:"readiness"`
	// Command is the `asl run` command line the run amounts to.
	Command string `json:"command"`
	// Project, Profile, and Directory are where the supervising session
	// launches, or would.
	Project   string `json:"project"`
	Profile   string `json:"profile"`
	Directory string `json:"directory"`
	// Backend and RuntimeSessionID are reported for a started run, as
	// `project launch` reports them.
	Backend          string `json:"backend,omitempty"`
	RuntimeSessionID string `json:"runtime_session_id,omitempty"`
}

func runPlanRun(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if len(args) == 0 {
		return cli.MissingParameter(cmd, jsonOutput, "PLAN", "a plan directory, or the name of one under the configured plans directory")
	}
	cfg, err := planLoadConfig()
	if err != nil {
		return cli.CommandFailure(cmd, jsonOutput, "config_invalid", err)
	}
	plan, err := selectPlan(cfg, args[0])
	if err != nil {
		return planActionError(cmd, jsonOutput, err)
	}

	check, _ := cmd.Flags().GetBool("check")
	parameters := map[string]string{}
	for flag, name := range map[string]string{"project": "project", "profile": "profile", "harness": "harness", "review-harness": "review_harness"} {
		if value, _ := cmd.Flags().GetString(flag); strings.TrimSpace(value) != "" {
			parameters[name] = strings.TrimSpace(value)
		}
	}
	if perSession, _ := cmd.Flags().GetBool("commit-per-session"); perSession {
		parameters["commit_mode"] = string(planrun.CommitPerSession)
	}
	if atEnd, _ := cmd.Flags().GetBool("commit-at-end"); atEnd {
		parameters["commit_mode"] = string(planrun.CommitAtEnd)
	}
	if check {
		parameters["check"] = "true"
	}

	deps := actions.Dependencies{Config: cfg, ASL: planASL}
	if !check {
		// A check launches nothing, so it needs no backend and must work
		// where none is available.
		if err := cli.SelectBackend(cmd, &deps); err != nil {
			return planActionError(cmd, jsonOutput, err)
		}
		if deps.Backend == runtimesession.BackendKitty {
			deps.Kitty = planNewKitty()
		}
	}
	result, err := actions.DefaultRegistry().Execute(cli.CommandContext(cmd), deps, actions.PlanRun, actions.Request{Plan: &plan, Parameters: parameters})
	readiness, answered := result.Data[actions.DataPlanReadiness].(planrun.Readiness)
	if !answered {
		// Nothing was learned about the plan: the failure is the whole answer.
		return planActionError(cmd, jsonOutput, err)
	}
	data := planRunData{
		Action: result.Action, Changed: result.Changed, Started: result.Changed, Message: result.Message,
		Plan:      planView{Name: plan.Name, Directory: plan.DirPath, WorkingDirectory: plan.CWD},
		Readiness: readiness,
	}
	data.Command, _ = result.Data[actions.DataPlanCommand].(string)
	data.Project, _ = result.Data[actions.DataPlanProject].(string)
	data.Profile, _ = result.Data[actions.DataPlanProfile].(string)
	data.Directory, _ = result.Data[actions.DataPlanDirectory].(string)
	data.Backend, _ = result.Data["backend"].(string)
	data.RuntimeSessionID, _ = result.Data["runtime_session_id"].(string)

	if err != nil && readiness.Ready() {
		// The plan was ready and the launch failed.
		return planActionError(cmd, jsonOutput, err)
	}
	if err != nil {
		data.Message = readiness.Summary()
		if jsonOutput {
			return cli.WriteJSONPartial(cmd, data, err)
		}
		writeFindings(cmd, readiness)
		return errors.New(readiness.Summary())
	}
	if jsonOutput {
		return cli.WriteJSON(cmd, data)
	}
	out := cmd.OutOrStdout()
	name := plan.Name
	if readiness.Title != "" {
		name = fmt.Sprintf("%q (%s)", readiness.Title, plan.Name)
	}
	from := ""
	if data.Project != "" {
		from = fmt.Sprintf(" from %s (%s)", data.Project, data.Profile)
	}
	if !data.Started {
		_, err = fmt.Fprintf(out, "%s is ready to run%s.\n  %s\n", name, from, data.Command)
		return err
	}
	_, err = fmt.Fprintf(out, "Started %s%s.\n  %s\n", name, from, data.Command)
	if err == nil && data.RuntimeSessionID != "" {
		_, err = fmt.Fprintf(out, "  amx attach %s\n", data.RuntimeSessionID)
	}
	return err
}

// writeFindings lists what preflight held against the plan, one finding to a
// line, worded as agent-session-loop worded it.
func writeFindings(cmd *cobra.Command, readiness planrun.Readiness) {
	rows := make([][]string, 0, len(readiness.Findings))
	for _, finding := range readiness.Findings {
		message := finding.Message
		if finding.Detail != "" {
			message += " (" + finding.Detail + ")"
		}
		rows = append(rows, []string{finding.Severity, finding.Category, message})
	}
	if len(rows) > 0 {
		_ = cli.WriteTable(cmd.OutOrStdout(), []string{"SEVERITY", "CHECK", "FINDING"}, rows)
	}
}

// planActionError maps a selection or action failure onto the caller's
// output mode, carrying candidates and missing parameters into the JSON
// error envelope.
func planActionError(cmd *cobra.Command, jsonOutput bool, err error) error {
	var actionErr *actions.Error
	if !errors.As(err, &actionErr) {
		return cli.CommandFailure(cmd, jsonOutput, "action_failed", err)
	}
	if !jsonOutput {
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

// planDirectories lists the plan directories under the configured root,
// sorted by name. Dot directories and the `sources` directory are not plans,
// as the Plans view has it.
func planDirectories(cfg *config.Config) []string {
	root := config.ExpandTilde(cfg.Defaults.PlansDirectory)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, entry := range entries {
		if name := entry.Name(); entry.IsDir() && !strings.HasPrefix(name, ".") && name != "sources" {
			dirs = append(dirs, filepath.Join(root, name))
		}
	}
	sort.Strings(dirs)
	return dirs
}

// selectPlan resolves a selector to one plan. A selector that names a
// directory is that directory. Any other is a name under the plans root: the
// exact one, else the only one that starts with it. Several return ordered
// candidates and never a guess.
func selectPlan(cfg *config.Config, selector string) (plans.PlanMeta, error) {
	selector = strings.TrimSpace(selector)
	path := config.ExpandTilde(selector)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return describePlan(path)
	}
	if strings.ContainsRune(selector, filepath.Separator) {
		return plans.PlanMeta{}, &actions.Error{Code: "not_found", Action: actions.PlanRun, Message: fmt.Sprintf("no plan directory at %s", selector)}
	}
	var matches []string
	for _, dir := range planDirectories(cfg) {
		base := filepath.Base(dir)
		if base == selector {
			return describePlan(dir)
		}
		if strings.HasPrefix(base, selector) {
			matches = append(matches, dir)
		}
	}
	switch len(matches) {
	case 0:
		return plans.PlanMeta{}, &actions.Error{Code: "not_found", Action: actions.PlanRun, Message: fmt.Sprintf("no plan named %q under %s", selector, cli.DisplayPath(config.ExpandTilde(cfg.Defaults.PlansDirectory)))}
	case 1:
		return describePlan(matches[0])
	}
	candidates := make([]actions.Candidate, 0, len(matches))
	for _, dir := range matches {
		candidates = append(candidates, actions.Candidate{ID: filepath.Base(dir), DisplayName: plans.DisplayName(filepath.Base(dir)), CanonicalPath: dir})
	}
	return plans.PlanMeta{}, &actions.Error{
		Code: "ambiguous_selector", Action: actions.PlanRun,
		Message: fmt.Sprintf("several plans begin with %q", selector), Candidates: candidates,
	}
}

func describePlan(dir string) (plans.PlanMeta, error) {
	plan, err := planDescribe(dir)
	if err != nil {
		return plans.PlanMeta{}, &actions.Error{Code: "invalid_plan", Action: actions.PlanRun, Message: err.Error(), Cause: err}
	}
	return plan, nil
}

func completePlans(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := planLoadConfig()
	if err != nil {
		return nil, cobra.ShellCompDirectiveDefault
	}
	var names []string
	for _, dir := range planDirectories(cfg) {
		if base := filepath.Base(dir); strings.HasPrefix(base, toComplete) {
			names = append(names, base)
		}
	}
	// A plan may also be named by path, so directories stay completable.
	return names, cobra.ShellCompDirectiveDefault
}
