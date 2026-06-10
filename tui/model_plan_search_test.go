package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/session"
)

// seedPlanDir writes a plan directory under root with the given files
// (relative path -> contents). Returns the absolute plan directory path.
func seedPlanDir(t *testing.T, root, name string, files map[string]string) string {
	t.Helper()
	planDir := filepath.Join(root, name)
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", planDir, err)
	}
	for rel, contents := range files {
		full := filepath.Join(planDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent: %v", err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return planDir
}

// planSearchTestModel returns a Model with two on-disk plan dirs indexed and
// the model placed directly into ModePlanSearch with the empty-query result
// set already populated. The mock runner is returned so callers can inspect
// the kitten calls issued by command dispatches under test.
func planSearchTestModel(t *testing.T) (Model, *kitty.MockRunner) {
	t.Helper()
	root := t.TempDir()
	seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md":  "# alpha plan\n\nalpha needle beta",
		"session-1.md": "needle body",
	})
	seedPlanDir(t, root, "beta-ccdd445566ee", map[string]string{
		"manifest.md":  "# beta plan\n\nno match here",
		"session-1.md": "completely unrelated body",
	})

	idx, err := plans.LoadIndexAt(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	results, err := plans.Search(idx, plans.Query{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("setup: want 2 indexed plans, got %d", len(results))
	}

	cfg := &config.Config{}
	cfg.Defaults.PlansDirectory = root
	cfg.Defaults.Editor = "myeditor"
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")

	m := Model{
		mode:        ModePlanSearch,
		width:       100,
		height:      30,
		cfg:         cfg,
		client:      client,
		plansIndex:  idx,
		planResults: results,
	}
	return m, runner
}

// TestEnterPlanSearch_TransitionsModeAndDispatchesRefresh verifies the
// entry-point sets ModePlanSearch and returns a non-nil cmd batch that
// includes the refresh tick.
func TestEnterPlanSearch_TransitionsModeAndDispatchesRefresh(t *testing.T) {
	cfg := &config.Config{}
	cfg.Defaults.PlansDirectory = t.TempDir()
	idx, err := plans.LoadIndexAt(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")

	m := Model{
		mode:       ModeList,
		width:      100,
		height:     30,
		cfg:        cfg,
		client:     client,
		plansIndex: idx,
	}
	updated, cmd := m.enterPlanSearch()
	got := updated.(Model)
	if got.mode != ModePlanSearch {
		t.Fatalf("mode = %v, want ModePlanSearch", got.mode)
	}
	if !got.planLoading {
		t.Errorf("planLoading = false, want true")
	}
	if got.planSearchSeq == 0 {
		t.Errorf("planSearchSeq = 0, want > 0")
	}
	if cmd == nil {
		t.Fatal("enterPlanSearch returned nil cmd; want refresh dispatch")
	}
}

// TestPlanSearch_TypingShrinksResultSet drives the dispatch chain end to end:
// ModePlanSearch → "/" → "needle" → debounce → search → done. After the
// final planSearchDoneMsg the model's planResults must contain only the
// entry whose corpus matches the typed needle.
func TestPlanSearch_TypingShrinksResultSet(t *testing.T) {
	m, _ := planSearchTestModel(t)
	if len(m.planResults) != 2 {
		t.Fatalf("setup: want 2 results, got %d", len(m.planResults))
	}

	m = pressKey(m, "/")
	if !m.planSearchMode {
		t.Fatalf("after `/`: planSearchMode=false, want true")
	}

	for _, r := range "needle" {
		m = pressKey(m, string(r))
	}
	if m.planQuery != "needle" {
		t.Fatalf("planQuery=%q, want %q", m.planQuery, "needle")
	}

	// Synthesize the debounce msg directly to avoid paying wall-clock time.
	debounceMsg := planSearchDebounceMsg{seq: m.planSearchSeq}
	updated, cmd := m.Update(debounceMsg)
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("debounce dispatch produced nil cmd; want runPlanSearchCmd")
	}
	doneMsg, ok := cmd().(planSearchDoneMsg)
	if !ok {
		t.Fatalf("debounce dispatch produced %T, want planSearchDoneMsg", doneMsg)
	}
	if doneMsg.err != nil {
		t.Fatalf("search returned error: %v", doneMsg.err)
	}

	updated, _ = m.Update(doneMsg)
	m = updated.(Model)
	if len(m.planResults) != 1 {
		t.Fatalf("after done: len(planResults)=%d, want 1", len(m.planResults))
	}
	if m.planResults[0].Meta.Name != "alpha" {
		t.Errorf("filtered result name=%q, want alpha", m.planResults[0].Meta.Name)
	}
	if m.planLoading {
		t.Errorf("planLoading still true after done msg")
	}
}

// TestPlanSearchLowercaseSRemainsSortKey pins the mode-scoped key contract:
// lowercase s sorts plan-search results and must not enter project-scoped
// Past Sessions from this mode.
func TestPlanSearchLowercaseSRemainsSortKey(t *testing.T) {
	m, _ := planSearchTestModel(t)
	m.planSortMode = plans.SortByMatches

	updated, cmd := m.Update(keyPress("s"))
	got := updated.(Model)

	if cmd != nil {
		t.Fatalf("plan search s returned cmd %T, want nil", cmd)
	}
	if got.mode != ModePlanSearch {
		t.Fatalf("mode = %v, want ModePlanSearch", got.mode)
	}
	if got.planSortMode != plans.SortByModTime {
		t.Fatalf("planSortMode = %v, want SortByModTime", got.planSortMode)
	}
	if singleChipValue(got.filterProject) != "" {
		t.Fatalf("filterProject = %#v, want unchanged empty project chip", got.filterProject)
	}

	updated, cmd = got.Update(keyPress("s"))
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("second plan search s returned cmd %T, want nil", cmd)
	}
	if got.mode != ModePlanSearch {
		t.Fatalf("second mode = %v, want ModePlanSearch", got.mode)
	}
	if got.planSortMode != plans.SortByMatches {
		t.Fatalf("second planSortMode = %v, want SortByMatches", got.planSortMode)
	}
}

// TestPlanSearch_OpenInEditorInvokesLaunch verifies pressing `o` on a row
// dispatches a tea.Cmd that drives kitten @ launch with the plan's directory.
func TestPlanSearch_OpenInEditorInvokesLaunch(t *testing.T) {
	m, runner := planSearchTestModel(t)

	_, cmd := m.Update(keyPress("o"))
	if cmd == nil {
		t.Fatal("`o` keypress produced nil cmd; want openPlanInEditorCmd")
	}

	// Drain the command — it invokes session.OpenPathInEditor synchronously,
	// which in turn issues the kitten @ launch call on the mock runner.
	if msg := cmd(); msg != nil {
		if errMsg, ok := msg.(planEditorErrMsg); ok {
			t.Fatalf("openPlanInEditorCmd surfaced an error: %v", errMsg.err)
		}
	}

	launchCalled := false
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		for _, a := range c.Args {
			if a == "launch" {
				launchCalled = true
				break
			}
		}
	}
	if !launchCalled {
		t.Error("expected kitten @ launch call after `o` keypress")
	}
}

func TestLoopRunKeyEntersLaunchPlanSearchWithProjectPrefill(t *testing.T) {
	root := t.TempDir()
	seedPlanDir(t, root, "api-aabb112233cc", map[string]string{
		"manifest.md":  "# api plan\n\napi rollout",
		"context.md":   "context",
		"session-1.md": "session",
	})
	idx, err := plans.LoadIndexAt(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatalf("LoadIndexAt: %v", err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	projectDir := t.TempDir()
	m := testModel()
	m.cfg.Defaults.PlansDirectory = root
	m.plansIndex = idx
	m = loadSessions(m, []session.RunningSession{{
		TabID:     1,
		Base:      "api",
		Project:   "api",
		Directory: projectDir,
	}})

	updated, cmd := m.Update(keyPress("F"))
	got := updated.(Model)
	if got.mode != ModeLoopRunPlanSearch {
		t.Fatalf("mode = %v, want ModeLoopRunPlanSearch", got.mode)
	}
	if got.planQuery != "api" {
		t.Fatalf("planQuery = %q, want project prefill %q", got.planQuery, "api")
	}
	if got.loopRunProjectDir != projectDir {
		t.Fatalf("loopRunProjectDir = %q, want %q", got.loopRunProjectDir, projectDir)
	}
	if got.loopRunProjectTitle != "api" {
		t.Fatalf("loopRunProjectTitle = %q, want api", got.loopRunProjectTitle)
	}
	if cmd == nil {
		t.Fatal("F launch picker entry returned nil cmd; want refresh dispatch")
	}
}

func TestPlanSearchEnterRemainsViewAction(t *testing.T) {
	m, _ := planSearchTestModel(t)

	updated, cmd := m.Update(keyPress("enter"))
	got := updated.(Model)
	if got.mode != ModePlanView {
		t.Fatalf("mode = %v, want ModePlanView", got.mode)
	}
	if got.viewerPlan == nil {
		t.Fatal("viewerPlan is nil after normal plan search enter")
	}
	if cmd == nil {
		t.Fatal("normal plan search enter returned nil cmd; want plan file load")
	}
}

func TestLoopRunPlanSearchEnterBuildsLaunchRequest(t *testing.T) {
	projectDir := t.TempDir()
	planDir := seedPlanDir(t, t.TempDir(), "api-aabb112233cc", map[string]string{
		"manifest.md":  "# api plan",
		"context.md":   "context",
		"session-1.md": "session",
	})
	m := testModel()
	m.mode = ModeLoopRunPlanSearch
	m.loopRunProjectDir = projectDir
	m.planResults = []plans.Result{{
		Meta: plans.PlanMeta{
			Name:    "api",
			DirPath: planDir,
			Files:   []string{"manifest.md", "context.md", "session-1.md"},
		},
	}}

	updated, cmd := m.Update(keyPress("enter"))
	got := updated.(Model)
	if got.mode != ModeLoopRunPlanSearch {
		t.Fatalf("mode after launch request = %v, want ModeLoopRunPlanSearch", got.mode)
	}
	if cmd == nil {
		t.Fatal("launch-mode enter returned nil cmd; want launch request command")
	}
	msg, ok := cmd().(loopRunPlanLaunchMsg)
	if !ok {
		t.Fatalf("launch request command produced %T, want loopRunPlanLaunchMsg", msg)
	}
	if msg.Err != nil {
		t.Fatalf("launch request validation failed: %v", msg.Err)
	}
	wantProject, err := agentloop.CanonicalizePath(projectDir)
	if err != nil {
		t.Fatalf("canonical project: %v", err)
	}
	wantPlan, err := agentloop.CanonicalizePath(planDir)
	if err != nil {
		t.Fatalf("canonical plan: %v", err)
	}
	if msg.Request.ProjectDir != wantProject {
		t.Fatalf("ProjectDir = %q, want %q", msg.Request.ProjectDir, wantProject)
	}
	if msg.Request.PlanDir != wantPlan {
		t.Fatalf("PlanDir = %q, want %q", msg.Request.PlanDir, wantPlan)
	}
	if msg.Plan.DirPath != wantPlan {
		t.Fatalf("Plan.DirPath = %q, want %q", msg.Plan.DirPath, wantPlan)
	}
}

func TestLoopRunPlanSearchVOpensViewerAndReturnsToLaunchPicker(t *testing.T) {
	planDir := seedPlanDir(t, t.TempDir(), "api-aabb112233cc", map[string]string{
		"manifest.md": "# api plan",
	})
	m := testModel()
	m.mode = ModeLoopRunPlanSearch
	m.loopRunProjectDir = t.TempDir()
	m.loopRunProjectTitle = "api"
	m.planResults = []plans.Result{{
		Meta: plans.PlanMeta{
			Name:    "api",
			DirPath: planDir,
			Files:   []string{"manifest.md"},
		},
	}}

	updated, cmd := m.Update(keyPress("v"))
	got := updated.(Model)
	if got.mode != ModePlanView {
		t.Fatalf("mode after v = %v, want ModePlanView", got.mode)
	}
	if got.planViewReturnMode != ModeLoopRunPlanSearch {
		t.Fatalf("planViewReturnMode = %v, want ModeLoopRunPlanSearch", got.planViewReturnMode)
	}
	if cmd == nil {
		t.Fatal("v returned nil cmd; want plan file load")
	}

	updated, _ = got.Update(keyPress("esc"))
	got = updated.(Model)
	if got.mode != ModeLoopRunPlanSearch {
		t.Fatalf("mode after viewer esc = %v, want ModeLoopRunPlanSearch", got.mode)
	}
	if got.loopRunProjectTitle != "api" {
		t.Fatalf("loopRunProjectTitle after viewer esc = %q, want api", got.loopRunProjectTitle)
	}
}

func TestLoopRunPlanSearchEscReturnsDashboard(t *testing.T) {
	m := testModel()
	m.mode = ModeLoopRunPlanSearch
	m.planQuery = "api"
	m.loopRunProjectDir = t.TempDir()
	m.loopRunProjectTitle = "api"

	updated, cmd := m.Update(keyPress("esc"))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("launch picker esc returned cmd %T, want nil", cmd)
	}
	if got.mode != ModeList {
		t.Fatalf("mode after esc = %v, want ModeList", got.mode)
	}
	if got.planQuery != "" {
		t.Fatalf("planQuery after launch exit = %q, want cleared", got.planQuery)
	}
	if got.loopRunProjectDir != "" || got.loopRunProjectTitle != "" {
		t.Fatalf("launch context after esc = (%q, %q), want cleared", got.loopRunProjectDir, got.loopRunProjectTitle)
	}
}
