package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
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

// sendDeadline bounds total request/response time so a wedged or crashed
// daemon cannot hang a hook script indefinitely. Hook commands are invoked
// per Claude event and must return promptly. Exposed as a var (not const) so
// tests can shrink it without sleeping for several seconds.
var sendDeadline = 5 * time.Second

var autoStartRetryDelay = 200 * time.Millisecond
var spawnDetached = SpawnDetached

const autoStartRetryCount = 5
const restartSocketPollCount = 20

var restartSocketPollDelay = 50 * time.Millisecond

// maxResponseBytes caps the response payload size the client will buffer.
// It mirrors the server's request ceiling (1 MiB) so an oversize or
// malformed response triggers an explicit error rather than bufio.Scanner's
// silent 64 KiB truncation.
const maxResponseBytes = 1 << 20

// Send sends req to the daemon and returns the response.
// Returns an error if the daemon is not reachable.
func (c *Client) Send(req Request) (Response, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, 2*time.Second)
	if err != nil {
		return Response{}, fmt.Errorf("connecting to daemon: %w", err)
	}
	defer func() { _ = conn.Close() }()

	// Bound the whole exchange so a stuck daemon cannot hang the caller.
	_ = conn.SetDeadline(time.Now().Add(sendDeadline))

	data, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("encoding request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return Response{}, fmt.Errorf("sending request: %w", err)
	}

	var resp Response
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), maxResponseBytes)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
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

// Health is the result of a TypeHealth query. Alive is false when the
// daemon is unreachable; in that case PID and Uptime are zero.
type Health struct {
	Alive  bool
	PID    int
	Uptime time.Duration
}

// Health pings the daemon and returns its liveness, PID and uptime. Never
// returns an error — an unreachable daemon yields Health{Alive: false}.
func (c *Client) Health() Health {
	resp, err := c.Send(Request{Type: TypeHealth})
	if err != nil || !resp.OK {
		return Health{}
	}
	return Health{
		Alive:  true,
		PID:    resp.PID,
		Uptime: time.Duration(resp.UptimeSeconds) * time.Second,
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

func waitForSocketRemoval(socketPath string) {
	for i := 0; i < restartSocketPollCount; i++ {
		if _, err := os.Stat(socketPath); os.IsNotExist(err) {
			return
		}
		time.Sleep(restartSocketPollDelay)
	}
}

// RestartDetached stops the current daemon if it is running, then starts a new
// detached daemon using amxBin. It does not wait for the new socket to become
// reachable; callers that need a live server should retry with Send afterward.
func (c *Client) RestartDetached(amxBin string) error {
	return c.restartDetached(amxBin, c.Health().Alive)
}

func (c *Client) restartDetached(amxBin string, daemonAlive bool) error {
	if daemonAlive {
		resp, err := c.Send(Request{Type: TypeShutdown})
		if err != nil {
			return fmt.Errorf("shutting down daemon: %w", err)
		}
		if !resp.OK {
			return fmt.Errorf("daemon error: %s", resp.Error)
		}
		waitForSocketRemoval(c.socketPath)
	}
	if err := spawnDetached(amxBin); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	return nil
}

// SendWithAutoStart sends req to the daemon, auto-starting it if not running.
// amxBin is the path to the amx binary used for auto-start.
func (c *Client) SendWithAutoStart(req Request, amxBin string) (Response, error) {
	restarted := false
	if daemonSocketOlderThanBinary(c.socketPath, amxBin) {
		if health := c.Health(); health.Alive {
			slog.Debug("daemon client: restarting stale daemon for newer amx binary",
				"socket_path", c.socketPath,
				"amx_bin", amxBin,
				"daemon_pid", health.PID,
			)
			if err := c.restartDetached(amxBin, true); err != nil {
				return Response{}, fmt.Errorf("daemon auto-restart failed: %w", err)
			}
			restarted = true
		}
	}

	resp, err := c.Send(req)
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
		if startErr := spawnDetached(amxBin); startErr != nil {
			return Response{}, fmt.Errorf("daemon auto-start failed: %w", startErr)
		}
	}

	// Retry for up to 1s.
	for i := 0; i < autoStartRetryCount; i++ {
		time.Sleep(autoStartRetryDelay)
		resp, err = c.Send(req)
		if err == nil {
			return resp, nil
		}
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
	resp, err := c.Send(Request{Type: TypeEventsExport, WindowID: windowID})
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

// SpawnDetached starts `amxBin daemon start` in a new session with stdio
// detached, freeing the calling terminal immediately. The spawned process
// survives the parent's termination. Does not wait for the daemon socket
// to become reachable.
func SpawnDetached(amxBin string) error {
	cmd := exec.Command(amxBin, "daemon", "start")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = detachSysProcAttr()
	return cmd.Start()
}
