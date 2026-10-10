// Package tmux is the private tmux terminal backend. AgentMux runs its own
// tmux server on a socket it owns, started with a configuration it writes, so
// agents survive the app and the daemon without ever touching the operator's
// default tmux server or their personal tmux configuration.
//
// Each runtime session is one tmux session holding one agent pane. A group
// reference is the tmux session ID ($N) and a pane reference is the pane ID
// (%N); both are only meaningful inside one server generation, which Instance
// reports. Exited panes stay in place (remain-on-exit) with their exit status
// and screen until AgentMux closes the group explicitly.
//
// This package is the only place that spells tmux commands or targets.
package tmux

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sglavoie/agentmux/internal/atomicfile"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// Config is the whole configuration of the private server. The server is
// started with `-f` this file, so tmux reads neither /etc/tmux.conf nor any
// personal ~/.tmux.conf or $XDG_CONFIG_HOME/tmux/tmux.conf.
const Config = `# Written by AgentMux; changes are overwritten. This private server never
# reads a personal tmux configuration.

# Agents receive every key: there is no prefix, so ctrl+b and friends reach
# the program instead of tmux.
set -g prefix None
set -g prefix2 None
unbind-key -a -T prefix

# The one key tmux keeps: it detaches the viewing client, which is how an
# attached terminal gets back to whatever attached it. The agent keeps running.
bind-key -n F12 detach-client

# Forward modified keys (shift+enter, ctrl+enter, alt+arrows) in the form the
# pane program asked for, rather than collapsing them to legacy bytes.
set -s extended-keys on
set -s extended-keys-format csi-u
set -as terminal-features 'xterm-ghostty:extkeys:RGB:clipboard:focus:sync'
set -s escape-time 10
set -s focus-events on
set -s set-clipboard on

set -g default-terminal tmux-256color
set -g history-limit 50000
set -g mouse on
set -g status off
set -g base-index 0
set -g window-size latest

# A session's environment is exactly the launch environment AgentMux passes
# with -e; a later client never copies its own variables in.
set -g update-environment ''

# Agents outlive every viewer, and an exited agent keeps its pane, exit
# status, and screen until AgentMux closes the session.
set -g destroy-unattached off
set -s exit-unattached off
set -g remain-on-exit on
set -g allow-rename off
set -g automatic-rename off
`

// DetachKey is the key that detaches an attached terminal from a session.
const DetachKey = "F12"

// ErrServerNotRunning reports that no server answers on the socket: it was
// never started, it exited with its last session, or it died and left a
// stale socket behind.
var ErrServerNotRunning = errors.New("tmux: server not running")

// Options configures a Server. Zero values select the defaults.
type Options struct {
	// Binary is the tmux executable. Default: tmux on PATH.
	Binary string
	// Dir holds the socket, the configuration, and the metadata lock. It is
	// created 0700 and must not be accessible to anyone else. Default:
	// DefaultDir().
	Dir string
	// Timeout bounds one tmux client invocation. Default: 5s.
	Timeout time.Duration
}

// Server is a handle on the private tmux server at one socket. The server
// itself is started lazily by the first launch and exits with its last
// session.
type Server struct {
	binary  string
	dir     string
	socket  string
	config  string
	timeout time.Duration
}

// maxSocketPath is the usable length of a Unix socket path on macOS (and
// below Linux's 108), so one limit serves both.
const maxSocketPath = 103

// DefaultDir is the per-user directory of the default private server. It is
// under the temporary directory on purpose: a reboot ends every agent, and
// the socket must not outlive the server it names.
func DefaultDir() string {
	return filepath.Join(os.TempDir(), "agent-mux-"+strconv.Itoa(os.Getuid()))
}

// NewServer prepares the private directory and configuration. It does not
// start the server.
func NewServer(opts Options) (*Server, error) {
	binary := opts.Binary
	if binary == "" {
		binary = "tmux"
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("tmux: finding %s: %w", binary, err)
	}
	dir := opts.Dir
	if dir == "" {
		dir = DefaultDir()
	}
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("tmux: server directory %q must be absolute", dir)
	}
	dir = filepath.Clean(dir)
	if err := ensurePrivateDir(dir); err != nil {
		return nil, err
	}
	s := &Server{
		binary:  resolved,
		dir:     dir,
		socket:  filepath.Join(dir, "tmux.sock"),
		config:  filepath.Join(dir, "tmux.conf"),
		timeout: opts.Timeout,
	}
	if s.timeout <= 0 {
		s.timeout = 5 * time.Second
	}
	if len(s.socket) > maxSocketPath {
		return nil, fmt.Errorf("tmux: socket path %q is longer than %d bytes", s.socket, maxSocketPath)
	}
	if err := writeConfig(s.config); err != nil {
		return nil, err
	}
	return s, nil
}

// Socket is the server's socket path, the address of its BackendInstance.
func (s *Server) Socket() string { return s.socket }

// ConfigPath is the configuration the server is started with.
func (s *Server) ConfigPath() string { return s.config }

// Binary is the resolved tmux executable.
func (s *Server) Binary() string { return s.binary }

// Instance identifies the running server. The generation is the server's PID
// and start time, so a server restarted on the same socket is a different
// instance and every target of the old one is stale.
func (s *Server) Instance() (runtimesession.BackendInstance, error) {
	out, err := s.run([]string{"list-sessions", "-F", "#{pid}" + fieldSep + "#{start_time}"})
	if err != nil {
		return runtimesession.BackendInstance{}, err
	}
	line, _, _ := strings.Cut(out, "\n")
	pid, start, ok := strings.Cut(line, fieldSep)
	if !ok || pid == "" || start == "" {
		return runtimesession.BackendInstance{}, fmt.Errorf("tmux: unexpected server identity %q", line)
	}
	return s.instance(pid, start), nil
}

func (s *Server) instance(pid, start string) runtimesession.BackendInstance {
	return runtimesession.BackendInstance{
		Kind:       runtimesession.BackendTmux,
		Address:    s.socket,
		Generation: pid + "-" + start,
	}
}

// Kill stops the whole server and every agent in it. Killing a server that is
// not running is not an error.
func (s *Server) Kill() error {
	_, err := s.run([]string{"kill-server"})
	if errors.Is(err, ErrServerNotRunning) {
		return nil
	}
	return err
}

// CommandError is a failed tmux client invocation.
type CommandError struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *CommandError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("tmux %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *CommandError) Unwrap() error { return e.Err }

// Is matches ErrServerNotRunning when tmux could not reach a server.
func (e *CommandError) Is(target error) bool {
	return target == ErrServerNotRunning && serverAbsent(e.Stderr)
}

func serverAbsent(stderr string) bool {
	return strings.Contains(stderr, "no server running on") ||
		(strings.Contains(stderr, "error connecting to") &&
			(strings.Contains(stderr, "No such file or directory") || strings.Contains(stderr, "Connection refused")))
}

// run executes one command; see runChain.
func (s *Server) run(cmd []string) (string, error) {
	return s.runChain([][]string{cmd})
}

// runChain executes commands as one client invocation, which the server runs
// back to back without interleaving another client's commands. Arguments are
// passed as argv, never through a shell; a trailing ';' is escaped so tmux
// does not read it as a command separator.
func (s *Server) runChain(cmds [][]string) (string, error) {
	args := []string{"-S", s.socket, "-f", s.config}
	var shown []string
	for i, cmd := range cmds {
		if i > 0 {
			args = append(args, ";")
			shown = append(shown, ";")
		}
		for j, a := range cmd {
			args = append(args, escapeArg(a))
			if j > 0 && cmd[j-1] == "-e" {
				// Launch environments carry profile secrets; errors name only the key.
				key, _, _ := strings.Cut(a, "=")
				a = key + "=<redacted>"
			}
			shown = append(shown, a)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	c := exec.CommandContext(ctx, s.binary, args...)
	c.Env = clientEnv(os.Environ())
	c.Dir = s.dir
	c.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("timed out after %s: %w", s.timeout, ctx.Err())
	}
	if err != nil {
		return stdout.String(), &CommandError{Args: shown, Stderr: stderr.String(), Err: err}
	}
	return stdout.String(), nil
}

func escapeArg(a string) string {
	if strings.HasSuffix(a, ";") {
		return a[:len(a)-1] + `\;`
	}
	return a
}

// clientEnv is the environment of every tmux client. The client that starts
// the server seeds the server's global environment, which every pane
// inherits, so variables naming the caller's own terminal or session are
// removed: a pane must never report into a Kitty window, a parent tmux, or a
// runtime session it does not belong to. A launch's own identity arrives
// through LaunchSpec.Env instead.
func clientEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		if strippedEnv(key) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func strippedEnv(key string) bool {
	switch key {
	case "TMUX", "TMUX_PANE", "WINDOWID", "CLAUDECODE", runtimesession.EnvVar:
		return true
	}
	return strings.HasPrefix(key, "KITTY_")
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("tmux: creating %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("tmux: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("tmux: %s is not a directory", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("tmux: %s is accessible to other users (mode %v)", dir, info.Mode().Perm())
	}
	if !ownedByMe(info) {
		return fmt.Errorf("tmux: %s is not owned by the current user", dir)
	}
	return nil
}

func ownedByMe(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

func writeConfig(path string) error {
	if current, err := os.ReadFile(path); err == nil && string(current) == Config {
		return nil
	}
	if err := atomicfile.Write(path, []byte(Config), 0o600); err != nil {
		return fmt.Errorf("tmux: writing %s: %w", path, err)
	}
	return nil
}

func newSessionName() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "amx-" + hex.EncodeToString(b[:]), nil
}
