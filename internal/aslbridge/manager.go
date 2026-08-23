package aslbridge

// This file supervises the bridge subprocess: spawning it, completing the
// handshake, keeping a snapshot current, and restarting it under backoff. The
// state those transitions write and the reads served from it live in
// manager_state.go, the request-issuing surface in manager_calls.go, and the
// rules for what a failure may say publicly in manager_reason.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"
)

// Errors a caller can branch on. None of them carry plan content.
var (
	// ErrDisabled means [defaults.asl] enabled = false. Nothing was probed.
	ErrDisabled = errors.New("asl integration is disabled by configuration")
	// ErrUnavailable means there is no bridge and no stale data left to serve.
	ErrUnavailable = errors.New("asl bridge is unavailable")
	// ErrMutationsDisabled means the daemon has no live handshake plus fresh
	// snapshot, so it refuses to act on state it cannot currently see.
	ErrMutationsDisabled = errors.New("asl mutations are disabled until a fresh snapshot arrives")
	// ErrInvalidRequest means the caller's request was rejected before any
	// bridge round trip.
	ErrInvalidRequest = errors.New("asl bridge request is invalid")
)

const maxReceipts = 64

// Status is the daemon's ASL capability block. It is reported through health,
// `amx daemon status`, and `amx doctor` independently of core daemon readiness:
// a missing or broken bridge is an unavailable optional integration, never a
// reason for the daemon itself to be unready.
type Status struct {
	Mode             string   `json:"mode"`
	Command          string   `json:"command,omitempty"`
	Executable       string   `json:"executable,omitempty"`
	Available        bool     `json:"available"`
	MutationsEnabled bool     `json:"mutations_enabled"`
	Protocol         int      `json:"protocol,omitempty"`
	Version          string   `json:"version,omitempty"`
	Capabilities     []string `json:"capabilities,omitempty"`
	// SnapshotAgeSeconds is nil when no snapshot is held at all, which is a
	// different condition from a snapshot that is zero seconds old.
	SnapshotAgeSeconds *int64   `json:"snapshot_age_seconds,omitempty"`
	SnapshotGeneration uint64   `json:"snapshot_generation,omitempty"`
	SnapshotStale      bool     `json:"snapshot_stale,omitempty"`
	RestartCount       int      `json:"restart_count"`
	Reason             string   `json:"reason,omitempty"`
	Metrics            *Metrics `json:"metrics,omitempty"`
}

// Options configures one bridge manager.
type Options struct {
	Mode    Mode
	Command string
	// Launcher replaces process spawning. Nil uses the configured command.
	Launcher Launcher
	// Now and Sleep are clock seams. Nil uses the real clock.
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) bool
	// Logger receives private diagnostics. Nil uses the default logger.
	Logger *slog.Logger
}

// Manager supervises one bridge subprocess and owns everything the daemon
// knows about ASL: the last snapshot, whether mutations are currently legal,
// the bounded receipt window, and the capability block.
type Manager struct {
	mode     Mode
	command  string
	launch   Launcher
	now      func() time.Time
	sleep    func(context.Context, time.Duration) bool
	log      *slog.Logger
	counters *metrics

	mu                sync.Mutex
	conn              *conn
	hello             Hello
	executable        string
	connected         bool
	handshakeGen      uint64
	snapshot          *Snapshot
	snapshotGen       uint64
	snapshotAt        time.Time
	snapshotHandshake uint64
	disconnectedAt    time.Time
	restarts          int
	reason            string
	receipts          []Operation

	refresh   chan struct{}
	cancel    context.CancelFunc
	done      chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

// New builds a manager. It performs no I/O: an explicitly disabled integration
// never probes, and every other mode waits for Start.
func New(opts Options) *Manager {
	mode := opts.Mode
	if mode == "" {
		mode = ModeAuto
	}
	m := &Manager{
		mode:     mode,
		command:  opts.Command,
		launch:   opts.Launcher,
		now:      opts.Now,
		sleep:    opts.Sleep,
		log:      opts.Logger,
		counters: newMetrics(),
		refresh:  make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.log == nil {
		m.log = slog.Default()
	}
	if m.launch == nil && m.command != "" {
		m.launch = execLauncher(m.command)
	}
	if mode == ModeDisabled {
		m.reason = "disabled by defaults.asl.enabled"
	}
	return m
}

// Start begins supervision and returns immediately. It is called after the
// daemon socket is published, so a missing or slow ASL cannot delay Agent Mux.
func (m *Manager) Start(ctx context.Context) {
	m.startOnce.Do(func() {
		if m.mode == ModeDisabled {
			close(m.done)
			return
		}
		if m.launch == nil {
			m.setReason("no asl command is configured")
			close(m.done)
			return
		}
		ctx, cancel := context.WithCancel(ctx)
		m.cancel = cancel
		go m.supervise(ctx)
	})
}

// Close stops supervision, tears the child down, and joins every goroutine this
// manager started.
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		if m.cancel != nil {
			m.cancel()
		}
	})
	<-m.done
}

func (m *Manager) supervise(ctx context.Context) {
	defer close(m.done)
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		connectedFor, err := m.runSession(ctx)
		if ctx.Err() != nil {
			m.setDisconnected("daemon shutdown")
			return
		}
		m.setDisconnected(sanitizeReason(err))
		// A missing executable is a configuration fact, not a crash: retrying it
		// on a schedule would spin forever on every machine without ASL.
		if errors.Is(err, exec.ErrNotFound) {
			m.log.Info("daemon: asl bridge is not installed; integration stays unavailable", "command", m.command)
			return
		}
		if connectedFor >= HealthyResetInterval {
			attempt = 0
		}
		delay := backoffFor(attempt)
		attempt++
		m.counters.restart()
		m.mu.Lock()
		m.restarts++
		m.mu.Unlock()
		// The daemon log is private diagnostics, so it renders the full error
		// including any retained stderr tail. The published reason stays
		// sanitized.
		m.log.Warn("daemon: asl bridge exited; restarting",
			"error", err, "delay", delay, "connected_for", connectedFor)
		if !m.wait(ctx, delay) {
			return
		}
	}
}

// runSession runs one child from spawn to exit and reports how long it stayed
// connected, so the caller can decide whether the backoff has earned a reset.
func (m *Manager) runSession(ctx context.Context) (time.Duration, error) {
	child, err := m.launch(ctx)
	if err != nil {
		return 0, err
	}

	tail := newStderrTail()
	go tail.consume(child.Stderr)

	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()

	c := newConn(child.Stdin, child.Stdout, m.handleEvent, m.counters)

	// Shutdown order is stdin first because that is the bridge's own
	// end-of-stream signal; termination is the fallback for a child that
	// ignores it. Every goroutine started above is joined before returning.
	teardown := func() {
		c.shutdown()
		timer := time.NewTimer(ShutdownGrace)
		select {
		case <-exited:
			timer.Stop()
		case <-timer.C:
			if child.Terminate != nil {
				_ = child.Terminate()
			}
			<-exited
		}
		c.awaitReader()
		tail.wait()
	}

	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, HandshakeTimeout)
	hello, err := c.waitHello(handshakeCtx)
	cancelHandshake()
	if err != nil {
		teardown()
		return 0, handshakeFailure(err, tail)
	}
	if hello.Protocol != ProtocolVersion {
		teardown()
		return 0, fmt.Errorf("asl bridge: hello announced protocol %d, want %d", hello.Protocol, ProtocolVersion)
	}
	if !hasCapability(hello.Capabilities, MethodRunsSnapshot) {
		teardown()
		return 0, errors.New("asl bridge: hello did not advertise runs.snapshot")
	}

	connectedAt := m.now()
	m.setConnected(child.Path, hello, c)
	m.log.Info("daemon: asl bridge connected",
		"executable", child.Path, "asl_version", hello.ASLVersion, "capabilities", len(hello.Capabilities))

	// The first snapshot is what re-enables actions: a fresh handshake alone
	// proves the child is alive, not that the daemon can see current state.
	m.refreshSnapshot(ctx, c)

	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		m.refreshLoop(ctx, c)
	}()

	select {
	case <-ctx.Done():
	case <-c.readerDone:
	}
	teardown()
	<-refreshDone
	return m.now().Sub(connectedAt), c.err()
}

func (m *Manager) refreshLoop(ctx context.Context, c *conn) {
	retry := time.NewTicker(SnapshotRetryInterval)
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.readerDone:
			return
		case <-m.refresh:
			m.refreshSnapshot(ctx, c)
		case <-retry.C:
			if !m.hasFreshSnapshot() {
				m.refreshSnapshot(ctx, c)
			}
		}
	}
}

// hasFreshSnapshot reports whether the daemon holds a snapshot taken under the
// current handshake. A connected child with none is the one state the refresh
// loop cannot wait out, because no corpus change is owed to it.
func (m *Manager) hasFreshSnapshot() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshot != nil && m.snapshotHandshake == m.handshakeGen
}

func (m *Manager) refreshSnapshot(ctx context.Context, c *conn) {
	callCtx, cancel := context.WithTimeout(ctx, SnapshotTimeout)
	raw, err := c.call(callCtx, MethodRunsSnapshot, nil)
	cancel()
	if err != nil {
		m.log.Debug("daemon: asl snapshot refresh failed", "reason", sanitizeReason(err))
		return
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		// A response body the daemon cannot decode is the same class of
		// failure as contamination: the child is not speaking protocol 1.
		c.closeWith(contaminationError("snapshot result"))
		return
	}
	m.storeSnapshot(snapshot)
}

// handleEvent runs on the reader goroutine and must never block: it records
// what it can and signals the refresher rather than issuing a request itself.
func (m *Manager) handleEvent(e event) {
	switch e.Name {
	case EventRunsChanged:
		select {
		case m.refresh <- struct{}{}:
		default:
		}
	case EventOperationUpdated:
		var op Operation
		if err := json.Unmarshal(e.Data, &op); err != nil {
			return
		}
		m.recordReceipt(op)
	}
}

func (m *Manager) wait(ctx context.Context, d time.Duration) bool {
	if m.sleep != nil {
		return m.sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func hasCapability(capabilities []string, name string) bool {
	for _, c := range capabilities {
		if c == name {
			return true
		}
	}
	return false
}
