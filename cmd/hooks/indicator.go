package hooks

import (
	"encoding/json"
	"fmt"

	"github.com/sglavoie/agentmux/internal/cli"
	ccerrors "github.com/sglavoie/agentmux/internal/errors"
	"github.com/sglavoie/agentmux/internal/indicator"
	"github.com/spf13/cobra"
)

var hookWindowID int
var hookHarness string
var hookRequestID string
var hookJSON bool
var hookStrict bool

var runHookIndicator = indicator.Deliver

// IndicatorCommand returns the top-level `hook-indicator` command so the root
// command can register it. Root assembly is the only consumer: harness hooks
// reach it through the binary, never through another command family.
func IndicatorCommand() *cobra.Command { return hookIndicatorCmd }

var hookIndicatorCmd = &cobra.Command{
	Use:   "hook-indicator <event>",
	Short: "Forward harness hook state to the daemon (internal)",
	Long: `Called by supported harness hooks to forward raw hook events to the daemon.
The daemon updates Kitty tab state and triggers notifications based on the
current session state.

The first argument must be a harness event name (e.g. Stop, PermissionRequest,
UserPromptSubmit). Unknown events are recorded as permanent non-delivery
evidence while legacy hook invocations remain fail-open.

Hooks that fire outside Kitty (for example from another Copilot or Codex CLI
surface sharing the same harness home) are treated as successful no-ops.

For hooks running inside Kitty, KITTY_WINDOW_ID must be set in the environment
(provided automatically by harness hooks), or supplied via --window-id.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Non-fatal: on failure AmxBinary yields the bare command name and the
		// hook proceeds without a resolvable auto-start binary.
		amxBin, err := cli.AmxBinary()
		if err != nil {
			ccerrors.Print(err)
		}
		result := runHookIndicator(args[0], hookWindowID, hookHarness, amxBin, indicator.DeliveryOptions{
			RequestID: hookRequestID,
		})
		if hookJSON {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
				return fmt.Errorf("encoding hook delivery result: %w", err)
			}
		}
		if hookStrict && !result.Successful() {
			code := 2
			if result.Retryable {
				code = 75
			}
			return &cli.ExitError{
				Code:  code,
				Err:   fmt.Errorf("hook delivery %s: %s", result.Status, result.Reason),
				Quiet: hookJSON,
			}
		}
		return nil
	},
}

func init() {
	hookIndicatorCmd.Flags().IntVar(&hookWindowID, "window-id", 0,
		"Kitty window ID (defaults to $KITTY_WINDOW_ID)")
	hookIndicatorCmd.Flags().StringVar(&hookHarness, "harness", "",
		"event-producing harness (claude, codex, or copilot)")
	hookIndicatorCmd.Flags().StringVar(&hookRequestID, "request-id", "",
		"stable delivery request ID (generated when omitted)")
	hookIndicatorCmd.Flags().BoolVar(&hookJSON, "json", false,
		"write the structured delivery result as JSON")
	hookIndicatorCmd.Flags().BoolVar(&hookStrict, "strict", false,
		"return 75 for retryable failure or 2 for permanent non-delivery")
}
