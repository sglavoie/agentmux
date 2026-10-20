package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

func TestFeedbackRetainsFailuresThroughOtherActionsAndDismisses(t *testing.T) {
	t.Parallel()

	m := testModel()
	now := time.Now()
	m.now = func() time.Time { return now }
	for i := 0; i < maxToasts+2; i++ {
		m.pushToast(Toast{Severity: toastError, Text: fmt.Sprintf("failure %d", i)})
	}
	m.pushToast(Toast{Severity: toastSuccess, Text: "unrelated success"})
	now = now.Add(time.Hour)
	if cmd := m.sweepToasts(); cmd != nil {
		t.Fatal("persistent notices should not keep the ticker awake")
	}
	if len(m.toasts) != maxToasts+2 {
		t.Fatal("a later action or expiry discarded a failure")
	}
	m.mode, m.feedbackReturnMode = ModeFeedback, ModeList
	text := feedbackText(m)
	if !strings.Contains(text, "failure 0") || strings.Contains(text, "unrelated success") {
		t.Fatal(text)
	}
	next, _ := m.handleFeedbackKey(keyPress("D"))
	m = next.(Model)
	if len(m.toasts) != 0 {
		t.Fatal("dismiss left notices")
	}
	next, _ = m.handleFeedbackKey(keyPress("esc"))
	if next.(Model).mode != ModeList {
		t.Fatal("feedback lost its return view")
	}
}

func TestFeedbackSelectDismissAndOutcomeKeepNoticeIdentity(t *testing.T) {
	t.Parallel()

	m := testModel()
	now := time.Date(2026, 10, 16, 12, 30, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	target := sessionactions.Capture(session.RunningSession{TabID: 9, WindowID: 90, Base: "api", LaunchID: "original"})
	m.beginActionFeedback(1, commands.RestartSession, "api", "work")
	m.setFeedbackTarget(1, target)
	m.feedbackSelected = m.toasts[0].NoticeID
	m.reportActionOutcome(sessionactions.Result{Request: 1, Action: commands.RestartSession, Target: target, Outcome: sessionactions.OutcomeFailed, Detail: "failed restart"})
	first := selectedFeedback(m)
	m.pushToast(Toast{Severity: toastError, Text: "other failure"})
	if selectedFeedback(m).NoticeID != first.NoticeID {
		t.Fatal("an unrelated notice moved selection")
	}
	if text := feedbackText(m); !strings.Contains(text, "Oct 16 12:30:00 · api") || !strings.Contains(text, "inspect this session") {
		t.Fatal(text)
	}
	next, _ := m.handleFeedbackKey(keyPress("d"))
	m = next.(Model)
	if len(m.toasts) != 1 || m.toasts[0].Text != "other failure" {
		t.Fatalf("dismiss removed the wrong notice: %+v", m.toasts)
	}
}

func TestFeedbackInspectDoesNotAttachOrRepeatAndRejectsReusedSession(t *testing.T) {
	t.Parallel()

	m, _ := listViewModel(dashboard.ViewUrgency)
	m = selectLine(t, m, "session:api")
	m.mode = ModeFeedback
	row, ok := m.selectedSession()
	if !ok {
		t.Fatal("fixture needs a session")
	}
	m.pushToast(Toast{Severity: toastWarning, Text: "Outcome unconfirmed", Target: sessionactions.Capture(row)})
	next, _ := m.handleFeedbackKey(keyPress("enter"))
	inspected := next.(Model)
	selected, ok := inspected.selectedSession()
	if inspected.mode != ModeList || !ok || selected.TabID != row.TabID || !inspected.inspectorActive() {
		t.Fatal("inspect did not select the original session")
	}
	// A new launch reusing the same window is not the notice's target.
	row.LaunchID = "different-launch"
	m.toasts[0].Target.Session.LaunchID = "original-launch"
	for i := range m.sessions {
		if m.sessions[i].TabID == row.TabID {
			m.sessions[i] = row
		}
	}
	next, _ = m.handleFeedbackKey(keyPress("enter"))
	if next.(Model).mode != ModeFeedback {
		t.Fatal("inspect followed a reused session")
	}
}

func TestFeedbackDelayedOperationIsReplacedOnlyByItsOwnResult(t *testing.T) {
	t.Parallel()

	m := testModel()
	now := time.Now()
	m.now = func() time.Time { return now }
	m.beginActionFeedback(1, commands.RestartSession, "api", "work")
	now = now.Add(progressToastTTL + time.Second)
	m.sweepToasts()
	if len(m.toasts) != 1 || !m.toasts[0].Persistent || !strings.Contains(m.toasts[0].Text, "Still waiting") {
		t.Fatalf("delayed operation vanished: %+v", m.toasts)
	}
	m.reportActionOutcome(sessionactions.Result{Request: 2, Action: commands.RestartSession, Outcome: sessionactions.OutcomeSucceeded})
	if !strings.Contains(feedbackText(m), "Still waiting") {
		t.Fatal("unrelated completion cleared pending operation")
	}
	m.reportActionOutcome(sessionactions.Result{Request: 1, Action: commands.RestartSession, Outcome: sessionactions.OutcomeUnconfirmed})
	if strings.Contains(feedbackText(m), "Still waiting") {
		t.Fatal("late reply did not replace its progress")
	}
	now = now.Add(time.Hour)
	m.sweepToasts()
	if len(m.toasts) != 1 || !m.toasts[0].Persistent {
		t.Fatal("unconfirmed result expired")
	}
	// A new transient after a quiescent persistent notice must restart expiry.
	if m.pushToast(Toast{Severity: toastSuccess, Text: "copied"}) == nil {
		t.Fatal("ticker did not restart")
	}
}

func TestFeedbackWrapsAndScrollsLongDiagnostic(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.width, m.height, m.mode = 60, 18, ModeFeedback
	m.pushToast(Toast{Severity: toastError, Text: strings.Repeat("a long diagnostic ", 80) + "final detail"})
	first := renderFeedbackBox(m)
	next, _ := m.handleFeedbackKey(keyPress("G"))
	m = next.(Model)
	last := renderFeedbackBox(m)
	if first == last || !strings.Contains(strings.Join(feedbackLines(m), " "), "final detail") {
		t.Fatal("diagnostic cannot be read")
	}
	if modalBoxRenderer(ModeFeedback) == nil || !modalUsesResponsiveWidth(ModeFeedback) {
		t.Fatal("feedback missing from layout checks")
	}
}

func TestRetainedNoticeKeepsItsDetailsRouteAtNarrowWidths(t *testing.T) {
	t.Parallel()

	for _, width := range []int{24, 40, 60, 80, 100} {
		m := testModel()
		m.width = width
		m.pushToast(Toast{Severity: toastError, Text: strings.Repeat("Restart diagnostic 世界 ", 20)})
		line := stripAnsi(renderStatusBar(m))
		if !strings.HasSuffix(line, ": feedback ↵") {
			t.Fatalf("width %d hid recovery: %q", width, line)
		}
	}
}

func TestFeedbackExplainsUnconfirmedOutcomeAndNextStep(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.beginActionFeedback(1, commands.RestartSession, "api", "work")
	m.reportActionOutcome(sessionactions.Result{Request: 1, Action: commands.RestartSession, Outcome: sessionactions.OutcomeUnconfirmed})
	text := feedbackText(m)
	for _, want := range []string{"What happened", "What remains uncertain", "Next step", "before repeating"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
}
