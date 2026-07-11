package activity

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sglavoie/agentmux/internal/datastore"
)

const (
	// QueueCapacity bounds retained hook outcomes when SQLite is slow or down.
	QueueCapacity = 1024
	batchLimit    = 100
	flushInterval = 250 * time.Millisecond
	sweepInterval = time.Hour
)

// Metrics exposes the journal writer's health without exposing its queue or
// SQLite work to hook callers.
type Metrics struct {
	Queued        uint64 `json:"queued"`
	Persisted     uint64 `json:"persisted"`
	Dropped       uint64 `json:"dropped"`
	WriteFailures uint64 `json:"write_failures"`
	Degraded      bool   `json:"degraded"`
	LastError     string `json:"last_error,omitempty"`
}

// WriterOptions permits tests to inject a repository while production opens the
// datastore only from the background worker.
type WriterOptions struct {
	Repository    *Repository
	OpenStore     func(context.Context) (*datastore.Store, error)
	Keep          time.Duration
	BatchSize     int
	FlushInterval time.Duration
}

// Writer asynchronously persists meaningful activity. Enqueue is deliberately
// nonblocking: a full buffer records a drop instead of delaying hook ACKs.
type Writer struct {
	input chan Item
	done  chan struct{}
	stop  chan struct{}

	mu         sync.RWMutex
	closed     bool
	repository *Repository
	openStore  func(context.Context) (*datastore.Store, error)
	store      *datastore.Store
	keep       time.Duration
	batchSize  int
	flush      time.Duration

	queued        atomic.Uint64
	persisted     atomic.Uint64
	dropped       atomic.Uint64
	writeFailures atomic.Uint64
	degraded      atomic.Bool
	errorMu       sync.RWMutex
	lastError     string
}

// NewWriter starts a bounded background writer. It never opens SQLite on the
// caller goroutine; datastore errors become degraded writer health instead.
func NewWriter(options WriterOptions) *Writer {
	batchSize := options.BatchSize
	if batchSize <= 0 || batchSize > batchLimit {
		batchSize = batchLimit
	}
	flush := options.FlushInterval
	if flush <= 0 {
		flush = flushInterval
	}
	w := &Writer{
		input:      make(chan Item, QueueCapacity),
		done:       make(chan struct{}),
		stop:       make(chan struct{}),
		repository: options.Repository,
		openStore:  options.OpenStore,
		keep:       options.Keep,
		batchSize:  batchSize,
		flush:      flush,
	}
	go w.run()
	return w
}

// Enqueue records an item for persistence and returns false only when the
// bounded buffer is saturated or the writer has already closed.
func (w *Writer) Enqueue(item Item) bool {
	if w == nil {
		return false
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		w.dropped.Add(1)
		return false
	}
	select {
	case w.input <- item:
		w.queued.Add(1)
		return true
	default:
		w.dropped.Add(1)
		return false
	}
}

// Metrics returns a snapshot suitable for daemon health responses.
func (w *Writer) Metrics() Metrics {
	if w == nil {
		return Metrics{Degraded: true, LastError: "activity writer is unavailable"}
	}
	w.errorMu.RLock()
	lastError := w.lastError
	w.errorMu.RUnlock()
	return Metrics{
		Queued:        w.queued.Load(),
		Persisted:     w.persisted.Load(),
		Dropped:       w.dropped.Load(),
		WriteFailures: w.writeFailures.Load(),
		Degraded:      w.degraded.Load(),
		LastError:     lastError,
	}
}

// Close stops accepting items and makes one bounded final persistence attempt.
// It is safe to call more than once.
func (w *Writer) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.input)
		close(w.stop)
	}
	w.mu.Unlock()
	<-w.done
}

func (w *Writer) run() {
	defer close(w.done)
	defer func() {
		if w.store != nil {
			_ = w.store.Close()
		}
	}()

	if w.keep > 0 {
		w.sweep()
	}
	ticker := time.NewTicker(w.flush)
	defer ticker.Stop()
	nextSweep := time.Now().Add(sweepInterval)
	pending := make([]Item, 0, w.batchSize)
	var input <-chan Item
	for {
		if len(pending) >= w.batchSize {
			input = nil // bound retained failures to one batch; the channel bounds the rest.
		} else {
			input = w.input
		}
		select {
		case <-w.stop:
			w.drainAndFinal(pending)
			return
		case item, ok := <-input:
			if !ok {
				w.flushFinal(pending)
				return
			}
			pending = append(pending, item)
			if len(pending) >= w.batchSize && w.persist(pending) {
				pending = pending[:0]
			}
		case <-ticker.C:
			if len(pending) > 0 && w.persist(pending) {
				pending = pending[:0]
			}
			if w.keep > 0 && !time.Now().Before(nextSweep) {
				w.sweep()
				nextSweep = time.Now().Add(sweepInterval)
			}
		}
	}
}

func (w *Writer) flushFinal(pending []Item) {
	if len(pending) == 0 {
		return
	}
	if !w.persist(pending) {
		w.dropped.Add(uint64(len(pending)))
	}
}

// drainAndFinal makes shutdown finite even when SQLite is unavailable. Items
// already queued are attempted in normal-sized batches; once one final write
// fails, the remaining bounded backlog is counted as dropped rather than
// trapping daemon shutdown behind a permanently unavailable database.
func (w *Writer) drainAndFinal(pending []Item) {
	for item := range w.input {
		pending = append(pending, item)
	}
	for len(pending) > 0 {
		end := w.batchSize
		if end > len(pending) {
			end = len(pending)
		}
		if !w.persist(pending[:end]) {
			w.dropped.Add(uint64(len(pending)))
			return
		}
		pending = pending[end:]
	}
}

func (w *Writer) persist(items []Item) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	repository, err := w.ensureRepository(ctx)
	if err == nil {
		_, err = repository.InsertBatch(ctx, items)
	}
	if err != nil {
		w.recordFailure(err)
		return false
	}
	w.persisted.Add(uint64(len(items)))
	w.degraded.Store(false)
	w.errorMu.Lock()
	w.lastError = ""
	w.errorMu.Unlock()
	return true
}

func (w *Writer) sweep() {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	repository, err := w.ensureRepository(ctx)
	if err == nil {
		_, err = repository.Sweep(ctx, w.keep)
	}
	if err != nil {
		w.recordFailure(err)
		return
	}
	w.degraded.Store(false)
	w.errorMu.Lock()
	w.lastError = ""
	w.errorMu.Unlock()
}

func (w *Writer) ensureRepository(ctx context.Context) (*Repository, error) {
	if w.repository != nil {
		return w.repository, nil
	}
	if w.openStore == nil {
		return nil, errors.New("activity writer has no datastore opener")
	}
	store, err := w.openStore(ctx)
	if err != nil {
		return nil, err
	}
	repository, err := NewRepository(store)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	w.store = store
	w.repository = repository
	return repository, nil
}

func (w *Writer) recordFailure(err error) {
	w.writeFailures.Add(1)
	w.degraded.Store(true)
	w.errorMu.Lock()
	w.lastError = err.Error()
	w.errorMu.Unlock()
}
