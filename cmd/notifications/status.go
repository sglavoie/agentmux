package notifications

import (
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/cli"
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
	health := notificationsHealth()
	if notificationsStatusJSON {
		return cli.WriteJSON(cmd, notificationsStatusDataFrom(health))
	}
	out := cmd.OutOrStdout()
	for _, line := range notificationsStatusLines(health) {
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, notificationsSubcommandHint(cmd))
	return err
}

// notificationsSubcommandHint is the one line that points from the status to
// the subcommands, rather than the whole usage, which a status piped into
// another program has no use for.
func notificationsSubcommandHint(cmd *cobra.Command) string {
	var names []string
	for _, sub := range cmd.Commands() {
		if sub.IsAvailableCommand() {
			names = append(names, sub.Name())
		}
	}
	return fmt.Sprintf("Subcommands: %s; `%s --help` describes them.", strings.Join(names, ", "), cmd.CommandPath())
}

// notificationsStatusJSON is a bare `amx notifications --json`.
var notificationsStatusJSON bool

// notificationsStatusData is the bare `amx notifications --json` data: the
// facts notificationsStatusLines puts in sentences, and the sentences
// themselves as summary. Every field past daemon_running is zero when the
// daemon is not running or too old to report notifications.
type notificationsStatusData struct {
	DaemonRunning  bool          `json:"daemon_running"`
	Enabled        bool          `json:"enabled"`
	App            string        `json:"app,omitempty"`
	AlerterReady   bool          `json:"alerter_ready"`
	AlerterVersion string        `json:"alerter_version,omitempty"`
	AlerterReason  string        `json:"alerter_reason,omitempty"`
	Snooze         snoozePayload `json:"snooze"`
	Summary        []string      `json:"summary"`
}

func notificationsStatusDataFrom(health ipc.Health) notificationsStatusData {
	data := notificationsStatusData{DaemonRunning: health.Alive, Summary: notificationsStatusLines(health)}
	n := health.Notification
	if !health.Alive || n == nil {
		return data
	}
	data.Enabled = n.Enabled
	data.App = notificationsApp(health.Companion)
	data.AlerterReady = n.Available && n.Interactive
	data.AlerterVersion, data.AlerterReason = n.Version, n.Reason
	data.Snooze = snoozePayloadOf(protocol.SnoozeState{Indefinite: n.SnoozeIndefinite, SnoozedUntil: n.SnoozedUntil})
	return data
}

// notificationsStatusLines says, in sentences, which backend delivers a
// notification — the connected app first, Alerter as its fallback, as the
// daemon chooses — and the snooze in force.
func notificationsStatusLines(health ipc.Health) []string {
	if !health.Alive {
		return []string{"The daemon is not running, so no notification is delivered; " + cli.DaemonRestartAdvice + "."}
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
