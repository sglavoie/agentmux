package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestPushToastAppendsNewestOnTop(t *testing.T) {
	m := testModel()
	m.pushToast(Toast{Severity: toastInfo, Text: "first"})
	m.pushToast(Toast{Severity: toastInfo, Text: "second"})
	if len(m.toasts) != 2 {
		t.Fatalf("expected 2 toasts, got %d", len(m.toasts))
	}
	if m.toasts[0].Text != "second" {
		t.Errorf("expected newest on top, got %q", m.toasts[0].Text)
	}
}

func TestPushToastCapsAtMax(t *testing.T) {
	m := testModel()
	for i := 0; i < maxToasts+2; i++ {
		m.pushToast(Toast{Severity: toastInfo, Text: "t"})
	}
	if len(m.toasts) != maxToasts {
		t.Errorf("expected toasts capped at %d, got %d", maxToasts, len(m.toasts))
	}
}

func TestPushToastDefaultsTTL(t *testing.T) {
	m := testModel()
	before := time.Now()
	m.pushToast(Toast{Severity: toastInfo, Text: "x"})
	if m.toasts[0].Expires.Before(before.Add(defaultToastTTL - time.Second)) {
		t.Errorf("expected default TTL applied, got expires=%v", m.toasts[0].Expires)
	}
}

func TestPushToastFirstReturnsTickCmd(t *testing.T) {
	m := testModel()
	cmd := m.pushToast(Toast{Severity: toastInfo, Text: "x"})
	if cmd == nil {
		t.Fatal("expected non-nil cmd when transitioning from empty toast list")
	}
	cmd2 := m.pushToast(Toast{Severity: toastInfo, Text: "y"})
	if cmd2 != nil {
		t.Error("expected nil cmd when a toast tick is already running")
	}
}

func TestSweepToastsExpires(t *testing.T) {
	m := testModel()
	m.toasts = []Toast{
		{Text: "alive", Expires: time.Now().Add(time.Hour)},
		{Text: "dead", Expires: time.Now().Add(-time.Hour)},
	}
	cmd := m.sweepToasts()
	if len(m.toasts) != 1 || m.toasts[0].Text != "alive" {
		t.Fatalf("expected only the live toast to remain, got %+v", m.toasts)
	}
	if cmd == nil {
		t.Error("expected re-arm cmd while toasts remain")
	}
}

func TestSweepToastsAllExpiredQuiesces(t *testing.T) {
	m := testModel()
	m.toasts = []Toast{
		{Text: "dead", Expires: time.Now().Add(-time.Hour)},
	}
	cmd := m.sweepToasts()
	if len(m.toasts) != 0 {
		t.Fatalf("expected toasts cleared, got %+v", m.toasts)
	}
	if cmd != nil {
		t.Error("expected nil cmd when no toasts remain (ticker quiesces)")
	}
}

func TestReplaceToastByIDUpdatesInPlace(t *testing.T) {
	m := testModel()
	m.replaceToastByID(42, Toast{Severity: toastInfo, Text: "a"})
	m.replaceToastByID(42, Toast{Severity: toastSuccess, Text: "b"})
	if len(m.toasts) != 1 {
		t.Fatalf("expected single toast (replaced), got %d", len(m.toasts))
	}
	if m.toasts[0].Text != "b" || m.toasts[0].Severity != toastSuccess {
		t.Errorf("expected replacement to overwrite content/severity, got %+v", m.toasts[0])
	}
}

func TestRenderToastsEmpty(t *testing.T) {
	m := testModel()
	if got := renderToasts(m); got != "" {
		t.Errorf("expected empty render with no toasts, got %q", got)
	}
}

func TestRenderToastsIncludesText(t *testing.T) {
	m := testModel()
	m.width = 80
	m.pushToast(Toast{Severity: toastSuccess, Text: "Launched 1/3 · api"})
	out := renderToasts(m)
	if !strings.Contains(stripAnsi(out), "Launched 1/3 · api") {
		t.Errorf("expected toast text in render, got %q", out)
	}
}

func TestLaunchProgressIncrementsCounter(t *testing.T) {
	m := testModel()
	m.launchBatchID = 1
	updated, _ := m.Update(launchProgressMsg{batchID: 1, total: 3, title: "api"})
	m = updated.(Model)
	if m.launchBatchDone != 1 {
		t.Errorf("expected launchBatchDone=1, got %d", m.launchBatchDone)
	}
	if len(m.toasts) != 1 {
		t.Fatalf("expected 1 progress toast, got %d", len(m.toasts))
	}
	if !strings.Contains(m.toasts[0].Text, "1/3") {
		t.Errorf("expected progress text '1/3', got %q", m.toasts[0].Text)
	}
}

func TestLaunchProgressErrorPushesErrorToast(t *testing.T) {
	m := testModel()
	m.launchBatchID = 5
	updated, _ := m.Update(launchProgressMsg{
		batchID: 5, total: 2, title: "api", err: errors.New("boom"),
	})
	m = updated.(Model)
	// First (newest) should be the progress toast; the second the error.
	// Both must be present.
	if len(m.toasts) != 2 {
		t.Fatalf("expected 2 toasts (progress + error), got %d", len(m.toasts))
	}
	hasError := false
	for _, tt := range m.toasts {
		if tt.Severity == toastError && strings.Contains(tt.Text, "boom") {
			hasError = true
		}
	}
	if !hasError {
		t.Errorf("expected an error toast carrying the failure reason, got %+v", m.toasts)
	}
}

func TestLaunchProgressFinalIsSuccess(t *testing.T) {
	m := testModel()
	m.launchBatchID = 7
	m.launchBatchQuitOnSuccess = true
	// First of two.
	updated, _ := m.Update(launchProgressMsg{batchID: 7, total: 2, title: "api"})
	m = updated.(Model)
	if m.toasts[0].Severity != toastInfo {
		t.Errorf("expected info severity mid-batch, got %v", m.toasts[0].Severity)
	}
	// Second/final.
	updated2, _ := m.Update(launchProgressMsg{batchID: 7, total: 2, title: "db"})
	m = updated2.(Model)
	if m.toasts[0].Severity != toastSuccess {
		t.Errorf("expected success severity on final progress, got %v", m.toasts[0].Severity)
	}
	if !strings.Contains(m.toasts[0].Text, "2/2") {
		t.Errorf("expected '2/2' in final progress, got %q", m.toasts[0].Text)
	}
}

func TestLaunchProgressFinalDashboardLaunchStaysInTUI(t *testing.T) {
	m := testModel()
	m.launchBatchID = 8
	m.launchBatchQuitOnSuccess = false
	_, cmd := m.Update(launchProgressMsg{batchID: 8, total: 1, title: "api"})
	if cmd == nil {
		t.Fatal("expected follow-up cmd on final dashboard launch progress")
	}
	for _, msg := range collectLeafMsgs(t, cmd) {
		if _, ok := msg.(tea.QuitMsg); ok {
			t.Fatal("dashboard launch progress should refresh, not quit")
		}
	}
}

func TestLaunchProgressStaleBatchIgnored(t *testing.T) {
	m := testModel()
	m.launchBatchID = 10
	updated, _ := m.Update(launchProgressMsg{batchID: 9, total: 1, title: "stale"})
	m = updated.(Model)
	if m.launchBatchDone != 0 {
		t.Errorf("expected stale batch to leave counter at 0, got %d", m.launchBatchDone)
	}
	if len(m.toasts) != 0 {
		t.Errorf("expected stale batch to push no toast, got %+v", m.toasts)
	}
}

// ---- Cursor restore tests ----

func TestRestoreCursorPreservesPriorBase(t *testing.T) {
	sessions := []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: ""},
		{TabID: 2, Base: "db", Account: "work", State: ""},
	}
	i := restoreCursor(sessions, "db", nil, 0)
	if i != 1 {
		t.Errorf("expected cursor on db (index 1), got %d", i)
	}
}

func TestRestoreCursorFallsBackToAttentionRow(t *testing.T) {
	sessions := []session.RunningSession{
		{TabID: 1, Base: "calm", Account: "work", State: "bolt"},
		{TabID: 2, Base: "needs-perm", Account: "work", State: "key"},
		{TabID: 3, Base: "stopped", Account: "work", State: "stop"},
	}
	// Prior row was deleted; should pick the first attention row.
	i := restoreCursor(sessions, "missing", nil, 0)
	if i != 1 {
		t.Errorf("expected fallback to first attention row (index 1), got %d", i)
	}
}

func TestRestoreCursorDefaultsToZero(t *testing.T) {
	sessions := []session.RunningSession{
		{TabID: 1, Base: "a", Account: "work", State: "bolt"},
		{TabID: 2, Base: "b", Account: "work", State: ""},
	}
	i := restoreCursor(sessions, "ghost", nil, 0)
	if i != 0 {
		t.Errorf("expected default to 0 when no attention row, got %d", i)
	}
}

func TestRestoreCursorEmptyListReturnsZero(t *testing.T) {
	if got := restoreCursor(nil, "anything", nil, 0); got != 0 {
		t.Errorf("expected 0 for empty list, got %d", got)
	}
}

func TestSessionsMsgRestoresCursorByBase(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	// With bolt-first ordering, backend (bolt) is at index 0; j moves to api.
	m = pressKey(m, "j")
	priorBase := m.filtered[m.cursor].Base
	if priorBase != "api" {
		t.Fatalf("setup expected api at cursor, got %q", priorBase)
	}
	// Reorder list (remove backend so cursor index would shift if not restored).
	newSessions := []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", Directory: "/x", State: ""},
		{TabID: 3, Base: "dotfiles", Account: "personal", Directory: "/y", State: ""},
	}
	m = loadSessions(m, newSessions)
	if m.filtered[m.cursor].Base != "api" {
		t.Errorf("expected cursor restored to api, got %q", m.filtered[m.cursor].Base)
	}
}

func TestSessionsMsgMovesCursorToAttentionRowWhenPriorRowGone(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	// Replace list with no api/backend, plus an attention row.
	newSessions := []session.RunningSession{
		{TabID: 4, Base: "other", Account: "work", Directory: "/o", State: ""},
		{TabID: 5, Base: "perm", Account: "work", Directory: "/p", State: "key"},
	}
	m = loadSessions(m, newSessions)
	if m.filtered[m.cursor].Base != "perm" {
		t.Errorf("expected cursor to land on attention row perm, got %q at index %d",
			m.filtered[m.cursor].Base, m.cursor)
	}
}
