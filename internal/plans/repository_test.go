package plans

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/workspace"
)

func newTestRepository(t *testing.T) (*Repository, *datastore.Store) {
	t.Helper()
	store := datastore.OpenTest(t)
	repo, err := NewRepository(store)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	return repo, store
}

func addTestWorkspace(t *testing.T, store *datastore.Store, id, path, title string) {
	t.Helper()
	now := time.Now().UnixNano()
	if _, err := store.DB().Exec(`
		INSERT INTO workspaces (id, canonical_path, display_name, configured_project_id, created_at_ns, updated_at_ns)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, path, title, id, now, now); err != nil {
		t.Fatalf("insert workspace %q: %v", id, err)
	}
}

func TestRepositoryLinksPersistAndSuggestionsDoNotWrite(t *testing.T) {
	ctx := context.Background()
	repo, store := newTestRepository(t)
	addTestWorkspace(t, store, "agent-mux", "/work/agentmux", "Agent Mux")
	addTestWorkspace(t, store, "other", "/work/other", "Other Workspace")

	pathPlan := PlanMeta{
		Name: "implementation", DirPath: "/plans/implementation", Files: []string{"manifest.md"},
		Size: 20, ModTime: time.Now().Add(-time.Minute), Snippet: "Agent Mux plan",
	}
	namePlan := PlanMeta{
		Name: "other-workspace-followup", DirPath: "/plans/other", Files: []string{"manifest.md"},
		Size: 10, ModTime: time.Now(), Snippet: "follow-up",
	}
	if err := repo.Upsert(ctx, pathPlan, "# plan\nrepository: /work/agentmux\n"); err != nil {
		t.Fatalf("Upsert path plan: %v", err)
	}
	if err := repo.Upsert(ctx, namePlan, "# follow-up"); err != nil {
		t.Fatalf("Upsert name plan: %v", err)
	}
	if err := repo.LinkWorkspace(ctx, PlanKey(pathPlan.DirPath), "agent-mux"); err != nil {
		t.Fatalf("LinkWorkspace: %v", err)
	}
	if err := repo.LinkWorkspace(ctx, PlanKey(pathPlan.DirPath), "agent-mux"); err != nil {
		t.Fatalf("idempotent LinkWorkspace: %v", err)
	}
	if err := repo.Upsert(ctx, pathPlan, "updated content /work/agentmux"); err != nil {
		t.Fatalf("Upsert must retain explicit links: %v", err)
	}
	ids, err := repo.WorkspaceIDs(ctx, PlanKey(pathPlan.DirPath))
	if err != nil || !reflect.DeepEqual(ids, []string{"agent-mux"}) {
		t.Fatalf("WorkspaceIDs = %v, %v", ids, err)
	}
	linked, err := repo.ListByWorkspace(ctx, "agent-mux")
	if err != nil || len(linked) != 1 || linked[0].DirPath != pathPlan.DirPath {
		t.Fatalf("ListByWorkspace = %#v, %v", linked, err)
	}

	suggestions, err := repo.SuggestWorkspaceLinks(ctx, []workspace.Workspace{
		{ID: "agent-mux", CanonicalPath: "/work/agentmux", DisplayName: "Agent Mux"},
		{ID: "other", CanonicalPath: "/work/other", DisplayName: "Other Workspace"},
	})
	if err != nil {
		t.Fatalf("SuggestWorkspaceLinks: %v", err)
	}
	if len(suggestions) != 1 || suggestions[0].PlanID != PlanKey(namePlan.DirPath) ||
		suggestions[0].WorkspaceID != "other" || suggestions[0].Reason != SuggestionNameMatch ||
		suggestions[0].Confidence != SuggestionLowConfidence {
		t.Fatalf("suggestions = %#v", suggestions)
	}
	if ids, err := repo.WorkspaceIDs(ctx, PlanKey(namePlan.DirPath)); err != nil || len(ids) != 0 {
		t.Fatalf("suggestions created an explicit link: ids=%v err=%v", ids, err)
	}

	if err := repo.UnlinkWorkspace(ctx, PlanKey(pathPlan.DirPath), "agent-mux"); err != nil {
		t.Fatalf("UnlinkWorkspace: %v", err)
	}
	suggestions, err = repo.SuggestWorkspaceLinks(ctx, []workspace.Workspace{{
		ID: "agent-mux", CanonicalPath: "/work/agentmux", DisplayName: "Agent Mux",
	}})
	if err != nil || len(suggestions) != 1 || suggestions[0].Reason != SuggestionCanonicalPath ||
		suggestions[0].Confidence != SuggestionHighConfidence {
		t.Fatalf("canonical suggestions = %#v, %v", suggestions, err)
	}
}

func TestEnableSQLiteIndexFallbackRetainsLegacyPlans(t *testing.T) {
	idx := newTestIndex(t)
	meta := PlanMeta{Name: "fallback", DirPath: "/plans/fallback", Files: []string{"manifest.md"}}
	idx.entries[PlanKey(meta.DirPath)] = meta
	idx = EnableSQLiteIndex(context.Background(), idx, nil)
	if idx.UsesSQLite() || idx.DegradedReason() == nil {
		t.Fatalf("fallback state: SQLite=%t reason=%v", idx.UsesSQLite(), idx.DegradedReason())
	}
	got, found := idx.Get(PlanKey(meta.DirPath))
	if !found || got.DirPath != meta.DirPath {
		t.Fatalf("legacy fallback plan = %+v, found=%t", got, found)
	}
}

func TestImportLegacyIndexIsIdempotentAndReadOnly(t *testing.T) {
	ctx := context.Background()
	repo, store := newTestRepository(t)
	now := time.Date(2026, 7, 11, 18, 40, 0, 123, time.UTC)
	repo.now = func() time.Time { return now }
	indexPath := filepath.Join(t.TempDir(), "plans.json")
	meta := PlanMeta{
		Name: "legacy", DirPath: "/plans/legacy", Files: []string{"manifest.md", "session-1.md"},
		Size: 123, ModTime: now.Add(-time.Hour), Snippet: "legacy snippet",
	}
	raw, err := json.MarshalIndent(indexFile{Entries: map[string]PlanMeta{PlanKey(meta.DirPath): meta}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	legacy, err := LoadIndexAt(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.store.Write(PlanKey(meta.DirPath), "legacy sidecar body"); err != nil {
		t.Fatal(err)
	}
	sidecarPath := legacy.store.Path(PlanKey(meta.DirPath))
	sidecarBefore, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}

	result, err := repo.ImportLegacyIndex(ctx, indexPath)
	if err != nil || !result.SourceFound || !result.Imported || result.RecordCount != 1 || len(result.Fingerprint) != 64 {
		t.Fatalf("ImportLegacyIndex = %#v, %v", result, err)
	}
	got, ok, err := repo.Get(ctx, PlanKey(meta.DirPath))
	if err != nil || !ok || got.Name != meta.Name || got.Size != meta.Size || !got.ModTime.Equal(meta.ModTime) ||
		!reflect.DeepEqual(got.Files, meta.Files) {
		t.Fatalf("imported meta = %#v, ok=%v err=%v", got, ok, err)
	}
	if content, ok, err := repo.SearchText(ctx, PlanKey(meta.DirPath)); err != nil || !ok || content != "legacy sidecar body" {
		t.Fatalf("SearchText = %q, %v, %v", content, ok, err)
	}
	var updatedBefore int64
	if err := store.DB().QueryRow(`SELECT updated_at_ns FROM plans WHERE id = ?`, PlanKey(meta.DirPath)).Scan(&updatedBefore); err != nil {
		t.Fatal(err)
	}
	repo.now = func() time.Time { return now.Add(time.Hour) }
	result, err = repo.ImportLegacyIndex(ctx, indexPath)
	if err != nil || result.Imported {
		t.Fatalf("second ImportLegacyIndex = %#v, %v", result, err)
	}
	var updatedAfter int64
	if err := store.DB().QueryRow(`SELECT updated_at_ns FROM plans WHERE id = ?`, PlanKey(meta.DirPath)).Scan(&updatedAfter); err != nil {
		t.Fatal(err)
	}
	jsonAfter, err := os.ReadFile(indexPath)
	if err != nil || !reflect.DeepEqual(jsonAfter, raw) {
		t.Fatalf("legacy JSON changed: err=%v", err)
	}
	sidecarAfter, err := os.ReadFile(sidecarPath)
	if err != nil || !reflect.DeepEqual(sidecarAfter, sidecarBefore) {
		t.Fatalf("legacy sidecar changed: err=%v", err)
	}
	if updatedAfter != updatedBefore {
		t.Fatalf("idempotent import updated row: before=%d after=%d", updatedBefore, updatedAfter)
	}
}

func TestSQLiteFacadeRefreshAndSearchLeaveLegacyArtifactsUntouched(t *testing.T) {
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md":  "# Alpha plan",
		"session-1.md": "body-only-needle",
	})
	legacy := newTestIndex(t)
	if err := legacy.Refresh(root); err != nil {
		t.Fatalf("legacy Refresh: %v", err)
	}
	indexBefore, err := os.ReadFile(legacy.Path())
	if err != nil {
		t.Fatal(err)
	}
	sidecarPath := legacy.store.Path(PlanKey(planDir))
	sidecarBefore, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}

	idx := EnableSQLiteIndex(context.Background(), legacy, datastore.OpenTest(t))
	if !idx.UsesSQLite() || idx.DegradedReason() != nil {
		t.Fatalf("SQLite facade unavailable: %v", idx.DegradedReason())
	}
	results, err := Search(idx, Query{Text: "body-only-needle"})
	if err != nil || len(results) != 1 || results[0].Meta.DirPath != planDir {
		t.Fatalf("SQLite search = %#v, %v", results, err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "session-1.md"), []byte("new-needle"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatalf("SQLite Refresh: %v", err)
	}
	results, err = Search(idx, Query{Text: "new-needle"})
	if err != nil || len(results) != 1 || results[0].Meta.DirPath != planDir {
		t.Fatalf("SQLite refreshed search = %#v, %v", results, err)
	}
	indexAfter, err := os.ReadFile(legacy.Path())
	if err != nil || !reflect.DeepEqual(indexAfter, indexBefore) {
		t.Fatalf("legacy index changed: err=%v", err)
	}
	sidecarAfter, err := os.ReadFile(sidecarPath)
	if err != nil || !reflect.DeepEqual(sidecarAfter, sidecarBefore) {
		t.Fatalf("legacy sidecar changed: err=%v", err)
	}
}

func TestSQLiteRefreshPreservesReferencedMissingPlanAndExplicitLink(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "linked-aabb112233cc", map[string]string{
		"manifest.md": "# Linked plan\nsearchable body",
	})
	idx := newTestIndex(t)
	store := datastore.OpenTest(t)
	idx = EnableSQLiteIndex(ctx, idx, store)
	if !idx.UsesSQLite() {
		t.Fatalf("SQLite facade degraded: %v", idx.DegradedReason())
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	repo, err := NewRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	addTestWorkspace(t, store, "linked", filepath.Join(root, "workspace"), "Linked")
	if err := repo.LinkWorkspace(ctx, PlanKey(planDir), "linked"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(planDir); err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(root); err != nil {
		t.Fatal(err)
	}
	if _, found := idx.Get(PlanKey(planDir)); !found {
		t.Fatal("referenced missing plan metadata was deleted")
	}
	if ids, err := repo.WorkspaceIDs(ctx, PlanKey(planDir)); err != nil || !reflect.DeepEqual(ids, []string{"linked"}) {
		t.Fatalf("explicit links after refresh = %v, %v", ids, err)
	}
	if _, found, err := repo.SearchText(ctx, PlanKey(planDir)); err != nil || found {
		t.Fatalf("stale search document found=%t err=%v", found, err)
	}
}
