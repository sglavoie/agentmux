package kitty

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	ccerrors "github.com/sglavoie/agentmux/internal/errors"
)

// KittyClient is the interface for all Kitty remote-control operations.
type KittyClient interface {
	// ListWindows returns all OS windows from `kitten @ ls`.
	ListWindows() ([]OSWindow, error)
	// LaunchTab creates a new tab with the given options.
	LaunchTab(opts LaunchOpts) error
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
	Location  string            // "first", "after", or "" for default
	Env       map[string]string
	UserVars  map[string]string
	Cmdline   []string
}

// SubprocessClient implements KittyClient via kitten @ subprocess calls.
type SubprocessClient struct {
	runner CommandRunner
	socket string // from KITTY_LISTEN_ON
}

// NewClient creates a SubprocessClient using KITTY_LISTEN_ON for socket discovery.
func NewClient(runner CommandRunner) *SubprocessClient {
	return &SubprocessClient{
		runner: runner,
		socket: os.Getenv("KITTY_LISTEN_ON"),
	}
}

// NewClientWithSocket creates a SubprocessClient with an explicit socket address.
// Pass an empty string to disable socket (useful in tests).
func NewClientWithSocket(runner CommandRunner, socket string) *SubprocessClient {
	return &SubprocessClient{runner: runner, socket: socket}
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

func (c *SubprocessClient) ListWindows() ([]OSWindow, error) {
	data, err := c.kitten("ls")
	if err != nil {
		return nil, err
	}
	var windows []OSWindow
	if err := json.Unmarshal(data, &windows); err != nil {
		return nil, fmt.Errorf("parsing kitten @ ls output: %w", err)
	}
	return windows, nil
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
	for k, v := range opts.Env {
		args = append(args, "--env="+k+"="+v)
	}
	for k, v := range opts.UserVars {
		args = append(args, "--var="+k+"="+v)
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
	action := "move_tab_forward"
	if delta < 0 {
		action = "move_tab_backward"
		delta = -delta
	}
	for i := 0; i < delta; i++ {
		if _, err := c.kitten("action", action); err != nil {
			return err
		}
	}
	return nil
}

func (c *SubprocessClient) SetTabColor(matchExpr, activeBg, inactiveBg, activeFg, inactiveFg string) error {
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
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := []string{"set-user-vars", "--match=" + matchExpr}
	for _, k := range keys {
		args = append(args, k+"="+vars[k])
	}
	_, err := c.kitten(args...)
	return err
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
	data, err := c.kitten("get-text", "--extent="+extent, "--match="+matchExpr)
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
