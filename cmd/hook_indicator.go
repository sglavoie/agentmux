package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	ccerrors "github.com/sglavoie/cc-manager-kitty/internal/errors"
	"github.com/sglavoie/cc-manager-kitty/internal/indicator"
)

var hookWindowID int

var hookIndicatorCmd = &cobra.Command{
	Use:   "hook-indicator <event>",
	Short: "Update Kitty tab state from a Claude Code hook (internal)",
	Long: `Called by Claude Code hooks to update the Kitty tab title and trigger
notifications based on the current session state.

The first argument must be a Claude Code event name (e.g. Stop, PermissionRequest,
UserPromptSubmit). Unknown events are silently ignored.

KITTY_WINDOW_ID must be set in the environment (provided automatically
by Claude Code hooks), or supplied via --window-id.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ccmBin, err := os.Executable()
		if err != nil {
			ccerrors.Print(fmt.Errorf("resolving ccm binary: %w", err))
			// Non-fatal: proceed without auto-start.
			ccmBin = "ccm"
		}
		return indicator.Run(args[0], hookWindowID, ccmBin)
	},
}

func init() {
	hookIndicatorCmd.Flags().IntVar(&hookWindowID, "window-id", 0,
		"Kitty window ID (defaults to $KITTY_WINDOW_ID)")
}
