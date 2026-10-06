// Package session holds the command surfaces that act on a conversation or on
// the Kitty tab carrying one.
//
// Two kinds of leaf live here and they are the same domain seen from two
// distances. The `amx session` family works on conversation identity — listing
// indexed conversations and continuing, forking, worktree-forking, or handing
// one off — and always resolves an exact (harness, session_id) before it acts.
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
	Short: "List, continue, fork, and hand off conversations",
}

func init() {
	sessionListCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	sessionListCmd.Flags().String("since", "", "only sessions active within this window, e.g. 12h or 7d")
	sessionListCmd.Flags().Bool("here", false, "only sessions started in the current directory or below it")
	sessionListCmd.Flags().StringSlice("harness", nil, "only sessions of this harness (repeatable)")
	_ = sessionListCmd.RegisterFlagCompletionFunc("harness", cli.CompleteHarnessFirstArg)
	sessionListCmd.Flags().StringSlice("profile", nil, "only sessions visible to this profile (repeatable)")
	_ = sessionListCmd.RegisterFlagCompletionFunc("profile", cli.CompleteProfiles)
	sessionListCmd.Flags().IntP("limit", "n", 0, "show at most this many of the most recent sessions (0 for all)")
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
	sessionCmd.AddCommand(sessionListCmd, sessionContinueCmd, sessionForkCmd, sessionForkWorktreeCmd, sessionHandoffCmd)
}
