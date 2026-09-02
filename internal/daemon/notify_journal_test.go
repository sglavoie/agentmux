package daemon

// The Handler side of notification dispatch: a terminal dispatch failure must
// reach the durable decision journal with its originating trace, so an alert
// that never appeared can still be explained offline. The notifier's own
// dispatch, focus, and disposition behavior lives in
// internal/daemon/notification.

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func TestNotificationDispatchFailurePersistsTerminalJournalOutcome(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := notification.NewNotifier(&config.NotificationConfig{Stop: true})
		n.SetBackend(func(context.Context, int, string, string, string) (bool, error) {
			return false, errors.New("backend unavailable")
		})
		h := NewHandler(NewStateGuard(time.Hour), n)
		journal, err := diagnostics.NewJournal(diagnostics.PathsForRoot(t.TempDir()), diagnostics.JournalOptions{})
		if err != nil {
			t.Fatal(err)
		}
		h.setDecisionJournal(journal)

		h.maybeNotifyTraced(42, "W:api", "work", "stop", false, notification.NotificationTrace{
			TraceID: "trace-notify-fail", RequestID: "request-notify-fail",
		})
		synctest.Wait()

		records, err := journal.Read(context.Background(), diagnostics.Selector{
			TraceID: "trace-notify-fail", Stages: []string{"daemon.notification.dispatch"},
		})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, record := range records {
			if record.Outcome == "failed" && record.Reason == "send_failed" {
				found = true
			}
		}
		if !found {
			t.Fatalf("journal lacks terminal notification failure: %+v", records)
		}
	})
}
