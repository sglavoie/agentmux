// Package session holds the command surfaces that act on a conversation or on
// the Kitty tab carrying one.
//
// Two kinds of leaf live here and they are the same domain seen from two
// distances. The `amx session` family works on conversation identity (list,
// search, show, continue, fork, fork-worktree, handoff) and always resolves an
// exact (harness, session_id) before it acts; `search` runs the same
// transcript.Search Past Sessions does, and it and `list` share one set of
// selector flags. The root-level tab controls work on the live instance
// instead: focus-prev-managed and focus-next-managed, focus-next (the next tab
// that wants attention), mute-toggle on the focused tab, and resort into the
// configured order. Both halves reduce to "which session, and what happens to
// it".
//
// `mute-toggle --json` reports {window_id, muted, changed} for the companion
// app; a mute whose title write failed is partial durable success.
//
// A file in this package may name the imported internal/session package
// unqualified as `session`: a package's own name is not in scope inside its
// files.
package session

import (
	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/cli"
)

// Command returns the `session` command tree so the root command can register
// it. Root assembly is the only consumer, as it is for the tab-control
// accessors beside it.
func Command() *cobra.Command { return sessionCmd }

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "List, search, show, continue, fork, and hand off conversations",
}

func init() {
	sessionListCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	addSessionSelectionFlags(sessionListCmd)
	sessionSearchCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionSearchCmd.Flags().String("source", "any", "match text from: any, user (your prompts), or harness (the agent's output)")
	_ = sessionSearchCmd.RegisterFlagCompletionFunc("source", cobra.FixedCompletions([]string{"any", "user", "harness"}, cobra.ShellCompDirectiveNoFileComp))
	addSessionSelectionFlags(sessionSearchCmd)
	sessionShowCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionShowCmd.Flags().Int("last", 0, "also print the last N user and assistant messages, oldest first")
	sessionContinueCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionContinueCmd.Flags().Bool("here", false, "continue the most recently active conversation started in the current directory or below it")
	sessionForkCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionForkCmd.Flags().Bool("here", false, "fork the most recently active conversation started in the current directory or below it")
	sessionForkWorktreeCmd.Flags().Bool("allow-dirty", false, "allow a dirty source; uncommitted changes are not copied")
	sessionForkWorktreeCmd.Flags().Bool("yes", false, "confirm creating the isolated fork")
	sessionForkWorktreeCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionHandoffCmd.Flags().String("profile", "", "target configured profile")
	_ = sessionHandoffCmd.RegisterFlagCompletionFunc("profile", cli.CompleteProfiles)
	sessionHandoffCmd.Flags().Bool("edit", false, "edit the generated handoff before launch")
	sessionHandoffCmd.Flags().Bool("no-launch", false, "finalize the handoff without launching a target")
	sessionHandoffCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionCmd.AddCommand(sessionListCmd, sessionSearchCmd, sessionShowCmd, sessionContinueCmd, sessionForkCmd, sessionForkWorktreeCmd, sessionHandoffCmd)
}
