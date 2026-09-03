package kitty

import (
	"encoding/json"
	"os"
	"sort"
	"time"

	ccerrors "github.com/sglavoie/agentmux/internal/errors"
)

// KittyClient is the interface for all Kitty remote-control operations.
type KittyClient interface {
	// ListWindows returns all OS windows from `kitten @ ls`.
	ListWindows() ([]OSWindow, error)
	// LaunchTab creates a new tab with the given options.
	LaunchTab(opts LaunchOpts) error
	// LaunchOverlay opens a Kitty overlay running the given command argv.
	LaunchOverlay(opts LaunchOpts) error
	// CloseTab closes the tab matched by matchExpr.
	CloseTab(matchExpr string) error
	// MoveTab focuses the tab matched by matchExpr and slides it |delta|
	// positions in the tab bar. Positive delta moves the tab forward (toward
	// the end of the bar); negative moves it backward. Tab IDs are preserved.
	// A delta of zero is a no-op.
	MoveTab(matchExpr string, delta int) error
	// SetTabColor sets foreground/background colors on a matched tab.
	SetTabColor(matchExpr, activeBg, inactiveBg, activeFg, inactiveFg string) error
	// SetTabTitle sets the title of a matched tab.
	SetTabTitle(matchExpr, title string) error
	// SetUserVars writes user variables onto an existing window matched by
	// matchExpr (use MatchWindowByID). An empty vars map is a no-op.
	SetUserVars(matchExpr string, vars map[string]string) error
	// FocusTab focuses the matched tab.
	FocusTab(matchExpr string) error
	// FocusWindow focuses the matched window.
	FocusWindow(matchExpr string) error
	// GetText captures terminal text from a matched window.
	GetText(matchExpr, extent string) (string, error)
	// LaunchOverlayPager opens less as a Kitty overlay with the source window's scrollback.
	LaunchOverlayPager(sourceMatchExpr string) error
}

// LaunchOpts holds options for kitten @ launch.
type LaunchOpts struct {
	Title     string
	Directory string
	Location  string // "first", "after", or "" for default
	Hold      bool
	Env       map[string]string
	UserVars  map[string]string
	Cmdline   []string
}

// SubprocessClient implements KittyClient via kitten @ subprocess calls.
type SubprocessClient struct {
	runner CommandRunner
	socket string // from KITTY_LISTEN_ON
	// lists collapses concurrent ListWindows reads that target the same
	// socket. It is shared by every WithSocket clone, which is what makes the
	// per-socket coalescing hold across a process rather than per client value.
	lists *listCoordinator
}

// NewClient creates a SubprocessClient using KITTY_LISTEN_ON for socket discovery.
func NewClient(runner CommandRunner) *SubprocessClient {
	return &SubprocessClient{
		runner: runner,
		socket: os.Getenv("KITTY_LISTEN_ON"),
		lists:  newListCoordinator(),
	}
}

// NewClientWithSocket creates a SubprocessClient with an explicit socket address.
// Pass an empty string to disable socket (useful in tests).
func NewClientWithSocket(runner CommandRunner, socket string) *SubprocessClient {
	return &SubprocessClient{runner: runner, socket: socket, lists: newListCoordinator()}
}

// WithSocket returns a shallow copy that targets a specific Kitty remote
// control socket. The runner and the list coordinator are shared; the socket
// selection is per copy. Sharing the coordinator is deliberate: the daemon
// scopes a clone per window, and one underlying `kitten @ ls` per socket is
// only achievable if those clones coordinate with each other.
func (c *SubprocessClient) WithSocket(socket string) *SubprocessClient {
	cp := *c
	cp.socket = socket
	return &cp
}

// SetListObserver installs a fail-open observer for list-health transitions.
// The observer is shared with every WithSocket clone and is invoked outside
// the coordinator's locks. Pass nil to remove it.
func (c *SubprocessClient) SetListObserver(observer ListObserver) {
	if c.lists == nil {
		return
	}
	c.lists.setObserver(observer)
}

// ListStatus returns the retained coordination accounting for this client's
// socket, reporting false when the socket has not been read yet or its idle
// key has been evicted.
func (c *SubprocessClient) ListStatus() (ListStatus, bool) {
	if c.lists == nil {
		return ListStatus{}, false
	}
	return c.lists.status(c.socket)
}

// ListStatuses returns the retained accounting for every socket key this
// client's coordinator still holds, ordered by socket.
func (c *SubprocessClient) ListStatuses() []ListStatus {
	if c.lists == nil {
		return nil
	}
	return c.lists.statuses()
}

func (c *SubprocessClient) kitten(args ...string) ([]byte, error) {
	cmdArgs := []string{"@"}
	if c.socket != "" {
		cmdArgs = append(cmdArgs, "--to", c.socket)
	}
	cmdArgs = append(cmdArgs, args...)
	stdout, stderr, err := c.runner.Run("kitten", cmdArgs...)
	if err != nil {
		return nil, &ccerrors.KittyError{
			Command: append([]string{"kitten"}, cmdArgs...),
			Output:  string(stderr),
			Err:     err,
		}
	}
	return stdout, nil
}

// ListWindows returns the Kitty topology. Concurrent callers targeting the
// same socket share one underlying subprocess and decode; each still receives
// an independent copy of the result.
func (c *SubprocessClient) ListWindows() ([]OSWindow, error) {
	capture, err := c.ListWindowsCapture(nil)
	return capture.Windows, err
}

// ListWindowsCapture reads the Kitty topology and returns the whole capture:
// the windows, the provenance sampled immediately before the underlying read
// started, and the instant that read completed.
//
// sample runs only in the caller that executes the underlying subprocess. A
// caller that coalesces onto a read already in flight receives the executing
// caller's provenance and completion time rather than sampling its own, which
// is what keeps the facts a caller binds to a topology older than the facts
// themselves. Use this instead of ListWindows whenever anything sampled around
// the read is published together with it.
func (c *SubprocessClient) ListWindowsCapture(sample func() any) (ListCapture, error) {
	run := func() ([]OSWindow, error) {
		data, err := c.kitten("ls")
		if err != nil {
			return nil, err
		}
		var windows []OSWindow
		if err := json.Unmarshal(data, &windows); err != nil {
			return nil, &listDecodeError{err: err}
		}
		return windows, nil
	}
	// A zero-value client never comes from the constructors, but falling back
	// to an uncoordinated read is cheaper than making every caller carry a nil
	// check. Nothing can join such a read, so this caller's own sample is
	// already the executor's.
	if c.lists == nil {
		var provenance any
		if sample != nil {
			provenance = sample()
		}
		windows, err := run()
		return ListCapture{
			Windows:     cloneOSWindows(windows),
			Provenance:  provenance,
			CompletedAt: time.Now(),
		}, err
	}
	return c.lists.capture(c.socket, sample, run)
}

func (c *SubprocessClient) LaunchTab(opts LaunchOpts) error {
	args := []string{"launch", "--type=tab", "--no-response"}
	if opts.Title != "" {
		args = append(args, "--tab-title="+opts.Title)
	}
	if opts.Directory != "" {
		args = append(args, "--cwd="+opts.Directory)
	}
	if opts.Location != "" {
		args = append(args, "--location="+opts.Location)
	}
	for _, k := range sortedKeys(opts.Env) {
		args = append(args, "--env="+k+"="+opts.Env[k])
	}
	for _, k := range sortedKeys(opts.UserVars) {
		args = append(args, "--var="+k+"="+opts.UserVars[k])
	}
	// Use a `--` separator so that a Cmdline whose first token begins with a
	// dash (e.g. an opaque shell wrapper passed in by callers) cannot be
	// misinterpreted by kitten as a flag belonging to `kitten @ launch`.
	if len(opts.Cmdline) > 0 {
		args = append(args, "--")
		args = append(args, opts.Cmdline...)
	}
	_, err := c.kitten(args...)
	return err
}

func (c *SubprocessClient) LaunchOverlay(opts LaunchOpts) error {
	args := []string{"launch", "--type=overlay", "--no-response"}
	if opts.Hold {
		args = append(args, "--hold")
	}
	if opts.Title != "" {
		args = append(args, "--title="+opts.Title)
	}
	if opts.Directory != "" {
		args = append(args, "--cwd="+opts.Directory)
	}
	for _, k := range sortedKeys(opts.Env) {
		args = append(args, "--env="+k+"="+opts.Env[k])
	}
	for _, k := range sortedKeys(opts.UserVars) {
		args = append(args, "--var="+k+"="+opts.UserVars[k])
	}
	// Use a `--` separator so command tokens that begin with a dash are
	// passed to the launched program rather than parsed as Kitty flags.
	if len(opts.Cmdline) > 0 {
		args = append(args, "--")
		args = append(args, opts.Cmdline...)
	}
	_, err := c.kitten(args...)
	return err
}

func (c *SubprocessClient) CloseTab(matchExpr string) error {
	_, err := c.kitten("close-tab", "--no-response", "--match="+matchExpr)
	return err
}

func (c *SubprocessClient) MoveTab(matchExpr string, delta int) error {
	if delta == 0 {
		return nil
	}
	if err := c.FocusTab(matchExpr); err != nil {
		return err
	}
	if delta > 0 {
		for i := 0; i < delta; i++ {
			if _, err := c.kitten("action", "move_tab_forward"); err != nil {
				return err
			}
		}
		return nil
	}
	for i := 0; i > delta; i-- {
		if _, err := c.kitten("action", "move_tab_backward"); err != nil {
			return err
		}
	}
	return nil
}

func (c *SubprocessClient) SetTabColor(matchExpr, activeBg, inactiveBg, activeFg, inactiveFg string) error {
	if activeBg == "" && inactiveBg == "" && activeFg == "" && inactiveFg == "" {
		return nil
	}
	args := []string{"set-tab-color", "--match=" + matchExpr}
	if activeBg != "" {
		args = append(args, "active_bg="+activeBg)
	}
	if inactiveBg != "" {
		args = append(args, "inactive_bg="+inactiveBg)
	}
	if activeFg != "" {
		args = append(args, "active_fg="+activeFg)
	}
	if inactiveFg != "" {
		args = append(args, "inactive_fg="+inactiveFg)
	}
	_, err := c.kitten(args...)
	return err
}

func (c *SubprocessClient) SetTabTitle(matchExpr, title string) error {
	// `--` ensures a title that happens to start with a dash (e.g. user typed
	// "-- scratch" or "-foo") is treated as the positional title argument
	// rather than being parsed as a kitten flag.
	_, err := c.kitten("set-tab-title", "--match="+matchExpr, "--", title)
	return err
}

func (c *SubprocessClient) SetUserVars(matchExpr string, vars map[string]string) error {
	if len(vars) == 0 {
		return nil
	}
	args := []string{"set-user-vars", "--match=" + matchExpr}
	for _, k := range sortedKeys(vars) {
		args = append(args, k+"="+vars[k])
	}
	_, err := c.kitten(args...)
	return err
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (c *SubprocessClient) FocusTab(matchExpr string) error {
	_, err := c.kitten("focus-tab", "--match="+matchExpr)
	return err
}

func (c *SubprocessClient) FocusWindow(matchExpr string) error {
	_, err := c.kitten("focus-window", "--match="+matchExpr)
	return err
}

func (c *SubprocessClient) GetText(matchExpr, extent string) (string, error) {
	args := []string{"get-text"}
	if extent != "" {
		args = append(args, "--extent="+extent)
	}
	args = append(args, "--match="+matchExpr)
	data, err := c.kitten(args...)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *SubprocessClient) LaunchOverlayPager(sourceMatchExpr string) error {
	_, err := c.kitten("launch", "--type=overlay",
		"--source-window="+sourceMatchExpr,
		"--stdin-source=@screen_scrollback",
		"less")
	return err
}
