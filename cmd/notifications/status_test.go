package notifications

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestNotificationsStatusNamesTheBackendAndTheSnooze(t *testing.T) {
	ready := &protocol.NotificationCapability{Enabled: true, Available: true, Interactive: true, Backend: "alerter", Version: "26.5"}
	missing := &protocol.NotificationCapability{Enabled: true, Backend: "alerter", Reason: "alerter not found"}
	snoozed := *ready
	snoozed.SnoozeIndefinite = true
	app := &protocol.CompanionHealth{Subscribers: 2, NotificationBackendConnected: true, Clients: []protocol.CompanionClient{
		{Client: "Agent Mux.app", ClientVersion: "0.2.0", NotificationBackend: true, ConnectedAt: 20},
		{Client: "Agent Mux.app", ClientVersion: "0.1.0", NotificationBackend: true, ConnectedAt: 10},
		{Client: "watcher", ConnectedAt: 30},
	}}

	for _, tc := range []struct {
		name   string
		health ipc.Health
		want   []string
	}{
		{"daemon down", ipc.Health{}, []string{"The daemon is not running, so no notification is delivered; run `amx daemon restart` to start it in the background."}},
		{"old daemon", ipc.Health{Alive: true}, []string{"The running daemon is too old to report notifications; `amx daemon restart` updates it."}},
		{"all kinds off", ipc.Health{Alive: true, Notification: &protocol.NotificationCapability{Backend: "alerter", Reason: "disabled"}}, []string{
			"Notifications are off: every kind under [defaults.notifications] is turned off; `amx config edit` turns one back on."}},
		{"app with fallback", ipc.Health{Alive: true, Notification: ready, Companion: app}, []string{
			"Notifications go to Agent Mux.app 0.2.0, with Alerter 26.5 as the fallback.", "Notifications are not snoozed."}},
		{"app without fallback", ipc.Health{Alive: true, Notification: missing, Companion: app}, []string{
			"Notifications go to Agent Mux.app 0.2.0; Alerter is unavailable as a fallback (alerter not found).", "Notifications are not snoozed."}},
		{"alerter only", ipc.Health{Alive: true, Notification: ready, Companion: &protocol.CompanionHealth{}}, []string{
			"Notifications go through Alerter 26.5; no Agent Mux app is connected.", "Notifications are not snoozed."}},
		{"nothing", ipc.Health{Alive: true, Notification: missing}, []string{
			"Nothing can deliver notifications: Alerter is unavailable (alerter not found) and no Agent Mux app is connected.", "Notifications are not snoozed."}},
		{"snoozed", ipc.Health{Alive: true, Notification: &snoozed}, []string{
			"Notifications go through Alerter 26.5; no Agent Mux app is connected.", "Notifications are snoozed until turned off."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := notificationsStatusLines(tc.health)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// A bare `amx notifications` answers the question the family exists for and
// still lists the subcommands, rather than printing only help.
func TestBareNotificationsPrintsStatusThenSubcommands(t *testing.T) {
	old := notificationsHealth
	t.Cleanup(func() { notificationsHealth = old })
	notificationsHealth = func() ipc.Health {
		return ipc.Health{Alive: true, Uptime: time.Minute, Notification: &protocol.NotificationCapability{Enabled: true, Available: true, Interactive: true, Backend: "alerter"}}
	}
	var out bytes.Buffer
	notificationsCmd.SetOut(&out)
	t.Cleanup(func() { notificationsCmd.SetOut(nil) })
	if err := notificationsCmd.RunE(notificationsCmd, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "Notifications go through Alerter; no Agent Mux app is connected.\nNotifications are not snoozed.\n\nUsage:") {
		t.Fatalf("output starts wrong:\n%s", got)
	}
	for _, leaf := range []string{"snooze", "test"} {
		if !strings.Contains(got, leaf) {
			t.Fatalf("output does not list %q:\n%s", leaf, got)
		}
	}
}
