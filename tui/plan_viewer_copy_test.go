package tui

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/plans"
)

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
