package notifications

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/spf13/cobra"
)

// Every notifications command that answers a machine caller declares --json
// and shares the retained schema-v1 envelope: the bare status, the snooze, and
// the test, whose answer is which backend confirmed delivery.
func TestNotificationsJSONCommandSurfaceIsVersioned(t *testing.T) {
	for _, command := range []*cobra.Command{notificationsCmd, notificationsSnoozeCmd, notificationsTestCmd} {
		if command.Flags().Lookup("json") == nil {
			t.Errorf("%s does not declare --json", command.CommandPath())
		}
	}
	if cli.JSONSchemaVersion != 1 {
		t.Fatalf("unexpected JSON schema version %d", cli.JSONSchemaVersion)
	}
}

func runNotificationsStatusJSON(t *testing.T, health ipc.Health) string {
	t.Helper()
	old, oldJSON := notificationsHealth, notificationsStatusJSON
	t.Cleanup(func() {
		notificationsHealth, notificationsStatusJSON = old, oldJSON
		notificationsCmd.SetOut(nil)
	})
	notificationsHealth = func() ipc.Health { return health }
	notificationsStatusJSON = true
	var out bytes.Buffer
	notificationsCmd.SetOut(&out)
	if err := notificationsCmd.RunE(notificationsCmd, nil); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out.String())
}

func TestNotificationsStatusJSON(t *testing.T) {
	if got, want := runNotificationsStatusJSON(t, ipc.Health{}),
		`{"schema_version":1,"data":{"daemon_running":false,"enabled":false,"alerter_ready":false,"snooze":{"snoozed":false,"indefinite":false},`+
			`"summary":["The daemon is not running, so no notification is delivered; run `+"`amx daemon restart`"+` to start it in the background."]}}`; got != want {
		t.Fatalf("daemon down:\n got %s\nwant %s", got, want)
	}
	health := ipc.Health{Alive: true, Notification: &protocol.NotificationCapability{
		Enabled: true, Available: true, Interactive: true, Backend: "alerter", Version: "26.5", SnoozeIndefinite: true,
	}}
	if got, want := runNotificationsStatusJSON(t, health),
		`{"schema_version":1,"data":{"daemon_running":true,"enabled":true,"alerter_ready":true,"alerter_version":"26.5","snooze":{"snoozed":true,"indefinite":true},`+
			`"summary":["Notifications go through Alerter 26.5; no Agent Mux app is connected.","Notifications are snoozed until turned off."]}}`; got != want {
		t.Fatalf("snoozed:\n got %s\nwant %s", got, want)
	}
}

func runNotificationsTestJSON(t *testing.T) (string, error) {
	t.Helper()
	notificationsTestJSON = true
	t.Cleanup(func() { notificationsTestJSON = false })
	var out bytes.Buffer
	notificationsTestCmd.SetOut(&out)
	err := notificationsTestCmd.RunE(notificationsTestCmd, nil)
	return strings.TrimSpace(out.String()), err
}

// The JSON test prints none of the progress lines a person sees: stdout is the
// one envelope, carrying the backend that confirmed delivery and why the app
// did not when Alerter had to.
func TestNotificationsTestJSON(t *testing.T) {
	resetNotificationsTestState(t)
	notificationsLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	notificationsAppTest = func() (protocol.NotificationTestResult, error) {
		return protocol.NotificationTestResult{Reason: "app_unconfirmed"}, nil
	}
	notificationsTestDelivery = func(context.Context, *config.NotificationConfig) (notification.NotificationDeliveryTestResult, error) {
		return notification.NotificationDeliveryTestResult{Capability: protocol.NotificationCapability{Backend: "alerter"}}, nil
	}
	out, err := runNotificationsTestJSON(t)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"schema_version":1,"data":{"delivered":true,"backend":"alerter","app_reason":"app_unconfirmed"}}`; out != want {
		t.Fatalf("json = %s\nwant   %s", out, want)
	}

	notificationsAppTest = func() (protocol.NotificationTestResult, error) {
		return protocol.NotificationTestResult{Backend: "app", Delivered: true}, nil
	}
	if out, err = runNotificationsTestJSON(t); err != nil || out != `{"schema_version":1,"data":{"delivered":true,"backend":"app"}}` {
		t.Fatalf("app delivery: out = %s, err = %v", out, err)
	}
}

func TestNotificationsTestJSONFailuresUseTheErrorEnvelope(t *testing.T) {
	resetNotificationsTestState(t)
	notificationsLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	notificationsTestDelivery = func(context.Context, *config.NotificationConfig) (notification.NotificationDeliveryTestResult, error) {
		return notification.NotificationDeliveryTestResult{}, notification.ErrNotificationDeliveryUnconfirmed
	}
	out, err := runNotificationsTestJSON(t)
	var exit *cli.ExitError
	if !errors.As(err, &exit) || !strings.Contains(out, `"code":"delivery_unconfirmed"`) {
		t.Fatalf("unconfirmed: out = %s, err = %v", out, err)
	}

	notificationsSnoozeClient = func() snoozeClient {
		return &fakeSnoozeClient{state: protocol.SnoozeState{Indefinite: true}}
	}
	if out, err = runNotificationsTestJSON(t); !errors.As(err, &exit) || !strings.Contains(out, `"code":"notification_suppressed"`) {
		t.Fatalf("snoozed: out = %s, err = %v", out, err)
	}
}
