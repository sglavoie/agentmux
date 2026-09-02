package notification

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

func notificationTestResolver() alerterResolver {
	return alerterResolver{
		goos:     "darwin",
		lookPath: func(string) (string, error) { return "/custom/alerter", nil },
		stat: func(path string) (os.FileInfo, error) {
			return executableInfo(path)
		},
		versionOf: func(context.Context, string) ([]byte, error) { return []byte("26.5\n"), nil },
	}
}

func TestNotificationDeliveryBypassesDisabledEventCategories(t *testing.T) {
	disabled := false
	cfg := &config.NotificationConfig{
		StopFailure: &disabled,
		StaleActive: &disabled,
		HookSilence: &disabled,
	}
	called := false
	got, err := testNotificationDeliveryWith(context.Background(), cfg, notificationTestResolver(), time.Second, func(_ context.Context, path, title, message string, timeout time.Duration) (dispatchResult, error) {
		called = true
		if path != "/custom/alerter" || title != "Agent Mux notification test" || message == "" || timeout != notificationTestTimeout {
			t.Fatalf("runner args = %q, %q, %q, %s", path, title, message, timeout)
		}
		return deliveredResult(interactionTimeout), nil
	})
	if err != nil {
		t.Fatalf("testNotificationDeliveryWith: %v", err)
	}
	if !called || got.Interaction != string(interactionTimeout) || !got.Capability.Available {
		t.Fatalf("result = %+v, called = %v", got, called)
	}
}

func TestNotificationDeliveryReportsUnconfirmedTimeout(t *testing.T) {
	_, err := testNotificationDeliveryWith(context.Background(), &config.NotificationConfig{}, notificationTestResolver(), time.Second, func(context.Context, string, string, string, time.Duration) (dispatchResult, error) {
		return dispatchResult{}, context.DeadlineExceeded
	})
	if !errors.Is(err, ErrNotificationDeliveryUnconfirmed) {
		t.Fatalf("error = %v; want ErrNotificationDeliveryUnconfirmed", err)
	}
}

func TestNotificationDeliveryReportsUnavailableBackend(t *testing.T) {
	resolver := notificationTestResolver()
	resolver.goos = "linux"
	_, err := testNotificationDeliveryWith(context.Background(), nil, resolver, time.Second, nil)
	if err == nil || err.Error() != "notification backend unavailable (unsupported_platform)" {
		t.Fatalf("error = %v; want unsupported-platform backend error", err)
	}
}

func TestAlerterTestArgsUseShortTimeout(t *testing.T) {
	got := alerterArgsWithMessage("Agent Mux notification test", "Notification test", "working", notificationTestTimeout)
	wantTimeout := false
	wantMessage := false
	for i := 0; i+1 < len(got); i++ {
		if got[i] == "--timeout" && got[i+1] == "5" {
			wantTimeout = true
		}
		if got[i] == "--message" && got[i+1] == "working" {
			wantMessage = true
		}
	}
	if !wantTimeout || !wantMessage {
		t.Fatalf("alerter args = %v; want non-empty message and --timeout 5", got)
	}
}
