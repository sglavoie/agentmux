package worktree

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/spf13/cobra"
)

// worktreeLandCmd brings a worktree's committed work onto the source branch
// it was forked from, by fast-forward only. It moves a branch the operator
// works on, so it takes --yes like every other worktree mutation; and it
// never merges or resolves anything, so a source branch that has moved on is
// refused with the way forward rather than landed some other way. `amx
// worktree diff` says beforehand which of those it will be.
var worktreeLandCmd = &cobra.Command{
	Use:   "land <selector>",
	Short: "Fast-forward the source branch to a clean inactive worktree's branch",
	Example: `  amx worktree diff 3f9c2a1e           # "lands: fast-forward onto main" means this will succeed
  amx worktree land 3f9c2a1e --yes
  amx worktree land 3f9c2a1e --yes --remove   # then remove the worktree and delete its branch`,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		selector, err := worktreeSelectorFromArgs(cmd, jsonOutput, "land", args)
		if err != nil {
			return err
		}
		confirmed, _ := cmd.Flags().GetBool("yes")
		parameters := make(map[string]string)
		if remove, _ := cmd.Flags().GetBool("remove"); remove {
			parameters["remove"] = "true"
		}
		result, actionErr := executeWorktreeAction(cmd, actions.WorktreeLand, actions.Request{Selector: selector, Parameters: parameters, Confirmed: confirmed})
		// The landing is said first in both endings: when the removal that
		// followed it failed, the source branch has still moved.
		if landing, ok := result.Data["landing"].(worktree.LandResult); ok && !jsonOutput {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s, %s..%s\n", landing.SourceBranch, cli.CountOf(landing.Commits, "commit", "commits"), shortRevision(landing.From), shortRevision(landing.To))
		}
		if actionErr != nil {
			return worktreeCommandError(cmd, jsonOutput, result, actionErr)
		}
		return writeWorktreeActionResult(cmd, jsonOutput, result)
	},
}

func shortRevision(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

func init() {
	worktreeLandCmd.Flags().Bool("yes", false, "confirm moving the source branch")
	worktreeLandCmd.Flags().Bool("remove", false, "then remove the worktree and delete its landed branch")
	worktreeLandCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	worktreeLandCmd.ValidArgsFunction = completeWorktreeSelectors
	worktreeCmd.AddCommand(worktreeLandCmd)
}
