package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/plans"
	searchsvc "github.com/sglavoie/agentmux/internal/search"
	"github.com/sglavoie/agentmux/internal/workspace"
)

func TestPaletteOpensUnifiedSearchAndPreservesReturnMode(t *testing.T) {
	m := testModel()
	m.mode = ModeWorkspace
	m = m.enterPalette()
	m.paletteQuery = "Unified search"
	entries := paletteEntries(m)
	if len(entries) != 1 || entries[0].kind != paletteEntrySearch {
		t.Fatalf("filtered entries = %#v", entries)
	}
	updated, cmd := m.runPaletteEntry(entries[0])
	got := updated.(Model)
	if cmd != nil || got.mode != ModeUnifiedSearch || !got.unifiedInput {
		t.Fatalf("opened state = mode %v input %v cmd %v", got.mode, got.unifiedInput, cmd)
	}
	if got.unifiedReturnMode != ModeWorkspace {
		t.Fatalf("return mode = %v, want workspace", got.unifiedReturnMode)
	}
	got.unifiedInput = false
	got = pressKey(got, keyEsc)
	if got.mode != ModeWorkspace {
		t.Fatalf("mode after esc = %v, want workspace", got.mode)
	}
}

func TestUnifiedPlanResultWaitsForAsyncPlanIndex(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "release-aabb112233cc", map[string]string{"manifest.md": "# Release"})
	index, err := plans.LoadIndexAt(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Refresh(root); err != nil {
		t.Fatal(err)
	}
	planID := plans.PlanKey(planDir)
	m := testModel()
	m.mode = ModeUnifiedSearch
	m.plansIndex = nil
	m.unifiedResults = []searchsvc.Result{{EntityType: searchsvc.EntityPlan, EntityID: planID}}
	updated, loadCmd := m.openUnifiedSearchResult()
	m = updated.(Model)
	if loadCmd == nil || !m.dataServicesInitInFlight || !m.unifiedLoading || m.pendingUnifiedPlanID != planID {
		t.Fatalf("pending plan = cmd %v services-loading %v unified-loading %v id %q", loadCmd != nil, m.dataServicesInitInFlight, m.unifiedLoading, m.pendingUnifiedPlanID)
	}
	updated, _ = m.Update(dataServicesReadyMsg{snapshot: dataServicesSnapshot{planIndex: index}})
	m = updated.(Model)
	if m.mode != ModePlanView || m.pendingUnifiedPlanID != "" || m.unifiedLoading {
		t.Fatalf("resolved plan = mode %v pending %q loading %v", m.mode, m.pendingUnifiedPlanID, m.unifiedLoading)
	}
}

func TestParseUnifiedSearchQuerySeparatesTypedFilters(t *testing.T) {
	query, err := parseUnifiedSearchQuery("alpha OR beta type:plan workspace:ws-a harness:codex profile:work from:2026-07-01 to:2026-07-31")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if query.Text != "alpha OR beta" {
		t.Fatalf("text = %q", query.Text)
	}
	if len(query.Types) != 1 || query.Types[0] != searchsvc.EntityPlan || query.Workspaces[0] != "ws-a" || query.Harnesses[0] != "codex" || query.Profiles[0] != "work" {
		t.Fatalf("filters = %#v", query)
	}
	if query.From.IsZero() || query.To.IsZero() || !query.To.After(query.From) {
		t.Fatalf("dates = %v .. %v", query.From, query.To)
	}
	if _, err := parseUnifiedSearchQuery("needle type:"); err == nil {
		t.Fatal("missing filter value unexpectedly accepted")
	}
	if _, err := parseUnifiedSearchQuery("type:plan"); err == nil {
		t.Fatal("filters-only query unexpectedly accepted")
	}
}

func TestUnifiedSearchDropsStaleResultsAndRendersMetadata(t *testing.T) {
	m := testModel()
	m.mode = ModeUnifiedSearch
	m.width = 100
	m.height = 20
	m.unifiedSearchSeq = 2
	stale, _ := m.handleUnifiedSearchDone(unifiedSearchDoneMsg{seq: 1, results: []searchsvc.Result{{EntityID: "stale"}}})
	if len(stale.(Model).unifiedResults) != 0 {
		t.Fatal("stale result was applied")
	}
	result := searchsvc.Result{
		EntityID: "plan-1", EntityType: searchsvc.EntityPlan, Title: "Release plan",
		Snippet: "needle in rollout notes", OccurredAt: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC),
		PrimaryAction: searchsvc.Action{ID: "plan.open", Label: "open plan"},
	}
	updated, _ := m.handleUnifiedSearchDone(unifiedSearchDoneMsg{seq: 2, results: []searchsvc.Result{result}})
	got := updated.(Model)
	rendered := renderUnifiedSearch(got)
	for _, want := range []string{"Unified Search", "plan", "Release plan", "needle in rollout notes"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("render missing %q:\n%s", want, rendered)
		}
	}
}

func TestUnifiedProjectAndActivityResultsNavigateByStableWorkspaceID(t *testing.T) {
	for _, entityType := range []searchsvc.EntityType{searchsvc.EntityProject, searchsvc.EntityActivity} {
		t.Run(string(entityType), func(t *testing.T) {
			m := testModel()
			m.mode = ModeUnifiedSearch
			m.workspaceSnapshot = workspace.Snapshot{Workspaces: []workspace.WorkspaceSnapshot{
				{ID: "ws-a", DisplayName: "Alpha"},
				{ID: "ws-b", DisplayName: "Beta"},
			}}
			m.unifiedResults = []searchsvc.Result{{EntityType: entityType, EntityID: "entity", WorkspaceID: "ws-b"}}
			updated, cmd := m.openUnifiedSearchResult()
			got := updated.(Model)
			if cmd != nil || got.mode != ModeWorkspace || got.workspaceCursor != 1 {
				t.Fatalf("navigation = mode %v cursor %d cmd %v", got.mode, got.workspaceCursor, cmd)
			}
		})
	}
}
