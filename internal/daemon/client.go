package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// SocketPath returns the Unix socket path for the daemon.
func SocketPath() string {
	home, _ := os.UserHomeDir()
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

// SendWithAutoStart sends req to the daemon, auto-starting it if not running.
// amxBin is the path to the amx binary used for auto-start.
func (c *Client) SendWithAutoStart(req Request, amxBin string) (Response, error) {
	resp, err := c.Send(req)
	if err == nil {
		return resp, nil
	}

	if startErr := SpawnDetached(amxBin); startErr != nil {
		return Response{}, fmt.Errorf("daemon auto-start failed: %w", startErr)
	}

	// Retry for up to 1s.
	for i := 0; i < 5; i++ {
		time.Sleep(200 * time.Millisecond)
		resp, err = c.Send(req)
		if err == nil {
			return resp, nil
		}
	}
	return Response{}, fmt.Errorf("daemon did not start in time: %w", err)
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
