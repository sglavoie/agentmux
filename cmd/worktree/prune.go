package worktree

import (
	"errors"
	"fmt"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/spf13/cobra"
)

// worktreePruneCmd removes every managed worktree that `remove` would accept
// without argument: present, valid, clean, unlocked, and in use by no live
// session or loop. It selects; it does not decide. Each candidate still goes
// through the WorktreeRemove action, which re-checks all of that against the
// live state at the moment it acts, so a worktree that became active between
// the listing and its turn is refused there rather than removed here.
//
// Without --yes it only previews, and that preview is a successful answer:
// listing what would go is the point of running it first.
var worktreePruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove every clean, inactive managed worktree (preview without --yes)",
	Example: `  amx worktree prune                    # preview
  amx worktree prune --merged --yes     # remove only worktrees whose branch is merged
  amx worktree prune --yes --delete-branch`,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		if len(args) != 0 {
			return cli.CommandFailure(cmd, jsonOutput, "invalid_arguments", errors.New("worktree prune accepts no arguments"))
		}
		confirmed, _ := cmd.Flags().GetBool("yes")
		mergedOnly, _ := cmd.Flags().GetBool("merged")
		deleteBranch, _ := cmd.Flags().GetBool("delete-branch")

		listed, err := executeWorktreeAction(cmd, actions.WorktreeList, actions.Request{})
		if err != nil {
			return worktreeCommandError(cmd, jsonOutput, listed, err)
		}
		views, _ := listed.Data["worktrees"].([]actions.WorktreeView)
		data := worktreePruneData{DryRun: !confirmed, Candidates: []string{}, Removed: []string{}, Failed: []worktreePruneFailure{}}
		var candidates []actions.WorktreeView
		for _, view := range views {
			if prunable(view, mergedOnly) {
				candidates = append(candidates, view)
				data.Candidates = append(data.Candidates, view.ID)
			}
		}
		out := cmd.OutOrStdout()
		if !confirmed {
			if jsonOutput {
				return cli.WriteJSON(cmd, data)
			}
			for _, view := range candidates {
				_, _ = fmt.Fprintf(out, "would remove %s\t%s\t%s\t%s\n", view.ID, displayWorktreeProject(view), view.Branch, view.Path)
			}
			if len(candidates) == 0 {
				_, _ = fmt.Fprintln(out, nothingToPrune(views))
				return nil
			}
			_, _ = fmt.Fprintf(out, "re-run with --yes to remove %d worktree(s)\n", len(candidates))
			return nil
		}

		parameters := map[string]string{}
		if deleteBranch {
			parameters["delete_branch"] = "true"
		}
		for _, view := range candidates {
			result, removeErr := executeWorktreeAction(cmd, actions.WorktreeRemove, actions.Request{
				Selector: view.ID, Parameters: parameters, Confirmed: true,
			})
			if removeErr != nil {
				data.Failed = append(data.Failed, worktreePruneFailure{ID: view.ID, Message: removeErr.Error()})
				if !jsonOutput {
					_, _ = fmt.Fprintf(out, "kept %s: %v\n", view.ID, removeErr)
				}
				continue
			}
			data.Removed = append(data.Removed, view.ID)
			if !jsonOutput && result.Message != "" {
				_, _ = fmt.Fprintln(out, result.Message)
			}
		}
		if !jsonOutput && len(candidates) == 0 {
			_, _ = fmt.Fprintln(out, nothingToPrune(views))
		}
		if jsonOutput {
			if err := cli.WriteJSON(cmd, data); err != nil {
				return err
			}
		}
		if len(data.Failed) > 0 {
			// Partial durable success: what was removed stays removed, and the
			// status says not everything was.
			return &cli.ExitError{Code: 1, Err: fmt.Errorf("%d of %d worktree(s) were kept", len(data.Failed), len(candidates)), Quiet: jsonOutput}
		}
		return nil
	},
}

// prunable is the selection rule. It mirrors what remove refuses so a preview
// does not promise removals that would then fail, but remove stays the
// authority.
// nothingToPrune says why a prune found nothing: there is nothing managed at
// all, or every worktree is kept for a reason the list shows.
func nothingToPrune(views []actions.WorktreeView) string {
	if len(views) == 0 {
		return "No managed worktrees to prune."
	}
	return "Nothing to prune; `amx worktree list` shows why each worktree is kept."
}

func prunable(view actions.WorktreeView, mergedOnly bool) bool {
	if !view.Exists || !view.Actionable || view.Classification != worktree.ClassificationValid {
		return false
	}
	if !view.GitAvailable || !view.StatusAvailable || view.Dirty || view.Locked {
		return false
	}
	if len(view.LiveSessions) > 0 || view.LoopCount > 0 {
		return false
	}
	if mergedOnly && (view.ReachableFromSource == nil || !*view.ReachableFromSource) {
		return false
	}
	return true
}

type worktreePruneData struct {
	DryRun     bool                   `json:"dry_run"`
	Candidates []string               `json:"candidates"`
	Removed    []string               `json:"removed"`
	Failed     []worktreePruneFailure `json:"failed"`
}

type worktreePruneFailure struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

func init() {
	worktreePruneCmd.Flags().Bool("yes", false, "remove the candidates instead of previewing them")
	worktreePruneCmd.Flags().Bool("merged", false, "only worktrees whose branch is reachable from its source branch")
	worktreePruneCmd.Flags().Bool("delete-branch", false, "also safely delete each removed worktree's merged branch")
	worktreePruneCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	worktreeCmd.AddCommand(worktreePruneCmd)
}
