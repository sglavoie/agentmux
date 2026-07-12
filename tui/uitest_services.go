package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	activityjournal "github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/compatimport"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/plans"
	unifiedsearch "github.com/sglavoie/agentmux/internal/search"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
)

var errUITestLiveDataAccess = errors.New("uitest: hermetic fixture attempted to initialize live data services")

// newHermeticUITestServices creates a ready process owner backed by a
// file-based datastore and compatibility paths beneath a private temporary
// directory. It deliberately seeds through repositories, so transcript,
// plan, and unified-search fixture behavior exercises the same SQLite reads as
// production without consulting DefaultPath or operator indexes.
func newHermeticUITestServices(ctx context.Context, parent string) (*DataServices, dataServicesSnapshot, string, error) {
	root, err := os.MkdirTemp(parent, "agentmux-uitest-")
	if err != nil {
		return nil, dataServicesSnapshot{}, "", fmt.Errorf("uitest: creating fixture root: %w", err)
	}
	cleanupRoot := true
	defer func() {
		if cleanupRoot {
			_ = os.RemoveAll(root)
		}
	}()

	store, err := datastore.OpenPath(ctx, filepath.Join(root, "data", datastore.DatabaseFileName))
	if err != nil {
		return nil, dataServicesSnapshot{}, "", fmt.Errorf("uitest: opening fixture datastore: %w", err)
	}
	keepStore := false
	defer func() {
		if !keepStore {
			_ = store.Close()
		}
	}()

	planDir := filepath.Join(root, "plans", "fixture-plan")
	if err := os.MkdirAll(planDir, 0o700); err != nil {
		return nil, dataServicesSnapshot{}, "", fmt.Errorf("uitest: creating fixture plan: %w", err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "manifest.md"), []byte("# Fixture plan\n\nDeterministic UI harness content.\n"), 0o600); err != nil {
		return nil, dataServicesSnapshot{}, "", fmt.Errorf("uitest: writing fixture plan: %w", err)
	}

	now := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)
	transcriptRepo, err := transcript.NewRepository(store)
	if err != nil {
		return nil, dataServicesSnapshot{}, "", err
	}
	transcriptMeta := transcript.SessionMeta{
		ID: "fixture-conversation", Harness: "claude", Profile: "work", Profiles: []string{"work"},
		Project: "api", CWD: filepath.Join(root, "projects", "api"),
		StartedAt: now.Add(-time.Hour), LastAt: now,
		Path:         filepath.Join(root, "transcripts", "fixture.jsonl"),
		MessageCount: 2, Snippet: "deterministic transcript fixture",
	}
	if err := transcriptRepo.Upsert(ctx, transcriptMeta, "deterministic transcript fixture\nworkspace api"); err != nil {
		return nil, dataServicesSnapshot{}, "", fmt.Errorf("uitest: seeding transcript: %w", err)
	}

	planRepo, err := plans.NewRepository(store)
	if err != nil {
		return nil, dataServicesSnapshot{}, "", err
	}
	planMeta := plans.PlanMeta{
		Name: "fixture plan", DirPath: planDir, Files: []string{"manifest.md"},
		Size: 48, ModTime: now, Snippet: "Deterministic UI harness content.",
	}
	if err := planRepo.Upsert(ctx, planMeta, "fixture plan deterministic ui harness content"); err != nil {
		return nil, dataServicesSnapshot{}, "", fmt.Errorf("uitest: seeding plan: %w", err)
	}

	readers := buildTranscriptReaders()
	transcriptIndex, err := transcript.LoadSQLiteIndexWithStore(ctx, filepath.Join(root, "data", "transcripts.json"), store, readers, compatimport.SyncSkip)
	if err != nil || transcriptIndex == nil || !transcriptIndex.UsesSQLite() {
		return nil, dataServicesSnapshot{}, "", fmt.Errorf("uitest: attaching transcript fixture: %w", errors.Join(err, transcriptIndexReason(transcriptIndex)))
	}
	planIndex, err := plans.LoadSQLiteIndexWithStore(ctx, filepath.Join(root, "data", "plans.json"), store, compatimport.SyncSkip)
	if err != nil || planIndex == nil || !planIndex.UsesSQLite() {
		return nil, dataServicesSnapshot{}, "", fmt.Errorf("uitest: attaching plan fixture: %w", errors.Join(err, planIndexReason(planIndex)))
	}

	workspaceService := workspace.New(store)
	catalog, err := workspaceService.Synchronize(ctx, &config.Config{})
	if err != nil {
		return nil, dataServicesSnapshot{}, "", fmt.Errorf("uitest: preparing workspace fixture: %w", err)
	}
	activityRepository, err := activityjournal.NewRepository(store)
	if err != nil {
		return nil, dataServicesSnapshot{}, "", err
	}
	searchService, err := unifiedsearch.New(store)
	if err != nil {
		return nil, dataServicesSnapshot{}, "", err
	}
	results, searchErr := transcript.Search(transcriptIndex, readers, transcript.Query{})
	if searchErr != nil {
		return nil, dataServicesSnapshot{}, "", fmt.Errorf("uitest: loading transcript fixture: %w", searchErr)
	}

	snapshot := dataServicesSnapshot{
		transcriptIndex: transcriptIndex, transcriptReaders: readers,
		planIndex: planIndex, workspaceService: workspaceService, workspaceCatalog: catalog,
		activityRepository: activityRepository, unifiedSearch: searchService,
		transcriptResults: results,
	}
	services := newDataServices(dataServicesDependencies{
		openStore:  func(context.Context) (*datastore.Store, error) { return nil, errUITestLiveDataAccess },
		closeStore: func(store *datastore.Store) error { return store.Close() },
	})
	services.store = store
	services.snapshot = snapshot
	services.ready = true
	keepStore = true
	cleanupRoot = false
	return services, snapshot, root, nil
}

func transcriptIndexReason(index *transcript.Index) error {
	if index == nil {
		return errors.New("nil transcript index")
	}
	return index.DegradedReason()
}

func planIndexReason(index *plans.Index) error {
	if index == nil {
		return errors.New("nil plan index")
	}
	return index.DegradedReason()
}
