// Package notifications holds the `amx notifications` command family: the
// operator-facing surface that sends a real notification through the configured
// backend and reports whether delivery could be confirmed, so a silent daemon
// can be told apart from a muted operating system, and that sets or clears the
// running daemon's in-memory snooze.
package notifications

import (
	"errors"
	"fmt"

	"github.com/sglavoie/agentmux/internal/cli"
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
}

var notificationsTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Send a visible notification and confirm delivery",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := notificationsLoadConfig()
		if err != nil {
			return err
		}
		// The test sends from this process rather than through the daemon, so
		// it would sail past a snooze the daemon is holding. Ask first, and
		// report the suppression the daemon would apply. An unreachable daemon
		// holds no snooze, so the test proceeds as before.
		if state, snoozeErr := notificationsSnoozeClient().NotificationSnooze(protocol.SnoozeRequest{}); snoozeErr == nil && (state.Indefinite || state.SnoozedUntil != 0) {
			return fmt.Errorf("test notification suppressed: %s Run `amx notifications snooze off` to restore delivery", describeSnooze(state))
		}
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Sending a test notification through Alerter…"); err != nil {
			return err
		}

		result, err := notificationsTestDelivery(cli.CommandContext(cmd), &cfg.Defaults.Notifications)
		if errors.Is(err, notification.ErrNotificationDeliveryUnconfirmed) {
			return errors.New("notification delivery could not be confirmed; in System Settings → Notifications → alerter, enable notifications and select Banners or Alerts, then turn off Focus and run `amx notifications test` again")
		}
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Notification delivery confirmed via %s. Agent Mux notifications are working.\n", result.Capability.Backend)
		return err
	},
}

var (
	notificationsLoadConfig   = cli.LoadRunnableConfig
	notificationsTestDelivery = notification.TestNotificationDelivery
)

func init() {
	notificationsCmd.AddCommand(notificationsTestCmd, notificationsSnoozeCmd)
}
