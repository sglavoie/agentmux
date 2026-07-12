package plans

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/searchstore"
)

func TestImportLegacyPlansRefreshesOnlyChangedEntriesAndRetainsRemovedRows(t *testing.T) {
	ctx := context.Background()
	repo, store := newTestRepository(t)
	now := time.Now().Add(-time.Hour).UTC()
	repo.now = func() time.Time { return now }
	indexPath := filepath.Join(t.TempDir(), "plans.json")
	entries := map[string]PlanMeta{
		"/plans/alpha": {Name: "alpha", DirPath: "/plans/alpha", Files: []string{"manifest.md"}, Size: 10, ModTime: now},
		"/plans/beta":  {Name: "beta", DirPath: "/plans/beta", Files: []string{"manifest.md"}, Size: 20, ModTime: now},
	}
	writePlanLegacyIndex(t, indexPath, entries)
	sidecars := searchstore.New(indexPath)
	if err := sidecars.Write("/plans/alpha", "alpha body"); err != nil {
		t.Fatal(err)
	}
	if err := sidecars.Write("/plans/beta", "beta body"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ImportLegacyIndex(ctx, indexPath); err != nil {
		t.Fatalf("initial import: %v", err)
	}

	var alphaUpdatedBefore, betaUpdatedBefore int64
	if err := store.DB().QueryRow(`SELECT updated_at_ns FROM plans WHERE id = '/plans/alpha'`).Scan(&alphaUpdatedBefore); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT updated_at_ns FROM plans WHERE id = '/plans/beta'`).Scan(&betaUpdatedBefore); err != nil {
		t.Fatal(err)
	}
	if err := sidecars.Write("/plans/beta", "beta body changed and longer"); err != nil {
		t.Fatal(err)
	}

	sidecarReads := 0
	writeTransactions := 0
	repo.legacySidecarRead = func(indexPath, key string) (string, error) {
		sidecarReads++
		return readLegacySidecar(indexPath, key)
	}
	repo.legacyImportTransaction = func(ctx context.Context, fn func(*sql.Tx) error) error {
		writeTransactions++
		return store.Transaction(ctx, fn)
	}
	repo.now = func() time.Time { return now.Add(time.Hour) }
	result, err := repo.ImportLegacyIndex(ctx, indexPath)
	if err != nil || !result.Imported {
		t.Fatalf("changed import = %#v, %v", result, err)
	}
	if sidecarReads != 1 || writeTransactions != 1 {
		t.Fatalf("changed sync work: sidecar reads=%d write transactions=%d, want 1 and 1", sidecarReads, writeTransactions)
	}
	if content, ok, err := repo.SearchText(ctx, "/plans/beta"); err != nil || !ok || content != "beta body changed and longer" {
		t.Fatalf("changed beta content = %q, %v, %v", content, ok, err)
	}
	var alphaUpdatedAfter, betaUpdatedAfter int64
	if err := store.DB().QueryRow(`SELECT updated_at_ns FROM plans WHERE id = '/plans/alpha'`).Scan(&alphaUpdatedAfter); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT updated_at_ns FROM plans WHERE id = '/plans/beta'`).Scan(&betaUpdatedAfter); err != nil {
		t.Fatal(err)
	}
	if alphaUpdatedAfter != alphaUpdatedBefore || betaUpdatedAfter == betaUpdatedBefore {
		t.Fatalf("incremental row updates: alpha %d->%d beta %d->%d", alphaUpdatedBefore, alphaUpdatedAfter, betaUpdatedBefore, betaUpdatedAfter)
	}

	writePlanLegacyIndex(t, indexPath, map[string]PlanMeta{"/plans/alpha": entries["/plans/alpha"]})
	sidecarReads = 0
	writeTransactions = 0
	result, err = repo.ImportLegacyIndex(ctx, indexPath)
	if err != nil || !result.Imported || result.RecordCount != 1 {
		t.Fatalf("removal import = %#v, %v", result, err)
	}
	if sidecarReads != 0 || writeTransactions != 1 {
		t.Fatalf("removal sync work: sidecar reads=%d write transactions=%d, want 0 and 1", sidecarReads, writeTransactions)
	}
	if _, ok, err := repo.Get(ctx, "/plans/beta"); err != nil || !ok {
		t.Fatalf("removed rollback entry deleted durable beta: ok=%v err=%v", ok, err)
	}
}

func TestImportLegacyPlansSeedsMissingLedgerFromSQLiteWithoutBodyRead(t *testing.T) {
	ctx := context.Background()
	repo, store := newTestRepository(t)
	now := time.Now().UTC()
	repo.now = func() time.Time { return now }
	indexPath := filepath.Join(t.TempDir(), "plans.json")
	meta := PlanMeta{Name: "seed", DirPath: "/plans/seed", Files: []string{"session-1.md", "manifest.md"}, Size: 30, ModTime: now.Add(-time.Hour)}
	writePlanLegacyIndex(t, indexPath, map[string]PlanMeta{"/plans/seed": meta})
	if err := searchstore.New(indexPath).Write("/plans/seed", "seeded content"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ImportLegacyIndex(ctx, indexPath); err != nil {
		t.Fatal(err)
	}
	completedAt := time.Now().Add(time.Hour).UnixNano()
	if _, err := store.DB().Exec(`DELETE FROM legacy_import_entries WHERE source_name = ?`, legacyPlanImportSource); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE import_records SET fingerprint = 'v3-fingerprint', completed_at_ns = ? WHERE source_name = ?`, completedAt, legacyPlanImportSource); err != nil {
		t.Fatal(err)
	}

	sidecarReads := 0
	repo.legacySidecarRead = func(indexPath, key string) (string, error) {
		sidecarReads++
		return readLegacySidecar(indexPath, key)
	}
	result, err := repo.ImportLegacyIndex(ctx, indexPath)
	if err != nil || !result.Imported {
		t.Fatalf("bootstrap import = %#v, %v", result, err)
	}
	if sidecarReads != 0 {
		t.Fatalf("bootstrap sidecar reads = %d, want 0", sidecarReads)
	}
	var count int
	var contentHash string
	if err := store.DB().QueryRow(`SELECT count(*), max(content_hash) FROM legacy_import_entries WHERE source_name = ?`, legacyPlanImportSource).Scan(&count, &contentHash); err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(contentHash) != 64 {
		t.Fatalf("seeded ledger count=%d content hash=%q", count, contentHash)
	}

	if _, err := store.DB().Exec(`DELETE FROM legacy_import_entries WHERE source_name = ?`, legacyPlanImportSource); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE import_records SET fingerprint = 'older-v3-fingerprint', completed_at_ns = ? WHERE source_name = ?`, time.Now().Add(-time.Hour).UnixNano(), legacyPlanImportSource); err != nil {
		t.Fatal(err)
	}
	if err := searchstore.New(indexPath).Write("/plans/seed", "newer sidecar content"); err != nil {
		t.Fatal(err)
	}
	sidecarReads = 0
	result, err = repo.ImportLegacyIndex(ctx, indexPath)
	if err != nil || !result.Imported {
		t.Fatalf("newer-sidecar import = %#v, %v", result, err)
	}
	if sidecarReads != 1 {
		t.Fatalf("newer-sidecar reads = %d, want 1", sidecarReads)
	}
	if content, ok, err := repo.SearchText(ctx, "/plans/seed"); err != nil || !ok || content != "newer sidecar content" {
		t.Fatalf("newer-sidecar content = %q, %v, %v", content, ok, err)
	}
}

func writePlanLegacyIndex(t *testing.T, path string, entries map[string]PlanMeta) {
	t.Helper()
	raw, err := json.MarshalIndent(indexFile{Entries: entries}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
