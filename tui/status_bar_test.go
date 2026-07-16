package tui

import (
	"strings"
	"testing"
	"time"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
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

func TestStatusBarFallbackBehavior(t *testing.T) {
	list := loadSessions(testModel(), makeSessions())

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
		{name: "list counts", m: list, want: "Sessions: 3 running"},
		{name: "search", m: Model{mode: ModeSearch, width: 80, filterText: "api"}, want: "Filter sessions: /api"},
		{name: "confirm", m: Model{mode: ModeConfirm, width: 80, confirmAction: "close session"}, want: "Confirm close session"},
		{name: "picker selection count", m: picker, want: "Launch Sessions: 2 selected - 2/3 visible"},
		{name: "config picker", m: Model{mode: ModeConfigPicker, width: 80, configPickerReturnMode: ModePicker}, want: "Choose launch profile"},
		{name: "help", m: Model{mode: ModeHelp, width: 80}, want: "Help"},
		{name: "detail", m: Model{mode: ModeDetail, width: 80, detailWindowID: 17}, want: "Session details: window 17"},
		{name: "actions", m: Model{mode: ModeActions, width: 80}, want: "Session actions"},
		{name: "command palette", m: Model{mode: ModeCommandPalette, width: 80, commandResults: make([]command, 3)}, want: "Command Palette: 3 results"},
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
