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
// What a terminal attached to a session shows of it, its title and an
// optional status line, is built by the configuration from two pane options
// (status.go) that AgentMux's daemon renders. The server decides nothing
// about a session's state.
//
// A plain terminal attached to a session gets from the configuration the keys
// Kitty bound for its tabs (internal/kittykeys.Tmux). Going to another session
// is a client switched to it (switch.go), under the rule attaching follows:
// one owner, and never an agent that is not running. An overlay is a popup
// over the terminal, which is a view and never a session.
//
// The tmux executable is chosen here too (runtime.go): an explicit choice,
// the runtime bundled with the desktop app (verified against its checksums
// and copied out of the bundle), or a supported tmux on PATH. A server that is
// running always keeps the executable it was started with, recorded beside
// its socket, so installing another tmux never changes what live sessions run
// on or what talks to them.
//
// Every command is one short-lived tmux client process, and a program runs at
// most MaxClientCommands of them at once (gate.go); the rest wait, inside the
// command's own timeout. Stats reports that activity and FailureClass names a
// failure in a fixed vocabulary, which is all a diagnostic may publish of one.
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
	"github.com/sglavoie/agentmux/internal/cachefile"
	"github.com/sglavoie/agentmux/internal/kittykeys"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// Config is the whole configuration of the private server. The server is
// started with `-f` this file, so tmux reads neither /etc/tmux.conf nor any
// personal ~/.tmux.conf or $XDG_CONFIG_HOME/tmux/tmux.conf.
var Config = configHead + kittykeys.TmuxConfig() + configTail

const configHead = `# Written by AgentMux; changes are overwritten. This private server never
# reads a personal tmux configuration.

# Agents receive every key: there is no prefix, so ctrl+b and friends reach
# the program instead of tmux.
set -g prefix None
set -g prefix2 None
unbind-key -a -T prefix

# F12 detaches the viewing client, which is how an attached terminal gets back
# to whatever attached it. The agent keeps running. The other function keys
# are bound further down; everything else reaches the agent.
bind-key -n F12 detach-client

# Forward modified keys (shift+enter, ctrl+enter, alt+arrows) in the form the
# pane program asked for, rather than collapsing them to legacy bytes.
set -s extended-keys on
set -s extended-keys-format csi-u
# An indexed entry, not an append: the configuration is sourced again at every
# attach, and that must not grow the list.
set -s terminal-features[20] 'xterm-ghostty:extkeys:RGB:clipboard:focus:sync:hyperlinks'
set -s escape-time 10
set -s focus-events on
set -s set-clipboard on

set -g default-terminal tmux-256color
set -g history-limit 50000
set -g mouse on
set -g base-index 0
set -g window-size latest

# What a session shows about itself is rendered by the AgentMux daemon into
# @amx_status (state icon and title) and @amx_status_color on the agent's
# pane; nothing here decides it. The outer terminal's title is that text, so
# its tab reads as a Kitty tab did. The one-line status shows the same text on
# the session's color; it is off unless the terminal that attaches asks for it
# (amx attach turns it on for a plain terminal and off for the desktop
# workspace, which lists every session itself). It is a view of the session,
# never a session: nothing is bound on it and it takes no key.
set -g set-titles on
set -g set-titles-string '#{?@amx_status,#{@amx_status},Agent Mux}'
set -g status off
set -g status-position bottom
set -g status-style default
set -g status-format[0] '#{?@amx_status_color,#[bg=#{@amx_status_color}#,fg=black],} #{?@amx_status,#{q/h:@amx_status},Agent Mux} '

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
# The pane title is the agent's to set: Claude Code names its turn there, and
# the daemon reads it with the screen. It names no window and no session.
set -g allow-set-title on

# History is held here, in the server, so it outlives every viewer: lines that
# scrolled off a pane are tmux's (history-limit above), and a full-screen
# program keeps its own transcript in a process this server holds. A viewer
# asks for history with the keys below, which no keyboard produces: the app
# sends them for its menu items, so nobody needs a prefix or a tmux key.
#
# Page up and down go to whichever holds the history: tmux's copy mode, or a
# full-screen program's own Page Up and Page Down. Copy mode entered with -e
# ends by itself at the bottom; Escape, q, and Enter leave it too. The vi key
# table is used because its searches leave the cursor at the start of a match,
# so stepping to the previous match always moves.
set -g mode-keys vi
set -s user-keys[0] "\e[5001~"
set -s user-keys[1] "\e[5002~"
set -s user-keys[2] "\e[5003~"
set -s user-keys[3] "\e[5004~"
set -s user-keys[4] "\e[5005~"
set -s user-keys[5] "\e[5006~"
set -s user-keys[6] "\e[5007~"
set -s user-keys[7] "\e[5008~"
bind-key -n User0 if-shell -F '#{pane_in_mode}' { send-keys -X page-up } { if-shell -F '#{alternate_on}' { send-keys PPage } { copy-mode -eu } }
bind-key -n User1 if-shell -F '#{pane_in_mode}' { send-keys -X page-down } { if-shell -F '#{alternate_on}' { send-keys NPage } }
bind-key -n User2 if-shell -F '#{||:#{pane_in_mode},#{!:#{alternate_on}}}' { copy-mode -e ; send-keys -X history-top }
bind-key -n User3 if-shell -F '#{pane_in_mode}' { send-keys -X cancel }
bind-key -n User4 if-shell -F '#{alternate_on}' { copy-mode -e ; command-prompt -i -T search -p "Find on visible screen:" { send-keys -X search-backward-incremental -- "%%" } } { copy-mode -e ; command-prompt -i -T search -p "Find in terminal history:" { send-keys -X search-backward-incremental -- "%%" } }
bind-key -n User5 if-shell -F '#{pane_in_mode}' { send-keys -X search-again }
bind-key -n User6 if-shell -F '#{pane_in_mode}' { send-keys -X search-reverse }
# Marks what went wrong: every error, failure, warning, and deprecation in the
# history is highlighted, from the last one up, and the same key again goes
# back to the live screen. A pattern with no capital matches any case, and
# the parentheses are what make tmux read it as a regular expression. A
# full-screen program keeps its own transcript, so there the key is handed on
# as the F3 the viewer pressed, which is that program's own search.
bind-key -n User7 if-shell -F '#{pane_in_mode}' { send-keys -X cancel } { if-shell -F '#{alternate_on}' { send-keys F3 } { copy-mode -e ; send-keys -X search-backward -- "(error|failed|warning|deprecated)" } }
# F3 is that same key for a terminal with no menu to send it.
bind-key -n F3 if-shell -F '#{pane_in_mode}' { send-keys -X cancel } { if-shell -F '#{alternate_on}' { send-keys F3 } { copy-mode -e ; send-keys -X search-backward -- "(error|failed|warning|deprecated)" } }
bind-key -T copy-mode-vi Escape send-keys -X cancel

# The keys Kitty bound for its tabs, for a plain terminal attached here: the
# dashboard, the overview, the reference, and the pickers open in a popup over
# the terminal that pressed the key, and the rest run amx in the background.
# Each runs in the environment of the session the terminal is on. The desktop
# app's menu takes these keys before they are sent, so there they never
# arrive. The lines are generated from internal/kittykeys.Tmux.
#
# A popup's command is run by default-shell, so that is the shell the lines
# are written for; no pane uses it, since every agent is started as an argv.
# AMX_BIN is set by the terminal that attaches (amx attach), because this
# server's PATH is that of whatever started it.
set -g default-shell /bin/sh
`
const configTail = `

# The mouse selects, scrolls, and reaches a program that asked for it. tmux's
# own menus and pane shuffling are removed: a right click must never offer to
# kill, respawn, or split an agent's pane.
bind-key -n MouseDown3Pane if-shell -F -t = '#{||:#{mouse_any_flag},#{pane_in_mode}}' { select-pane -t = ; send-keys -M }
unbind-key -n M-MouseDown3Pane
unbind-key -n MouseDown3Status
unbind-key -n M-MouseDown3Status
unbind-key -n MouseDown3StatusLeft
unbind-key -n M-MouseDown3StatusLeft
# tmux 3.8 adds menus on empty space and Ctrl-drag pane creation. Empty-space
# key names are unknown to older supported runtimes; -q keeps sourcing quiet.
unbind-key -q -n MouseDown3Empty
unbind-key -q -n M-MouseDown3Empty
unbind-key -q -n C-MouseDrag1Empty
unbind-key -n C-MouseDrag1Pane
unbind-key -n M-MouseDrag1Pane
unbind-key -n M-MouseDrag1Border
unbind-key -n C-MouseDown1Pane
unbind-key -n C-MouseDown1Status
unbind-key -n MouseDown1Status
unbind-key -n MouseDown1Control7
unbind-key -n MouseDown1Control8
unbind-key -n MouseDown1Control9
unbind-key -n WheelUpStatus
unbind-key -n WheelDownStatus
`

// DetachKey is the key that detaches an attached terminal from a session.
const DetachKey = "F12"

// ViewerKey is something a viewer asks the server to do with a pane's history.
// Each is one of the configuration's user keys: an escape sequence no keyboard
// produces, which a viewer writes to its tmux client.
type ViewerKey int

// The history requests, in user-key order.
const (
	ViewerPageUp ViewerKey = iota
	ViewerPageDown
	ViewerTop
	ViewerBottom
	ViewerFind
	ViewerFindNext
	ViewerFindPrevious
	ViewerMarkers
	viewerKeyCount
)

// CSI is what follows the CSI introducer (ESC [) in the key's sequence.
func (k ViewerKey) CSI() string { return strconv.Itoa(5001+int(k)) + "~" }

// ViewerKeys lists every history request.
func ViewerKeys() []ViewerKey {
	keys := make([]ViewerKey, viewerKeyCount)
	for i := range keys {
		keys[i] = ViewerKey(i)
	}
	return keys
}

// ErrServerNotRunning reports that no server answers on the socket: it was
// never started, it exited with its last session, or it died and left a
// stale socket behind.
var ErrServerNotRunning = errors.New("tmux: server not running")

// Options configures a Server. Zero values select the defaults.
type Options struct {
	// Binary is the tmux executable. Default: the one the running server was
	// started with, else what ResolveRuntime picks (the app's bundled
	// runtime, or a supported tmux on PATH).
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
	var resolved string
	if opts.Binary != "" {
		found, err := exec.LookPath(opts.Binary)
		if err != nil {
			return nil, fmt.Errorf("tmux: finding %s: %w", opts.Binary, err)
		}
		resolved = found
	} else {
		chosen, err := serverBinary(dir, filepath.Join(dir, "tmux.sock"))
		if err != nil {
			return nil, err
		}
		resolved = chosen
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

// Kill stops the whole server and every agent in it, then waits up to one
// client timeout for the socket to stop accepting connections. Launches share
// the start lock so a new server cannot appear during that wait. Killing a
// server that is not running is not an error.
func (s *Server) Kill() error {
	return cachefile.WithExclusive(filepath.Join(s.dir, "server-start"), func() error {
		_, err := s.run([]string{"kill-server"})
		if err != nil && !errors.Is(err, ErrServerNotRunning) {
			return err
		}
		deadline := time.Now().Add(s.timeout)
		for Listening(s.socket) {
			if !time.Now().Before(deadline) {
				return fmt.Errorf("tmux: waiting for server at %s to stop: %w", s.socket, context.DeadlineExceeded)
			}
			time.Sleep(10 * time.Millisecond)
		}
		return nil
	})
}

// starting runs fn, a client invocation that may start the server, and
// records the executable beside the socket when it did. Launches hold a lock
// beside the socket for this, so of two that race to start a server, the one
// that found none listening under the lock is the one whose client started
// it, and the record names the tmux that server runs. A record that cannot be
// written does not undo a launch that worked: the server is then reported as
// running an unknown tmux, which is the truth.
func (s *Server) starting(fn func() error) error {
	return cachefile.WithExclusive(filepath.Join(s.dir, "server-start"), func() error {
		running := Listening(s.socket)
		err := fn()
		if !running && Listening(s.socket) && filepath.IsAbs(s.binary) {
			_ = recordBinary(s.dir, s.binary)
		}
		return err
	})
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
		strings.Contains(stderr, "server exited unexpectedly") ||
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
	return s.runChainInput(cmds, nil)
}

// runChainInput is runChain with stdin given to the client, for a command
// that reads from it. What is read that way appears in no argument list and
// so in no error.
//
// Every such client is started with -u. tmux rewrites what it prints to a
// client it does not take for UTF-8 (control characters and multi-byte
// sequences become '_'), and it decides that from the caller's locale, so
// without the flag fieldSep and any title beside it are lost wherever LANG is
// unset or C: launchd, cron, a minimal shell. The flag is the client's alone;
// the environment, and so what a pane inherits, is left as the caller had it.
func (s *Server) runChainInput(cmds [][]string, stdin []byte) (string, error) {
	args := []string{"-u", "-S", s.socket, "-f", s.config}
	var shown []string
	for i, cmd := range cmds {
		if i > 0 {
			args = append(args, ";")
			shown = append(shown, ";")
		}
		for _, a := range cmd {
			args = append(args, escapeArg(a))
		}
		shown = append(shown, redactArgs(cmd)...)
	}
	// The timeout covers the wait for a client slot as well as the client
	// itself, so a caller never waits longer than it would have for a server
	// that did not answer.
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	release, err := clients.acquire(ctx)
	if err != nil {
		return "", &CommandError{Args: shown, Err: fmt.Errorf("timed out after %s: %w", s.timeout, err)}
	}
	c := exec.CommandContext(ctx, s.binary, args...)
	c.Env = clientEnv(os.Environ())
	c.Dir = s.dir
	c.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	}
	err, timedOut := clientOutcome(c.Run(), ctx.Err(), s.timeout)
	release(err != nil, timedOut)
	if err != nil {
		return stdout.String(), &CommandError{Args: shown, Stderr: stderr.String(), Err: err}
	}
	return stdout.String(), nil
}

// clientOutcome is how a client that ran ended: runErr is what running it
// returned and ctxErr the state of its deadline afterwards. An expired
// deadline is a timeout only for a client that failed. One that succeeded
// right at the deadline (the wait for a slot counts against the same budget)
// did what it was asked, and reporting it as failed would have a caller undo
// or distrust a change the server made: a switch that moved, a session that
// launched, a send that was written.
func clientOutcome(runErr, ctxErr error, timeout time.Duration) (err error, timedOut bool) {
	if runErr == nil {
		return nil, false
	}
	if ctxErr != nil {
		return fmt.Errorf("timed out after %s: %w", timeout, ctxErr), true
	}
	return runErr, false
}

// redactedArg stands in for an argument a CommandError must not show.
const redactedArg = "<redacted>"

// redactArgs is cmd as a CommandError shows it. A launch environment carries
// profile secrets, so only its keys are named; the pane metadata and the
// program's argv after "--" carry the initial prompt and whatever the harness
// was given, so none of it is shown at all.
func redactArgs(cmd []string) []string {
	shown := make([]string, 0, len(cmd))
	argv := false
	for j, a := range cmd {
		switch {
		case argv:
			a = redactedArg
		case j > 0 && a == "--":
			argv = true
		case j > 0 && cmd[j-1] == "-e":
			key, _, _ := strings.Cut(a, "=")
			a = key + "=" + redactedArg
		case j > 0 && cmd[j-1] == varsOption:
			a = redactedArg
		}
		shown = append(shown, a)
	}
	return shown
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
	case "TMUX", "TMUX_PANE", "WINDOWID", "CLAUDECODE", runtimesession.EnvVar, WorkspaceHostEnv, "AMX_WORKSPACE_CONFIRMATION_FILE":
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
