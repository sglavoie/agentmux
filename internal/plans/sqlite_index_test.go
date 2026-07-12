package plans

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/compatimport"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestBorrowedPlanAndTranscriptFacadesNeverCloseSharedStore(t *testing.T) {
	ctx := context.Background()
	store := datastore.OpenTest(t)
	root := t.TempDir()
	planIndex, err := LoadSQLiteIndexWithStore(ctx, filepath.Join(root, "plans.json"), store, compatimport.SyncSkip)
	if err != nil {
		t.Fatal(err)
	}
	transcriptIndex, err := transcript.LoadSQLiteIndexWithStore(ctx, filepath.Join(root, "transcripts.json"), store, nil, compatimport.SyncSkip)
	if err != nil {
		t.Fatal(err)
	}
	if err := planIndex.Close(); err != nil {
		t.Fatalf("close borrowed plan facade: %v", err)
	}
	if err := transcriptIndex.Close(); err != nil {
		t.Fatalf("close borrowed transcript facade: %v", err)
	}
	if err := store.DB().PingContext(ctx); err != nil {
		t.Fatalf("borrowed facade closed shared store: %v", err)
	}
}

func TestLoadSQLiteIndexWrapperOwnsAndClosesStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	idx, err := LoadSQLiteIndex(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	repo := idx.repository()
	if repo == nil {
		t.Fatalf("wrapper facade degraded: %v", idx.DegradedReason())
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := repo.store.DB().PingContext(context.Background()); err == nil {
		t.Fatal("owned wrapper store remained open after Close")
	}
}

func TestEnableSQLiteIndexImportFailureDoesNotCloseBorrowedStore(t *testing.T) {
	idx := newTestIndex(t)
	idx.entries[PlanKey("/plans/borrowed")] = PlanMeta{Name: "borrowed", DirPath: "/plans/borrowed"}
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	store := datastore.OpenTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	idx = EnableSQLiteIndex(ctx, idx, store)
	if idx.UsesSQLite() || idx.DegradedReason() == nil {
		t.Fatalf("import failure did not degrade facade: SQLite=%t reason=%v", idx.UsesSQLite(), idx.DegradedReason())
	}
	if err := store.DB().PingContext(context.Background()); err != nil {
		t.Fatalf("import failure closed borrowed store: %v", err)
	}
}
