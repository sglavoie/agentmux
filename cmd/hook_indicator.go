package cmd

import (
	"fmt"
	"os"

	ccerrors "github.com/sglavoie/agentmux/internal/errors"
	"github.com/sglavoie/agentmux/internal/indicator"
	"github.com/spf13/cobra"
)

var hookWindowID int

var hookIndicatorCmd = &cobra.Command{
	Use:   "hook-indicator <event>",
	Short: "Update Kitty tab state from a harness hook (internal)",
	Long: `Called by supported harness hooks to update the Kitty tab title and trigger
notifications based on the current session state.

The first argument must be a harness event name (e.g. Stop, PermissionRequest,
UserPromptSubmit). Unknown events are silently ignored.

Hooks that fire outside Kitty (for example from another Copilot or Codex CLI
surface sharing the same harness home) are treated as successful no-ops.

For hooks running inside Kitty, KITTY_WINDOW_ID must be set in the environment
(provided automatically by harness hooks), or supplied via --window-id.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		amxBin, err := os.Executable()
		if err != nil {
			ccerrors.Print(fmt.Errorf("resolving amx binary: %w", err))
			// Non-fatal: proceed without auto-start.
			amxBin = "amx"
		}
		return indicator.Run(args[0], hookWindowID, amxBin)
	},
}

func init() {
	hookIndicatorCmd.Flags().IntVar(&hookWindowID, "window-id", 0,
		"Kitty window ID (defaults to $KITTY_WINDOW_ID)")
}
