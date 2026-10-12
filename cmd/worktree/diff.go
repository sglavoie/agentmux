package worktree

import (
	"fmt"
	"io"
	"strings"

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
	Example: `  amx worktree diff wt-1a2b              # summary, files, and landing verdict
  amx worktree diff wt-1a2b --patch      # followed by the unified diff
  amx worktree diff wt-1a2b --committed  # leave uncommitted changes out
  amx worktree diff wt-1a2b --json`,
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
		return writeWorktreeReview(out, review)
	},
}

type worktreeDiffData struct {
	Review worktree.Review `json:"review"`
}

func writeWorktreeReview(out io.Writer, review worktree.Review) error {
	var b strings.Builder
	branch := review.Branch
	if branch == "" {
		branch = "detached HEAD"
	}
	facts := []string{
		cli.CountOf(review.Ahead, "commit", "commits") + " ahead",
		fmt.Sprintf("%d behind", review.Behind),
	}
	if review.Dirty {
		facts = append(facts, "uncommitted changes")
	}
	fmt.Fprintf(&b, "%s against %s: %s\n", branch, review.SourceBranch, strings.Join(facts, ", "))
	fmt.Fprintf(&b, "lands: %s\n", landsText(review))

	width := 0
	for _, file := range review.Files {
		width = max(width, len(file.Path))
	}
	for _, file := range review.Files {
		if file.Binary {
			fmt.Fprintf(&b, " %-*s | binary\n", width, file.Path)
			continue
		}
		fmt.Fprintf(&b, " %-*s | +%d -%d\n", width, file.Path, file.Added, file.Deleted)
	}
	switch {
	case len(review.Files) > 0:
		fmt.Fprintf(&b, "%s changed, +%d -%d\n", cli.CountOf(len(review.Files), "file", "files"), review.Added, review.Deleted)
	case review.CommittedOnly:
		b.WriteString("no committed changes\n")
	default:
		b.WriteString("no changes to tracked files\n")
	}
	for _, path := range review.Untracked {
		fmt.Fprintf(&b, "untracked: %s\n", path)
	}
	for _, path := range review.Conflicts {
		fmt.Fprintf(&b, "conflict: %s\n", path)
	}
	for _, warning := range review.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", warning)
	}
	if review.Patch != "" {
		b.WriteString("\n")
		b.WriteString(review.Patch)
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// landsText says the verdict in words. It speaks of committed work only, which
// is all a landing could carry.
func landsText(review worktree.Review) string {
	switch review.Lands {
	case worktree.LandsNothing:
		if review.Dirty {
			return "nothing committed to land"
		}
		return "nothing to land"
	case worktree.LandsFastForward:
		return "fast-forward onto " + review.SourceBranch
	case worktree.LandsDivergedClean:
		return "merges with " + review.SourceBranch + " without conflict"
	case worktree.LandsConflicts:
		return fmt.Sprintf("conflicts with %s in %s", review.SourceBranch, cli.CountOf(len(review.Conflicts), "file", "files"))
	default:
		return "could not be predicted"
	}
}

func init() {
	worktreeDiffCmd.Flags().Bool("committed", false, "leave uncommitted changes out")
	worktreeDiffCmd.Flags().Bool("patch", false, "also print the unified diff")
	worktreeDiffCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	worktreeDiffCmd.ValidArgsFunction = completeWorktreeSelectors
	worktreeCmd.AddCommand(worktreeDiffCmd)
}
