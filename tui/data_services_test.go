package tui

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	activityjournal "github.com/sglavoie/agentmux/internal/activity"
	"github.com/sglavoie/agentmux/internal/compatimport"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/datastore"
	"github.com/sglavoie/agentmux/internal/plans"
	unifiedsearch "github.com/sglavoie/agentmux/internal/search"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/workspace"
)

func TestDataServicesInitializesInOrderAndClosesOnePoolOnce(t *testing.T) {
	root := t.TempDir()
	var traceMu sync.Mutex
	var trace []string
	appendTrace := func(value string) {
		traceMu.Lock()
		trace = append(trace, value)
		traceMu.Unlock()
	}
	var opens, closes atomic.Int32
	deps := dataServicesDependencies{
		openStore: func(ctx context.Context) (*datastore.Store, error) {
			appendTrace("open")
			opens.Add(1)
			return datastore.OpenPath(ctx, filepath.Join(root, "agent-mux.db"))
		},
		closeStore: func(store *datastore.Store) error {
			appendTrace("close")
			closes.Add(1)
			return store.Close()
		},
		loadTranscript: func(ctx context.Context, store *datastore.Store) (*transcript.Index, map[string]transcript.Reader, error) {
			appendTrace("transcripts")
			readers := buildTranscriptReaders()
			index, err := transcript.LoadSQLiteIndexWithStore(ctx, filepath.Join(root, "transcripts.json"), store, readers, compatimport.SyncIfChanged)
			return index, readers, err
		},
		loadPlans: func(ctx context.Context, store *datastore.Store) (*plans.Index, error) {
			appendTrace("plans")
			return plans.LoadSQLiteIndexWithStore(ctx, filepath.Join(root, "plans.json"), store, compatimport.SyncIfChanged)
		},
		newWorkspace: func(store *datastore.Store) *workspace.Service {
			appendTrace("workspaces")
			return workspace.New(store)
		},
		newActivity: func(store *datastore.Store) (*activityjournal.Repository, error) {
			appendTrace("activity")
			return activityjournal.NewRepository(store)
		},
		newSearch: func(store *datastore.Store) (*unifiedsearch.Service, error) {
			appendTrace("search")
			return unifiedsearch.New(store)
		},
	}
	services := newDataServices(deps)
	snapshot, err := services.Initialize(t.Context(), &config.Config{})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if snapshot.transcriptIndex == nil || snapshot.planIndex == nil || snapshot.workspaceService == nil || snapshot.activityRepository == nil || snapshot.unifiedSearch == nil || !snapshot.workspaceCatalog.Synchronized() {
		t.Fatalf("incomplete ready snapshot: %#v", snapshot)
	}
	if _, err := services.Initialize(t.Context(), &config.Config{}); err != nil {
		t.Fatalf("second Initialize: %v", err)
	}
	if opens.Load() != 1 {
		t.Fatalf("datastore opens = %d, want 1", opens.Load())
	}
	if err := services.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := services.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if closes.Load() != 1 {
		t.Fatalf("datastore closes = %d, want 1", closes.Load())
	}
	want := []string{"open", "transcripts", "plans", "workspaces", "activity", "search", "close"}
	if !reflect.DeepEqual(trace, want) {
		t.Fatalf("initialization trace = %v, want %v", trace, want)
	}
}

func TestDataServicesFailedInitializationRetriesWithoutPublishingPartialState(t *testing.T) {
	root := t.TempDir()
	deps := defaultDataServicesDependencies()
	var attempts atomic.Int32
	deps.openStore = func(ctx context.Context) (*datastore.Store, error) {
		if attempts.Add(1) == 1 {
			return nil, errors.New("temporary open failure")
		}
		return datastore.OpenPath(ctx, filepath.Join(root, "agent-mux.db"))
	}
	deps.loadTranscript = func(ctx context.Context, store *datastore.Store) (*transcript.Index, map[string]transcript.Reader, error) {
		readers := buildTranscriptReaders()
		index, err := transcript.LoadSQLiteIndexWithStore(ctx, filepath.Join(root, "transcripts.json"), store, readers, compatimport.SyncIfChanged)
		return index, readers, err
	}
	deps.loadPlans = func(ctx context.Context, store *datastore.Store) (*plans.Index, error) {
		return plans.LoadSQLiteIndexWithStore(ctx, filepath.Join(root, "plans.json"), store, compatimport.SyncIfChanged)
	}
	services := newDataServices(deps)
	t.Cleanup(func() { _ = services.Close() })
	if _, err := services.Initialize(t.Context(), &config.Config{}); err == nil {
		t.Fatal("first Initialize unexpectedly succeeded")
	}
	if _, ready := services.Snapshot(); ready {
		t.Fatal("failed initialization published a partial snapshot")
	}
	if _, err := services.Initialize(t.Context(), &config.Config{}); err != nil {
		t.Fatalf("retry Initialize: %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("open attempts = %d, want 2", attempts.Load())
	}
}

func TestDataServicesCancellationClosesFailedCandidate(t *testing.T) {
	root := t.TempDir()
	var closes atomic.Int32
	started := make(chan struct{})
	deps := defaultDataServicesDependencies()
	deps.openStore = func(ctx context.Context) (*datastore.Store, error) {
		return datastore.OpenPath(ctx, filepath.Join(root, "agent-mux.db"))
	}
	deps.closeStore = func(store *datastore.Store) error {
		closes.Add(1)
		return store.Close()
	}
	deps.loadTranscript = func(ctx context.Context, _ *datastore.Store) (*transcript.Index, map[string]transcript.Reader, error) {
		close(started)
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	services := newDataServices(deps)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := services.Initialize(ctx, &config.Config{})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Initialize error = %v, want context canceled", err)
	}
	if closes.Load() != 1 {
		t.Fatalf("candidate closes = %d, want 1", closes.Load())
	}
	if err := services.Close(); err != nil {
		t.Fatalf("Close after canceled init: %v", err)
	}
	if closes.Load() != 1 {
		t.Fatalf("Close closed candidate again: %d", closes.Load())
	}
}

func TestDataServicesRetryBackoffIsBounded(t *testing.T) {
	if got := dataServicesRetryDelay(0); got != dataServicesRetryMin {
		t.Fatalf("first delay = %v, want %v", got, dataServicesRetryMin)
	}
	if got := dataServicesRetryDelay(100); got != dataServicesRetryMax {
		t.Fatalf("bounded delay = %v, want %v", got, dataServicesRetryMax)
	}
}

func TestDataServicesModelInitializationIsSingleFlightAndHonorsBackoff(t *testing.T) {
	m := Model{cfg: &config.Config{}, dataServices: &DataServices{}}
	if first := m.dispatchDataServicesInit(); first == nil || !m.dataServicesInitInFlight {
		t.Fatalf("first dispatch = cmd %v in-flight %v", first != nil, m.dataServicesInitInFlight)
	}
	if duplicate := m.dispatchDataServicesInit(); duplicate != nil {
		t.Fatal("in-flight initialization dispatched duplicate work")
	}
	m.dataServicesInitInFlight = false
	m.dataServicesRetryScheduled = true
	if early := m.dispatchDataServicesInit(); early != nil {
		t.Fatal("scheduled backoff allowed an early retry")
	}
}
