package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	activityjournal "github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/compatimport"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/datastorerecovery"
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
	// restartAfterRecovery is guarded by writerMu. A previously live daemon
	// stays stopped until the first replacement initialization attempt has
	// finished all compatibility writes and workspace synchronization.
	restartAfterRecovery func() error
	closeErr             error
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
	recoverCorrupt func(context.Context, error) (datastore.RecoveryResult, func() error, error)
}

type dataServicesInitRequestMsg struct{}

type dataServicesRetryMsg struct{}

type dataServicesRecoveryRequestMsg struct {
	cause error
}

type dataServicesRecoveryReadyMsg struct {
	result datastore.RecoveryResult
	err    error
}

type dataServicesReadyMsg struct {
	snapshot dataServicesSnapshot
	err      error
}

type dataServicesRecoveryState uint8

const (
	dataServicesRecoveryIdle dataServicesRecoveryState = iota
	dataServicesRecoveryRequested
	dataServicesRecoveryInFlight
	dataServicesRecoverySucceeded
	dataServicesRecoveryFailed
	dataServicesRecoveryAlreadyAttempted
)

type dataServicesCandidateCloseError struct {
	initErr  error
	closeErr error
}

func (e *dataServicesCandidateCloseError) Error() string {
	return fmt.Sprintf("%v; closing failed datastore candidate: %v", e.initErr, e.closeErr)
}

func (e *dataServicesCandidateCloseError) Unwrap() []error {
	return []error{e.initErr, e.closeErr}
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
		newWorkspace:   workspace.New,
		newActivity:    activityjournal.NewRepository,
		newSearch:      unifiedsearch.New,
		recoverCorrupt: datastorerecovery.RecoverForRebuild,
	}
}

// Initialize builds the complete service snapshot in the required writer
// order. A failed attempt publishes nothing and closes its candidate pool so a
// later retry starts cleanly. Once ready, repeated calls are read-only.
func (s *DataServices) Initialize(ctx context.Context, cfg *config.Config) (_ dataServicesSnapshot, retErr error) {
	if s == nil {
		return dataServicesSnapshot{}, errors.New("tui data services: nil owner")
	}
	s.writerMu.Lock()
	defer s.writerMu.Unlock()
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, s.restartDaemonAfterRecovery())
		}
	}()

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
			if closeErr := s.deps.closeStore(store); closeErr != nil {
				retErr = &dataServicesCandidateCloseError{initErr: retErr, closeErr: closeErr}
			}
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
	if err := s.restartDaemonAfterRecovery(); err != nil {
		return dataServicesSnapshot{}, fmt.Errorf("restarting daemon after datastore rebuild: %w", err)
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

// RecoverCorruption serializes destructive recovery with initialization,
// refresh writers, and Close. Initialize has returned (and therefore closed
// its failed candidate Store) before this method can be dispatched.
func (s *DataServices) RecoverCorruption(ctx context.Context, cause error) (datastore.RecoveryResult, error) {
	if s == nil {
		return datastore.RecoveryResult{}, errors.New("tui data services: nil owner")
	}
	if ctx == nil {
		return datastore.RecoveryResult{}, errors.New("tui data services: recovery context is nil")
	}
	s.writerMu.Lock()
	defer s.writerMu.Unlock()

	s.stateMu.RLock()
	closed, ready := s.closed, s.ready
	s.stateMu.RUnlock()
	if closed {
		return datastore.RecoveryResult{}, errors.New("tui data services: owner is closed")
	}
	if ready {
		return datastore.RecoveryResult{}, errors.New("tui data services: refusing recovery while services are ready")
	}
	if err := ctx.Err(); err != nil {
		return datastore.RecoveryResult{}, err
	}
	if s.deps.recoverCorrupt == nil {
		return datastore.RecoveryResult{}, errors.New("tui data services: corruption recovery is unavailable")
	}
	result, restart, err := s.deps.recoverCorrupt(ctx, cause)
	if restart != nil {
		if s.restartAfterRecovery != nil {
			return result, errors.Join(err, errors.New("tui data services: daemon restart is already pending"))
		}
		s.restartAfterRecovery = restart
	}
	return result, err
}

func (s *DataServices) restartDaemonAfterRecovery() error {
	restart := s.restartAfterRecovery
	s.restartAfterRecovery = nil
	if restart == nil {
		return nil
	}
	return restart()
}

// takeDaemonRestartForProcessHandoff transfers responsibility for restoring a
// previously live daemon out of this process. It is used only after successful
// replacement, immediately before the dashboard closes all process-owned
// resources and re-execs. If re-exec fails, the old process invokes the
// returned fallback itself.
func (s *DataServices) takeDaemonRestartForProcessHandoff() func() error {
	if s == nil {
		return nil
	}
	s.writerMu.Lock()
	defer s.writerMu.Unlock()
	restart := s.restartAfterRecovery
	s.restartAfterRecovery = nil
	return restart
}

// deferDaemonRestartAfterRecovery installs daemon restart ownership received
// from a pre-recovery dashboard process. The fresh process invokes it only
// after compatibility import and workspace synchronization, or from Close on
// an early exit.
func (s *DataServices) deferDaemonRestartAfterRecovery(restart func() error) error {
	if s == nil {
		return errors.New("tui data services: nil owner")
	}
	if restart == nil {
		return nil
	}
	s.writerMu.Lock()
	defer s.writerMu.Unlock()
	s.stateMu.RLock()
	closed, ready := s.closed, s.ready
	s.stateMu.RUnlock()
	if closed {
		return errors.New("tui data services: owner is closed")
	}
	if ready {
		return errors.New("tui data services: services are already ready")
	}
	if s.restartAfterRecovery != nil {
		return errors.New("tui data services: daemon restart is already pending")
	}
	s.restartAfterRecovery = restart
	return nil
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
		s.closeErr = errors.Join(s.closeErr, s.restartDaemonAfterRecovery())
	})
	return s.closeErr
}

func (m *Model) dispatchDataServicesInit() tea.Cmd {
	if m == nil || m.dataServices == nil || m.dataServicesInitInFlight || m.dataServicesRetryScheduled {
		return nil
	}
	if m.dataServicesRecoveryState == dataServicesRecoveryRequested ||
		m.dataServicesRecoveryState == dataServicesRecoveryInFlight ||
		m.dataServicesRecoveryState == dataServicesRecoveryFailed ||
		m.dataServicesRecoveryState == dataServicesRecoveryAlreadyAttempted {
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
		m.setDataServicesErrors(msg.err)
		m.workspaceLoading = false
		if datastore.IsCorruption(msg.err) {
			var closeErr *dataServicesCandidateCloseError
			if errors.As(msg.err, &closeErr) {
				return m.failDataServicesRecovery(
					dataServicesRecoveryFailed,
					fmt.Errorf("automatic database recovery cannot start because the failed datastore did not close: %w", closeErr.closeErr),
				)
			}
			if m.dataServicesRecoveryState != dataServicesRecoveryIdle {
				return m.failDataServicesRecovery(
					dataServicesRecoveryAlreadyAttempted,
					fmt.Errorf("database remains corrupt after one automatic recovery attempt: %w", msg.err),
				)
			}
			m.dataServicesRetryScheduled = false
			m.dataServicesRecoveryState = dataServicesRecoveryRequested
			m.dataServicesRecoveryErr = nil
			return m, func() tea.Msg { return dataServicesRecoveryRequestMsg{cause: msg.err} }
		}
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
	m.sessionSearchErr = nil
	m.planRefreshErr = nil
	m.planSearchErr = nil
	m.unifiedSearchErr = nil
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
	case ModeUnifiedSearch:
		if m.pendingUnifiedPlanID == "" && m.pendingUnifiedConversationID == "" && strings.TrimSpace(m.unifiedQuery) != "" {
			if cmd := m.dispatchUnifiedSearch(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
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

func (m Model) handleDataServicesRecoveryRequest(msg dataServicesRecoveryRequestMsg) (tea.Model, tea.Cmd) {
	if m.ctx != nil && m.ctx.Err() != nil {
		return m, nil
	}
	if m.dataServicesRecoveryState == dataServicesRecoveryIdle {
		m.dataServicesRecoveryState = dataServicesRecoveryRequested
	}
	if m.dataServicesRecoveryState != dataServicesRecoveryRequested || m.dataServicesInitInFlight {
		return m.failDataServicesRecovery(
			dataServicesRecoveryAlreadyAttempted,
			fmt.Errorf("automatic database recovery was requested more than once"),
		)
	}
	if m.dataServices == nil {
		return m.failDataServicesRecovery(
			dataServicesRecoveryFailed,
			errors.New("automatic database recovery is unavailable: data services are nil"),
		)
	}
	m.dataServicesRecoveryState = dataServicesRecoveryInFlight
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	services := m.dataServices
	return m, func() tea.Msg {
		result, err := services.RecoverCorruption(ctx, msg.cause)
		return dataServicesRecoveryReadyMsg{result: result, err: err}
	}
}

func (m Model) handleDataServicesRecoveryReady(msg dataServicesRecoveryReadyMsg) (tea.Model, tea.Cmd) {
	if m.dataServicesRecoveryState != dataServicesRecoveryInFlight {
		return m, nil
	}
	if m.ctx != nil && m.ctx.Err() != nil {
		return m, nil
	}
	if msg.err != nil {
		return m.failDataServicesRecovery(
			dataServicesRecoveryFailed,
			fmt.Errorf("automatic database recovery failed permanently: %w", msg.err),
		)
	}

	m.dataServicesRecoveryState = dataServicesRecoverySucceeded
	m.dataServicesRecoveryResult = msg.result
	m.dataServicesRecoveryErr = nil
	m.dataServicesRetryAttempt = 0
	m.dataServicesRetryScheduled = false
	m.clearDataServicesErrors()
	m.workspaceLoading = m.mode == ModeWorkspace && len(m.workspaceSnapshot.Workspaces) == 0
	if msg.result.Replaced {
		// Replacing the database at the same pathname invalidates more than the
		// failed Store. modernc SQLite can retain WAL/shared-memory mappings at
		// process scope, and reopening the replacement in this process can read
		// those stale mappings. Quit cleanly and let RunFromWindow re-exec before
		// any compatibility or workspace write touches the fresh database.
		m.dataServicesProcessRestartRequested = true
		return m, tea.Quit
	}

	notice := "Database was repaired by another process; rebuilding workspace data…"
	cmds := make([]tea.Cmd, 0, 2)
	if cmd := m.pushToast(Toast{Severity: toastSuccess, Text: notice, Expires: time.Now().Add(progressToastTTL)}); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.dispatchDataServicesInit(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) setDataServicesErrors(err error) {
	m.workspaceLoadErr = err
	m.sessionRefreshErr = err
	m.sessionSearchErr = err
	m.planRefreshErr = err
	m.planSearchErr = err
	m.unifiedSearchErr = err
}

func (m *Model) clearDataServicesErrors() {
	m.workspaceLoadErr = nil
	m.sessionRefreshErr = nil
	m.sessionSearchErr = nil
	m.planRefreshErr = nil
	m.planSearchErr = nil
	m.unifiedSearchErr = nil
}

func (m Model) failDataServicesRecovery(state dataServicesRecoveryState, err error) (tea.Model, tea.Cmd) {
	m.dataServicesRecoveryState = state
	m.dataServicesRecoveryErr = err
	m.dataServicesRetryScheduled = false
	m.dataServicesInitInFlight = false
	m.setDataServicesErrors(err)
	m.workspaceLoading = false
	m.sessionLoading = false
	m.planLoading = false
	m.unifiedLoading = false
	return m, nil
}

func (m Model) dataServicesRecoveryActive() bool {
	return m.dataServicesRecoveryState == dataServicesRecoveryRequested ||
		m.dataServicesRecoveryState == dataServicesRecoveryInFlight
}

func (m Model) dataServicesRecoveryTerminal() bool {
	return m.dataServicesRecoveryState == dataServicesRecoveryFailed ||
		m.dataServicesRecoveryState == dataServicesRecoveryAlreadyAttempted
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
