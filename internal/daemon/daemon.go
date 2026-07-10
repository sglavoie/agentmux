package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// Run starts the daemon, blocks until it shuts down, and cleans up the socket.
// Shutdown happens when:
//   - ctx is cancelled
//   - a TypeShutdown request is received
func Run(ctx context.Context, cfg *config.Config) error {
	socketPath := SocketPath()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return fmt.Errorf("daemon: creating socket directory: %w", err)
	}

	slog.Info("daemon: acquiring single-instance lock", "socket", socketPath, "lock", daemonLockPath(socketPath))
	lockFile, err := acquireDaemonLock(socketPath)
	if err != nil {
		if errors.Is(err, errDaemonAlreadyRunning) {
			slog.Info("daemon: duplicate start exiting", "socket", socketPath, "lock", daemonLockPath(socketPath))
			return nil
		}
		return fmt.Errorf("daemon: acquiring lock %s: %w", daemonLockPath(socketPath), err)
	}
	defer releaseDaemonLock(socketPath, lockFile)
	slog.Info("daemon: acquired single-instance lock", "socket", socketPath, "lock", daemonLockPath(socketPath))

	guardDur := time.Duration(cfg.Defaults.GuardDuration) * time.Second
	guard := NewStateGuard(guardDur)
	notifier := NewNotifier(&cfg.Defaults.Notifications)
	handler := NewHandler(guard, notifier)
	notifier.SetIsMuted(handler.IsMuted)

	server, err := NewServer(socketPath, handler)
	if err != nil {
		return fmt.Errorf("daemon: binding socket %s: %w", socketPath, err)
	}
	defer func() {
		server.Close()
		_ = os.Remove(socketPath)
	}()

	kittyRunner := kitty.NewRealRunner()
	kittyClient := newDaemonKittyClient(kittyRunner)
	handler.SetKittyClient(kittyClient)
	notifier.SetFocus(kittyClient)
	global, perAccount := buildStaleThresholds(cfg)
	handler.SetStaleThresholds(global, perAccount)
	hsGlobal, hsPerAccount := buildHookSilenceThresholds(cfg)
	handler.SetHookSilenceThresholds(hsGlobal, hsPerAccount)
	if cfg.Defaults.Watchdog.ClaudeContextLimitProbe != nil {
		handler.SetClaudeContextLimitProbe(*cfg.Defaults.Watchdog.ClaudeContextLimitProbe)
	}
	handler.restoreStateFromKitty(time.Now())

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	notifier.SetParentContext(ctx)

	handler.onShutdown = cancel

	slog.Info("daemon: started", "socket", socketPath, "guard_duration", guardDur)

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ctx) }()
	go handler.runRenderer(ctx)
	slog.Info("daemon: ready", "socket", socketPath)

	for {
		select {
		case err := <-serveErr:
			return err

		case <-ctx.Done():
			slog.Info("daemon: shutting down")
			return nil
		}
	}
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
	_, err := c.Send(Request{Type: TypeStateQuery})
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
