package tmux

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cachefile"
	"github.com/sglavoie/agentmux/internal/kittykeys"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// Backend adapts the private tmux server to backend.Backend. A group is a
// tmux session and a pane is its one agent pane. AgentMux-owned strings (the
// group title and the pane metadata) live in tmux user options, base64url
// encoded so no value can collide with tmux's format or output syntax.
type Backend struct {
	server *Server
}

var _ backend.TargetLauncher = (*Backend)(nil)

// NewBackend wraps server.
func NewBackend(server *Server) *Backend {
	return &Backend{server: server}
}

// Server returns the server this backend drives.
func (b *Backend) Server() *Server { return b.server }

const (
	// titleOption is the session option holding the group title.
	titleOption = "@amx_title"
	// varsOption is the pane option holding the metadata map as JSON.
	varsOption = "@amx_vars"
	// fieldSep separates fields of one formatted tmux output line. tmux
	// writes control characters in a -F format through unchanged to a UTF-8
	// client, which runChainInput makes every command client.
	fieldSep = "\x1f"
)

// Kind reports the tmux backend kind.
func (b *Backend) Kind() runtimesession.BackendKind { return runtimesession.BackendTmux }

// Capabilities reports the core operations plus titles and input. Focus,
// colors, and ordering belong to whatever view attaches to the session, not
// to the server, so they are unsupported here.
func (b *Backend) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		backend.OpDiscover: true,
		backend.OpLaunch:   true,
		backend.OpClose:    true,
		backend.OpCapture:  true,
		backend.OpMetadata: true,
		backend.OpTitle:    true,
		backend.OpInput:    true,
	}
}

var discoverFields = []string{
	"#{session_id}",
	"#{session_name}",
	"#{session_attached}",
	"#{pane_id}",
	"#{pane_pid}",
	"#{pane_active}",
	"#{pane_dead}",
	"#{pane_dead_status}",
	"#{pane_dead_signal}",
	"#{" + titleOption + "}",
	"#{" + varsOption + "}",
	// Free text last. tmux keeps control characters out of a pane title, but
	// a working directory can hold anything, fieldSep and newlines included,
	// so it is last of all and cannot shift another field.
	"#{pane_title}",
	"#{pane_current_path}",
}

// Discover lists every pane on the server. A server that is not running hosts
// nothing, so it reports no panes rather than an error; any other failure is
// an error so a caller never mistakes an unreachable server for an empty one.
// A line that is not a pane is skipped, so one odd pane never hides the rest.
func (b *Backend) Discover() ([]backend.Pane, error) {
	out, err := b.server.run([]string{"list-panes", "-a", "-F", strings.Join(discoverFields, fieldSep)})
	if errors.Is(err, ErrServerNotRunning) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var panes []backend.Pane
	for _, record := range records(out, isPaneLine, func(string) bool { return true }) {
		if pane, ok := parsePane(record); ok {
			panes = append(panes, pane)
		}
	}
	return panes, nil
}

// records splits formatted output into one record per item. A working
// directory is the last field of its line and may itself hold a newline, so
// a line that starts no record continues the one before it when that record
// may be continued, and is dropped otherwise.
func records(out string, starts, continues func(string) bool) []string {
	var recs []string
	open := false
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		switch {
		case starts(line):
			recs = append(recs, line)
			open = continues(line)
		case open:
			recs[len(recs)-1] += "\n" + line
		}
	}
	return recs
}

// isPaneLine reports whether line begins a discoverFields record.
func isPaneLine(line string) bool {
	f := strings.SplitN(line, fieldSep, len(discoverFields))
	return len(f) == len(discoverFields) && canonicalID(f[0], '$') && canonicalID(f[3], '%')
}

func parsePane(line string) (backend.Pane, bool) {
	if !isPaneLine(line) {
		return backend.Pane{}, false
	}
	f := strings.SplitN(line, fieldSep, len(discoverFields))
	p := backend.Pane{
		Group:       backend.GroupID(f[0]),
		GroupActive: f[2] != "" && f[2] != "0",
		Ref:         backend.PaneID(f[3]),
		Active:      f[5] == "1",
		CWD:         f[12],
		Title:       f[11],
		GroupTitle:  f[1],
	}
	if title, err := decodeString(f[9]); err == nil && f[9] != "" {
		p.GroupTitle = title
	}
	if f[10] != "" {
		if vars, err := decodeVars(f[10]); err == nil {
			p.Vars = vars
		}
	}
	if f[6] == "1" {
		exit := &backend.Exit{Status: -1, Signal: f[8]}
		if code, err := strconv.Atoi(f[7]); err == nil && f[8] == "" {
			exit.Status = code
		}
		p.Exit = exit
	} else if pid, err := strconv.Atoi(f[4]); err == nil && pid > 0 {
		p.Foreground = []backend.Process{{PID: pid, CWD: f[12]}}
	}
	return p, true
}

// Launched is what one launch created.
type Launched = backend.Launched

// Launch starts spec in a new session.
func (b *Backend) Launch(spec backend.LaunchSpec) error {
	_, err := b.LaunchTarget(spec)
	return err
}

// LaunchTarget starts spec in a new session holding one pane and reports the
// exact target, so a runtime session can be committed without searching for
// it. The session, its title, and its metadata are created by one client
// invocation, which the server runs without interleaving another client, so
// the pane is never discoverable without its metadata. If that invocation
// fails or times out after the server created the session, the session is
// killed before the error is returned: a failed launch leaves nothing behind.
func (b *Backend) LaunchTarget(spec backend.LaunchSpec) (Launched, error) {
	if err := validateLaunch(spec); err != nil {
		return Launched{}, err
	}
	name, err := newSessionName()
	if err != nil {
		return Launched{}, fmt.Errorf("tmux: naming session: %w", err)
	}
	newSession := []string{"new-session", "-d", "-s", name, "-P", "-F",
		strings.Join([]string{"#{session_id}", "#{pane_id}", "#{pane_pid}", "#{pid}", "#{start_time}"}, fieldSep)}
	if spec.Directory != "" {
		// tmux expands -c as a format: an unescaped '#' would make
		// `C#Tools` a title expansion and start the agent somewhere else.
		newSession = append(newSession, "-c", strings.ReplaceAll(spec.Directory, "#", "##"))
	}
	for _, k := range slices.Sorted(maps.Keys(spec.Env)) {
		newSession = append(newSession, "-e", k+"="+spec.Env[k])
	}
	newSession = append(newSession, "--")
	newSession = append(newSession, paneArgv(spec.Argv)...)
	target := "=" + name + ":"
	cmds := [][]string{
		newSession,
		{"set-option", "-t", target, titleOption, encodeString(spec.Title)},
	}
	if vars := encodeVars(spec.Vars); vars != "" {
		cmds = append(cmds, []string{"set-option", "-p", "-t", target, varsOption, vars})
	}
	var out string
	err = b.server.starting(func() error {
		var err error
		out, err = b.server.runChain(cmds)
		return err
	})
	var l Launched
	if err == nil {
		l, err = b.parseLaunched(out)
	}
	if err != nil {
		if cleanupErr := b.abandon(name); cleanupErr != nil {
			return Launched{}, fmt.Errorf("tmux: launch failed: %w; removing partial session %s also failed: %w", err, name, cleanupErr)
		}
		return Launched{}, fmt.Errorf("tmux: launch failed (partial session %s removed): %w", name, err)
	}
	return l, nil
}

func (b *Backend) parseLaunched(out string) (Launched, error) {
	line, _, _ := strings.Cut(out, "\n")
	f := strings.Split(line, fieldSep)
	if len(f) != 5 {
		return Launched{}, fmt.Errorf("tmux: unexpected launch output %q", out)
	}
	group, pane := backend.GroupID(f[0]), backend.PaneID(f[1])
	pid, err := strconv.Atoi(f[2])
	if err != nil || groupID(group) != nil || paneOnly(pane) != nil {
		return Launched{}, fmt.Errorf("tmux: unexpected launch output %q", out)
	}
	return Launched{Group: group, Pane: pane, PID: pid, Instance: b.server.instance(f[3], f[4])}, nil
}

// abandon kills a session a failed launch may have created. A session that
// does not exist, or a server that is gone, is already clean.
func (b *Backend) abandon(name string) error {
	_, err := b.server.run([]string{"kill-session", "-t", "=" + name})
	if err == nil || errors.Is(err, ErrServerNotRunning) {
		return nil
	}
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) && strings.Contains(cmdErr.Stderr, "can't find session") {
		return nil
	}
	return err
}

// paneArgv makes tmux execute argv directly. tmux runs a single argument
// through the shell as a command string, so a lone program is wrapped in
// `sh -c 'exec "$0"'`, which execs it unchanged.
func paneArgv(argv []string) []string {
	if len(argv) == 1 {
		return []string{"/bin/sh", "-c", `exec "$0"`, argv[0]}
	}
	return argv
}

func validateLaunch(spec backend.LaunchSpec) error {
	if len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return errors.New("tmux: launch needs a program")
	}
	switch spec.Placement {
	case backend.PlacementDefault, backend.PlacementFirst, backend.PlacementBefore, backend.PlacementAfter:
	default:
		return fmt.Errorf("tmux: unknown placement %q", spec.Placement)
	}
	if spec.Directory != "" {
		// tmux silently falls back to another directory, so check first.
		if !filepath.IsAbs(spec.Directory) {
			return fmt.Errorf("tmux: launch directory %q must be absolute", spec.Directory)
		}
		info, err := os.Stat(spec.Directory)
		if err != nil {
			return fmt.Errorf("tmux: launch directory: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("tmux: launch directory %s is not a directory", spec.Directory)
		}
	}
	for k, v := range spec.Env {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
			return fmt.Errorf("tmux: invalid environment variable %q", k)
		}
	}
	for _, a := range spec.Argv {
		if strings.ContainsRune(a, 0) {
			return errors.New("tmux: argument contains NUL")
		}
	}
	return nil
}

// Close kills the session a group ref names or a pane ref belongs to. The
// agent's process group receives SIGHUP.
func (b *Backend) Close(ref backend.Ref) error {
	if err := validRef(ref); err != nil {
		return err
	}
	_, err := b.server.run([]string{"kill-session", "-t", ref.ID})
	return err
}

// Capture reads a pane's visible screen, or its screen plus history. An
// exited pane can still be captured until it is closed.
func (b *Backend) Capture(ref backend.Ref, extent backend.Extent) (string, error) {
	if err := paneOnly(ref); err != nil {
		return "", err
	}
	args := []string{"capture-pane", "-p", "-t", ref.ID}
	switch extent {
	case backend.ExtentScreen:
	case backend.ExtentAll:
		args = append(args, "-S", "-", "-E", "-")
	default:
		return "", fmt.Errorf("tmux: unknown capture extent %q", extent)
	}
	return b.server.run(args)
}

// observeFields is the pane state Observe reads in the same client invocation
// as the capture, so the screen and the facts that qualify it are one moment.
var observeFields = []string{
	"#{pane_id}",
	"#{pid}",
	"#{start_time}",
	"#{pane_width}",
	"#{pane_height}",
	"#{alternate_on}",
	"#{pane_dead}",
	// Last, so whatever a program put in its title cannot shift a field.
	"#{pane_title}",
}

// Observe reads a pane's visible screen with the server generation, pane size,
// screen mode, exit state, and title that were current for that read. capture-pane
// reads whichever screen the pane is showing, so a program on the alternate
// screen is captured as it appears.
func (b *Backend) Observe(ref backend.Ref) (backend.Screen, error) {
	if err := paneOnly(ref); err != nil {
		return backend.Screen{}, err
	}
	out, err := b.server.runChain([][]string{
		{"display-message", "-p", "-t", ref.ID, strings.Join(observeFields, fieldSep)},
		{"capture-pane", "-p", "-t", ref.ID},
	})
	if err != nil {
		return backend.Screen{}, err
	}
	head, text, _ := strings.Cut(out, "\n")
	f := strings.SplitN(head, fieldSep, len(observeFields))
	// display-message answers for another pane when the target is gone.
	if len(f) != len(observeFields) || f[0] != ref.ID || f[1] == "" || f[2] == "" {
		return backend.Screen{}, fmt.Errorf("tmux: pane %s not observable: %q", ref.ID, head)
	}
	cols, _ := strconv.Atoi(f[3])
	rows, _ := strconv.Atoi(f[4])
	return backend.Screen{
		Instance:        b.server.instance(f[1], f[2]),
		Pane:            ref,
		Text:            text,
		Title:           f[7],
		Cols:            cols,
		Rows:            rows,
		AlternateScreen: f[5] == "1",
		Exited:          f[6] == "1",
		CapturedAt:      time.Now(),
	}, nil
}

// SetMetadata merges vars into the pane's metadata; an empty value removes a
// key. The read-modify-write is serialized across processes by a lock beside
// the socket, so two writers never lose each other's keys.
func (b *Backend) SetMetadata(ref backend.Ref, vars map[string]string) error {
	if err := paneOnly(ref); err != nil {
		return err
	}
	if len(vars) == 0 {
		return nil
	}
	return cachefile.WithExclusive(filepath.Join(b.server.dir, "metadata"), func() error {
		out, err := b.server.run([]string{"display-message", "-p", "-t", ref.ID,
			"#{pane_id}" + fieldSep + "#{" + varsOption + "}"})
		if err != nil {
			return err
		}
		id, encoded, _ := strings.Cut(strings.TrimSuffix(out, "\n"), fieldSep)
		if id != ref.ID {
			// display-message falls back to another pane for a missing one.
			return fmt.Errorf("tmux: pane %s not found", ref.ID)
		}
		current := map[string]string{}
		if encoded != "" {
			if current, err = decodeVars(encoded); err != nil {
				return fmt.Errorf("tmux: pane %s metadata: %w", ref.ID, err)
			}
		}
		maps.Copy(current, vars)
		if next := encodeVars(current); next != "" {
			_, err = b.server.run([]string{"set-option", "-p", "-t", ref.ID, varsOption, next})
		} else {
			_, err = b.server.run([]string{"set-option", "-p", "-u", "-t", ref.ID, varsOption})
		}
		return err
	})
}

// Instance identifies the running server; see Server.Instance.
func (b *Backend) Instance() (runtimesession.BackendInstance, error) { return b.server.Instance() }

// AttachView is what the terminal that attaches shows around the pane.
type AttachView struct {
	// StatusLine shows the session's one-line status (its rendered state and
	// title, on its color) under the pane. A terminal that lists sessions
	// itself, as the desktop workspace does, leaves it off.
	StatusLine bool
	// Amx is the amx executable of the terminal that attaches, recorded in
	// the server's environment for the keys bound in the configuration
	// (kittykeys.TmuxAmxEnv). Empty leaves what is there.
	Amx string
}

// AttachCommand is the interactive client that attaches a terminal to the
// session holding pane. It detaches every other client of that session, so
// attaching transfers interactive ownership (input and size) instead of
// sharing it; the agent itself is untouched. The caller connects the
// command's standard streams to a terminal and runs it.
//
// The configuration is sourced again in the same invocation, because a server
// started by an earlier build read one without the keys and mouse bindings
// this build's viewers rely on. Every line of it can be applied twice.
//
// The status line is set for the session in the same invocation. One terminal
// owns a session, so the line always follows the terminal attached last.
//
// The client is started with -u, as every other client is (runChainInput):
// the terminals it runs in are UTF-8, but the locale it inherits may not say
// so (an app launched by launchd has no LANG), and tmux would then draw every
// non-ASCII character of the agent's screen and the status line as '_'.
func (b *Backend) AttachCommand(pane backend.Ref, view AttachView) (*exec.Cmd, error) {
	if err := paneOnly(pane); err != nil {
		return nil, err
	}
	status := "off"
	if view.StatusLine {
		status = "on"
	}
	args := []string{"-u", "-S", b.server.socket, "-f", b.server.config, "source-file", b.server.config, ";"}
	if view.Amx != "" {
		if !filepath.IsAbs(view.Amx) || strings.ContainsRune(view.Amx, 0) {
			return nil, fmt.Errorf("tmux: amx executable %q must be an absolute path", view.Amx)
		}
		args = append(args, "set-environment", "-g", kittykeys.TmuxAmxEnv, escapeArg(view.Amx), ";")
	}
	args = append(args, "set-option", "-t", pane.ID, "status", status, ";", "attach-session", "-d", "-t", pane.ID)
	// #nosec G204 -- the binary was resolved at NewServer and the target is a canonical pane ID.
	c := exec.Command(b.server.binary, args...)
	c.Env = clientEnv(os.Environ())
	return c, nil
}

// Focus is unsupported: the server has no view of its own. Showing a session
// in a terminal that is already attached is SwitchClient, which names the
// terminal it moves.
func (b *Backend) Focus(ref backend.Ref) error {
	if err := validRef(ref); err != nil {
		return err
	}
	return b.unsupported(backend.OpFocus)
}

// SetTitle sets the title of the session a ref names or contains.
func (b *Backend) SetTitle(ref backend.Ref, title string) error {
	if err := validRef(ref); err != nil {
		return err
	}
	_, err := b.server.run([]string{"set-option", "-t", ref.ID, titleOption, encodeString(title)})
	return err
}

// SetColors is unsupported.
func (b *Backend) SetColors(ref backend.Ref, _ backend.Colors) error {
	if err := validRef(ref); err != nil {
		return err
	}
	return b.unsupported(backend.OpColors)
}

// Move is unsupported: sessions have no order.
func (b *Backend) Move(ref backend.Ref, _ int) error {
	if err := validRef(ref); err != nil {
		return err
	}
	return b.unsupported(backend.OpReorder)
}

func (b *Backend) unsupported(op backend.Operation) error {
	return &backend.UnsupportedError{Kind: runtimesession.BackendTmux, Operation: op}
}

// validRef accepts a session ID ($N) as a group and a pane ID (%N) as a pane,
// in the canonical form tmux reports, so a ref can never carry a session name
// or any other target syntax into a command.
func validRef(ref backend.Ref) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if ref.Scope == backend.ScopePane {
		return paneOnly(ref)
	}
	return groupID(ref)
}

func groupID(ref backend.Ref) error {
	if ref.Scope != backend.ScopeGroup || !canonicalID(ref.ID, '$') {
		return &backend.InvalidRefError{Ref: ref, Reason: "tmux: group id must be a session id ($N)"}
	}
	return nil
}

func paneOnly(ref backend.Ref) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if ref.Scope != backend.ScopePane || !canonicalID(ref.ID, '%') {
		return &backend.InvalidRefError{Ref: ref, Reason: "tmux: operation needs a pane id (%N)"}
	}
	return nil
}

func canonicalID(id string, sigil byte) bool {
	if len(id) < 2 || id[0] != sigil {
		return false
	}
	n, err := strconv.Atoi(id[1:])
	return err == nil && n >= 0 && strconv.Itoa(n) == id[1:]
}

func encodeString(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func decodeString(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return string(b), err
}

// encodeVars drops empty values and returns "" when nothing remains.
func encodeVars(vars map[string]string) string {
	kept := make(map[string]string, len(vars))
	for k, v := range vars {
		if v != "" {
			kept[k] = v
		}
	}
	if len(kept) == 0 {
		return ""
	}
	data, err := json.Marshal(kept)
	if err != nil {
		return ""
	}
	return encodeString(string(data))
}

func decodeVars(s string) (map[string]string, error) {
	raw, err := decodeString(s)
	if err != nil {
		return nil, err
	}
	var vars map[string]string
	if err := json.Unmarshal([]byte(raw), &vars); err != nil {
		return nil, err
	}
	return vars, nil
}
