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
	return filepath.Join(home, ".local", "share", "cc-manager", "daemon.sock")
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

// Send sends req to the daemon and returns the response.
// Returns an error if the daemon is not reachable.
func (c *Client) Send(req Request) (Response, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, 2*time.Second)
	if err != nil {
		return Response{}, fmt.Errorf("connecting to daemon: %w", err)
	}
	defer func() { _ = conn.Close() }()

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

// SendWithAutoStart sends req to the daemon, auto-starting it if not running.
// ccmBin is the path to the ccm binary used for auto-start.
func (c *Client) SendWithAutoStart(req Request, ccmBin string) (Response, error) {
	resp, err := c.Send(req)
	if err == nil {
		return resp, nil
	}

	// Daemon not running — try to start it.
	cmd := exec.Command(ccmBin, "daemon", "start")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = detachSysProcAttr()
	if startErr := cmd.Start(); startErr != nil {
		return Response{}, fmt.Errorf("daemon auto-start failed: %w", startErr)
	}
	// Do not Wait(); leave the daemon running.

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
