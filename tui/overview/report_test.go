package overview

import (
	"testing"
	"time"
)

// The feedback row keeps a failure until something newer replaces it, as the
// dashboard keeps an error toast, and never blanks an operation still in
// flight: past its window that one reads as still waiting. Ordinary outcomes
// still fade on their own.
func TestFeedbackRowRetainsFailuresAndPendingOperations(t *testing.T) {
	r := &recorder{now: viewNow}
	m := newModel(actionConfig(), nil, 0)
	r.install(&m)

	m = m.withFailureReport("restart failed for api")
	r.now = r.now.Add(time.Hour)
	if got := m.activeReport(); got != "restart failed for api" {
		t.Fatalf("failure after an hour = %q, want it retained", got)
	}

	m = m.withPendingReport("restarting api…")
	if got := m.activeReport(); got != "restarting api…" {
		t.Fatalf("pending report = %q", got)
	}
	r.now = r.now.Add(reportPendingWindow + time.Second)
	if got := m.activeReport(); got != "still waiting: restarting api…" {
		t.Fatalf("overdue pending report = %q, want it reworded rather than blanked", got)
	}

	m = m.withReport("restarted api", reportWindow)
	r.now = r.now.Add(reportWindow + time.Second)
	if got := m.activeReport(); got != "" {
		t.Fatalf("a success outlived its window: %q", got)
	}
}
