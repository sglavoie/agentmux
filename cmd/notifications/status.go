package notifications

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/spf13/cobra"
)

// notificationsHealth asks the running daemon for its health. It never starts
// one: a status that started the daemon it reports on would describe a world
// the operator did not have.
var notificationsHealth = func() ipc.Health { return ipc.NewClient().Health() }

// runNotificationsStatus is what a bare `amx notifications` prints: where a
// notification would go right now and whether a snooze is holding it, ahead
// of the subcommands, which are the next thing the operator reaches for.
func runNotificationsStatus(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	for _, line := range notificationsStatusLines(notificationsHealth()) {
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(out, "\n"+cmd.UsageString())
	return err
}

// notificationsStatusLines says, in sentences, which backend delivers a
// notification — the connected app first, Alerter as its fallback, as the
// daemon chooses — and the snooze in force.
func notificationsStatusLines(health ipc.Health) []string {
	if !health.Alive {
		return []string{"The daemon is not running, so no notification is delivered; `amx daemon start` starts it."}
	}
	n := health.Notification
	if n == nil {
		return []string{"The running daemon is too old to report notifications; `amx daemon restart` updates it."}
	}
	if !n.Enabled {
		return []string{"Notifications are off: every kind under [defaults.notifications] is turned off; `amx config edit` turns one back on."}
	}
	alerter := "Alerter"
	if n.Version != "" {
		alerter += " " + n.Version
	}
	alerterReady := n.Available && n.Interactive
	alerterWhy := ""
	if n.Reason != "" {
		alerterWhy = " (" + n.Reason + ")"
	}
	app := notificationsApp(health.Companion)
	var backend string
	switch {
	case app != "" && alerterReady:
		backend = fmt.Sprintf("Notifications go to %s, with %s as the fallback.", app, alerter)
	case app != "":
		backend = fmt.Sprintf("Notifications go to %s; Alerter is unavailable as a fallback%s.", app, alerterWhy)
	case alerterReady:
		backend = fmt.Sprintf("Notifications go through %s; no Agent Mux app is connected.", alerter)
	default:
		backend = fmt.Sprintf("Nothing can deliver notifications: Alerter is unavailable%s and no Agent Mux app is connected.", alerterWhy)
	}
	return []string{backend, describeSnooze(protocol.SnoozeState{Indefinite: n.SnoozeIndefinite, SnoozedUntil: n.SnoozedUntil})}
}

// notificationsApp names the subscriber holding the notification backend
// role, or "" when none does. The daemon gives the role to the newest
// claimant, so the latest-connected claimant is the one named.
func notificationsApp(c *protocol.CompanionHealth) string {
	if c == nil || !c.NotificationBackendConnected {
		return ""
	}
	name := "the Agent Mux app"
	var newest int64 = -1
	for _, client := range c.Clients {
		if !client.NotificationBackend || client.ConnectedAt < newest {
			continue
		}
		newest = client.ConnectedAt
		switch {
		case client.Client == "":
			name = "the Agent Mux app"
		case client.ClientVersion != "":
			name = client.Client + " " + client.ClientVersion
		default:
			name = client.Client
		}
	}
	return name
}
