package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestFullScreenModesReserveStickyStatusBar(t *testing.T) {
	cases := []struct {
		name       string
		m          Model
		status     string
		footer     string
		absentBody string
	}{
		{
			name:       "session search",
			m:          fullScreenSessionSearchModel(30, 12),
			status:     "Past Sessions: 30 results",
			footer:     "esc back",
			absentBody: indexedRowName(3),
		},
		{
			name:       "session viewer",
			m:          fullScreenSessionViewModel(30, 10),
			status:     "Transcript: api - 30 messages",
			footer:     "g/G top/bot",
			absentBody: indexedRowName(3),
		},
		{
			name:       "plan search",
			m:          fullScreenPlanSearchModel(30, 11),
			status:     "Plans: 30 results",
			footer:     "ctrl+f/b page",
			absentBody: indexedRowName(3),
		},
		{
			name:       "plan viewer",
			m:          fullScreenPlanViewModel(30, 10),
			status:     "Plan: api-plan",
			footer:     "ctrl+f/b page",
			absentBody: indexedRowName(3),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := stripAnsi(tc.m.render())
			lines := strings.Split(out, "\n")
			if len(lines) != tc.m.height {
				t.Fatalf("rendered line count = %d, want terminal height %d:\n%s", len(lines), tc.m.height, out)
			}
			if last := strings.TrimSpace(lines[len(lines)-1]); !strings.Contains(last, tc.status) {
				t.Fatalf("final row = %q, want status %q", last, tc.status)
			}
			footerLines := rootFooterLines(tc.m)
			if len(footerLines) == 0 {
				t.Fatalf("expected footer lines for %s", tc.name)
			}
			footerStart := len(lines) - statusBarLines - len(footerLines)
			if footerStart < 0 {
				t.Fatalf("footer start underflow for %s: lines=%d footer=%d", tc.name, len(lines), len(footerLines))
			}
			footer := strings.Join(lines[footerStart:len(lines)-statusBarLines], "\n")
			if !strings.Contains(footer, tc.footer) {
				t.Fatalf("footer above status missing %q:\n%s", tc.footer, out)
			}
			body := strings.Join(lines[:footerStart], "\n")
			if strings.Contains(body, tc.absentBody) {
				t.Fatalf("body rendered row %q past its scroll budget:\n%s", tc.absentBody, out)
			}
		})
	}
}

func TestFullScreenModeErrorsMirrorToStatusBar(t *testing.T) {
	cases := []struct {
		name string
		m    Model
		want string
	}{
		{
			name: "session search",
			m:    Model{mode: ModeSessionSearch, width: 80, sessionSearchErr: errors.New("bad query")},
			want: "Search error: bad query",
		},
		{
			name: "session viewer",
			m:    Model{mode: ModeSessionView, width: 80, viewerLoadErr: errors.New("missing transcript")},
			want: "Read error: missing transcript",
		},
		{
			name: "plan search",
			m:    Model{mode: ModePlanSearch, width: 80, planRefreshErr: errors.New("scan failed")},
			want: "Refresh error: scan failed",
		},
		{
			name: "plan viewer",
			m:    Model{mode: ModePlanView, width: 80, planViewerLoadErr: errors.New("missing file")},
			want: "Read error: missing file",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.TrimSpace(stripAnsi(renderStatusBar(tc.m)))
			if !strings.Contains(got, tc.want) {
				t.Fatalf("status bar = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSessionSearchOverlaysReserveStickyStatusBar(t *testing.T) {
	target := transcript.SessionMeta{
		ID:      "abc",
		Harness: "claude",
		Profile: "work",
		Project: "agentmux",
		Path:    "/tmp/demo.jsonl",
	}
	cases := []struct {
		name   string
		m      Model
		status string
		footer string
	}{
		{
			name: "chip picker",
			m: Model{
				mode:              ModeChipPicker,
				width:             80,
				height:            10,
				cfg:               &config.Config{},
				chipPickerKind:    chipPickerProject,
				chipPickerOptions: []string{"", "api", "web"},
			},
			status: "Choose project",
			footer: "/ search",
		},
		{
			name: "delete choice",
			m: Model{
				mode:         ModeDeleteChoice,
				width:        80,
				height:       10,
				cfg:          &config.Config{},
				deleteTarget: &target,
			},
			status: "Delete session",
			footer: "[i] index only",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := stripAnsi(tc.m.render())
			lines := strings.Split(out, "\n")
			if len(lines) != tc.m.height {
				t.Fatalf("rendered line count = %d, want terminal height %d:\n%s", len(lines), tc.m.height, out)
			}
			if last := strings.TrimSpace(lines[len(lines)-1]); !strings.Contains(last, tc.status) {
				t.Fatalf("final row = %q, want status %q", last, tc.status)
			}
			footerLines := rootFooterLines(tc.m)
			footerStart := len(lines) - statusBarLines - len(footerLines)
			if footerStart < 0 {
				t.Fatalf("footer start underflow for %s", tc.name)
			}
			footer := strings.Join(lines[footerStart:len(lines)-statusBarLines], "\n")
			if !strings.Contains(footer, tc.footer) {
				t.Fatalf("footer above status missing %q:\n%s", tc.footer, out)
			}
		})
	}
}

func fullScreenSessionSearchModel(total, height int) Model {
	results := make([]transcript.Result, total)
	for i := range results {
		results[i] = transcript.Result{
			Meta: transcript.SessionMeta{
				ID:      indexedRowName(i),
				Project: indexedRowName(i),
				Path:    "/tmp/session-" + indexedRowName(i) + ".jsonl",
			},
		}
	}
	return Model{
		mode:           ModeSessionSearch,
		width:          100,
		height:         height,
		cfg:            &config.Config{},
		sessionResults: results,
	}
}

func fullScreenSessionViewModel(total, height int) Model {
	messages := make([]transcript.Message, total)
	for i := range messages {
		messages[i] = transcript.Message{Role: "user", Text: indexedRowName(i)}
	}
	return Model{
		mode:           ModeSessionView,
		width:          100,
		height:         height,
		cfg:            &config.Config{},
		viewerSession:  &transcript.SessionMeta{Project: "api", Path: "/tmp/api-session.jsonl"},
		viewerMessages: messages,
	}
}

func fullScreenPlanSearchModel(total, height int) Model {
	results := make([]plans.Result, total)
	for i := range results {
		results[i] = plans.Result{Meta: plans.PlanMeta{Name: indexedRowName(i), DirPath: "/tmp/plan-" + indexedRowName(i)}}
	}
	return Model{
		mode:        ModePlanSearch,
		width:       100,
		height:      height,
		cfg:         &config.Config{},
		planResults: results,
	}
}

func fullScreenPlanViewModel(total, height int) Model {
	lines := make([]string, total)
	for i := range lines {
		lines[i] = indexedRowName(i)
	}
	return Model{
		mode:             ModePlanView,
		width:            100,
		height:           height,
		cfg:              &config.Config{},
		viewerPlan:       &plans.PlanMeta{Name: "api-plan", DirPath: "/tmp/api-plan"},
		planViewerLines:  lines,
		planViewerCursor: 0,
	}
}
