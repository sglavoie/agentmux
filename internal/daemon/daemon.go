package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

const idleTimeout = 30 * time.Minute

// Run starts the daemon, blocks until it shuts down, and cleans up the socket.
// Shutdown happens when:
//   - ctx is cancelled
//   - a TypeShutdown request is received
//   - no requests arrive for idleTimeout (30 minutes)
func Run(ctx context.Context, cfg *config.Config) error {
	socketPath := SocketPath()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return fmt.Errorf("daemon: creating socket directory: %w", err)
	}

	guardDur := time.Duration(cfg.Defaults.GuardDuration) * time.Second
	guard := NewStateGuard(guardDur)
	notifier := NewNotifier(&cfg.Defaults.Notifications)
	handler := NewHandler(guard, notifier)
	kittyRunner := kitty.NewRealRunner()
	handler.SetKittyClient(kitty.NewClient(kittyRunner))
	handler.SetStaleThreshold(time.Duration(cfg.Defaults.StaleActiveThreshold) * time.Second)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Idle timer: shut down after 30 minutes of no requests.
	idleTimer := time.NewTimer(idleTimeout)
	defer idleTimer.Stop()

	// Sweep ticker: periodically clear stranded active-class icons.
	sweepInterval := 30 * time.Second
	sweepTicker := time.NewTicker(sweepInterval)
	defer sweepTicker.Stop()

	// activityCh receives a signal on every handled request.
	activityCh := make(chan struct{}, 1)
	handler.onActivity = func() {
		select {
		case activityCh <- struct{}{}:
		default:
		}
	}
	handler.onShutdown = cancel

	server, err := NewServer(socketPath, handler)
	if err != nil {
		return fmt.Errorf("daemon: binding socket %s: %w", socketPath, err)
	}
	defer func() {
		server.Close()
		_ = os.Remove(socketPath)
	}()

	slog.Info("daemon: started", "socket", socketPath, "guard_duration", guardDur)

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ctx) }()

	for {
		select {
		case <-activityCh:
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(idleTimeout)

		case <-idleTimer.C:
			slog.Info("daemon: idle timeout, shutting down")
			cancel()
			return nil

		case <-sweepTicker.C:
			// Sweep is internal — it must not signal activityCh, since
			// idleTimeout governs external request inactivity only.
			handler.SweepStale(time.Now())

		case err := <-serveErr:
			return err

		case <-ctx.Done():
			slog.Info("daemon: shutting down")
			return nil
		}
	}
}

// Ping checks whether the daemon is reachable on the default socket.
func Ping() error {
	c := NewClient()
	_, err := c.Send(Request{Type: TypeStateQuery})
	return err
}
