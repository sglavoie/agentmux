package worktree

import (
	"io"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/spf13/cobra"
)

// worktreeDiffCmd reviews one managed worktree: what it changed since it left
// its recorded source branch, and what bringing that work back would take. It
// reads and never writes, so it needs no confirmation and is as safe on a
// worktree an agent is working in as on an idle one. Whatever the verdict, a
// review that could be made is a successful answer.
var worktreeDiffCmd = &cobra.Command{
	Use:   "diff <selector>",
	Short: "Review what a managed worktree changed and how it would land",
	Example: `  amx worktree diff 3f9c2a1e             # summary, files, and landing verdict
  amx worktree diff 3f9c2a1e --patch      # followed by the unified diff
  amx worktree diff 3f9c2a1e --committed  # leave uncommitted changes out
  amx worktree diff 3f9c2a1e --json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		selector, err := worktreeSelectorFromArgs(cmd, jsonOutput, "diff", args)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		parameters := make(map[string]string)
		if committed, _ := cmd.Flags().GetBool("committed"); committed {
			parameters["committed"] = "true"
		}
		if patch, _ := cmd.Flags().GetBool("patch"); patch {
			parameters["patch"] = "true"
			if !jsonOutput && worktreeListIsTerminal(out) {
				parameters["color"] = "true"
			}
		}
		result, actionErr := executeWorktreeAction(cmd, actions.WorktreeDiff, actions.Request{Selector: selector, Parameters: parameters})
		if actionErr != nil {
			return worktreeCommandError(cmd, jsonOutput, result, actionErr)
		}
		review, _ := result.Data["review"].(worktree.Review)
		if jsonOutput {
			return cli.WriteJSON(cmd, worktreeDiffData{Review: review})
		}
		_, err = io.WriteString(out, review.Text())
		return err
	},
}

type worktreeDiffData struct {
	Review worktree.Review `json:"review"`
}

func init() {
	worktreeDiffCmd.Flags().Bool("committed", false, "leave uncommitted changes out")
	worktreeDiffCmd.Flags().Bool("patch", false, "also print the unified diff")
	worktreeDiffCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	worktreeDiffCmd.ValidArgsFunction = completeWorktreeSelectors
	worktreeCmd.AddCommand(worktreeDiffCmd)
}
