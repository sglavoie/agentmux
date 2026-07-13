package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/transcript"
)

type testSQLiteResultError struct {
	code int
}

func (e testSQLiteResultError) Error() string { return "sqlite result error" }
func (e testSQLiteResultError) Code() int     { return e.code }

func testCorruptionError() error {
	return fmt.Errorf("wrapped initialization failure: %w", testSQLiteResultError{code: 11})
}

func TestDataServicesCorruptionClosesCandidateBeforeExactlyOneRecovery(t *testing.T) {
	root := t.TempDir()
	var trace []string
	var recoveries atomic.Int32
	deps := defaultDataServicesDependencies()
	deps.openStore = func(ctx context.Context) (*datastore.Store, error) {
		trace = append(trace, "open")
		return datastore.OpenPath(ctx, filepath.Join(root, "agent-mux.db"))
	}
	deps.closeStore = func(store *datastore.Store) error {
		trace = append(trace, "close")
		return store.Close()
	}
	deps.loadTranscript = func(context.Context, *datastore.Store) (*transcript.Index, map[string]transcript.Reader, error) {
		return nil, nil, testCorruptionError()
	}
	deps.recoverCorrupt = func(context.Context, error) (datastore.RecoveryResult, func() error, error) {
		trace = append(trace, "recover")
		recoveries.Add(1)
		return datastore.RecoveryResult{Replaced: true, QuarantineDir: filepath.Join(root, "recovery", "now")}, func() error {
			trace = append(trace, "restart")
			return nil
		}, nil
	}
	services := newDataServices(deps)
	t.Cleanup(func() { _ = services.Close() })

	_, initErr := services.Initialize(t.Context(), &config.Config{})
	if !datastore.IsCorruption(initErr) {
		t.Fatalf("Initialize error = %v, want wrapped corruption", initErr)
	}
	m := Model{
		ctx:                      t.Context(),
		cfg:                      &config.Config{},
		dataServices:             services,
		dataServicesInitInFlight: true,
		mode:                     ModeWorkspace,
	}
	updated, requestCmd := m.Update(dataServicesReadyMsg{err: initErr})
	m = updated.(Model)
	if requestCmd == nil || m.dataServicesRecoveryState != dataServicesRecoveryRequested || m.dataServicesRetryScheduled {
		t.Fatalf("corruption transition = cmd %v state %v retry %v", requestCmd != nil, m.dataServicesRecoveryState, m.dataServicesRetryScheduled)
	}
	request, ok := requestCmd().(dataServicesRecoveryRequestMsg)
	if !ok {
		t.Fatal("corruption did not dispatch a recovery request message")
	}
	updated, recoveryCmd := m.Update(request)
	m = updated.(Model)
	if recoveryCmd == nil || m.dataServicesRecoveryState != dataServicesRecoveryInFlight || m.dataServicesInitInFlight {
		t.Fatalf("recovery dispatch = cmd %v state %v init-in-flight %v", recoveryCmd != nil, m.dataServicesRecoveryState, m.dataServicesInitInFlight)
	}
	ready, ok := recoveryCmd().(dataServicesRecoveryReadyMsg)
	if !ok || ready.err != nil {
		t.Fatalf("recovery command result = %#v", ready)
	}
	if recoveries.Load() != 1 || strings.Join(trace, ",") != "open,close,recover" {
		t.Fatalf("recovery trace = %v, calls %d; candidate must close before one recovery", trace, recoveries.Load())
	}

	updated, quitCmd := m.Update(ready)
	m = updated.(Model)
	if quitCmd == nil {
		t.Fatal("successful replacement did not request a fresh process")
	}
	if _, ok := quitCmd().(tea.QuitMsg); !ok || m.dataServicesInitInFlight || !m.dataServicesProcessRestartRequested || m.dataServicesRecoveryState != dataServicesRecoverySucceeded {
		t.Fatalf("successful recovery = quit %v init-in-flight %v process-restart %v state %v", ok, m.dataServicesInitInFlight, m.dataServicesProcessRestartRequested, m.dataServicesRecoveryState)
	}
	if m.dataServicesRecoveryResult.QuarantineDir != ready.result.QuarantineDir {
		t.Fatalf("quarantine path = %q, want %q", m.dataServicesRecoveryResult.QuarantineDir, ready.result.QuarantineDir)
	}
	if m.workspaceLoadErr != nil || m.sessionRefreshErr != nil || m.planRefreshErr != nil || m.unifiedSearchErr != nil {
		t.Fatalf("recovery success retained data-service errors: workspace=%v session=%v plan=%v unified=%v", m.workspaceLoadErr, m.sessionRefreshErr, m.planRefreshErr, m.unifiedSearchErr)
	}
}

func TestDataServicesHealthyNoOpRecoveryStillRetriesInitialization(t *testing.T) {
	services := newDataServices(dataServicesDependencies{})
	m := Model{
		ctx:                       t.Context(),
		cfg:                       &config.Config{},
		dataServices:              services,
		dataServicesRecoveryState: dataServicesRecoveryInFlight,
		workspaceLoadErr:          errors.New("old workspace error"),
		sessionRefreshErr:         errors.New("old session error"),
		planRefreshErr:            errors.New("old plan error"),
		unifiedSearchErr:          errors.New("old unified error"),
	}
	updated, cmd := m.Update(dataServicesRecoveryReadyMsg{result: datastore.RecoveryResult{Replaced: false}})
	m = updated.(Model)
	if cmd == nil || !m.dataServicesInitInFlight {
		t.Fatal("healthy no-op recovery did not immediately retry initialization")
	}
	if m.dataServicesRecoveryState != dataServicesRecoverySucceeded || m.dataServicesRecoveryResult.Replaced {
		t.Fatalf("healthy no-op state = %v result %#v", m.dataServicesRecoveryState, m.dataServicesRecoveryResult)
	}
	if m.workspaceLoadErr != nil || m.sessionRefreshErr != nil || m.planRefreshErr != nil || m.unifiedSearchErr != nil {
		t.Fatal("healthy no-op recovery did not clear data-service errors")
	}
}

func TestDataServicesRecoveryFailureAndRepeatedCorruptionAreTerminal(t *testing.T) {
	t.Run("recovery failure", func(t *testing.T) {
		services := newDataServices(dataServicesDependencies{})
		m := Model{
			ctx:                       t.Context(),
			cfg:                       &config.Config{},
			dataServices:              services,
			dataServicesRecoveryState: dataServicesRecoveryInFlight,
			workspaceLoading:          true,
			sessionLoading:            true,
			planLoading:               true,
			unifiedLoading:            true,
		}
		updated, cmd := m.Update(dataServicesRecoveryReadyMsg{err: errors.New("quarantine failed")})
		m = updated.(Model)
		if cmd != nil || m.dataServicesRecoveryState != dataServicesRecoveryFailed || m.dataServicesRetryScheduled {
			t.Fatalf("failure transition = cmd %v state %v retry %v", cmd != nil, m.dataServicesRecoveryState, m.dataServicesRetryScheduled)
		}
		if m.workspaceLoading || m.sessionLoading || m.planLoading || m.unifiedLoading {
			t.Fatal("terminal recovery left a loading indicator active")
		}
		if retry := m.dispatchDataServicesInit(); retry != nil {
			t.Fatal("terminal recovery allowed another initialization")
		}
	})

	t.Run("repeated corruption", func(t *testing.T) {
		m := Model{
			ctx:                       t.Context(),
			cfg:                       &config.Config{},
			dataServices:              newDataServices(dataServicesDependencies{}),
			dataServicesInitInFlight:  true,
			dataServicesRecoveryState: dataServicesRecoverySucceeded,
		}
		updated, cmd := m.Update(dataServicesReadyMsg{err: testCorruptionError()})
		m = updated.(Model)
		if cmd != nil || m.dataServicesRecoveryState != dataServicesRecoveryAlreadyAttempted || m.dataServicesRetryScheduled {
			t.Fatalf("repeated corruption = cmd %v state %v retry %v", cmd != nil, m.dataServicesRecoveryState, m.dataServicesRetryScheduled)
		}
	})

	t.Run("second request", func(t *testing.T) {
		m := Model{
			ctx:                       t.Context(),
			cfg:                       &config.Config{},
			dataServices:              newDataServices(dataServicesDependencies{}),
			dataServicesRecoveryState: dataServicesRecoveryInFlight,
		}
		updated, cmd := m.Update(dataServicesRecoveryRequestMsg{cause: testCorruptionError()})
		m = updated.(Model)
		if cmd != nil || m.dataServicesRecoveryState != dataServicesRecoveryAlreadyAttempted {
			t.Fatalf("second request = cmd %v state %v", cmd != nil, m.dataServicesRecoveryState)
		}
	})
}

func TestDataServicesRecoveryCancellationAndQuitDoNotPublishState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	services := newDataServices(dataServicesDependencies{
		recoverCorrupt: func(ctx context.Context, _ error) (datastore.RecoveryResult, func() error, error) {
			close(started)
			<-ctx.Done()
			return datastore.RecoveryResult{Replaced: true, QuarantineDir: "/should/not/publish"}, nil, ctx.Err()
		},
	})
	m := Model{
		ctx:                       ctx,
		cfg:                       &config.Config{},
		dataServices:              services,
		dataServicesRecoveryState: dataServicesRecoveryRequested,
	}
	updated, recoveryCmd := m.Update(dataServicesRecoveryRequestMsg{cause: testCorruptionError()})
	m = updated.(Model)
	if recoveryCmd == nil || m.dataServicesRecoveryState != dataServicesRecoveryInFlight {
		t.Fatal("recovery did not enter in-flight state")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- recoveryCmd() }()
	<-started
	cancel()
	closeDone := make(chan error, 1)
	go func() { closeDone <- services.Close() }()
	ready := (<-result).(dataServicesRecoveryReadyMsg)
	if !errors.Is(ready.err, context.Canceled) {
		t.Fatalf("recovery result = %v, want context canceled", ready.err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
	updated, cmd := m.Update(ready)
	m = updated.(Model)
	if cmd != nil || m.dataServicesRecoveryState != dataServicesRecoveryInFlight || m.dataServicesRecoveryResult.Replaced || len(m.toasts) != 0 {
		t.Fatalf("canceled recovery published state: cmd %v state %v result %#v toasts %#v", cmd != nil, m.dataServicesRecoveryState, m.dataServicesRecoveryResult, m.toasts)
	}
}

func TestDataServicesCloseRestartsDaemonWhenRebuildNeverStarts(t *testing.T) {
	var restarts atomic.Int32
	services := newDataServices(dataServicesDependencies{
		recoverCorrupt: func(context.Context, error) (datastore.RecoveryResult, func() error, error) {
			return datastore.RecoveryResult{Replaced: true}, func() error {
				restarts.Add(1)
				return nil
			}, nil
		},
	})
	result, err := services.RecoverCorruption(t.Context(), testCorruptionError())
	if err != nil || !result.Replaced || restarts.Load() != 0 {
		t.Fatalf("recovery = %+v error %v restarts %d", result, err, restarts.Load())
	}
	if err := services.Close(); err != nil {
		t.Fatalf("close services: %v", err)
	}
	if restarts.Load() != 1 {
		t.Fatalf("close restarts = %d, want 1", restarts.Load())
	}
	if err := services.Close(); err != nil || restarts.Load() != 1 {
		t.Fatalf("second close = %v restarts %d", err, restarts.Load())
	}
}

func TestDataServicesRecoveryRenderingStates(t *testing.T) {
	t.Run("loading", func(t *testing.T) {
		m := Model{mode: ModeWorkspace, width: 160, workspaceLoading: true}
		if got := stripAnsi(renderWorkspaceHome(m)); !strings.Contains(got, "Loading project workspaces") {
			t.Fatalf("loading workspace render = %q", got)
		}
	})

	t.Run("recovering", func(t *testing.T) {
		m := Model{mode: ModeWorkspace, width: 160, dataServicesRecoveryState: dataServicesRecoveryInFlight, workspaceLoadErr: testCorruptionError()}
		workspaceView := stripAnsi(renderWorkspaceHome(m))
		if !strings.Contains(workspaceView, "Database corruption detected; preserving and rebuilding workspace data") {
			t.Fatalf("recovering workspace render = %q", workspaceView)
		}
		msg := currentStatusMessage(m, time.Now())
		if msg.Severity != statusWarning || !strings.Contains(msg.Text, "Database corruption detected") {
			t.Fatalf("recovering status = %#v", msg)
		}
	})

	t.Run("recovered", func(t *testing.T) {
		m := Model{
			mode:                      ModeWorkspace,
			width:                     200,
			dataServicesRecoveryState: dataServicesRecoverySucceeded,
			dataServicesRecoveryResult: datastore.RecoveryResult{
				Replaced:      true,
				QuarantineDir: "/tmp/recovery/20260713T120000Z",
			},
		}
		workspaceView := stripAnsi(renderWorkspaceHome(m))
		if !strings.Contains(workspaceView, m.dataServicesRecoveryResult.QuarantineDir) {
			t.Fatalf("recovered workspace render = %q", workspaceView)
		}
		msg := currentStatusMessage(m, time.Now())
		if msg.Severity != statusSuccess || !strings.Contains(msg.Text, m.dataServicesRecoveryResult.QuarantineDir) {
			t.Fatalf("recovered status = %#v", msg)
		}
	})

	t.Run("terminal", func(t *testing.T) {
		terminalErr := errors.New("replacement validation failed")
		m := Model{
			mode:                      ModeWorkspace,
			width:                     200,
			dataServicesRecoveryState: dataServicesRecoveryFailed,
			dataServicesRecoveryErr:   terminalErr,
			workspaceLoadErr:          terminalErr,
		}
		workspaceView := stripAnsi(renderWorkspaceHome(m))
		if !strings.Contains(workspaceView, "amx doctor") || strings.Contains(workspaceView, "retrying in the background") || strings.Contains(workspaceView, "attempt is scheduled") {
			t.Fatalf("terminal workspace render = %q", workspaceView)
		}
		msg := currentStatusMessage(m, time.Now())
		if msg.Severity != statusError || !strings.Contains(msg.Text, "Database recovery stopped") {
			t.Fatalf("terminal status = %#v", msg)
		}
	})
}

func TestRecoveredDataServicesDispatchOneModeWarmup(t *testing.T) {
	snapshot := dataServicesSnapshot{}
	services := newDataServices(dataServicesDependencies{})
	services.ready = true
	services.snapshot = snapshot

	tests := []struct {
		name   string
		model  Model
		assert func(*testing.T, Model, tea.Cmd)
	}{
		{
			name:  "workspace",
			model: Model{mode: ModeWorkspace, workspaceLoading: true},
			assert: func(t *testing.T, m Model, cmd tea.Cmd) {
				if cmd == nil || !m.workspaceProjectionInFlight || m.workspaceProjectionSeq != 1 {
					t.Fatalf("workspace warmup = cmd %v in-flight %v seq %d", cmd != nil, m.workspaceProjectionInFlight, m.workspaceProjectionSeq)
				}
			},
		},
		{
			name:  "session search",
			model: Model{mode: ModeSessionSearch, sessionLoading: true},
			assert: func(t *testing.T, m Model, cmd tea.Cmd) {
				if cmd == nil || !m.sessionRefreshInFlight {
					t.Fatalf("session warmup = cmd %v in-flight %v", cmd != nil, m.sessionRefreshInFlight)
				}
			},
		},
		{
			name:  "plan search",
			model: Model{mode: ModePlanSearch, planLoading: true},
			assert: func(t *testing.T, m Model, cmd tea.Cmd) {
				if cmd == nil || m.planSearchSeq != 1 {
					t.Fatalf("plan warmup = cmd %v seq %d", cmd != nil, m.planSearchSeq)
				}
			},
		},
		{
			name:  "unified search",
			model: Model{mode: ModeUnifiedSearch, unifiedQuery: "needle"},
			assert: func(t *testing.T, m Model, cmd tea.Cmd) {
				if cmd == nil || !m.unifiedLoading || m.unifiedSearchSeq != 1 {
					t.Fatalf("unified warmup = cmd %v loading %v seq %d", cmd != nil, m.unifiedLoading, m.unifiedSearchSeq)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := test.model
			m.ctx = t.Context()
			m.cfg = &config.Config{}
			m.dataServices = services
			m.dataServicesInitInFlight = true
			m.dataServicesRecoveryState = dataServicesRecoverySucceeded
			updated, cmd := m.Update(dataServicesReadyMsg{snapshot: snapshot})
			test.assert(t, updated.(Model), cmd)
		})
	}
}
