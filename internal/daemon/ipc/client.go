package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/daemon/process"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
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
	socketPath  string
	dialContext func(context.Context, string, string) (net.Conn, error)
}

func defaultDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

// NewClient returns a Client targeting the default socket path.
func NewClient() *Client {
	return &Client{socketPath: SocketPath(), dialContext: defaultDialContext}
}

// NewClientWithSocket returns a Client targeting an explicit socket path (for tests).
func NewClientWithSocket(path string) *Client {
	return &Client{socketPath: path, dialContext: defaultDialContext}
}

// sendDeadline bounds one request/response exchange with a live daemon. A
// normal acknowledgement returns well within this budget; the ceiling only
// guards against a wedged daemon. autoStartDeadline bounds the full hook-client
// path, including a cold daemon start and retry, so a hook never delays its
// harness beyond the two-second cold-start budget.
//
// NormalAckBudget is exported for one caller: the daemon's startup publication
// probe, which dials its own freshly bound socket and must not wait longer for
// its own health answer than a hook client would.
const NormalAckBudget = 250 * time.Millisecond
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

var sendDeadline = NormalAckBudget
var autoStartDeadline = coldStartBudget

var autoStartRetryDelay = 50 * time.Millisecond
var spawnDetached = process.SpawnDetached

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

// stateQueryDeadline bounds one dashboard state query. It sits deliberately
// between the two existing budgets. The 250ms acknowledgement budget is sized
// for a hook that must not delay its harness and is too tight here: a state
// query marshals every tracked window plus the cached Kitty tree. The 5s export
// budget is far too loose: the whole point of serving the query from the cache
// is that it never waits on Kitty, so a second is already generous and anything
// longer would let a wedged daemon stall a dashboard refresh instead of letting
// it fall back.
var stateQueryDeadline = 1 * time.Second

// Send sends req to the daemon and returns the response.
// Returns an error if the daemon is not reachable.
func (c *Client) Send(req protocol.Request) (protocol.Response, error) {
	return c.sendWithDeadline(req, sendDeadline)
}

func (c *Client) sendWithDeadline(req protocol.Request, timeout time.Duration) (protocol.Response, error) {
	if timeout <= 0 {
		return protocol.Response{}, errors.New("daemon request deadline elapsed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return c.SendWithContext(ctx, req)
}

// SendWithContext sends req under an explicit context rather than one of the
// package's own deadlines. Send and ShutdownAndWait use it internally; it is
// exported for the daemon's startup publication probe, which already owns a
// cancellable startup context and must abandon the probe the moment startup is
// cancelled rather than after a fixed budget.
func (c *Client) SendWithContext(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	if ctx == nil {
		return protocol.Response{}, errors.New("daemon request context is nil")
	}
	if err := ctx.Err(); err != nil {
		return protocol.Response{}, err
	}
	dialContext := c.dialContext
	if dialContext == nil {
		dialContext = defaultDialContext
	}
	conn, err := dialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return protocol.Response{}, fmt.Errorf("connecting to daemon: %w", err)
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
		return protocol.Response{}, fmt.Errorf("encoding request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return protocol.Response{}, ctxErr
		}
		return protocol.Response{}, fmt.Errorf("sending request: %w", err)
	}

	var resp protocol.Response
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), maxResponseBytes)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return protocol.Response{}, ctxErr
			}
			if errors.Is(err, bufio.ErrTooLong) {
				return protocol.Response{}, fmt.Errorf("daemon response exceeds %d bytes", maxResponseBytes)
			}
			return protocol.Response{}, fmt.Errorf("reading response: %w", err)
		}
		return protocol.Response{}, fmt.Errorf("daemon closed connection without responding")
	}
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return protocol.Response{}, fmt.Errorf("decoding response: %w", err)
	}
	return resp, nil
}

func (c *Client) sendBefore(req protocol.Request, deadline time.Time) (protocol.Response, error) {
	timeout := sendDeadline
	if !deadline.IsZero() {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return protocol.Response{}, errors.New("daemon auto-start deadline elapsed")
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
	DaemonSettings map[string]string
	Alive          bool
	PID            int
	Uptime         time.Duration
	Ready          bool
	DegradedReason string
	Build          *buildinfo.Info
	Metrics        *protocol.MetricsSnapshot
	Notification   *protocol.NotificationCapability
	// ASL is the optional agent-session-loop capability block. Nil means the
	// daemon publishes no ASL integration at all.
	ASL *aslbridge.Status
	// ASLReconcile and ASLHookDelivery are the two directions of the pairing
	// that survive a missing bridge: what the daemon can read from ASL's state
	// root, and what ASL managed to push through the hook path. Both are nil
	// against a daemon that predates them, which is why doctor reports their
	// absence as an unknown rather than as a zero.
	ASLReconcile    *protocol.ASLReconcileSnapshot
	ASLHookDelivery *protocol.ASLHookDeliverySnapshot
	// ScreenPatterns is the startup screen-pattern load result: the active
	// source per pattern set plus every load warning. Nil against a daemon that
	// predates the block.
	ScreenPatterns *protocol.ScreenPatternsSnapshot
	// KittySnapshot is the status-only view of the daemon's cached Kitty
	// topology: readiness, staleness, generation, capture time, age, and the
	// list-coordinator counters. Its Windows field is always empty here —
	// health is a probe, and the tree belongs to StateQuery. Nil against a
	// daemon that predates the block, which a consumer must report as unknown
	// rather than as a cold cache.
	//
	// Staleness here is deliberately independent of Ready and DegradedReason
	// above: a wedged `kitten @ ls` is a different fault from a daemon that
	// failed its bounded restore, and folding one into the other would hide
	// whichever came second.
	KittySnapshot *protocol.KittySnapshot
	// Companion lists the connected companion subscribers. Nil against a
	// daemon that predates the companion protocol.
	Companion *protocol.CompanionHealth
	// Runtime is the daemon's runtime-session and backend work: how current
	// reconciliation is, how many sessions have a terminal attached, how the
	// screen reads are going, and its tmux client activity. Nil against a
	// daemon that predates the block.
	Runtime *protocol.RuntimeHealth
}

// Health pings the daemon and returns its liveness, PID and uptime. Never
// returns an error — an unreachable daemon yields Health{Alive: false}.
func (c *Client) Health() Health {
	return c.healthBefore(time.Time{})
}

func (c *Client) healthBefore(deadline time.Time) Health {
	resp, err := c.sendBefore(protocol.Request{Type: protocol.TypeHealth}, deadline)
	if err != nil || !resp.OK {
		return Health{}
	}
	return Health{
		DaemonSettings: resp.DaemonSettings,
		Alive:          true,
		PID:            resp.PID,
		Uptime:         time.Duration(resp.UptimeSeconds) * time.Second,
		Ready:          resp.Ready,
		DegradedReason: resp.DegradedReason,
		Build:          resp.Build,
		Metrics:        resp.Metrics,
		Notification:   resp.Notification,
		ASL:            resp.ASLCapability,

		ASLReconcile:    resp.ASLReconcile,
		ASLHookDelivery: resp.ASLHookDelivery,
		ScreenPatterns:  resp.ScreenPatterns,
		KittySnapshot:   resp.KittySnapshot,
		Companion:       resp.Companion,
		Runtime:         resp.Runtime,
	}
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
	resp, err := c.SendWithContext(ctx, protocol.Request{Type: protocol.TypeShutdown})
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

// NotificationSnooze sets, clears, or reads the daemon's global notification
// snooze and returns the snooze now in force. An empty Mode is a read. It never
// auto-starts the daemon: a snooze lives in a running daemon's memory, so
// starting one just to snooze it — or to report that nothing is snoozed —
// would answer a question nobody asked. It uses the state-query budget, since
// the daemon answers from memory without touching Kitty.
func (c *Client) NotificationSnooze(req protocol.SnoozeRequest) (protocol.SnoozeState, error) {
	resp, err := c.sendWithDeadline(protocol.Request{Type: protocol.TypeNotificationSnooze, Snooze: &req}, stateQueryDeadline)
	if err != nil {
		return protocol.SnoozeState{}, err
	}
	if !resp.OK {
		return protocol.SnoozeState{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	if resp.Snooze == nil {
		return protocol.SnoozeState{}, errors.New("daemon answered notification_snooze without a snooze block (restart an older daemon)")
	}
	return *resp.Snooze, nil
}

// NotificationTest asks the daemon to offer one test notification to the
// companion app holding the notification backend role. A result that is not
// Delivered names why no app confirmed one; the caller then tests Alerter
// itself. Like NotificationSnooze it never auto-starts the daemon, since a
// daemon started just now holds no companion subscriber.
func (c *Client) NotificationTest() (protocol.NotificationTestResult, error) {
	resp, err := c.sendWithDeadline(protocol.Request{Type: protocol.TypeNotificationTest}, NotificationTestDeadline)
	if err != nil {
		return protocol.NotificationTestResult{}, err
	}
	if !resp.OK {
		return protocol.NotificationTestResult{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	if resp.NotificationTest == nil {
		return protocol.NotificationTestResult{}, errors.New("daemon answered notification_test without a result (restart an older daemon)")
	}
	return *resp.NotificationTest, nil
}

// WorkspaceView asks the daemon to have the desktop workspace show a view. A
// refusal is returned as an error whose text is the daemon's refusal code
// (protocol.WorkspaceViewError...), which is also what an older daemon's
// "unknown request type" becomes: either way the caller shows the view itself.
// It never auto-starts the daemon, since a daemon started just now has no
// workspace connected, and it uses the state-query budget because the daemon
// answers without touching a terminal.
func (c *Client) WorkspaceView(view protocol.WorkspaceViewRequest) (protocol.WorkspaceViewResult, error) {
	resp, err := c.sendWithDeadline(protocol.Request{Type: protocol.TypeWorkspaceView, WorkspaceView: &view}, stateQueryDeadline)
	if err != nil {
		return protocol.WorkspaceViewResult{}, err
	}
	if !resp.OK {
		return protocol.WorkspaceViewResult{}, errors.New(resp.Error)
	}
	if resp.WorkspaceView == nil {
		return protocol.WorkspaceViewResult{}, nil
	}
	return *resp.WorkspaceView, nil
}
