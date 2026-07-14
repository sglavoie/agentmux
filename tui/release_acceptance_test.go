package tui

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	activityjournal "github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/compatimport"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/plans"
	unifiedsearch "github.com/sglavoie/agentmux/internal/search"
	"github.com/sglavoie/agentmux/internal/searchstore"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
)

func TestAcceptanceCorruptProfileMembershipRecoversCompatibilityDashboardOnce(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "data", datastore.DatabaseFileName)
	transcriptPath := filepath.Join(root, "data", "transcripts.json")
	planPath := filepath.Join(root, "data", "plans.json")
	cfg := seedRecoveryAcceptanceCompatibility(t, root, transcriptPath, planPath)
	preserved := truncateRecoveryAcceptanceProfileMembership(t, databasePath, transcriptPath, planPath, cfg)
	sourceTranscript := mustReadAcceptanceFile(t, transcriptPath)
	sourcePlans := mustReadAcceptanceFile(t, planPath)

	var recoveries atomic.Int32
	var trace []string
	daemonAlive := true
	candidateCloses := 0
	deps := defaultDataServicesDependencies()
	deps.openStore = func(ctx context.Context) (*datastore.Store, error) {
		trace = append(trace, "open")
		return datastore.OpenPath(ctx, databasePath)
	}
	deps.closeStore = func(store *datastore.Store) error {
		trace = append(trace, "close")
		candidateCloses++
		err := store.Close()
		if candidateCloses == 1 && err == nil {
			// Preserve the complete production artifact set. Empty companions are
			// valid regular artifacts and leave the deliberately truncated primary
			// database as the corruption authority for the locked quick-check.
			for _, path := range []string{databasePath + "-wal", databasePath + "-shm"} {
				if writeErr := os.WriteFile(path, nil, 0o600); writeErr != nil {
					t.Fatalf("write recovery acceptance companion %s: %v", path, writeErr)
				}
				preserved[path] = nil
			}
		}
		return err
	}
	deps.loadTranscript = func(ctx context.Context, store *datastore.Store) (*transcript.Index, map[string]transcript.Reader, error) {
		readers := buildTranscriptReaders()
		index, err := transcript.LoadSQLiteIndexWithStore(ctx, transcriptPath, store, readers, compatimport.SyncIfChanged)
		return index, readers, err
	}
	deps.loadPlans = func(ctx context.Context, store *datastore.Store) (*plans.Index, error) {
		return plans.LoadSQLiteIndexWithStore(ctx, planPath, store, compatimport.SyncIfChanged)
	}
	deps.recoverCorrupt = func(ctx context.Context, cause error) (datastore.RecoveryResult, func() error, error) {
		if !daemonAlive {
			return datastore.RecoveryResult{}, nil, errors.New("acceptance daemon is already stopped")
		}
		trace = append(trace, "daemon-shutdown")
		daemonAlive = false
		recoveries.Add(1)
		trace = append(trace, "recover")
		result, err := datastore.RecoverCorruptPath(ctx, databasePath, cause)
		if err != nil {
			trace = append(trace, "daemon-restart")
			daemonAlive = true
			return result, nil, err
		}
		return result, func() error {
			trace = append(trace, "daemon-restart")
			daemonAlive = true
			return nil
		}, nil
	}
	services := newDataServices(deps)
	t.Cleanup(func() {
		if err := services.Close(); err != nil {
			t.Errorf("close recovered acceptance services: %v", err)
		}
	})

	m := Model{
		ctx:          t.Context(),
		cfg:          cfg,
		dataServices: services,
		mode:         ModeWorkspace,
		width:        180,
	}
	initialCmd := m.dispatchDataServicesInit()
	if initialCmd == nil {
		t.Fatal("corrupt acceptance initialization did not dispatch")
	}
	initial := initialCmd().(dataServicesReadyMsg)
	if !datastore.IsCorruption(initial.err) || !strings.Contains(initial.err.Error(), "profile membership") {
		t.Fatalf("initialization error = %v, want wrapped profile-membership SQLITE_CORRUPT", initial.err)
	}

	updated, requestCmd := m.Update(initial)
	m = updated.(Model)
	request, ok := requestCmd().(dataServicesRecoveryRequestMsg)
	if !ok {
		t.Fatalf("corruption transition produced %T, want dataServicesRecoveryRequestMsg", requestCmd())
	}
	updated, recoveryCmd := m.Update(request)
	m = updated.(Model)
	ready, ok := recoveryCmd().(dataServicesRecoveryReadyMsg)
	if !ok || ready.err != nil || !ready.result.Replaced {
		t.Fatalf("recovery result = %#v", ready)
	}
	if recoveries.Load() != 1 || daemonAlive {
		t.Fatalf("recovery calls = %d daemon alive = %t", recoveries.Load(), daemonAlive)
	}
	if got, want := strings.Join(trace, ","), "open,close,daemon-shutdown,recover"; got != want {
		t.Fatalf("coordinated recovery trace = %q, want %q", got, want)
	}

	updated, quitCmd := m.Update(ready)
	m = updated.(Model)
	if quitCmd == nil {
		t.Fatal("post-recovery process restart was not requested")
	}
	if _, ok := quitCmd().(tea.QuitMsg); !ok || m.dataServicesInitInFlight || !m.dataServicesProcessRestartRequested || m.dataServicesRecoveryState != dataServicesRecoverySucceeded {
		t.Fatalf("post-recovery handoff = quit %v in-flight %v process-restart %v state %v", ok, m.dataServicesInitInFlight, m.dataServicesProcessRestartRequested, m.dataServicesRecoveryState)
	}

	// Model the process boundary explicitly: the recovering owner transfers
	// daemon-restart responsibility and closes without touching SQLite again;
	// a fresh owner performs the ordinary compatibility rebuild.
	finishRebuild := services.takeRebuildFinishForProcessHandoff()
	if finishRebuild == nil {
		t.Fatal("recovery did not transfer the pending daemon restart")
	}
	if err := services.Close(); err != nil {
		t.Fatalf("close pre-exec data services: %v", err)
	}
	if daemonAlive {
		t.Fatal("pre-exec owner restarted daemon before the fresh-process rebuild")
	}
	freshServices := newDataServices(deps)
	if err := freshServices.deferRebuildFinishAfterRecovery(finishRebuild); err != nil {
		t.Fatalf("receive daemon restart in fresh process: %v", err)
	}
	services = freshServices
	m.dataServices = freshServices
	m.dataServicesProcessRestartRequested = false
	snapshot, err := freshServices.Initialize(t.Context(), cfg)
	if err != nil {
		t.Fatalf("initialize replacement datastore: %v", err)
	}
	updated, projectionCmd := m.Update(dataServicesReadyMsg{snapshot: snapshot})
	m = updated.(Model)
	if projectionCmd == nil {
		t.Fatal("healthy replacement did not dispatch workspace projection")
	}
	projection, ok := projectionCmd().(workspaceProjectionMsg)
	if !ok || projection.err != nil {
		t.Fatalf("replacement workspace projection = %#v", projection)
	}
	updated, _ = m.Update(projection)
	m = updated.(Model)

	if recoveries.Load() != 1 || candidateCloses != 1 {
		t.Fatalf("replacement attempts = %d candidate closes = %d, want exactly one each", recoveries.Load(), candidateCloses)
	}
	if !daemonAlive {
		t.Fatal("daemon was not restarted after compatibility rebuild")
	}
	if got, want := strings.Join(trace, ","), "open,close,daemon-shutdown,recover,open,daemon-restart"; got != want {
		t.Fatalf("complete recovery trace = %q, want %q", got, want)
	}
	if len(snapshot.transcriptResults) != 1 || snapshot.planIndex.Len() != 1 || len(snapshot.workspaceCatalog.Workspaces()) != 1 {
		t.Fatalf("rebuilt compatibility state = transcripts %d plans %d workspaces %d", len(snapshot.transcriptResults), snapshot.planIndex.Len(), len(snapshot.workspaceCatalog.Workspaces()))
	}
	if len(m.workspaceSnapshot.Workspaces) != 1 || m.workspaceSnapshot.Workspaces[0].ConfiguredProjectID != "recovery-api" {
		t.Fatalf("usable dashboard snapshot = %#v", m.workspaceSnapshot.Workspaces)
	}
	searchResults, err := snapshot.unifiedSearch.Search(t.Context(), unifiedsearch.Query{Text: "acceptance"})
	if err != nil {
		t.Fatalf("search rebuilt compatibility state: %v", err)
	}
	foundConversation, foundPlan := false, false
	for _, result := range searchResults {
		foundConversation = foundConversation || result.EntityType == unifiedsearch.EntityConversation
		foundPlan = foundPlan || result.EntityType == unifiedsearch.EntityPlan
	}
	projectResults, err := snapshot.unifiedSearch.Search(t.Context(), unifiedsearch.Query{Text: "Recovery API"})
	if err != nil {
		t.Fatalf("search rebuilt workspace state: %v", err)
	}
	foundProject := false
	for _, result := range projectResults {
		foundProject = foundProject || result.EntityType == unifiedsearch.EntityProject
	}
	if !foundConversation || !foundPlan || !foundProject {
		t.Fatalf("rebuilt unified search types = acceptance %#v recovery %#v", searchResults, projectResults)
	}
	if !bytes.Equal(mustReadAcceptanceFile(t, transcriptPath), sourceTranscript) || !bytes.Equal(mustReadAcceptanceFile(t, planPath), sourcePlans) {
		t.Fatal("compatibility rebuild modified a read-only source index")
	}
	assertRecoveryAcceptanceQuarantine(t, ready.result, preserved)

	health, err := datastore.OpenPath(t.Context(), databasePath)
	if err != nil {
		t.Fatalf("open healthy replacement: %v", err)
	}
	defer func() { _ = health.Close() }()
	status, err := health.Inspect(t.Context())
	if err != nil || !status.Healthy || !status.MigrationCurrent {
		t.Fatalf("replacement status = %+v, %v", status, err)
	}
}

func TestAcceptanceTransientInitializationRetriesWithoutReplacement(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "data", datastore.DatabaseFileName)
	var attempts atomic.Int32
	var recoveries atomic.Int32
	deps := defaultDataServicesDependencies()
	deps.openStore = func(ctx context.Context) (*datastore.Store, error) {
		if attempts.Add(1) == 1 {
			return nil, errors.New("temporary acceptance open failure")
		}
		return datastore.OpenPath(ctx, databasePath)
	}
	deps.loadTranscript = func(ctx context.Context, store *datastore.Store) (*transcript.Index, map[string]transcript.Reader, error) {
		readers := buildTranscriptReaders()
		index, err := transcript.LoadSQLiteIndexWithStore(ctx, filepath.Join(root, "transcripts.json"), store, readers, compatimport.SyncIfChanged)
		return index, readers, err
	}
	deps.loadPlans = func(ctx context.Context, store *datastore.Store) (*plans.Index, error) {
		return plans.LoadSQLiteIndexWithStore(ctx, filepath.Join(root, "plans.json"), store, compatimport.SyncIfChanged)
	}
	deps.recoverCorrupt = func(context.Context, error) (datastore.RecoveryResult, func() error, error) {
		recoveries.Add(1)
		return datastore.RecoveryResult{}, nil, errors.New("transient error must not invoke recovery")
	}
	services := newDataServices(deps)
	t.Cleanup(func() { _ = services.Close() })
	m := Model{ctx: t.Context(), cfg: &config.Config{}, dataServices: services, mode: ModeWorkspace}

	first := m.dispatchDataServicesInit()().(dataServicesReadyMsg)
	updated, timerCmd := m.Update(first)
	m = updated.(Model)
	if timerCmd == nil || !m.dataServicesRetryScheduled || m.dataServicesRecoveryState != dataServicesRecoveryIdle {
		t.Fatalf("transient retry state = timer %v scheduled %v recovery %v", timerCmd != nil, m.dataServicesRetryScheduled, m.dataServicesRecoveryState)
	}
	updated, retryCmd := m.Update(dataServicesRetryMsg{})
	m = updated.(Model)
	if retryCmd == nil {
		t.Fatal("transient retry did not dispatch a second initialization")
	}
	second := retryCmd().(dataServicesReadyMsg)
	updated, _ = m.Update(second)
	m = updated.(Model)
	if second.err != nil || attempts.Load() != 2 || recoveries.Load() != 0 || m.dataServicesRetryScheduled {
		t.Fatalf("transient retry = err %v attempts %d recoveries %d scheduled %v", second.err, attempts.Load(), recoveries.Load(), m.dataServicesRetryScheduled)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(databasePath), "recovery")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transient initialization created recovery state: %v", err)
	}
}

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

func seedRecoveryAcceptanceCompatibility(t *testing.T, root, transcriptPath, planPath string) *config.Config {
	t.Helper()
	profileHome := filepath.Join(root, "profiles", "work")
	projectDir := filepath.Join(root, "projects", "api")
	planDir := filepath.Join(root, "plans", "recovery-plan-aabb112233cc")
	transcriptSource := filepath.Join(profileHome, "projects", "acceptance.jsonl")
	for _, dir := range []string{filepath.Dir(transcriptPath), filepath.Dir(transcriptSource), projectDir, planDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create recovery acceptance directory %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(transcriptSource, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "manifest.md"), []byte("# Recovery acceptance plan\n\nAcceptance rebuild authority.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	transcriptMeta := transcript.SessionMeta{
		ID: "recovery-acceptance", Harness: harness.Claude, Profile: "work", Profiles: []string{"work"},
		Project: "Recovery API", CWD: projectDir, Path: transcriptSource,
		FileSize: 3, FileModTime: now, StartedAt: now.Add(-time.Hour), LastAt: now,
		MessageCount: 1, Snippet: "acceptance transcript restored from compatibility", SearchText: "acceptance transcript restored from compatibility",
	}
	transcriptRaw, err := json.Marshal(struct {
		Entries map[string]transcript.SessionMeta `json:"entries"`
	}{Entries: map[string]transcript.SessionMeta{transcript.IndexKey(transcriptMeta.Harness, transcriptMeta.ID): transcriptMeta}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, transcriptRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := searchstore.New(transcriptPath).Write(transcript.IndexKey(transcriptMeta.Harness, transcriptMeta.ID), transcriptMeta.SearchText); err != nil {
		t.Fatalf("write transcript acceptance sidecar: %v", err)
	}

	planMeta := plans.PlanMeta{
		Name: "recovery plan", DirPath: planDir, Files: []string{"manifest.md"},
		Size: 55, ModTime: now, Snippet: "acceptance plan restored from compatibility",
	}
	planRaw, err := json.Marshal(struct {
		Entries map[string]plans.PlanMeta `json:"entries"`
	}{Entries: map[string]plans.PlanMeta{plans.PlanKey(planDir): planMeta}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, planRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := searchstore.New(planPath).Write(plans.PlanKey(planDir), "acceptance plan restored from compatibility"); err != nil {
		t.Fatalf("write plan acceptance sidecar: %v", err)
	}

	return &config.Config{
		Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: profileHome}},
		Projects: []config.Session{{ID: "recovery-api", Title: "Recovery API", Directory: projectDir, Profile: "work"}},
	}
}

func truncateRecoveryAcceptanceProfileMembership(t *testing.T, databasePath, transcriptPath, planPath string, cfg *config.Config) map[string][]byte {
	t.Helper()
	store, err := datastore.OpenPath(t.Context(), databasePath)
	if err != nil {
		t.Fatalf("open recovery acceptance seed database: %v", err)
	}
	readers := buildTranscriptReaders()
	transcriptIndex, err := transcript.LoadSQLiteIndexWithStore(t.Context(), transcriptPath, store, readers, compatimport.SyncIfChanged)
	if err != nil || transcriptIndex == nil || !transcriptIndex.UsesSQLite() {
		_ = store.Close()
		t.Fatalf("seed transcript compatibility import: %v", errors.Join(err, transcriptIndexReason(transcriptIndex)))
	}
	planIndex, err := plans.LoadSQLiteIndexWithStore(t.Context(), planPath, store, compatimport.SyncIfChanged)
	if err != nil || planIndex == nil || !planIndex.UsesSQLite() {
		_ = store.Close()
		t.Fatalf("seed plan compatibility import: %v", errors.Join(err, planIndexReason(planIndex)))
	}
	if _, err := workspace.New(store).Synchronize(t.Context(), cfg); err != nil {
		_ = store.Close()
		t.Fatalf("seed workspace compatibility projection: %v", err)
	}

	var conversationID int64
	if err := store.DB().QueryRowContext(t.Context(), `SELECT id FROM conversations WHERE harness = ? AND session_id = ?`, harness.Claude, "recovery-acceptance").Scan(&conversationID); err != nil {
		_ = store.Close()
		t.Fatalf("read acceptance conversation id: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `DELETE FROM legacy_import_entries WHERE source_name = 'legacy-transcripts-json-v1'`); err != nil {
		_ = store.Close()
		t.Fatalf("remove acceptance transcript ledger: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `DROP TABLE profile_memberships`); err != nil {
		_ = store.Close()
		t.Fatalf("relocate profile memberships: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `VACUUM`); err != nil {
		_ = store.Close()
		t.Fatalf("compact acceptance database before relocation: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE recovery_acceptance_padding (payload BLOB NOT NULL)`,
		`INSERT INTO recovery_acceptance_padding (payload) VALUES (zeroblob(8388608))`,
		`CREATE TABLE profile_memberships (
			conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
			profile_name TEXT NOT NULL,
			is_display_profile INTEGER NOT NULL DEFAULT 0 CHECK (is_display_profile IN (0, 1)),
			position INTEGER NOT NULL DEFAULT 0,
			created_at_ns INTEGER NOT NULL,
			PRIMARY KEY (conversation_id, profile_name)
		)`,
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			_ = store.Close()
			t.Fatalf("prepare relocated profile memberships: %v", err)
		}
	}
	if _, err := store.DB().ExecContext(t.Context(), `
		INSERT INTO profile_memberships (
			conversation_id, profile_name, is_display_profile, position, created_at_ns
		) VALUES (?, 'work', 1, 0, ?)
	`, conversationID, time.Now().UnixNano()); err != nil {
		_ = store.Close()
		t.Fatalf("seed relocated profile membership: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `
		WITH RECURSIVE memberships(position) AS (
			SELECT 1
			UNION ALL
			SELECT position + 1 FROM memberships WHERE position < 5000
		)
		INSERT INTO profile_memberships (
			conversation_id, profile_name, is_display_profile, position, created_at_ns
		)
		SELECT ?, printf('acceptance-%05d-%s', position, hex(zeroblob(64))), 0, position, ?
		FROM memberships
	`, conversationID, time.Now().UnixNano()); err != nil {
		_ = store.Close()
		t.Fatalf("expand relocated profile-membership B-tree: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		_ = store.Close()
		t.Fatalf("checkpoint relocated acceptance database: %v", err)
	}
	var pageSize, rootPage, autoIndexRoot, pageCount int64
	if err := store.DB().QueryRowContext(t.Context(), `PRAGMA page_size`).Scan(&pageSize); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT rootpage FROM sqlite_schema WHERE name = 'profile_memberships'`).Scan(&rootPage); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT rootpage FROM sqlite_schema WHERE name = 'sqlite_autoindex_profile_memberships_1'`).Scan(&autoIndexRoot); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `PRAGMA page_count`).Scan(&pageCount); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	lastRootPage := max(rootPage, autoIndexRoot)
	if rootPage < 32 || autoIndexRoot < 32 || lastRootPage >= pageCount {
		_ = store.Close()
		t.Fatalf("relocated profile roots = table %d autoindex %d of %d; fixture did not move them beyond durable metadata", rootPage, autoIndexRoot, pageCount)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close acceptance seed database: %v", err)
	}

	truncateAt := lastRootPage * pageSize
	if err := os.Truncate(databasePath, truncateAt); err != nil {
		t.Fatalf("truncate acceptance database at profile root page: %v", err)
	}
	corruptDatabase := mustReadAcceptanceFile(t, databasePath)
	if len(corruptDatabase) < 32 {
		t.Fatalf("truncated acceptance database is too small: %d", len(corruptDatabase))
	}
	// SQLite stores the database page count as a big-endian uint32 at header
	// bytes 28..31. Match the physical truncation boundary while retaining the
	// profile_memberships table and autoindex root pages. Their interior child
	// references point beyond that boundary. This mirrors the local failure
	// shape: the header and schema are internally plausible, but following the
	// membership B-tree reference reports SQLITE_CORRUPT.
	binary.BigEndian.PutUint32(corruptDatabase[28:32], uint32(lastRootPage))
	if err := os.WriteFile(databasePath, corruptDatabase, 0o600); err != nil {
		t.Fatalf("publish truncated acceptance header: %v", err)
	}

	// Change only JSON formatting after the first import. The source fingerprint
	// changes while metadata stays identical, forcing the legacy-upgrade seeding
	// path to read the now-truncated profile-membership B-tree.
	raw := mustReadAcceptanceFile(t, transcriptPath)
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	formatted.WriteByte('\n')
	if err := os.WriteFile(transcriptPath, formatted.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{databasePath: corruptDatabase}
}

func assertRecoveryAcceptanceQuarantine(t *testing.T, result datastore.RecoveryResult, preserved map[string][]byte) {
	t.Helper()
	if result.QuarantineDir == "" {
		t.Fatal("recovery result omitted quarantine directory")
	}
	entries, err := os.ReadDir(filepath.Dir(result.QuarantineDir))
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("recovery directories = %v, %v; want exactly one quarantine", entries, err)
	}
	manifestRaw := mustReadAcceptanceFile(t, filepath.Join(result.QuarantineDir, datastore.RecoveryManifestFileName))
	var manifest struct {
		DatabasePath       string `json:"database_path"`
		ReplacementOutcome string `json:"replacement_outcome"`
		Artifacts          []struct {
			Name           string `json:"name"`
			OriginalPath   string `json:"original_path"`
			QuarantinePath string `json:"quarantine_path"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatalf("decode recovery acceptance manifest: %v", err)
	}
	if manifest.DatabasePath != result.DatabasePath || manifest.ReplacementOutcome != "replaced" || len(manifest.Artifacts) != len(preserved) {
		t.Fatalf("recovery acceptance manifest = %+v", manifest)
	}
	seen := make(map[string]bool, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		want, ok := preserved[artifact.OriginalPath]
		if !ok || filepath.Dir(artifact.QuarantinePath) != result.QuarantineDir {
			t.Fatalf("unexpected recovery artifact = %+v", artifact)
		}
		got := mustReadAcceptanceFile(t, artifact.QuarantinePath)
		if !bytes.Equal(got, want) {
			t.Fatalf("quarantined artifact %s differs from original", artifact.Name)
		}
		seen[artifact.OriginalPath] = true
	}
	if len(seen) != len(preserved) {
		t.Fatalf("quarantined artifacts = %v, want %v", seen, preserved)
	}
}

func mustReadAcceptanceFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read acceptance file %s: %v", path, err)
	}
	return data
}
