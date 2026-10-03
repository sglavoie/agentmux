// Package worktree holds the `amx worktree` command family: listing managed
// Agent Mux Git worktrees, showing one, reopening it as a live session, opening
// a shell or the configured editor in it, and the bounded destructive leaves —
// removing a clean inactive worktree, forgetting stale metadata, and pruning
// every worktree that removal would accept (prune.go).
//
// A file in this package may name the imported internal/worktree package
// unqualified as `worktree`: a package's own name is not in scope inside its
// files.
package worktree

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/spf13/cobra"
)

var (
	worktreeCommandsLoadConfig   = cli.LoadRunnableConfig
	worktreeCommandsNewManager   = func() actions.WorktreeLifecycle { return worktree.NewManager() }
	worktreeCommandsNewKitty     = func() kitty.KittyClient { return cli.NewKittyClient() }
	worktreeCommandsLiveSessions = func(client kitty.KittyClient, cfg *config.Config) ([]session.RunningSession, error) {
		running, _, err := session.ListWithConfig(client, cfg)
		return running, err
	}
	worktreeCommandsActiveLoops = func() ([]agentloop.Candidate, error) {
		return agentloop.Discover(agentloop.DiscoverOptions{})
	}
	worktreeCommandsTranscriptIndex = transcript.LoadIndex
	worktreeCommandsReaders         = cli.TranscriptReaders
)

// Command returns the `worktree` command tree so the root command can register
// it. Root assembly is the only consumer.
func Command() *cobra.Command { return worktreeCmd }

var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Inspect and manage Agent Mux Git worktrees",
}

var worktreeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List managed worktrees",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if len(args) != 0 {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("worktree list accepts no arguments"))
		}
		result, err := executeWorktreeAction(cmd, actions.WorktreeList, actions.Request{})
		if err != nil {
			return worktreeCommandError(cmd, jsonOutput, result, err)
		}
		views, _ := result.Data["worktrees"].([]actions.WorktreeView)
		warnings, _ := result.Data["warnings"].([]string)
		if jsonOutput {
			return cli.WriteJSON(cmd, worktreeListData{Worktrees: nonNilWorktreeViews(views), Warnings: nonNilStrings(warnings)})
		}
		for _, view := range views {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\n", view.ID, displayWorktreeProject(view), view.Branch, worktreeState(view), view.Path)
		}
		writeWorktreeWarnings(cmd, warnings)
		return nil
	},
}

var worktreeShowCmd = newWorktreeSelectorCommand("show", actions.WorktreeShow, "Show one managed worktree")
var worktreeShellCmd = newWorktreeSelectorCommand("shell", actions.WorktreeShell, "Open a shell in a managed worktree")
var worktreeEditCmd = newWorktreeSelectorCommand("edit", actions.WorktreeEditor, "Open a managed worktree in the configured editor")

var worktreeReopenCmd = &cobra.Command{
	Use:   "reopen <selector>",
	Short: "Focus, resume, or freshly launch a managed worktree",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		selector, err := worktreeSelectorFromArgs(cmd, jsonOutput, "reopen", args)
		if err != nil {
			return err
		}
		parameters := make(map[string]string)
		if value, _ := cmd.Flags().GetString("session"); strings.TrimSpace(value) != "" {
			parameters["session"] = value
		}
		if value, _ := cmd.Flags().GetString("profile"); strings.TrimSpace(value) != "" {
			parameters["profile"] = value
		}
		return runWorktreeMutationCommand(cmd, jsonOutput, actions.WorktreeReopen, actions.Request{Selector: selector, Parameters: parameters})
	},
}

var worktreeRemoveCmd = &cobra.Command{
	Use:   "remove <selector>",
	Short: "Safely remove a clean inactive worktree",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		selector, err := worktreeSelectorFromArgs(cmd, jsonOutput, "remove", args)
		if err != nil {
			return err
		}
		confirmed, _ := cmd.Flags().GetBool("yes")
		deleteBranch, _ := cmd.Flags().GetBool("delete-branch")
		parameters := make(map[string]string)
		if deleteBranch {
			parameters["delete_branch"] = "true"
		}
		return runWorktreeMutationCommand(cmd, jsonOutput, actions.WorktreeRemove, actions.Request{Selector: selector, Parameters: parameters, Confirmed: confirmed})
	},
}

var worktreeForgetCmd = &cobra.Command{
	Use:   "forget <selector>",
	Short: "Forget stale or invalid worktree metadata",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		selector, err := worktreeSelectorFromArgs(cmd, jsonOutput, "forget", args)
		if err != nil {
			return err
		}
		confirmed, _ := cmd.Flags().GetBool("yes")
		return runWorktreeMutationCommand(cmd, jsonOutput, actions.WorktreeForget, actions.Request{Selector: selector, Confirmed: confirmed})
	},
}

func newWorktreeSelectorCommand(name string, actionID actions.ID, short string) *cobra.Command {
	return &cobra.Command{
		Use: name + " <selector>", Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonOutput, _ := cmd.Flags().GetBool("json")
			selector, err := worktreeSelectorFromArgs(cmd, jsonOutput, name, args)
			if err != nil {
				return err
			}
			result, actionErr := executeWorktreeAction(cmd, actionID, actions.Request{Selector: selector})
			if actionErr != nil {
				return worktreeCommandError(cmd, jsonOutput, result, actionErr)
			}
			if actionID == actions.WorktreeShow {
				view, _ := result.Data["worktree"].(actions.WorktreeView)
				warnings, _ := result.Data["warnings"].([]string)
				if jsonOutput {
					return cli.WriteJSON(cmd, worktreeShowData{Worktree: view, Warnings: nonNilStrings(warnings)})
				}
				printWorktree(cmd, view)
				writeWorktreeWarnings(cmd, warnings)
				return nil
			}
			return writeWorktreeActionResult(cmd, jsonOutput, result)
		},
	}
}

func executeWorktreeAction(cmd *cobra.Command, actionID actions.ID, request actions.Request) (actions.Result, error) {
	cfg, err := worktreeCommandsLoadConfig()
	if err != nil {
		return actions.Result{}, &actions.Error{Code: "config_error", Action: actionID, Message: err.Error()}
	}
	client := worktreeCommandsNewKitty()
	deps := actions.Dependencies{
		Kitty: client, Config: cfg, WorktreeManager: worktreeCommandsNewManager(), Readers: worktreeCommandsReaders(),
		LiveSessions: func(current *config.Config) ([]session.RunningSession, error) {
			return worktreeCommandsLiveSessions(client, current)
		},
		ActiveLoops:     worktreeCommandsActiveLoops,
		TranscriptIndex: worktreeCommandsTranscriptIndex,
	}
	return actions.DefaultRegistry().Execute(cli.CommandContext(cmd), deps, actionID, request)
}

func runWorktreeMutationCommand(cmd *cobra.Command, jsonOutput bool, actionID actions.ID, request actions.Request) error {
	result, err := executeWorktreeAction(cmd, actionID, request)
	if err != nil {
		return worktreeCommandError(cmd, jsonOutput, result, err)
	}
	return writeWorktreeActionResult(cmd, jsonOutput, result)
}

func writeWorktreeActionResult(cmd *cobra.Command, jsonOutput bool, result actions.Result) error {
	data := worktreeActionData{Action: result.Action, Changed: result.Changed, Message: result.Message, Data: result.Data}
	if jsonOutput {
		return cli.WriteJSON(cmd, data)
	}
	if result.Message != "" {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.Message)
	}
	return nil
}

func worktreeCommandError(cmd *cobra.Command, jsonOutput bool, result actions.Result, err error) error {
	var partial *actions.PartialResultError
	if errors.As(err, &partial) {
		data := worktreeActionData{Action: result.Action, Changed: result.Changed, Message: result.Message, Data: result.Data}
		if jsonOutput {
			return cli.WriteJSONPartial(cmd, data, partial)
		}
		if result.Message != "" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), result.Message)
		}
		return partial
	}
	var actionErr *actions.Error
	if errors.As(err, &actionErr) {
		if !jsonOutput {
			return cli.ActionErrorText(actionErr)
		}
		candidates := make([]cli.Candidate, 0, len(actionErr.Candidates))
		for _, candidate := range actionErr.Candidates {
			candidates = append(candidates, cli.Candidate{ID: candidate.ID, DisplayName: candidate.DisplayName, CanonicalPath: candidate.CanonicalPath})
		}
		parameters := make([]cli.ParameterHelp, 0, len(actionErr.MissingParameters))
		for _, parameter := range actionErr.MissingParameters {
			parameters = append(parameters, cli.ParameterHelp{Name: parameter.Name, Description: parameter.Description, Required: parameter.Required})
		}
		return cli.WriteJSONError(cmd, cli.JSONError{Code: actionErr.Code, Message: actionErr.Message, Candidates: candidates, Parameters: parameters})
	}
	return cli.CommandFailure(cmd, jsonOutput, "action_failed", err)
}

func worktreeSelectorFromArgs(cmd *cobra.Command, jsonOutput bool, name string, args []string) (string, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return "", cli.MissingParameter(cmd, jsonOutput, "selector", "stable ID, unique ID prefix, branch, or canonical path")
	}
	if len(args) != 1 {
		return "", cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", fmt.Errorf("worktree %s accepts exactly one selector", name))
	}
	return args[0], nil
}

type worktreeListData struct {
	Worktrees []actions.WorktreeView `json:"worktrees"`
	Warnings  []string               `json:"warnings"`
}

type worktreeShowData struct {
	Worktree actions.WorktreeView `json:"worktree"`
	Warnings []string             `json:"warnings"`
}

type worktreeActionData struct {
	Action  actions.ID     `json:"action"`
	Changed bool           `json:"changed"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

func printWorktree(cmd *cobra.Command, view actions.WorktreeView) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "id: %s\n", view.ID)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "project: %s\n", displayWorktreeProject(view))
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "path: %s\n", view.Path)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "branch: %s\n", view.Branch)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "classification: %s\n", view.Classification)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "dirty: %t\n", view.Dirty)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "locked: %t\n", view.Locked)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "live_sessions: %d\n", len(view.LiveSessions))
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "active_loops: %d\n", view.LoopCount)
	writeWorktreeWarnings(cmd, view.Warnings)
}

func writeWorktreeWarnings(cmd *cobra.Command, warnings []string) {
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "warning: %s\n", warning)
	}
}

func displayWorktreeProject(view actions.WorktreeView) string {
	if view.ProjectLabel != "" {
		return view.ProjectLabel
	}
	return "-"
}

func worktreeState(view actions.WorktreeView) string {
	switch {
	case len(view.LiveSessions) > 0 || view.LoopCount > 0:
		return "active"
	case view.Dirty:
		return "dirty"
	case !view.GitAvailable:
		return "unavailable"
	default:
		return "clean"
	}
}

func nonNilWorktreeViews(values []actions.WorktreeView) []actions.WorktreeView {
	if values == nil {
		return []actions.WorktreeView{}
	}
	return values
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func init() {
	for _, command := range []*cobra.Command{worktreeListCmd, worktreeShowCmd, worktreeShellCmd, worktreeEditCmd} {
		command.Flags().Bool("json", false, "emit schema-v1 JSON")
	}
	worktreeReopenCmd.Flags().String("session", "", "live-session candidate identifier")
	worktreeReopenCmd.Flags().String("profile", "", "replacement profile for a fresh launch")
	_ = worktreeReopenCmd.RegisterFlagCompletionFunc("profile", cli.CompleteProfiles)
	worktreeReopenCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	worktreeRemoveCmd.Flags().Bool("yes", false, "confirm worktree removal")
	worktreeRemoveCmd.Flags().Bool("delete-branch", false, "also safely delete the merged worktree branch")
	worktreeRemoveCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	worktreeForgetCmd.Flags().Bool("yes", false, "confirm metadata forgetting")
	worktreeForgetCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	worktreeCmd.AddCommand(worktreeListCmd, worktreeShowCmd, worktreeReopenCmd, worktreeShellCmd, worktreeEditCmd, worktreeRemoveCmd, worktreeForgetCmd)
}
