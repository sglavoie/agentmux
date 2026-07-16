package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

// SocketPath returns the Unix socket path for the daemon. If the user's home
// directory cannot be determined (e.g. empty HOME), fall back to a path under
// os.TempDir() rather than returning a relative path that would silently
// create the socket in the current working directory — producing a
// client/daemon mismatch when their cwds differ.
func SocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "agent-mux", "daemon.sock")
	}
	return filepath.Join(home, ".local", "share", "agent-mux", "daemon.sock")
}

// Client connects to the daemon socket and sends single request/response pairs.
type Client struct {
	socketPath string
}

// NewClient returns a Client targeting the default socket path.
func NewClient() *Client {
	return &Client{socketPath: SocketPath()}
}

// NewClientWithSocket returns a Client targeting an explicit socket path (for tests).
func NewClientWithSocket(path string) *Client {
	return &Client{socketPath: path}
}

// sendDeadline bounds one request/response exchange with a live daemon. A
// normal acknowledgement returns well within this budget; the ceiling only
// guards against a wedged daemon. autoStartDeadline bounds the full hook-client
// path, including a cold daemon start and retry, so a hook never delays its
// harness beyond the two-second cold-start budget.
const normalAckBudget = 250 * time.Millisecond
const coldStartBudget = 2 * time.Second

const (
	DeliveryStageIPCAttempt      = "ipc_attempt"
	DeliveryStageDaemonAutostart = "daemon_autostart"
)

// DeliveryStageEvent reports one bounded client transport stage. The hook
// indicator uses it to persist attempt/autostart evidence while Client keeps
// ownership of retries and deadline enforcement.
type DeliveryStageEvent struct {
	Stage     string
	Attempt   int
	Outcome   string
	Reason    string
	LatencyMS float64
}

type DeliveryObserver func(DeliveryStageEvent)

var sendDeadline = normalAckBudget
var autoStartDeadline = coldStartBudget

var autoStartRetryDelay = 50 * time.Millisecond
var spawnDetached = SpawnDetached

const restartSocketPollCount = 20

var restartSocketPollDelay = 50 * time.Millisecond

// maxResponseBytes caps the response payload size the client will buffer.
// Requests stay bounded by the server's 1 MiB ceiling, but responses are
// asymmetric: a full events export (TypeEventsExport, WindowID 0) carries
// every window's event ring buffer and exceeds 1 MiB on a busy daemon —
// observed live against the production daemon, where doctor and audit
// degraded to a daemon_export failure finding. 64 MiB gives bulk
// diagnostics generous headroom while still surfacing a runaway response
// as an explicit error rather than bufio.Scanner's silent 64 KiB
// truncation. The scanner allocates lazily (64 KiB initial), so the raised
// ceiling costs nothing on small responses. A var so tests can pin the
// oversize path without shuttling 64 MiB through a socket.
var maxResponseBytes = 64 << 20

// exportDeadline bounds the full events-export exchange. Bulk diagnostic
// reads legitimately take longer than the 250ms hook-ack budget (the daemon
// deep-copies and marshals every window's ring buffer), so ExportEvents
// gets its own generous bound instead of inheriting sendDeadline.
var exportDeadline = 5 * time.Second

// Send sends req to the daemon and returns the response.
// Returns an error if the daemon is not reachable.
func (c *Client) Send(req Request) (Response, error) {
	return c.sendWithDeadline(req, sendDeadline)
}

func (c *Client) sendWithDeadline(req Request, timeout time.Duration) (Response, error) {
	if timeout <= 0 {
		return Response{}, errors.New("daemon request deadline elapsed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return c.sendWithContext(ctx, req)
}

func (c *Client) sendWithContext(ctx context.Context, req Request) (Response, error) {
	if ctx == nil {
		return Response{}, errors.New("daemon request context is nil")
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return Response{}, fmt.Errorf("connecting to daemon: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// DialContext covers only connection setup. Close the established socket on
	// later cancellation too so a daemon that accepts but never responds cannot
	// outlive the caller's context.
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	data, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("encoding request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Response{}, ctxErr
		}
		return Response{}, fmt.Errorf("sending request: %w", err)
	}

	var resp Response
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), maxResponseBytes)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Response{}, ctxErr
			}
			if errors.Is(err, bufio.ErrTooLong) {
				return Response{}, fmt.Errorf("daemon response exceeds %d bytes", maxResponseBytes)
			}
			return Response{}, fmt.Errorf("reading response: %w", err)
		}
		return Response{}, fmt.Errorf("daemon closed connection without responding")
	}
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return Response{}, fmt.Errorf("decoding response: %w", err)
	}
	return resp, nil
}

func (c *Client) sendBefore(req Request, deadline time.Time) (Response, error) {
	timeout := sendDeadline
	if !deadline.IsZero() {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return Response{}, errors.New("daemon auto-start deadline elapsed")
		}
		if remaining < timeout {
			timeout = remaining
		}
	}
	return c.sendWithDeadline(req, timeout)
}

// Health is the result of a TypeHealth query. Alive is false when the
// daemon is unreachable; in that case every other field is zero. Build and
// Metrics are nil when the daemon predates them (or, for Metrics, when the
// handler runs without a server).
type Health struct {
	Alive          bool
	PID            int
	Uptime         time.Duration
	Ready          bool
	DegradedReason string
	Build          *buildinfo.Info
	Metrics        *MetricsSnapshot
}

// Health pings the daemon and returns its liveness, PID and uptime. Never
// returns an error — an unreachable daemon yields Health{Alive: false}.
func (c *Client) Health() Health {
	return c.healthBefore(time.Time{})
}

func (c *Client) healthBefore(deadline time.Time) Health {
	resp, err := c.sendBefore(Request{Type: TypeHealth}, deadline)
	if err != nil || !resp.OK {
		return Health{}
	}
	return Health{
		Alive:          true,
		PID:            resp.PID,
		Uptime:         time.Duration(resp.UptimeSeconds) * time.Second,
		Ready:          resp.Ready,
		DegradedReason: resp.DegradedReason,
		Build:          resp.Build,
		Metrics:        resp.Metrics,
	}
}

func daemonSocketOlderThanBinary(socketPath, amxBin string) bool {
	if amxBin == "" {
		return false
	}
	socketInfo, err := os.Stat(socketPath)
	if err != nil {
		return false
	}
	binInfo, err := os.Stat(amxBin)
	if err != nil {
		return false
	}
	return binInfo.ModTime().After(socketInfo.ModTime())
}

// ShutdownAndWait asks a live daemon to stop and waits until its original
// socket has been unpublished. Run removes that socket only after handlers
// drain, making this a complete teardown barrier rather than a shutdown
// acknowledgement.
// An absent or refused socket is treated as an already-stopped daemon.
func (c *Client) ShutdownAndWait(ctx context.Context) error {
	if ctx == nil {
		return errors.New("daemon shutdown context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	originalSocket, statErr := os.Stat(c.socketPath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("inspecting daemon socket before shutdown: %w", statErr)
	}
	resp, err := c.sendWithContext(ctx, Request{Type: TypeShutdown})
	if err != nil {
		if os.IsNotExist(statErr) || daemonConnectionRefused(err) {
			return nil
		}
		if _, currentErr := os.Stat(c.socketPath); os.IsNotExist(currentErr) {
			return nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("requesting daemon shutdown timed out: %w", err)
		}
		return fmt.Errorf("requesting daemon shutdown: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("daemon rejected shutdown: %s", resp.Error)
	}

	for attempt := 1; ; attempt++ {
		currentSocket, currentErr := os.Stat(c.socketPath)
		switch {
		case os.IsNotExist(currentErr):
			slog.Debug("daemon client: shutdown socket removed", "socket_path", c.socketPath, "attempt", attempt)
			return nil
		case currentErr != nil:
			return fmt.Errorf("inspecting daemon socket during shutdown: %w", currentErr)
		case originalSocket != nil && !os.SameFile(originalSocket, currentSocket):
			// A hook may auto-start a replacement immediately after the old
			// daemon removes its socket. A different inode proves the original
			// teardown boundary was crossed even if polling missed the gap.
			return nil
		}

		timer := time.NewTimer(restartSocketPollDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("waiting for daemon teardown timed out: %w", ctx.Err())
			}
			return fmt.Errorf("waiting for daemon teardown: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func daemonConnectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT)
}

// RestartDetached stops the current daemon if it is running, then starts a new
// detached daemon using amxBin. It does not wait for the new socket to become
// reachable; callers that need a live server should retry with Send afterward.
func (c *Client) RestartDetached(amxBin string) error {
	return c.restartDetachedBefore(amxBin, c.Health().Alive, time.Time{})
}

func (c *Client) restartDetachedBefore(amxBin string, daemonAlive bool, deadline time.Time) error {
	if daemonAlive {
		slog.Debug("daemon client: requesting daemon shutdown before restart", "socket_path", c.socketPath)
		var shutdownCtx context.Context
		var cancel context.CancelFunc
		if deadline.IsZero() {
			shutdownCtx, cancel = context.WithTimeout(context.Background(), time.Duration(restartSocketPollCount)*restartSocketPollDelay)
		} else {
			shutdownCtx, cancel = context.WithDeadline(context.Background(), deadline)
		}
		err := c.ShutdownAndWait(shutdownCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("shutting down daemon: %w", err)
		}
	}
	if !deadline.IsZero() && time.Until(deadline) <= 0 {
		return errors.New("daemon auto-start deadline elapsed before spawn")
	}
	slog.Debug("daemon client: spawning detached daemon", "socket_path", c.socketPath, "amx_bin", amxBin)
	if err := spawnDetached(amxBin); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	return nil
}

type observedRequestSender struct {
	client   *Client
	req      Request
	deadline time.Time
	observer DeliveryObserver
	attempt  int
}

func (s *observedRequestSender) send() (Response, error) {
	s.attempt++
	req := s.req
	req.Attempt = s.attempt
	started := time.Now()
	resp, err := s.client.sendBefore(req, s.deadline)
	event := DeliveryStageEvent{
		Stage:     DeliveryStageIPCAttempt,
		Attempt:   s.attempt,
		Outcome:   "success",
		Reason:    "acknowledged",
		LatencyMS: float64(time.Since(started)) / float64(time.Millisecond),
	}
	if err != nil {
		event.Outcome = "error"
		event.Reason = err.Error()
	}
	if s.observer != nil {
		s.observer(event)
	}
	return resp, err
}

func observeDeliveryStage(observer DeliveryObserver, stage, outcome, reason string, started time.Time) {
	if observer == nil {
		return
	}
	observer(DeliveryStageEvent{
		Stage:     stage,
		Outcome:   outcome,
		Reason:    reason,
		LatencyMS: float64(time.Since(started)) / float64(time.Millisecond),
	})
}

func (c *Client) sendWithRetriesObserved(sender *observedRequestSender, deadline time.Time) (Response, error) {
	var resp Response
	err := errors.New("daemon did not start in time")
	for time.Until(deadline) > 0 {
		delay := autoStartRetryDelay
		if remaining := time.Until(deadline); remaining < delay {
			delay = remaining
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		resp, err = sender.send()
		if err == nil {
			return resp, nil
		}
	}
	return Response{}, err
}

// SendWithAutoStart sends req to the daemon, auto-starting it if not running.
// amxBin is the path to the amx binary used for auto-start.
func (c *Client) SendWithAutoStart(req Request, amxBin string) (Response, error) {
	return c.SendWithAutoStartObserved(req, amxBin, nil)
}

// SendWithAutoStartObserved is SendWithAutoStart with additive transport-stage
// observation. The observer runs synchronously after each attempt or autostart
// action; callers must keep it bounded and fail-open.
func (c *Client) SendWithAutoStartObserved(req Request, amxBin string, observer DeliveryObserver) (Response, error) {
	deadline := time.Now().Add(autoStartDeadline)
	sender := &observedRequestSender{client: c, req: req, deadline: deadline, observer: observer}
	restarted := false
	var autoStartLock *os.File
	defer func() {
		if autoStartLock != nil {
			releaseAutoStartLock(autoStartLock)
		}
	}()

	if daemonSocketOlderThanBinary(c.socketPath, amxBin) {
		lock, lockErr := acquireAutoStartLock(c.socketPath)
		if lockErr != nil {
			if !errors.Is(lockErr, errAutoStartInProgress) {
				return Response{}, fmt.Errorf("daemon auto-start lock failed: %w", lockErr)
			}
			observeDeliveryStage(observer, DeliveryStageDaemonAutostart, "waiting", "another_client_starting", time.Now())
			resp, err := sender.send()
			if err == nil {
				return resp, nil
			}
			resp, retryErr := c.sendWithRetriesObserved(sender, deadline)
			if retryErr == nil {
				return resp, nil
			}
			return Response{}, fmt.Errorf("daemon did not start in time: %w", retryErr)
		}
		autoStartLock = lock

		if health := c.healthBefore(deadline); health.Alive {
			slog.Debug("daemon client: restarting stale daemon for newer amx binary",
				"socket_path", c.socketPath,
				"amx_bin", amxBin,
				"daemon_pid", health.PID,
			)
			started := time.Now()
			if err := c.restartDetachedBefore(amxBin, true, deadline); err != nil {
				observeDeliveryStage(observer, DeliveryStageDaemonAutostart, "error", err.Error(), started)
				return Response{}, fmt.Errorf("daemon auto-restart failed: %w", err)
			}
			observeDeliveryStage(observer, DeliveryStageDaemonAutostart, "success", "stale_daemon_restarted", started)
			restarted = true
		}
	}

	resp, err := sender.send()
	if err == nil {
		return resp, nil
	}

	// If we just restarted the daemon, restartDetached has already spawned the
	// replacement process — it just hasn't bound the socket yet. Spawning again
	// here would create a second daemon process whose NewServer races with the
	// first by removing the socket file out from under it, potentially leaving
	// behind a ghost daemon listening on no filesystem entry. Skip the
	// spawnDetached and fall straight into the retry loop instead.
	if !restarted {
		if autoStartLock == nil {
			lock, lockErr := acquireAutoStartLock(c.socketPath)
			if lockErr != nil {
				if !errors.Is(lockErr, errAutoStartInProgress) {
					return Response{}, fmt.Errorf("daemon auto-start lock failed: %w", lockErr)
				}
			} else {
				autoStartLock = lock
			}
		}
		if autoStartLock != nil {
			// Another process may have completed startup between our failed Send
			// and acquiring the lock. Recheck before spawning.
			resp, err = sender.send()
			if err == nil {
				return resp, nil
			}
			started := time.Now()
			if startErr := spawnDetached(amxBin); startErr != nil {
				observeDeliveryStage(observer, DeliveryStageDaemonAutostart, "error", startErr.Error(), started)
				return Response{}, fmt.Errorf("daemon auto-start failed: %w", startErr)
			}
			observeDeliveryStage(observer, DeliveryStageDaemonAutostart, "success", "daemon_spawned", started)
		}
	}

	// Retry only for the remainder of the hook's auto-start budget.
	resp, err = c.sendWithRetriesObserved(sender, deadline)
	if err == nil {
		return resp, nil
	}
	return Response{}, fmt.Errorf("daemon did not start in time: %w", err)
}

// ExportEvents returns the per-window hook-event ring buffers plus durable
// metadata from the running daemon. When windowID is non-zero, the daemon
// filters the snapshot to that single window; otherwise every exported window
// is returned. Existing TypeSessionDetail callers build the request inline and
// call Send directly (see tui/model_modes.go); ExportEvents is the typed helper
// for the richer CLI export so the command does not have to know about the
// message-type constant or response-shape details.
func (c *Client) ExportEvents(windowID int) (EventsExport, error) {
	resp, err := c.sendWithDeadline(Request{Type: TypeEventsExport, WindowID: windowID}, exportDeadline)
	if err != nil {
		return EventsExport{}, err
	}
	if !resp.OK {
		return EventsExport{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	return EventsExport{
		EventsByWindow: resp.EventsByWindow,
		WindowMetadata: resp.WindowMetadata,
	}, nil
}

// SpawnDetached starts `amxBin daemon start` in a new session. Stdio is
// detached from the caller and inherited by a private bootstrap log when the
// diagnostics directory is available. Diagnostics setup is fail-open: if the
// private log cannot be created, the daemon is still started with nil stdio.
func SpawnDetached(amxBin string) error {
	cmd := exec.Command(amxBin, "daemon", "start")
	cmd.SysProcAttr = detachSysProcAttr()

	var bootstrap *os.File
	if paths, err := diagnostics.DefaultPaths(); err == nil {
		launchID := strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		if file, bootstrapPath, openErr := diagnostics.OpenBootstrapLog(paths, launchID); openErr == nil {
			bootstrap = file
			cmd.Stdout = bootstrap
			cmd.Stderr = bootstrap
			cmd.Env = environmentWithOverrides(os.Environ(),
				daemonDetachedEnv+"=1",
				daemonLaunchIDEnv+"="+launchID,
				daemonBootstrapEnv+"="+bootstrapPath,
			)
		}
	}
	if err := cmd.Start(); err != nil {
		if bootstrap != nil {
			return errors.Join(err, bootstrap.Close())
		}
		return err
	}
	_ = cmd.Process.Release()
	if bootstrap != nil {
		_ = bootstrap.Close()
	}
	return nil
}

func environmentWithOverrides(base []string, overrides ...string) []string {
	keys := make(map[string]bool, len(overrides))
	for _, override := range overrides {
		key, _, _ := strings.Cut(override, "=")
		keys[key] = true
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if !keys[key] {
			result = append(result, entry)
		}
	}
	return append(result, overrides...)
}
