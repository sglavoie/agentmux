package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/process"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// Run starts the daemon, blocks until it shuts down, and cleans up the socket.
// Shutdown happens when:
//   - ctx is cancelled
//   - a TypeShutdown request is received
func Run(ctx context.Context, cfg *config.Config) error {
	return run(ctx, cfg, runDependencies{})
}

type runDependencies struct {
	beforeStartupConfirmation func(context.Context)
	afterStartupPublication   func()
	afterRenderWorkerShutdown func()
}

func run(ctx context.Context, cfg *config.Config, deps runDependencies) (retErr error) {
	socketPath := SocketPath()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return fmt.Errorf("daemon: creating socket directory: %w", err)
	}

	slog.Info("daemon: acquiring single-instance lock", "socket", socketPath, "lock", process.DaemonLockPath(socketPath))
	lockFile, err := process.AcquireDaemonLock(socketPath)
	if err != nil {
		if errors.Is(err, process.ErrDaemonAlreadyRunning) {
			slog.Info("daemon: duplicate start exiting", "socket", socketPath, "lock", process.DaemonLockPath(socketPath))
			return nil
		}
		return fmt.Errorf("daemon: acquiring lock %s: %w", process.DaemonLockPath(socketPath), err)
	}
	defer process.ReleaseDaemonLock(socketPath, lockFile)
	slog.Info("daemon: acquired single-instance lock", "socket", socketPath, "lock", process.DaemonLockPath(socketPath))

	recorder, recorderErr := newLifecycleRecorder(cfg)
	if recorderErr != nil {
		slog.Warn("daemon: diagnostic journal unavailable", "error", recorderErr)
	} else if recorder != nil {
		recorder.record("daemon.startup", "started", "process_entered", map[string]string{"pid": fmt.Sprint(os.Getpid())})
		defer func() {
			if retErr != nil {
				recorder.record("daemon.exit", "error", retErr.Error(), nil)
				return
			}
			recorder.record("daemon.exit", "success", "shutdown_complete", nil)
		}()
	}
	recorder.record("daemon.lock", "success", "acquired", map[string]string{"socket": socketPath})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	journalMaintenanceDone := make(chan struct{})
	if recorder == nil {
		close(journalMaintenanceDone)
	} else {
		go func() {
			recorder.runJournalMaintenance(ctx)
			close(journalMaintenanceDone)
		}()
	}
	defer func() {
		cancel()
		<-journalMaintenanceDone
	}()

	guardDur := time.Duration(cfg.Defaults.GuardDuration) * time.Second
	guard := activity.NewStateGuard(guardDur)
	notifier := notification.NewNotifier(&cfg.Defaults.Notifications)
	handler := NewHandler(guard, notifier)
	if recorder != nil {
		handler.setDecisionJournal(recorder.journal)
	}
	if paths, pathsErr := diagnostics.DefaultPaths(); pathsErr == nil {
		if captureStore, captureErr := diagnostics.NewCaptureStore(paths, diagnostics.CaptureOptions{}); captureErr == nil {
			handler.setCaptureStore(captureStore)
			maintenanceCtx, cancelMaintenance := context.WithTimeout(ctx, 250*time.Millisecond)
			_, _ = captureStore.Maintain(maintenanceCtx)
			cancelMaintenance()
		}
	}
	notifier.SetIsMuted(handler.IsMuted)
	// Capture the artifact identity now, before any later `just install` can
	// replace the on-disk binary under this running process: health must always
	// report what the daemon is actually executing.
	handler.SetBuildInfo(buildinfo.Current())

	kittyRunner := kitty.NewRealRunner()
	kittyClient := newDaemonKittyClient(kittyRunner)
	// Assign the Kitty client on the startup goroutine before any server
	// goroutine can read it. Set the field directly rather than via
	// SetKittyClient: SetKittyClient primes the cache with an UNBOUNDED
	// ListWindows, which a wedged Kitty could hang on before the bounded restore
	// even runs. The bounded restore below is the authoritative initial snapshot.
	handler.useKittyClient(kittyClient)
	notifier.SetFocus(notification.NewKittyFocus(kittyClient))
	global, perAccount := buildStaleThresholds(cfg)
	handler.SetStaleThresholds(global, perAccount)
	hsGlobal, hsPerAccount := buildHookSilenceThresholds(cfg)
	handler.SetHookSilenceThresholds(hsGlobal, hsPerAccount)
	if cfg.Defaults.Watchdog.ClaudeContextLimitProbe != nil {
		handler.SetClaudeContextLimitProbe(*cfg.Defaults.Watchdog.ClaudeContextLimitProbe)
	}
	handler.SetStopConfirm(
		time.Duration(cfg.Defaults.StopConfirm.Delay)*time.Second,
		time.Duration(cfg.Defaults.StopConfirm.Timeout)*time.Second,
		cfg.Defaults.StopConfirm.Enabled == nil || *cfg.Defaults.StopConfirm.Enabled,
	)
	handler.SetCopilotTranscriptHomes(copilotProfileHomes(cfg), config.ExpandTilde(harness.DefaultHomeDir(harness.Copilot)))
	// Screen-pattern sets are a package-level registry read lock-free by every
	// probe goroutine, so the load must happen here on the startup goroutine —
	// ahead of restoreStateFromKittyBounded below and well ahead of
	// runScreenProbes. It never fails: a missing, unreadable or invalid override
	// degrades to the embedded default and comes back as warnings, which the
	// health response carries and `amx doctor` turns into a finding. Reporting
	// them here as log lines only would make a stale override invisible to
	// anyone not reading the daemon log at startup.
	handler.SetScreenPatterns(screenstate.Load(config.DefaultConfigDir()))
	// The canonical ASL state root must be installed before the maintenance loop
	// starts, since its first tick performs the startup reconciliation. The
	// bridge itself starts much later; discovery does not depend on it.
	handler.SetASLStateRoot(cfg.Defaults.ASL.StateRoot)
	// The shadow reducer mirrors the same projections the legacy paths already
	// consume: guard_duration for the legacy trailing-event grace and the
	// watchdog thresholds for active-class staleness.
	handler.SetReducerConfig(state.ReducerConfig{
		GuardDuration: int64(cfg.Defaults.GuardDuration),
		StaleActive:   int64(cfg.Defaults.Watchdog.Active),
		StaleSubagent: int64(cfg.Defaults.Watchdog.Subagent),
		StaleCompact:  int64(cfg.Defaults.Watchdog.Compact),
	})

	// Take one bounded initial Kitty snapshot and restore durable state BEFORE
	// binding the public socket, so the first client request already sees
	// restored state. A slow or wedged Kitty must not delay startup: past the
	// budget the daemon comes up degraded and the maintenance loop reconciles.
	if ready, reason := handler.restoreStateFromKittyBounded(time.Now(), initialSnapshotBudget); ready {
		handler.MarkReady()
		slog.Info("daemon: initial snapshot restored", "socket", socketPath)
		recorder.record("daemon.snapshot", "success", "restored", nil)
	} else {
		handler.MarkDegraded(reason)
		slog.Warn("daemon: starting degraded; initial snapshot did not complete in time", "reason", reason)
		recorder.record("daemon.snapshot", "degraded", reason, nil)
	}

	server, err := NewServer(socketPath, handler)
	if err != nil {
		return fmt.Errorf("daemon: binding socket %s: %w", socketPath, err)
	}
	defer func() {
		server.Close()
		_ = os.Remove(socketPath)
	}()

	notifier.SetParentContext(ctx)

	shutdownAccepted := make(chan struct{})
	var shutdownRecord sync.Once
	recordShutdown := func(reason string) {
		shutdownRecord.Do(func() {
			recorder.record("daemon.shutdown", "started", reason, nil)
		})
	}
	handler.onShutdown = func() {
		recordShutdown("request_accepted")
		close(shutdownAccepted)
		cancel()
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ctx) }()
	if deps.beforeStartupConfirmation != nil {
		deps.beforeStartupConfirmation(ctx)
	}
	if err := confirmDaemonServing(ctx, socketPath); err != nil {
		select {
		case <-shutdownAccepted:
			return nil
		default:
		}
		return fmt.Errorf("daemon: confirming startup publication: %w", err)
	}
	if deps.afterStartupPublication != nil {
		deps.afterStartupPublication()
	}
	slog.Info("daemon: started", "socket", socketPath, "guard_duration", guardDur)
	recorder.record("daemon.started", "success", "socket_published", map[string]string{"socket": socketPath})
	// Independent loops: snapshot maintenance (the periodic lister), the
	// watchdog (the 1s staleness evaluator on in-process elapsed time), harness
	// process lifecycle reconciliation, the render worker (the single Kitty
	// writer), and the screen probes. A slow Kitty write, process-table query, or
	// wedged screen read stalls only its own loop — never snapshot publication,
	// watchdog expiry, or event ingestion.
	//
	// Every loop is owned: run tracks all of them in one wait group and joins
	// that group before returning. A returned Run therefore means the daemon's
	// own goroutines have exited, not merely that cancellation was signalled —
	// which is what lets shutdown tests assert on a join instead of settling on
	// a wall clock.
	var loops sync.WaitGroup
	startLoop := func(loop func(context.Context)) {
		loops.Add(1)
		go func() {
			defer loops.Done()
			loop(ctx)
		}()
	}
	startLoop(handler.runMaintenance)
	startLoop(handler.runWatchdog)
	startLoop(handler.runHarnessProcessLifecycle)
	startLoop(func(loopCtx context.Context) {
		handler.runRenderWorker(loopCtx)
		if deps.afterRenderWorkerShutdown != nil {
			deps.afterRenderWorkerShutdown()
		}
	})
	startLoop(handler.runScreenProbes)
	// The ASL bridge is started only here, after the socket is published and
	// the core loops are running, so a missing, slow, or wedged ASL can never
	// delay Agent Mux startup or affect readiness.
	aslManager := aslbridge.New(aslbridge.Options{
		Mode:    aslbridge.Mode(cfg.Defaults.ASL.ASLModeValue()),
		Command: cfg.Defaults.ASL.Command,
	})
	handler.SetASLBridge(aslManager)
	aslManager.Start(ctx)
	defer aslManager.Close()
	slog.Info("daemon: ready", "socket", socketPath)
	recorder.record("daemon.ready", "success", "maintenance_loops_started", nil)
	// waitForLoops cancels every loop and joins the group. The budget is a
	// safety valve, not a schedule: a loop wedged inside an in-flight Kitty
	// round-trip must not hold the process open forever, so an exceeded budget
	// is reported and shutdown continues.
	waitForLoops := func() {
		cancel()
		drained := make(chan struct{})
		go func() {
			loops.Wait()
			close(drained)
		}()
		select {
		case <-drained:
		case <-time.After(shutdownDrainBudget):
			slog.Warn("daemon: shutdown drain budget elapsed with loops still running", "budget", shutdownDrainBudget)
		}
	}

	for {
		select {
		case err := <-serveErr:
			waitForLoops()
			return err

		case <-ctx.Done():
			slog.Info("daemon: shutting down")
			recordShutdown("context_cancelled")
			waitForLoops()
			// Close is idempotent (the deferred Close observes closeOnce). Closing
			// here rather than only on the way out makes the serve goroutine's exit
			// an owned join too: Serve drains its in-flight handlers and returns
			// before run does.
			server.Close()
			<-serveErr
			return nil
		}
	}
}

// shutdownDrainBudget bounds how long run waits for its own loops to exit
// after cancellation before giving up and letting the process finish shutting
// down. Loops select on ctx.Done at the top of every iteration, so a healthy
// daemon drains immediately; the budget only covers a tick already parked in a
// wedged external call.
const shutdownDrainBudget = 5 * time.Second

func confirmDaemonServing(ctx context.Context, socketPath string) error {
	probeCtx, cancel := context.WithTimeout(ctx, normalAckBudget)
	defer cancel()
	resp, err := NewClientWithSocket(socketPath).sendWithContext(probeCtx, protocol.Request{Type: protocol.TypeHealth})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("health probe was rejected: %s", resp.Error)
	}
	if resp.PID != os.Getpid() {
		return fmt.Errorf("health probe reached pid %d, want startup pid %d", resp.PID, os.Getpid())
	}
	return nil
}

// newDaemonKittyClient builds the daemon's Kitty client from the current
// environment, normalizing KITTY_LISTEN_ON so a daemon auto-started by an F8
// `launch --allow-remote-control` child does not inherit an unusable
// process-local "fd:" descriptor. An empty normalized address leaves the
// client without a socket, matching kitty.NewClient's behavior outside Kitty.
func newDaemonKittyClient(runner kitty.CommandRunner) *kitty.SubprocessClient {
	return kitty.NewClientWithSocket(runner, kitty.NormalizedListenAddressFromEnv())
}

// Ping checks whether the daemon is reachable on the default socket.
func Ping() error {
	c := NewClient()
	_, err := c.Send(protocol.Request{Type: protocol.TypeStateQuery})
	return err
}

// buildStaleThresholds projects cfg.Defaults.Watchdog and each per-profile
// watchdog override into the (global, perAccount) maps consumed by
// Handler.SetStaleThresholds. Per-profile entries are keyed by lowercased
// profile name to match the case-insensitive lookup used in the rest of the
// config package.
func buildStaleThresholds(cfg *config.Config) (map[string]time.Duration, map[string]map[string]time.Duration) {
	wd := cfg.Defaults.Watchdog
	global := map[string]time.Duration{
		"active":   time.Duration(wd.Active) * time.Second,
		"subagent": time.Duration(wd.Subagent) * time.Second,
		"compact":  time.Duration(wd.Compact) * time.Second,
	}

	perAccount := make(map[string]map[string]time.Duration)
	for _, p := range cfg.Profiles {
		pwd := p.Watchdog
		if pwd.Active == 0 && pwd.Subagent == 0 && pwd.Compact == 0 {
			continue
		}
		inner := make(map[string]time.Duration, 3)
		if pwd.Active > 0 {
			inner["active"] = time.Duration(pwd.Active) * time.Second
		}
		if pwd.Subagent > 0 {
			inner["subagent"] = time.Duration(pwd.Subagent) * time.Second
		}
		if pwd.Compact > 0 {
			inner["compact"] = time.Duration(pwd.Compact) * time.Second
		}
		perAccount[strings.ToLower(p.Name)] = inner
	}
	return global, perAccount
}

// copilotProfileHomes gathers the ~-expanded home directories of every
// configured Copilot profile, in configured order, for the transcript stop
// probe's session resolver. internal/transcript stays free of internal/config
// to avoid an import cycle, so the daemon does the profile filtering and tilde
// expansion here (ResolveCopilotTranscript joins the homes verbatim). Duplicate
// homes are collapsed so multiple Copilot profiles sharing a home are probed
// once.
func copilotProfileHomes(cfg *config.Config) []string {
	var homes []string
	seen := make(map[string]bool)
	for _, p := range cfg.Profiles {
		if p.Harness != harness.Copilot {
			continue
		}
		home := config.ExpandTilde(p.HomeDir)
		if home == "" || seen[home] {
			continue
		}
		seen[home] = true
		homes = append(homes, home)
	}
	return homes
}

// buildHookSilenceThresholds projects cfg.Defaults.Watchdog.HookSilence and
// each per-profile HookSilence override into the (global, perAccount) pair
// consumed by Handler.SetHookSilenceThresholds. Per-profile keys are
// lowercased to match the case-insensitive lookup used everywhere else in
// the config package.
func buildHookSilenceThresholds(cfg *config.Config) (time.Duration, map[string]time.Duration) {
	global := time.Duration(cfg.Defaults.Watchdog.HookSilence) * time.Second
	perAccount := make(map[string]time.Duration)
	for _, p := range cfg.Profiles {
		if p.Watchdog.HookSilence > 0 {
			perAccount[strings.ToLower(p.Name)] = time.Duration(p.Watchdog.HookSilence) * time.Second
		}
	}
	return global, perAccount
}
