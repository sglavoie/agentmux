// Package notifications holds the `amx notifications` command family: the
// operator-facing surface that sends a real notification through the backend a
// real one would take — the connected companion app via the daemon's
// notification_test request, else Alerter from the CLI process — and reports
// which backend confirmed delivery, so a silent daemon can be told apart from
// a muted operating system, and that sets or clears the running daemon's
// in-memory snooze (`snooze 1h|until HH:MM|indefinite|off [--json]`).
//
// Snooze never auto-starts a daemon, since a snooze held by a daemon that was
// not running would change nothing. `test` sends from the CLI process rather
// than through the daemon, so it asks the daemon for the snooze first and
// reports a snoozed test as suppressed instead of sending past it.
package notifications

import (
	"errors"
	"fmt"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/spf13/cobra"
)

// Command returns the `notifications` command tree so the root command can
// register it. Root assembly is the only consumer.
func Command() *cobra.Command { return notificationsCmd }

var notificationsCmd = &cobra.Command{
	Use:   "notifications",
	Short: "Inspect and test native notifications",
	Long: `With no subcommand, says where a notification would go right now — the
connected Agent Mux app, Alerter as its fallback, or nothing — and whether a
snooze is holding it, as the running daemon reports. The daemon is never
started on your behalf.

With --json it reports {daemon_running, enabled, app, alerter_ready,
alerter_version, alerter_reason, snooze, summary}: summary holds the
sentences the text output prints.`,
	Example: `  amx notifications
  amx notifications --json
  amx notifications test
  amx notifications snooze 1h`,
	Args: cobra.NoArgs,
	RunE: runNotificationsStatus,
}

var notificationsTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Send a visible notification and confirm delivery",
	Long: `Sends one real notification through the backend a real one would take and
reports which backend confirmed it was shown. A connected Agent Mux app is
tried first, through the daemon; when no app confirms delivery, Alerter is
tried from this process, the same fallback a real notification takes.

A snooze held by the running daemon suppresses the test, as it would any
notification: the command fails and says how to lift it. An unreachable
daemon holds no snooze, so the test still runs and can only use Alerter.

With --json it reports {delivered, backend, app_reason}: backend is the one
that confirmed delivery, and app_reason, when present, is why the app did
not. A test that could not be delivered is a structured error.`,
	Example: `  amx notifications test
  amx notifications test --json`,
	Args: cobra.NoArgs,
	RunE: runNotificationsTest,
}

// notificationsTestJSON is `notifications test --json`.
var notificationsTestJSON bool

// notificationsTestData is the `notifications test --json` data. Delivered is
// always true on success: a test that was not confirmed is an error.
type notificationsTestData struct {
	Delivered bool   `json:"delivered"`
	Backend   string `json:"backend"`
	AppReason string `json:"app_reason,omitempty"`
}

func runNotificationsTest(cmd *cobra.Command, _ []string) error {
	jsonOutput := notificationsTestJSON
	// Progress lines are for a person; a JSON caller reads the one answer.
	say := func(format string, args ...any) error {
		if jsonOutput {
			return nil
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), format, args...)
		return err
	}
	cfg, err := notificationsLoadConfig()
	if err != nil {
		return cli.CommandFailure(cmd, jsonOutput, "config_error", err)
	}
	// The test sends from this process rather than through the daemon, so
	// it would sail past a snooze the daemon is holding. Ask first, and
	// report the suppression the daemon would apply. An unreachable daemon
	// holds no snooze, so the test proceeds as before.
	if state, snoozeErr := notificationsSnoozeClient().NotificationSnooze(protocol.SnoozeRequest{}); snoozeErr == nil && (state.Indefinite || state.SnoozedUntil != 0) {
		return cli.CommandFailure(cmd, jsonOutput, "notification_suppressed",
			fmt.Errorf("test notification suppressed: %s Run `amx notifications snooze off` to restore delivery", describeSnooze(state)))
	}
	// A connected companion app is the backend the daemon prefers, so it is
	// tested first, through the daemon. Alerter is tested only when no app
	// confirmed delivery — the same fallback a real notification takes.
	var data notificationsTestData
	if app, appErr := notificationsAppTest(); appErr == nil {
		if app.Delivered {
			data = notificationsTestData{Delivered: true, Backend: app.Backend}
			if jsonOutput {
				return cli.WriteJSON(cmd, data)
			}
			return say("Notification delivery confirmed via %s. Agent Mux notifications are working.\n", app.Backend)
		}
		if app.Reason != "" && app.Reason != "app_unavailable" {
			data.AppReason = app.Reason
			if err := say("The Agent Mux app did not confirm delivery (%s); falling back to Alerter.\n", app.Reason); err != nil {
				return err
			}
		}
	}
	if err := say("Sending a test notification through Alerter…\n"); err != nil {
		return err
	}

	result, err := notificationsTestDelivery(cli.CommandContext(cmd), &cfg.Defaults.Notifications)
	if errors.Is(err, notification.ErrNotificationDeliveryUnconfirmed) {
		return cli.CommandFailure(cmd, jsonOutput, "delivery_unconfirmed",
			errors.New("notification delivery could not be confirmed; in System Settings → Notifications → Terminal (the identity Alerter posts under), enable notifications and select Banners or Alerts, then turn off Focus and run `amx notifications test` again"))
	}
	if err != nil {
		return cli.CommandFailure(cmd, jsonOutput, "delivery_failed", err)
	}
	data.Delivered, data.Backend = true, result.Capability.Backend
	if jsonOutput {
		return cli.WriteJSON(cmd, data)
	}
	return say("Notification delivery confirmed via %s. Agent Mux notifications are working.\n", result.Capability.Backend)
}

var (
	notificationsLoadConfig   = cli.LoadRunnableConfig
	notificationsTestDelivery = notification.TestNotificationDelivery
	// notificationsAppTest asks the daemon to test the companion app backend.
	// An unreachable daemon is an error and leaves Alerter as the only test.
	notificationsAppTest = func() (protocol.NotificationTestResult, error) { return ipc.NewClient().NotificationTest() }
)

func init() {
	notificationsCmd.Flags().BoolVar(&notificationsStatusJSON, "json", false, "emit schema-v1 JSON")
	notificationsTestCmd.Flags().BoolVar(&notificationsTestJSON, "json", false, "emit schema-v1 JSON")
	notificationsCmd.AddCommand(notificationsTestCmd, notificationsSnoozeCmd)
}
