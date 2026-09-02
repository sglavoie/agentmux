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
	oldOut := notificationsTestCmd.OutOrStdout()
	t.Cleanup(func() {
		notificationsLoadConfig = oldLoadConfig
		notificationsTestDelivery = oldTestDelivery
		notificationsTestCmd.SetOut(oldOut)
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
	for _, text := range []string{"could not be confirmed", "System Settings → Notifications → alerter", "Banners or Alerts", "turn off Focus", "amx notifications test"} {
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
