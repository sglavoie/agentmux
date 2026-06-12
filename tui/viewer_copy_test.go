package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func runViewerCopyCmd(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("copy command is nil")
	}

	prev := copyToClipboard
	var copied []string
	copyToClipboard = func(text string) error {
		copied = append(copied, text)
		return nil
	}
	t.Cleanup(func() { copyToClipboard = prev })

	msg := cmd()
	done, ok := msg.(copyDoneMsg)
	if !ok {
		t.Fatalf("copy command returned %T, want copyDoneMsg", msg)
	}
	if done.err != nil {
		t.Fatalf("copy command err = %v, want nil", done.err)
	}
	if len(copied) != 1 {
		t.Fatalf("copy calls = %d, want 1", len(copied))
	}
	return copied[0]
}

func TestSessionViewerYCopiesSelectedMessage(t *testing.T) {
	m := Model{
		mode: ModeSessionView,
		viewerMessages: []transcript.Message{
			{Role: "user", Text: "first"},
			{Role: "assistant", Text: "selected"},
		},
		viewerCursor: 1,
		width:        80,
		height:       20,
	}

	updated, cmd := m.Update(keyPress("y"))
	got := updated.(Model)
	if got.mode != ModeSessionView {
		t.Fatalf("mode = %v, want ModeSessionView", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != "selected" {
		t.Fatalf("copied = %q, want selected message text", copied)
	}
}

func TestSessionViewerYCopiesBackingMultilineText(t *testing.T) {
	raw := "first line\nsecond\tline   still raw"
	if collapsed := collapseWhitespace(raw); collapsed == raw {
		t.Fatal("setup: raw text should differ from collapsed rendered text")
	}
	m := Model{
		mode: ModeSessionView,
		viewerMessages: []transcript.Message{
			{Role: "user", Text: raw},
		},
		viewerCursor: 0,
		width:        24,
		height:       20,
	}

	updated, cmd := m.Update(keyPress("y"))
	got := updated.(Model)
	if got.mode != ModeSessionView {
		t.Fatalf("mode = %v, want ModeSessionView", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != raw {
		t.Fatalf("copied = %q, want raw Message.Text %q", copied, raw)
	}
}

func TestSessionViewerYNoOpForEmptyOrInvalidSelection(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "empty",
			m:    Model{mode: ModeSessionView},
		},
		{
			name: "cursor below zero",
			m: Model{
				mode:           ModeSessionView,
				viewerMessages: []transcript.Message{{Text: "present"}},
				viewerCursor:   -1,
			},
		},
		{
			name: "cursor past end",
			m: Model{
				mode:           ModeSessionView,
				viewerMessages: []transcript.Message{{Text: "present"}},
				viewerCursor:   1,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated, cmd := tc.m.Update(keyPress("y"))
			got := updated.(Model)
			if got.mode != ModeSessionView {
				t.Fatalf("mode = %v, want ModeSessionView", got.mode)
			}
			if cmd != nil {
				t.Fatalf("cmd = %T, want nil", cmd)
			}
		})
	}
}

func TestSessionSearchCopyPathCopiesSelectedTranscriptPath(t *testing.T) {
	m := Model{
		mode: ModeSessionSearch,
		sessionResults: []transcript.Result{
			{Meta: transcript.SessionMeta{Path: "/tmp/first.jsonl"}},
			{Meta: transcript.SessionMeta{Path: "/tmp/selected.jsonl"}},
		},
		sessionCursor: 1,
		width:         80,
		height:        20,
	}

	updated, cmd := m.Update(keyPress("Y"))
	got := updated.(Model)
	if got.mode != ModeSessionSearch {
		t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != "/tmp/selected.jsonl" {
		t.Fatalf("copied = %q, want selected transcript path", copied)
	}
}

func TestSessionSearchCopyPathNoOpForEmptyOrInvalidSelection(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "empty results",
			m:    Model{mode: ModeSessionSearch},
		},
		{
			name: "cursor below zero",
			m: Model{
				mode:           ModeSessionSearch,
				sessionResults: []transcript.Result{{Meta: transcript.SessionMeta{Path: "/tmp/present.jsonl"}}},
				sessionCursor:  -1,
			},
		},
		{
			name: "cursor past end",
			m: Model{
				mode:           ModeSessionSearch,
				sessionResults: []transcript.Result{{Meta: transcript.SessionMeta{Path: "/tmp/present.jsonl"}}},
				sessionCursor:  1,
			},
		},
		{
			name: "empty path",
			m: Model{
				mode:           ModeSessionSearch,
				sessionResults: []transcript.Result{{Meta: transcript.SessionMeta{Path: ""}}},
				sessionCursor:  0,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated, cmd := tc.m.Update(keyPress("Y"))
			got := updated.(Model)
			if got.mode != ModeSessionSearch {
				t.Fatalf("mode = %v, want ModeSessionSearch", got.mode)
			}
			if cmd != nil {
				t.Fatalf("cmd = %T, want nil", cmd)
			}
		})
	}
}

func TestSessionSearchCopyPathIgnoredDuringTextEntry(t *testing.T) {
	m := Model{
		mode:              ModeSessionSearch,
		sessionSearchMode: true,
		sessionQuery:      "path:",
		sessionResults: []transcript.Result{
			{Meta: transcript.SessionMeta{Path: "/tmp/selected.jsonl"}},
		},
		sessionCursor: 0,
		width:         80,
		height:        20,
	}

	updated, cmd := m.Update(keyPress("Y"))
	got := updated.(Model)
	if got.sessionQuery != "path:Y" {
		t.Fatalf("sessionQuery = %q, want typed uppercase Y appended", got.sessionQuery)
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want search dispatch command")
	}
}

func TestSessionViewerCopyPathCopiesTranscriptPath(t *testing.T) {
	m := Model{
		mode:          ModeSessionView,
		viewerSession: &transcript.SessionMeta{Path: "/tmp/current.jsonl"},
		width:         80,
		height:        20,
	}

	updated, cmd := m.Update(keyPress("Y"))
	got := updated.(Model)
	if got.mode != ModeSessionView {
		t.Fatalf("mode = %v, want ModeSessionView", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != "/tmp/current.jsonl" {
		t.Fatalf("copied = %q, want current transcript path", copied)
	}
}

func TestSessionViewerCopyPathNoOpForNilOrEmptyPath(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "nil session",
			m:    Model{mode: ModeSessionView},
		},
		{
			name: "empty path",
			m: Model{
				mode:          ModeSessionView,
				viewerSession: &transcript.SessionMeta{},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated, cmd := tc.m.Update(keyPress("Y"))
			got := updated.(Model)
			if got.mode != ModeSessionView {
				t.Fatalf("mode = %v, want ModeSessionView", got.mode)
			}
			if cmd != nil {
				t.Fatalf("cmd = %T, want nil", cmd)
			}
		})
	}
}

func TestPlanViewerYCopiesSelectedLine(t *testing.T) {
	m := Model{
		mode:             ModePlanView,
		planViewerLines:  []string{"first", "selected"},
		planViewerCursor: 1,
		width:            80,
		height:           20,
	}

	updated, cmd := m.Update(keyPress("y"))
	got := updated.(Model)
	if got.mode != ModePlanView {
		t.Fatalf("mode = %v, want ModePlanView", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != "selected" {
		t.Fatalf("copied = %q, want selected plan line", copied)
	}
}

func TestPlanViewerYCopiesRawLineNotRenderedTruncation(t *testing.T) {
	raw := "this plan line is intentionally longer than the rendered viewport"
	m := Model{
		mode:             ModePlanView,
		planViewerLines:  []string{raw},
		planViewerCursor: 0,
		width:            16,
		height:           20,
	}

	updated, cmd := m.Update(keyPress("y"))
	got := updated.(Model)
	if got.mode != ModePlanView {
		t.Fatalf("mode = %v, want ModePlanView", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != raw {
		t.Fatalf("copied = %q, want raw plan line %q", copied, raw)
	}
}

func TestPlanViewerYNoOpForEmptyOrInvalidSelection(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "empty",
			m:    Model{mode: ModePlanView},
		},
		{
			name: "cursor below zero",
			m: Model{
				mode:             ModePlanView,
				planViewerLines:  []string{"present"},
				planViewerCursor: -1,
			},
		},
		{
			name: "cursor past end",
			m: Model{
				mode:             ModePlanView,
				planViewerLines:  []string{"present"},
				planViewerCursor: 1,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated, cmd := tc.m.Update(keyPress("y"))
			got := updated.(Model)
			if got.mode != ModePlanView {
				t.Fatalf("mode = %v, want ModePlanView", got.mode)
			}
			if cmd != nil {
				t.Fatalf("cmd = %T, want nil", cmd)
			}
		})
	}
}

func TestPlanSearchCopyPathCopiesSelectedPlanPath(t *testing.T) {
	m := Model{
		mode: ModePlanSearch,
		planResults: []plans.Result{
			{Meta: plans.PlanMeta{DirPath: "/tmp/first-plan"}},
			{Meta: plans.PlanMeta{DirPath: "/tmp/selected-plan"}},
		},
		planCursor: 1,
		width:      80,
		height:     20,
	}

	updated, cmd := m.Update(keyPress("Y"))
	got := updated.(Model)
	if got.mode != ModePlanSearch {
		t.Fatalf("mode = %v, want ModePlanSearch", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != "/tmp/selected-plan" {
		t.Fatalf("copied = %q, want selected plan path", copied)
	}
}

func TestPlanSearchCopyPathNoOpForEmptyOrInvalidSelection(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "empty results",
			m:    Model{mode: ModePlanSearch},
		},
		{
			name: "cursor below zero",
			m: Model{
				mode:        ModePlanSearch,
				planResults: []plans.Result{{Meta: plans.PlanMeta{DirPath: "/tmp/present-plan"}}},
				planCursor:  -1,
			},
		},
		{
			name: "cursor past end",
			m: Model{
				mode:        ModePlanSearch,
				planResults: []plans.Result{{Meta: plans.PlanMeta{DirPath: "/tmp/present-plan"}}},
				planCursor:  1,
			},
		},
		{
			name: "empty path",
			m: Model{
				mode:        ModePlanSearch,
				planResults: []plans.Result{{Meta: plans.PlanMeta{DirPath: ""}}},
				planCursor:  0,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated, cmd := tc.m.Update(keyPress("Y"))
			got := updated.(Model)
			if got.mode != ModePlanSearch {
				t.Fatalf("mode = %v, want ModePlanSearch", got.mode)
			}
			if cmd != nil {
				t.Fatalf("cmd = %T, want nil", cmd)
			}
		})
	}
}

func TestPlanSearchCopyPathIgnoredDuringTextEntry(t *testing.T) {
	m := Model{
		mode:           ModePlanSearch,
		planSearchMode: true,
		planQuery:      "path:",
		planResults: []plans.Result{
			{Meta: plans.PlanMeta{DirPath: "/tmp/selected-plan"}},
		},
		planCursor: 0,
		width:      80,
		height:     20,
	}

	updated, cmd := m.Update(keyPress("Y"))
	got := updated.(Model)
	if got.planQuery != "path:Y" {
		t.Fatalf("planQuery = %q, want typed uppercase Y appended", got.planQuery)
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want search dispatch command")
	}
}

func TestPlanViewerCopyPathCopiesPlanPath(t *testing.T) {
	m := Model{
		mode:       ModePlanView,
		viewerPlan: &plans.PlanMeta{DirPath: "/tmp/current-plan"},
		width:      80,
		height:     20,
	}

	updated, cmd := m.Update(keyPress("Y"))
	got := updated.(Model)
	if got.mode != ModePlanView {
		t.Fatalf("mode = %v, want ModePlanView", got.mode)
	}
	if copied := runViewerCopyCmd(t, cmd); copied != "/tmp/current-plan" {
		t.Fatalf("copied = %q, want current plan path", copied)
	}
}

func TestPlanViewerCopyPathNoOpForNilOrEmptyPath(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "nil plan",
			m:    Model{mode: ModePlanView},
		},
		{
			name: "empty path",
			m: Model{
				mode:       ModePlanView,
				viewerPlan: &plans.PlanMeta{},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated, cmd := tc.m.Update(keyPress("Y"))
			got := updated.(Model)
			if got.mode != ModePlanView {
				t.Fatalf("mode = %v, want ModePlanView", got.mode)
			}
			if cmd != nil {
				t.Fatalf("cmd = %T, want nil", cmd)
			}
		})
	}
}
