package notifications

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func resetNotificationsTestState(t *testing.T) {
	t.Helper()
	oldLoadConfig := notificationsLoadConfig
	oldTestDelivery := notificationsTestDelivery
	oldSnoozeClient := notificationsSnoozeClient
	oldNow := notificationsNow
	oldAppTest := notificationsAppTest
	oldOut := notificationsTestCmd.OutOrStdout()
	oldSnoozeOut := notificationsSnoozeCmd.OutOrStdout()
	oldSnoozeJSON := snoozeJSON
	oldTestJSON := notificationsTestJSON
	// No test may reach the operator's daemon: by default the daemon is
	// unreachable, which is what a missing daemon looks like.
	notificationsSnoozeClient = func() snoozeClient { return &fakeSnoozeClient{err: errors.New("dial unix: no such file")} }
	notificationsAppTest = func() (protocol.NotificationTestResult, error) {
		return protocol.NotificationTestResult{}, errors.New("dial unix: no such file")
	}
	t.Cleanup(func() {
		notificationsAppTest = oldAppTest
		notificationsLoadConfig = oldLoadConfig
		notificationsTestDelivery = oldTestDelivery
		notificationsSnoozeClient = oldSnoozeClient
		notificationsNow = oldNow
		notificationsTestCmd.SetOut(oldOut)
		notificationsSnoozeCmd.SetOut(oldSnoozeOut)
		snoozeJSON = oldSnoozeJSON
		notificationsTestJSON = oldTestJSON
	})
}

func TestNotificationsTestCommandConfirmsDelivery(t *testing.T) {
	resetNotificationsTestState(t)
	notificationsLoadConfig = func() (*config.Config, error) {
		return &config.Config{}, nil
	}
	notificationsTestDelivery = func(context.Context, *config.NotificationConfig) (notification.NotificationDeliveryTestResult, error) {
		return notification.NotificationDeliveryTestResult{
			Capability:  protocol.NotificationCapability{Backend: "alerter", Available: true, Interactive: true},
			Interaction: "interaction_timeout",
		}, nil
	}
	var out bytes.Buffer
	notificationsTestCmd.SetOut(&out)

	if err := notificationsTestCmd.RunE(notificationsTestCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	want := "Sending a test notification through Alerter…\nNotification delivery confirmed via alerter. Agent Mux notifications are working.\n"
	if got := out.String(); got != want {
		t.Fatalf("output = %q; want %q", got, want)
	}
}

func TestNotificationsTestCommandExplainsUnconfirmedDelivery(t *testing.T) {
	resetNotificationsTestState(t)
	notificationsLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	notificationsTestDelivery = func(context.Context, *config.NotificationConfig) (notification.NotificationDeliveryTestResult, error) {
		return notification.NotificationDeliveryTestResult{}, notification.ErrNotificationDeliveryUnconfirmed
	}
	var out bytes.Buffer
	notificationsTestCmd.SetOut(&out)

	err := notificationsTestCmd.RunE(notificationsTestCmd, nil)
	if err == nil {
		t.Fatal("RunE returned nil; want unconfirmed-delivery error")
	}
	for _, text := range []string{"could not be confirmed", "System Settings → Notifications → Terminal", "Banners or Alerts", "turn off Focus", "amx notifications test"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("error = %q; want %q", err, text)
		}
	}
}

func TestNotificationsTestCommandReturnsBackendFailure(t *testing.T) {
	resetNotificationsTestState(t)
	notificationsLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	wantErr := errors.New("notification backend unavailable (not_found)")
	notificationsTestDelivery = func(context.Context, *config.NotificationConfig) (notification.NotificationDeliveryTestResult, error) {
		return notification.NotificationDeliveryTestResult{}, wantErr
	}

	if err := notificationsTestCmd.RunE(notificationsTestCmd, nil); !errors.Is(err, wantErr) {
		t.Fatalf("RunE error = %v; want %v", err, wantErr)
	}
}

func TestNotificationsTestCommandReportsAppDelivery(t *testing.T) {
	resetNotificationsTestState(t)
	notificationsLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	notificationsAppTest = func() (protocol.NotificationTestResult, error) {
		return protocol.NotificationTestResult{Backend: "app", Delivered: true}, nil
	}
	notificationsTestDelivery = func(context.Context, *config.NotificationConfig) (notification.NotificationDeliveryTestResult, error) {
		t.Fatal("Alerter was tested although the app confirmed delivery")
		return notification.NotificationDeliveryTestResult{}, nil
	}
	var out bytes.Buffer
	notificationsTestCmd.SetOut(&out)

	if err := notificationsTestCmd.RunE(notificationsTestCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if want := "Notification delivery confirmed via app. Agent Mux notifications are working.\n"; out.String() != want {
		t.Fatalf("output = %q; want %q", out.String(), want)
	}
}

func TestNotificationsTestCommandFallsBackWhenAppDoesNotConfirm(t *testing.T) {
	resetNotificationsTestState(t)
	notificationsLoadConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	notificationsAppTest = func() (protocol.NotificationTestResult, error) {
		return protocol.NotificationTestResult{Reason: "app_unconfirmed"}, nil
	}
	notificationsTestDelivery = func(context.Context, *config.NotificationConfig) (notification.NotificationDeliveryTestResult, error) {
		return notification.NotificationDeliveryTestResult{Capability: protocol.NotificationCapability{Backend: "alerter"}}, nil
	}
	var out bytes.Buffer
	notificationsTestCmd.SetOut(&out)

	if err := notificationsTestCmd.RunE(notificationsTestCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	want := "The Agent Mux app did not confirm delivery (app_unconfirmed); falling back to Alerter.\n" +
		"Sending a test notification through Alerter…\n" +
		"Notification delivery confirmed via alerter. Agent Mux notifications are working.\n"
	if out.String() != want {
		t.Fatalf("output = %q; want %q", out.String(), want)
	}
}
