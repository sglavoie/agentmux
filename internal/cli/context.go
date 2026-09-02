package cli

import (
	"context"

	"github.com/spf13/cobra"
)

// CommandContext is the context a command's work runs under: the one Cobra
// attached to the invocation when there is one, and a background context when
// the command was driven directly (as tests do) and never got one.
func CommandContext(cmd *cobra.Command) context.Context {
	if cmd != nil && cmd.Context() != nil {
		return cmd.Context()
	}
	return context.Background()
}
