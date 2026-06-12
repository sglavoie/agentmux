package tui

import (
	"strings"
	"testing"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestPathRowsRenderInPathAwareModes(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{
			name: "session search",
			out: stripAnsi(renderSessionSearch(Model{
				width: 100,
				cfg:   &config.Config{},
				sessionResults: []transcript.Result{{
					Meta: transcript.SessionMeta{Project: "api", Path: "/tmp/session.jsonl"},
				}},
			})),
			want: "session path: /tmp/session.jsonl",
		},
		{
			name: "session viewer",
			out: stripAnsi(renderSessionView(Model{
				width:         100,
				viewerSession: &transcript.SessionMeta{Project: "api", Path: "/tmp/viewer.jsonl"},
			})),
			want: "session path: /tmp/viewer.jsonl",
		},
		{
			name: "plan search",
			out: stripAnsi(renderPlanSearch(Model{
				width: 100,
				planResults: []plans.Result{{
					Meta: plans.PlanMeta{Name: "plan", DirPath: "/tmp/plan-dir"},
				}},
			})),
			want: "plan path: /tmp/plan-dir",
		},
		{
			name: "plan viewer",
			out: stripAnsi(renderPlanView(Model{
				width:      100,
				viewerPlan: &plans.PlanMeta{Name: "plan", DirPath: "/tmp/viewer-plan"},
			})),
			want: "plan path: /tmp/viewer-plan",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.out, tc.want) {
				t.Fatalf("rendered output missing %q:\n%s", tc.want, tc.out)
			}
		})
	}
}

func TestRenderPathRowTruncatesToViewportWidth(t *testing.T) {
	const width = 28
	row := stripAnsi(renderPathRow("session path", "/very/long/transcript/path/that/does/not/fit.jsonl", width))
	if got := runewidth.StringWidth(row); got > width {
		t.Fatalf("path row width = %d, want <= %d: %q", got, width, row)
	}
	if !strings.Contains(row, "…") {
		t.Fatalf("path row was not truncated: %q", row)
	}
	if !strings.Contains(row, "session path:") {
		t.Fatalf("path row lost its label: %q", row)
	}
}
