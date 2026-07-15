package cmd

import (
	"fmt"
	"os"

	ccerrors "github.com/sglavoie/agentmux/internal/errors"
	"github.com/sglavoie/agentmux/internal/indicator"
	"github.com/spf13/cobra"
)

var hookWindowID int
var hookHarness string

var runHookIndicator = indicator.Run

var hookIndicatorCmd = &cobra.Command{
	Use:   "hook-indicator <event>",
	Short: "Forward harness hook state to the daemon (internal)",
	Long: `Called by supported harness hooks to forward raw hook events to the daemon.
The daemon updates Kitty tab state and triggers notifications based on the
current session state.

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
		if err := runHookIndicator(args[0], hookWindowID, hookHarness, amxBin); err != nil {
			ccerrors.Print(fmt.Errorf("processing hook event: %w", err))
		}
		return nil
	},
}

func init() {
	hookIndicatorCmd.Flags().IntVar(&hookWindowID, "window-id", 0,
		"Kitty window ID (defaults to $KITTY_WINDOW_ID)")
	hookIndicatorCmd.Flags().StringVar(&hookHarness, "harness", "",
		"event-producing harness (claude, codex, or copilot)")
}
