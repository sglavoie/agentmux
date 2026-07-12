package tui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	activityjournal "github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/compatimport"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/plans"
	unifiedsearch "github.com/sglavoie/agentmux/internal/search"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
)

// DataServices owns every datastore-backed dependency used by one TUI
// process. Initialization and later index refreshes share writerMu so the TUI
// never starts competing SQLite writers against its own pool.
type DataServices struct {
	writerMu  sync.Mutex
	stateMu   sync.RWMutex
	closeOnce sync.Once

	deps     dataServicesDependencies
	ready    bool
	closed   bool
	store    *datastore.Store
	snapshot dataServicesSnapshot
	closeErr error
}

type dataServicesSnapshot struct {
	transcriptIndex     *transcript.Index
	transcriptReaders   map[string]transcript.Reader
	planIndex           *plans.Index
	workspaceService    *workspace.Service
	workspaceCatalog    workspace.Catalog
	activityRepository  *activityjournal.Repository
	unifiedSearch       *unifiedsearch.Service
	transcriptResults   []transcript.Result
	transcriptSearchErr error
}

type dataServicesDependencies struct {
	openStore      func(context.Context) (*datastore.Store, error)
	closeStore     func(*datastore.Store) error
	loadTranscript func(context.Context, *datastore.Store) (*transcript.Index, map[string]transcript.Reader, error)
	loadPlans      func(context.Context, *datastore.Store) (*plans.Index, error)
	newWorkspace   func(*datastore.Store) *workspace.Service
	newActivity    func(*datastore.Store) (*activityjournal.Repository, error)
	newSearch      func(*datastore.Store) (*unifiedsearch.Service, error)
}

type dataServicesInitRequestMsg struct{}

type dataServicesRetryMsg struct{}

type dataServicesReadyMsg struct {
	snapshot dataServicesSnapshot
	err      error
}

const (
	dataServicesRetryMin = 100 * time.Millisecond
	dataServicesRetryMax = 2 * time.Second
)

// NewDataServices creates the production process owner. The datastore is not
// opened until Initialize runs from the model's background command.
func NewDataServices() *DataServices {
	return newDataServices(defaultDataServicesDependencies())
}

func newDataServices(deps dataServicesDependencies) *DataServices {
	return &DataServices{deps: deps}
}

func defaultDataServicesDependencies() dataServicesDependencies {
	return dataServicesDependencies{
		openStore:  datastore.Open,
		closeStore: func(store *datastore.Store) error { return store.Close() },
		loadTranscript: func(ctx context.Context, store *datastore.Store) (*transcript.Index, map[string]transcript.Reader, error) {
			readers := buildTranscriptReaders()
			index, err := transcript.LoadSQLiteIndexWithStore(ctx, transcript.IndexPath(), store, readers, compatimport.SyncIfChanged)
			return index, readers, err
		},
		loadPlans: func(ctx context.Context, store *datastore.Store) (*plans.Index, error) {
			return plans.LoadSQLiteIndexWithStore(ctx, plans.IndexPath(), store, compatimport.SyncIfChanged)
		},
		newWorkspace: workspace.New,
		newActivity:  activityjournal.NewRepository,
		newSearch:    unifiedsearch.New,
	}
}

// Initialize builds the complete service snapshot in the required writer
// order. A failed attempt publishes nothing and closes its candidate pool so a
// later retry starts cleanly. Once ready, repeated calls are read-only.
func (s *DataServices) Initialize(ctx context.Context, cfg *config.Config) (dataServicesSnapshot, error) {
	if s == nil {
		return dataServicesSnapshot{}, errors.New("tui data services: nil owner")
	}
	s.writerMu.Lock()
	defer s.writerMu.Unlock()

	s.stateMu.RLock()
	if s.closed {
		s.stateMu.RUnlock()
		return dataServicesSnapshot{}, errors.New("tui data services: owner is closed")
	}
	if s.ready {
		snapshot := s.snapshot
		s.stateMu.RUnlock()
		return snapshot, nil
	}
	s.stateMu.RUnlock()
	if err := ctx.Err(); err != nil {
		return dataServicesSnapshot{}, err
	}

	store, err := s.deps.openStore(ctx)
	if err != nil {
		return dataServicesSnapshot{}, fmt.Errorf("opening datastore: %w", err)
	}
	keepStore := false
	defer func() {
		if !keepStore {
			_ = s.deps.closeStore(store)
		}
	}()

	transcriptIndex, readers, err := s.deps.loadTranscript(ctx, store)
	if err != nil {
		return dataServicesSnapshot{}, fmt.Errorf("synchronizing transcripts: %w", err)
	}
	if transcriptIndex == nil || !transcriptIndex.UsesSQLite() {
		if transcriptIndex != nil && transcriptIndex.DegradedReason() != nil {
			return dataServicesSnapshot{}, fmt.Errorf("synchronizing transcripts: %w", transcriptIndex.DegradedReason())
		}
		return dataServicesSnapshot{}, errors.New("synchronizing transcripts: SQLite facade is unavailable")
	}

	planIndex, err := s.deps.loadPlans(ctx, store)
	if err != nil {
		return dataServicesSnapshot{}, fmt.Errorf("synchronizing plans: %w", err)
	}
	if planIndex == nil || !planIndex.UsesSQLite() {
		if planIndex != nil && planIndex.DegradedReason() != nil {
			return dataServicesSnapshot{}, fmt.Errorf("synchronizing plans: %w", planIndex.DegradedReason())
		}
		return dataServicesSnapshot{}, errors.New("synchronizing plans: SQLite facade is unavailable")
	}

	workspaceService := s.deps.newWorkspace(store)
	catalog, err := workspaceService.Synchronize(ctx, cfg)
	if err != nil {
		return dataServicesSnapshot{}, fmt.Errorf("synchronizing workspaces: %w", err)
	}
	activityRepository, err := s.deps.newActivity(store)
	if err != nil {
		return dataServicesSnapshot{}, fmt.Errorf("creating activity repository: %w", err)
	}
	searchService, err := s.deps.newSearch(store)
	if err != nil {
		return dataServicesSnapshot{}, fmt.Errorf("creating unified search: %w", err)
	}
	results, searchErr := transcript.Search(transcriptIndex, readers, transcript.Query{})

	snapshot := dataServicesSnapshot{
		transcriptIndex:     transcriptIndex,
		transcriptReaders:   readers,
		planIndex:           planIndex,
		workspaceService:    workspaceService,
		workspaceCatalog:    catalog,
		activityRepository:  activityRepository,
		unifiedSearch:       searchService,
		transcriptResults:   results,
		transcriptSearchErr: searchErr,
	}
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return dataServicesSnapshot{}, errors.New("tui data services: owner closed during initialization")
	}
	s.store = store
	s.snapshot = snapshot
	s.ready = true
	s.stateMu.Unlock()
	keepStore = true
	return snapshot, nil
}

func (s *DataServices) Snapshot() (dataServicesSnapshot, bool) {
	if s == nil {
		return dataServicesSnapshot{}, false
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.snapshot, s.ready && !s.closed
}

// WithWriter serializes a post-initialization transcript or plan refresh with
// all other TUI-owned write work.
func (s *DataServices) WithWriter(ctx context.Context, fn func(dataServicesSnapshot) error) error {
	if s == nil {
		return errors.New("tui data services: nil owner")
	}
	s.writerMu.Lock()
	defer s.writerMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot, ok := s.Snapshot()
	if !ok {
		return errors.New("tui data services: services are not ready")
	}
	if err := fn(snapshot); err != nil {
		return err
	}
	return ctx.Err()
}

// Close releases the single process-owned pool exactly once. Borrowed index
// facades are deliberately not closed separately.
//
// RunFromWindow cancels the model context before calling Close, but Bubble Tea
// does not join an in-flight tea.Cmd before Program.Run returns. Keep the
// writerMu acquisition before closing the store: Initialize and WithWriter
// hold it while their pool work is in flight, so this is the load-bearing
// shutdown barrier that prevents a cancelled background command from using a
// closed datastore.
func (s *DataServices) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.writerMu.Lock()
		defer s.writerMu.Unlock()
		s.stateMu.Lock()
		s.closed = true
		store := s.store
		s.store = nil
		s.ready = false
		s.stateMu.Unlock()
		if store != nil {
			s.closeErr = s.deps.closeStore(store)
		}
	})
	return s.closeErr
}

func (m *Model) dispatchDataServicesInit() tea.Cmd {
	if m == nil || m.dataServices == nil || m.dataServicesInitInFlight || m.dataServicesRetryScheduled {
		return nil
	}
	if _, ready := m.dataServices.Snapshot(); ready {
		return nil
	}
	if m.ctx != nil && m.ctx.Err() != nil {
		return nil
	}
	m.dataServicesInitInFlight = true
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	services, cfg := m.dataServices, m.cfg
	return func() tea.Msg {
		snapshot, err := services.Initialize(ctx, cfg)
		return dataServicesReadyMsg{snapshot: snapshot, err: err}
	}
}

func (m Model) handleDataServicesReady(msg dataServicesReadyMsg) (tea.Model, tea.Cmd) {
	m.dataServicesInitInFlight = false
	if msg.err != nil {
		if m.ctx != nil && m.ctx.Err() != nil {
			return m, nil
		}
		m.workspaceLoadErr = msg.err
		m.sessionRefreshErr = msg.err
		m.planRefreshErr = msg.err
		m.workspaceLoading = false
		if m.dataServicesRetryScheduled {
			return m, nil
		}
		delay := dataServicesRetryDelay(m.dataServicesRetryAttempt)
		m.dataServicesRetryAttempt++
		m.dataServicesRetryScheduled = true
		return m, tea.Tick(delay, func(time.Time) tea.Msg { return dataServicesRetryMsg{} })
	}

	m.dataServicesRetryAttempt = 0
	m.dataServicesRetryScheduled = false
	m.workspaceLoadErr = nil
	m.sessionRefreshErr = nil
	m.planRefreshErr = nil
	m.transcriptIndex = msg.snapshot.transcriptIndex
	m.transcriptReaders = msg.snapshot.transcriptReaders
	m.plansIndex = msg.snapshot.planIndex
	m.workspaceCatalog = msg.snapshot.workspaceCatalog
	m.sessionResults = msg.snapshot.transcriptResults
	m.sessionSearchErr = msg.snapshot.transcriptSearchErr
	m.sessionLoading = false

	cmds := make([]tea.Cmd, 0, 3)
	if m.mode == ModeWorkspace || !m.workspaceSnapshot.GeneratedAt.IsZero() {
		if cmd := m.dispatchWorkspaceProjection(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	switch m.mode {
	case ModePlanSearch:
		updated, cmd := m.startPlanSearchRefresh()
		m = updated.(Model)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	case ModeSessionSearch:
		m.sessionRefreshInFlight = true
		m.sessionLoading = len(m.sessionResults) == 0
		cmds = append(cmds, m.runSessionWarmRefreshCmd(m.transcriptIndex, m.cfg, m.transcriptReaders, m.sessionSearchSeq, m.currentSessionQuery()))
	}
	if m.mode == ModeUnifiedSearch && (m.pendingUnifiedPlanID != "" || m.pendingUnifiedConversationID != "") {
		m.pendingUnifiedPlanID = ""
		m.pendingUnifiedConversationID = ""
		m.unifiedLoading = false
		updated, cmd := m.openUnifiedSearchResult()
		m = updated.(Model)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func dataServicesRetryDelay(attempt int) time.Duration {
	delay := dataServicesRetryMin
	for i := 0; i < attempt && delay < dataServicesRetryMax; i++ {
		delay *= 2
		if delay > dataServicesRetryMax {
			return dataServicesRetryMax
		}
	}
	return delay
}
