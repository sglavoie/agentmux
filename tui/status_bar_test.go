package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/layout"
)

func assertStatusBarContains(t *testing.T, m Model, want string) {
	t.Helper()
	got := strings.TrimSpace(stripAnsi(renderStatusBar(m)))
	if !strings.Contains(got, want) {
		t.Fatalf("status bar = %q, want to contain %q", got, want)
	}
}

func TestRenderStatusBarExactWidthNoTrailingNewline(t *testing.T) {
	m := testModel()
	m.width = 24
	m.toasts = []Toast{{
		Severity: toastInfo,
		Text:     "this is a very long status message that must be truncated",
		Expires:  time.Now().Add(time.Hour),
	}}

	out := renderStatusBar(m)
	if strings.Contains(out, "\n") {
		t.Fatalf("status bar must be one row with no newline, got %q", out)
	}
	if got := runewidth.StringWidth(stripAnsi(out)); got != m.width {
		t.Fatalf("visible width = %d, want %d; output %q", got, m.width, stripAnsi(out))
	}
}

func TestStatusBarSelectsLatestActiveToast(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	m := testModel()
	m.toasts = []Toast{
		{Severity: toastSuccess, Text: "newest active", Expires: now.Add(time.Minute)},
		{Severity: toastInfo, Text: "older active", Expires: now.Add(time.Minute)},
	}

	msg := currentStatusMessage(m, now)
	if msg.Text != "newest active" {
		t.Fatalf("status text = %q, want latest active toast", msg.Text)
	}
	if msg.Severity != statusSuccess {
		t.Fatalf("severity = %v, want %v", msg.Severity, statusSuccess)
	}
}

func TestStatusBarIgnoresExpiredToasts(t *testing.T) {
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	m := testModel()
	m.toasts = []Toast{
		{Severity: toastError, Text: "expired newest", Expires: now.Add(-time.Minute)},
		{Severity: toastInfo, Text: "active older", Expires: now.Add(time.Minute)},
	}

	msg := currentStatusMessage(m, now)
	if msg.Text != "active older" {
		t.Fatalf("status text = %q, want next active toast", msg.Text)
	}
	if msg.Severity != statusInfo {
		t.Fatalf("severity = %v, want %v", msg.Severity, statusInfo)
	}

	m.toasts = []Toast{{Severity: toastError, Text: "expired only", Expires: now.Add(-time.Minute)}}
	msg = currentStatusMessage(m, now)
	if strings.Contains(msg.Text, "expired only") {
		t.Fatalf("expired-only toast leaked into status text: %q", msg.Text)
	}
	if msg.Severity != statusIdle {
		t.Fatalf("severity = %v, want fallback idle severity", msg.Severity)
	}
}

// Every root mode that still narrates itself. ModeList is deliberately absent:
// the dashboard's row is a feedback slot, and what it does when nothing has
// happened is asserted by TestDashboardFeedbackSlotIsBlankWhenIdle.
func TestStatusBarFallbackBehavior(t *testing.T) {
	picker := testModel()
	picker.mode = ModePicker
	picker.pickerSessions = []config.Session{{Title: "api"}, {Title: "db"}, {Title: "web"}}
	picker.pickerFilteredIdx = []int{0, 2}
	picker.pickerSelected = map[int]bool{0: true, 2: true}

	cases := []struct {
		name string
		m    Model
		want string
	}{
		{name: "search", m: Model{mode: ModeSearch, width: 80, filterText: "api"}, want: "Filter sessions: /api"},
		{name: "confirm", m: Model{mode: ModeConfirm, width: 80, confirmAction: "close session"}, want: "Confirm close session"},
		{name: "picker selection count", m: picker, want: "Launch Sessions: 2 selected - 2/3 visible"},
		{name: "config picker", m: Model{mode: ModeConfigPicker, width: 80, configPickerReturnMode: ModePicker}, want: "Choose launch profile"},
		{name: "help", m: Model{mode: ModeHelp, width: 80}, want: "Help"},
		{name: "detail", m: Model{mode: ModeDetail, width: 80, detailWindowID: 17}, want: "Session details: window 17"},
		{name: "actions", m: Model{mode: ModeActions, width: 80}, want: "Session actions"},
		{name: "command palette", m: Model{mode: ModeCommandPalette, width: 80, commandResults: make([]commands.Command[Model], 3)}, want: "Command Palette: 3 results"},
		{name: "session search", m: Model{mode: ModeSessionSearch, width: 80, sessionResults: []transcript.Result{{}, {}}}, want: "Past Sessions: 2 results"},
		{
			name: "session view",
			m: Model{
				mode:           ModeSessionView,
				width:          80,
				viewerSession:  &transcript.SessionMeta{Project: "api"},
				viewerMessages: []transcript.Message{{}, {}},
			},
			want: "Transcript: api - 2 messages",
		},
		{name: "delete choice", m: Model{mode: ModeDeleteChoice, width: 80}, want: "Delete session"},
		{name: "chip picker", m: Model{mode: ModeChipPicker, width: 80, chipPickerKind: chipPickerProject}, want: "Choose project"},
		{
			name: "message detail",
			m: Model{
				mode:             ModeMessageDetail,
				width:            80,
				messageDetailIdx: 1,
				viewerMessages:   []transcript.Message{{}, {}},
			},
			want: "Message detail: 2/2",
		},
		{name: "plan search", m: Model{mode: ModePlanSearch, width: 80, planResults: []plans.Result{{}, {}}}, want: "Plans: 2 results"},
		{name: "plan view", m: Model{mode: ModePlanView, width: 80, viewerPlan: &plans.PlanMeta{Name: "plan-a"}}, want: "Plan: plan-a"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.m.width = 80
			got := strings.TrimSpace(stripAnsi(renderStatusBar(tc.m)))
			if !strings.Contains(got, tc.want) {
				t.Fatalf("status fallback = %q, want to contain %q", got, tc.want)
			}
		})
	}
}

// The dashboard's status row is a feedback slot, not a status line: it reports
// what just happened and is otherwise blank. The session tally it used to carry
// is gone — the urgency group headings count their own members and the
// inspector describes the selected row — and so is the "Health degraded" line,
// which the header's indicator now names precisely instead of generically.
func TestDashboardFeedbackSlotIsBlankWhenIdle(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.width, m.height = 120, 32

	row := renderStatusBar(m)
	if got := strings.TrimSpace(stripAnsi(row)); got != "" {
		t.Fatalf("idle feedback slot = %q, want blank", got)
	}
	// Blank, but still a full-width row: the slot is reserved, so nothing below
	// it moves when something finally appears.
	if got := layout.StyledWidth(row); got != m.width {
		t.Fatalf("idle feedback row is %d cells wide, want %d", got, m.width)
	}
}

// An action error is the reason the slot exists. It must reach it even though
// nothing else on the dashboard does.
func TestDashboardFeedbackSlotCarriesActionErrors(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.width, m.height = 120, 32
	m.actionErr = errors.New("kitty refused the focus")

	if got := stripAnsi(renderStatusBar(m)); !strings.Contains(got, "kitty refused the focus") {
		t.Fatalf("feedback slot = %q, want the action error", got)
	}
	// And it is on screen, not clipped out of the composed frame.
	if got := stripAnsi(m.View().Content); !strings.Contains(got, "kitty refused the focus") {
		t.Fatalf("composed dashboard does not show the action error:\n%s", got)
	}
}

// The dashboard rests its feedback row on the list, directly above the footer.
// Every other root keeps the sticky status line under the footer, so the two
// readings of the row never appear on one screen.
func TestFeedbackRowSitsAboveTheFooterOnTheDashboardOnly(t *testing.T) {
	dash := loadSessions(testModel(), makeSessions())
	dash.width, dash.height = 120, 32
	dash.actionErr = errors.New("marker error")
	lines := strings.Split(stripAnsi(dash.View().Content), "\n")
	feedback, footer := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "marker error") {
			feedback = i
		}
		if strings.Contains(line, "? help") || strings.Contains(line, "? more") {
			footer = i
		}
	}
	if feedback < 0 || footer < 0 {
		t.Fatalf("dashboard frame missing feedback (%d) or footer (%d):\n%s", feedback, footer, strings.Join(lines, "\n"))
	}
	if feedback > footer {
		t.Fatalf("dashboard feedback row %d is below the footer row %d", feedback, footer)
	}
	if !dashboardOwnsChrome(dash) {
		t.Fatal("the dashboard does not claim its own chrome")
	}
	// The dashboard's own overlays are the dashboard: each is reachable only
	// from the list and each `esc`s back to it, so the row underneath them has
	// to keep reading as the dashboard's feedback slot rather than flipping to
	// another root's status line for as long as one is open.
	for _, mode := range []Mode{ModeSearch, ModeProjectSwitcher, ModeActions, ModeDetail} {
		own := dash
		own.mode = mode
		if !dashboardOwnsChrome(own) {
			t.Fatalf("%s gave up the dashboard's chrome", uitestModeName(mode))
		}
	}
	for _, mode := range []Mode{ModeSessionSearch, ModeSessionView, ModePlanSearch, ModePlanView, ModeWorktrees, ModeASLRuns, ModePicker} {
		other := dash
		other.mode = mode
		if dashboardOwnsChrome(other) {
			t.Fatalf("%s claims the dashboard's chrome", uitestModeName(mode))
		}
	}
}

// The cold-load and confirmed-empty readings belong to the list body, which
// already draws each as a hero with the remedy attached. The feedback slot does
// not repeat them: two copies of "no running sessions" one row apart is the
// duplication the quiet chrome exists to remove. TestRenderListEmptyHeroNoSessions
// and TestRenderListInitialLoadDoesNotClaimTheListIsEmpty pin the surviving copy.
func TestDashboardFeedbackSlotDoesNotRepeatTheEmptyListHero(t *testing.T) {
	m := testModel()
	m.width = 80
	assertStatusBarLacks(t, m, "Loading sessions")

	m = loadSessions(m, nil)
	assertStatusBarLacks(t, m, "No running sessions")
	if !strings.Contains(stripAnsi(renderList(m)), "No running sessions.") {
		t.Fatal("the list body dropped the confirmed-empty hero along with the status copy")
	}
}

func TestStatusBarPromotesTranscriptScanWithCachedResults(t *testing.T) {
	m := Model{
		mode:                   ModeSessionSearch,
		width:                  100,
		sessionRefreshInFlight: true,
		sessionResults:         []transcript.Result{{}, {}},
	}

	msg := fallbackStatusMessage(m)
	if msg.Severity != statusInfo {
		t.Fatalf("scan status severity = %v, want %v", msg.Severity, statusInfo)
	}
	for _, want := range []string{"Scanning transcripts", "2 cached results", "updates automatically"} {
		if !strings.Contains(msg.Text, want) {
			t.Fatalf("scan status = %q, want %q", msg.Text, want)
		}
	}

	m.sessionResults = nil
	msg = fallbackStatusMessage(m)
	if msg.Text != "Scanning transcripts… results will update automatically" {
		t.Fatalf("empty scan status = %q", msg.Text)
	}
}
