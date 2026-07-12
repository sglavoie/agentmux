package tui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	activityjournal "github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/compatimport"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/plans"
	unifiedsearch "github.com/sglavoie/agentmux/internal/search"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
)

func TestAcceptanceConcurrentFreshStartupAcrossDaemonTUIAndCLI(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "data", datastore.DatabaseFileName)
	start := make(chan struct{})
	var startOnce sync.Once
	releaseStart := func() { startOnce.Do(func() { close(start) }) }
	t.Cleanup(releaseStart)
	ready := make(chan string, 3)

	openAfterBarrier := func(role string) func(context.Context) (*datastore.Store, error) {
		return func(ctx context.Context) (*datastore.Store, error) {
			ready <- role
			select {
			case <-start:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return datastore.OpenPath(ctx, databasePath)
		}
	}

	deps := dataServicesDependencies{
		openStore:  openAfterBarrier("tui"),
		closeStore: func(store *datastore.Store) error { return store.Close() },
		loadTranscript: func(ctx context.Context, store *datastore.Store) (*transcript.Index, map[string]transcript.Reader, error) {
			readers := buildTranscriptReaders()
			index, err := transcript.LoadSQLiteIndexWithStore(
				ctx, filepath.Join(root, "rollback", "transcripts.json"), store, readers, compatimport.SyncIfChanged,
			)
			return index, readers, err
		},
		loadPlans: func(ctx context.Context, store *datastore.Store) (*plans.Index, error) {
			return plans.LoadSQLiteIndexWithStore(
				ctx, filepath.Join(root, "rollback", "plans.json"), store, compatimport.SyncIfChanged,
			)
		},
		newWorkspace: workspace.New,
		newActivity:  activityjournal.NewRepository,
		newSearch:    unifiedsearch.New,
	}
	services := newDataServices(deps)
	t.Cleanup(func() {
		releaseStart()
		if err := services.Close(); err != nil {
			t.Errorf("close TUI services: %v", err)
		}
	})

	tuiResult := make(chan error, 1)
	go func() {
		_, err := services.Initialize(t.Context(), &config.Config{})
		tuiResult <- err
	}()

	daemonWriter := activityjournal.NewWriter(activityjournal.WriterOptions{
		OpenStore:     openAfterBarrier("daemon"),
		BatchSize:     1,
		FlushInterval: time.Millisecond,
	})
	t.Cleanup(daemonWriter.Close)
	if !daemonWriter.Enqueue(activityjournal.Item{
		DedupeKey: "acceptance:fresh-startup",
		Kind:      "startup",
		Summary:   "daemon fresh startup",
	}) {
		t.Fatal("daemon activity enqueue failed")
	}

	cliResult := make(chan error, 1)
	go func() {
		store, err := openAfterBarrier("cli")(t.Context())
		if err == nil {
			var status datastore.Status
			status, err = store.Inspect(t.Context())
			if err == nil && (!status.Healthy || !status.MigrationCurrent) {
				err = fmt.Errorf("CLI status after fresh startup is unhealthy: %+v", status)
			}
			err = errors.Join(err, store.Close())
		}
		cliResult <- err
	}()

	roles := make(map[string]bool, 3)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for len(roles) < 3 {
		select {
		case role := <-ready:
			roles[role] = true
		case <-deadline.C:
			releaseStart()
			t.Fatalf("startup barrier reached by %v; want daemon, TUI, and CLI", roles)
		}
	}
	releaseStart()

	if err := <-tuiResult; err != nil {
		t.Fatalf("TUI fresh startup: %v", err)
	}
	if err := <-cliResult; err != nil {
		t.Fatalf("CLI fresh startup: %v", err)
	}
	daemonWriter.Close()
	if metrics := daemonWriter.Metrics(); metrics.Persisted != 1 || metrics.WriteFailures != 0 || metrics.Degraded {
		t.Fatalf("daemon fresh startup metrics = %+v", metrics)
	}

	verification, err := datastore.OpenPath(t.Context(), databasePath)
	if err != nil {
		t.Fatalf("open final datastore: %v", err)
	}
	defer func() { _ = verification.Close() }()
	version, err := verification.SchemaVersion(t.Context())
	if err != nil || version != datastore.CurrentSchemaVersion {
		t.Fatalf("final schema version = %d, %v; want %d", version, err, datastore.CurrentSchemaVersion)
	}
	var migrations, activities int
	if err := verification.DB().QueryRowContext(t.Context(), `SELECT count(*) FROM schema_migrations`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if err := verification.DB().QueryRowContext(t.Context(), `SELECT count(*) FROM activity_items`).Scan(&activities); err != nil {
		t.Fatal(err)
	}
	if migrations != datastore.CurrentSchemaVersion || activities != 1 {
		t.Fatalf("final durable state = migrations %d activities %d", migrations, activities)
	}
}

func TestAcceptanceLongTranscriptRefreshRetainsLastGoodDashboardSnapshot(t *testing.T) {
	services, snapshot, root, err := newHermeticUITestServices(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("create hermetic services: %v", err)
	}
	t.Cleanup(func() {
		if err := services.Close(); err != nil {
			t.Errorf("close services: %v", err)
		}
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove fixture root: %v", err)
		}
	})

	home := filepath.Join(root, "slow-profile")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(home, "slow.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseReader := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseReader)
	reader := &blockingAcceptanceReader{
		started: started,
		release: release,
		metas: []transcript.SessionMeta{{
			ID: "slow-refresh", Harness: harness.Claude, Path: sourcePath,
			FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 1,
			StartedAt: time.Now().Add(-time.Minute), LastAt: time.Now(),
			Snippet: "fresh row after slow refresh", SearchText: "fresh row after slow refresh",
		}},
	}
	cfg := &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}}}
	lastGood := workspace.Snapshot{
		GeneratedAt: time.Now().Add(-time.Minute),
		Workspaces:  []workspace.WorkspaceSnapshot{{ID: "last-good", DisplayName: "Last good"}},
	}
	m := Model{
		ctx:               t.Context(),
		cfg:               cfg,
		dataServices:      services,
		transcriptIndex:   snapshot.transcriptIndex,
		transcriptReaders: map[string]transcript.Reader{harness.Claude: reader},
		sessionResults:    append([]transcript.Result(nil), snapshot.transcriptResults...),
		workspaceSnapshot: lastGood,
	}
	oldResults := append([]transcript.Result(nil), m.sessionResults...)
	updated, cmd := m.enterSessionSearch()
	m = updated.(Model)
	if cmd == nil || len(oldResults) == 0 {
		t.Fatalf("warm refresh setup = cmd %v old results %d", cmd != nil, len(oldResults))
	}
	done := make(chan any, 1)
	go func() { done <- cmd() }()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		releaseReader()
		t.Fatal("transcript refresh did not reach the blocking reader")
	}
	if !reflect.DeepEqual(m.sessionResults, oldResults) {
		t.Fatalf("in-flight refresh replaced warm rows: before %#v after %#v", oldResults, m.sessionResults)
	}
	if got := selectedWorkspaceID(m.workspaceSnapshot, 0); got != "last-good" {
		t.Fatalf("in-flight refresh replaced workspace snapshot with %q", got)
	}
	releaseReader()

	var msg any
	select {
	case msg = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("transcript refresh did not complete")
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.sessionRefreshErr != nil || m.sessionSearchErr != nil {
		t.Fatalf("completed refresh errors = refresh %v search %v", m.sessionRefreshErr, m.sessionSearchErr)
	}
	found := false
	for _, result := range m.sessionResults {
		if result.Meta.ID == "slow-refresh" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("completed refresh did not publish the new transcript: %#v", m.sessionResults)
	}
	if got := selectedWorkspaceID(m.workspaceSnapshot, 0); got != "last-good" {
		t.Fatalf("transcript refresh changed workspace snapshot to %q", got)
	}
}

func TestAcceptancePeriodicWorkspaceProjectionPerformsZeroDatastoreWrites(t *testing.T) {
	services, snapshot, root, err := newHermeticUITestServices(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("create hermetic services: %v", err)
	}
	t.Cleanup(func() {
		if err := services.Close(); err != nil {
			t.Errorf("close services: %v", err)
		}
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove fixture root: %v", err)
		}
	})

	projectDir := filepath.Join(root, "projects", "api")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: filepath.Join(root, "profiles", "work")}},
		Projects: []config.Session{{ID: "api", Title: "API", Directory: projectDir, Profile: "work"}},
	}
	catalog, err := snapshot.workspaceService.Synchronize(t.Context(), cfg)
	if err != nil {
		t.Fatalf("synchronize acceptance workspace: %v", err)
	}
	snapshot.workspaceCatalog = catalog
	services.stateMu.Lock()
	services.snapshot = snapshot
	services.stateMu.Unlock()

	observer, err := datastore.OpenPath(t.Context(), services.store.Path())
	if err != nil {
		t.Fatalf("open independent data-version observer: %v", err)
	}
	defer func() { _ = observer.Close() }()
	observer.DB().SetMaxOpenConns(1)
	observer.DB().SetMaxIdleConns(1)
	dataVersionBefore := sqliteDataVersion(t, observer.DB())

	m := Model{ctx: t.Context(), cfg: cfg, dataServices: services, workspaceCatalog: catalog}
	for poll := 0; poll < 3; poll++ {
		cmd := m.dispatchWorkspaceProjection()
		if cmd == nil {
			t.Fatalf("workspace projection poll %d returned nil command", poll)
		}
		msg := cmd()
		updated, follow := m.Update(msg)
		m = updated.(Model)
		if follow != nil || m.workspaceLoadErr != nil {
			t.Fatalf("workspace projection poll %d = follow %v error %v", poll, follow != nil, m.workspaceLoadErr)
		}
	}
	dataVersionAfter := sqliteDataVersion(t, observer.DB())
	if dataVersionAfter != dataVersionBefore {
		t.Fatalf("periodic workspace projection committed datastore writes: data_version %d -> %d", dataVersionBefore, dataVersionAfter)
	}
	if len(m.workspaceSnapshot.Workspaces) != 1 || m.workspaceSnapshot.Workspaces[0].ID != "api" {
		t.Fatalf("periodic workspace projection = %#v", m.workspaceSnapshot.Workspaces)
	}
}

type blockingAcceptanceReader struct {
	started chan struct{}
	release chan struct{}
	metas   []transcript.SessionMeta
	once    sync.Once
}

func (r *blockingAcceptanceReader) Harness() string { return harness.Claude }

func (r *blockingAcceptanceReader) Discover(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return append([]transcript.SessionMeta(nil), r.metas...), nil
}

func (*blockingAcceptanceReader) ReadMessages(string) ([]transcript.Message, error) { return nil, nil }

func sqliteDataVersion(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var version int64
	if err := db.QueryRowContext(t.Context(), `PRAGMA data_version`).Scan(&version); err != nil {
		t.Fatalf("read PRAGMA data_version: %v", err)
	}
	return version
}
