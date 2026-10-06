// Package session holds the command surfaces that act on a conversation or on
// the Kitty tab carrying one.
//
// Two kinds of leaf live here and they are the same domain seen from two
// distances. The `amx session` family works on conversation identity — listing,
// searching, and showing indexed conversations and continuing, forking,
// worktree-forking, or handing one off — and always resolves an exact
// (harness, session_id) before it acts.
// The root-level tab controls work on the live instance instead: focusing the
// previous or next managed tab, focusing the next tab that wants attention,
// toggling mute on the focused tab, and resorting tabs into the configured
// order. Both halves reduce to "which session, and what happens to it".
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
	sessionContinueCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionForkCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
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
