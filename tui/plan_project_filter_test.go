package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/plans"
	tuiplans "github.com/sglavoie/agentmux/tui/plans"
)

func planProjectFilterTestModel() Model {
	return Model{
		mode:   ModePlanSearch,
		width:  100,
		height: 30,
		cfg: &config.Config{Projects: []config.Session{
			{Title: "Primary"},
			{Title: "Alias"},
			{Title: "Unrepresented"},
		}},
		planResults: []plans.Result{
			{Meta: plans.PlanMeta{Name: "known", Project: "Primary", Projects: []string{"Primary", "Alias"}}},
			{Meta: plans.PlanMeta{Name: "unknown"}},
		},
	}
}

func TestPlanSearchProjectPickerOffersAliasesAndUnknown(t *testing.T) {
	t.Parallel()

	m := planProjectFilterTestModel()
	updated, cmd := m.Update(keyPress("r"))
	m = updated.(Model)

	if cmd != nil || m.mode != ModeChipPicker || chipPickerSourceMode(m) != ModePlanSearch {
		t.Fatalf("open project picker = mode %v source %v cmd %T", m.mode, chipPickerSourceMode(m), cmd)
	}
	want := []string{"", "Primary", "Alias", chipPickerUnknownProjectValue}
	if !slices.Equal(m.chipPickerOptions, want) {
		t.Fatalf("project options = %#v, want %#v", m.chipPickerOptions, want)
	}

	m.chipPickerCursor = len(m.chipPickerOptions) - 1
	updated, cmd = m.Update(keyPress("enter"))
	m = updated.(Model)
	if cmd == nil || m.mode != ModePlanSearch || m.planFilterProject.Kind != plans.ProjectUnknown {
		t.Fatalf("select Unknown = mode %v filter %#v cmd %T", m.mode, m.planFilterProject, cmd)
	}
	if got := m.currentPlanQuery().Project; got.Kind != plans.ProjectUnknown {
		t.Fatalf("query project = %#v, want Unknown", got)
	}
}

func TestPlanSearchProjectPickerCancelReturnsToPlans(t *testing.T) {
	t.Parallel()

	m := planProjectFilterTestModel()
	m = pressKey(m, "r")
	m = pressKey(m, "esc")
	if m.mode != ModePlanSearch {
		t.Fatalf("cancel mode = %v, want ModePlanSearch", m.mode)
	}
}

func TestPlanSearchAndViewerRenderProjectLabels(t *testing.T) {
	t.Parallel()

	known := plans.PlanMeta{Name: "known", Project: "Primary", Projects: []string{"Primary", "Alias"}, Files: []string{"manifest.md"}}
	if got := stripAnsi(tuiplans.FormatRow(plans.Result{Meta: known}, 100)); !strings.Contains(got, "[Primary]") {
		t.Fatalf("known row missing project label: %q", got)
	}
	if got := stripAnsi(tuiplans.FormatRow(plans.Result{Meta: plans.PlanMeta{Name: "legacy"}}, 100)); !strings.Contains(got, "[Unknown]") {
		t.Fatalf("unknown row missing project label: %q", got)
	}

	m := planProjectFilterTestModel()
	m.planFilterProject = plans.ProjectFilter{Kind: plans.ProjectUnknown}
	if got := stripAnsi(renderPlanSearch(m)); !strings.Contains(got, "[r] project: Unknown") {
		t.Fatalf("plan search missing active project chip:\n%s", got)
	}
	viewer := Model{width: 100, height: 20, viewerPlan: &known}
	if got := stripAnsi(renderPlanView(viewer)); !strings.Contains(got, "[project: Primary]") {
		t.Fatalf("plan viewer missing project label:\n%s", got)
	}
}
