// Package kittytest gives tests and the tag-gated TUI scenario harness a
// kitty.KittyClient that never talks to a live Kitty instance: reads answer
// from canned data, and every operation that would mutate a terminal or move
// focus is recorded instead of executed.
//
// Nothing in a release build imports it. It lives in its own package rather
// than beside the harness in tui so the dashboard's untagged unit tests and the
// `tuitest`-tagged harness share one recording client, and a release binary
// links neither.
package kittytest

import (
	"maps"
	"sync"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// Call records one Kitty operation the client received. Reads
// (ListWindows/GetText) are not recorded; every call that would mutate a live
// terminal or move focus is captured here instead of being executed, so a test
// can assert on the side effects the model attempted.
type Call struct {
	Op    string            // operation name, e.g. "LaunchTab", "CloseTab", "FocusTab"
	Match string            // match expression, when the op takes one
	Title string            // for SetTabTitle / launch titles
	Vars  map[string]string // for SetUserVars
	Opts  *kitty.LaunchOpts // for LaunchTab / LaunchOverlay
	Delta int               // for MoveTab
}

// Client is a kitty.KittyClient that never talks to a live Kitty instance.
// ListWindows and GetText return canned data; every mutating or focus
// operation is appended to Calls.
type Client struct {
	windows []kitty.OSWindow
	text    string
	// Calls is the ordered log of trapped mutating/focus/overlay operations.
	Calls []Call

	// The read seam. A preview read runs inside a tea.Cmd, on its own
	// goroutine, while the update loop goes on handling keys; readMu guards
	// this state because that is the arrangement the seam exists to exercise
	// rather than an incidental hazard. reads counts every GetText that reached
	// the client, which is how a test observes the one-read-at-a-time bound from
	// outside the model. readFn, when a test installs one, supplies the answer
	// and is what makes a read slow, make it fail, or park it until released.
	readMu sync.Mutex
	reads  int
	readFn func(matchExpr string) (string, error)
}

// compile-time assertion that the recording client satisfies the full interface.
var _ kitty.KittyClient = (*Client)(nil)

// NewClient builds a recording client that reports the given windows from
// ListWindows and traps every mutating call.
func NewClient(windows []kitty.OSWindow) *Client {
	return &Client{windows: windows}
}

// ListWindows reports the windows the client was built with.
func (c *Client) ListWindows() ([]kitty.OSWindow, error) { return c.windows, nil }

// LaunchTab records the launch.
func (c *Client) LaunchTab(opts kitty.LaunchOpts) error {
	o := opts
	c.Calls = append(c.Calls, Call{Op: "LaunchTab", Title: opts.Title, Opts: &o})
	return nil
}

// LaunchOverlay records the launch.
func (c *Client) LaunchOverlay(opts kitty.LaunchOpts) error {
	o := opts
	c.Calls = append(c.Calls, Call{Op: "LaunchOverlay", Title: opts.Title, Opts: &o})
	return nil
}

// CloseTab records the close.
func (c *Client) CloseTab(matchExpr string) error {
	c.Calls = append(c.Calls, Call{Op: "CloseTab", Match: matchExpr})
	return nil
}

// MoveTab records the move.
func (c *Client) MoveTab(matchExpr string, delta int) error {
	c.Calls = append(c.Calls, Call{Op: "MoveTab", Match: matchExpr, Delta: delta})
	return nil
}

// SetTabColor records the color change.
func (c *Client) SetTabColor(matchExpr, _, _, _, _ string) error {
	c.Calls = append(c.Calls, Call{Op: "SetTabColor", Match: matchExpr})
	return nil
}

// SetTabTitle records the title change.
func (c *Client) SetTabTitle(matchExpr, title string) error {
	c.Calls = append(c.Calls, Call{Op: "SetTabTitle", Match: matchExpr, Title: title})
	return nil
}

// SetUserVars records a copy of the variables.
func (c *Client) SetUserVars(matchExpr string, vars map[string]string) error {
	cp := make(map[string]string, len(vars))
	maps.Copy(cp, vars)
	c.Calls = append(c.Calls, Call{Op: "SetUserVars", Match: matchExpr, Vars: cp})
	return nil
}

// FocusTab records the focus.
func (c *Client) FocusTab(matchExpr string) error {
	c.Calls = append(c.Calls, Call{Op: "FocusTab", Match: matchExpr})
	return nil
}

// FocusWindow records the focus.
func (c *Client) FocusWindow(matchExpr string) error {
	c.Calls = append(c.Calls, Call{Op: "FocusWindow", Match: matchExpr})
	return nil
}

// SetText sets the text GetText serves when no read behavior is installed.
func (c *Client) SetText(text string) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	c.text = text
}

// GetText serves the canned text, or whatever behavior SetReadBehavior
// installed. The installed function is invoked with the lock released: a test
// parks a read here for as long as it likes, and the count and the behavior stay
// readable while it is parked.
func (c *Client) GetText(matchExpr, _ string) (string, error) {
	c.readMu.Lock()
	c.reads++
	fn, text := c.readFn, c.text
	c.readMu.Unlock()
	if fn == nil {
		return text, nil
	}
	return fn(matchExpr)
}

// SetReadBehavior installs the function GetText answers with. Passing nil
// restores the plain canned text.
func (c *Client) SetReadBehavior(fn func(matchExpr string) (string, error)) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	c.readFn = fn
}

// ReadCount reports how many reads have reached the client.
func (c *Client) ReadCount() int {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	return c.reads
}

// LaunchOverlayPager records the pager launch.
func (c *Client) LaunchOverlayPager(sourceMatchExpr string) error {
	c.Calls = append(c.Calls, Call{Op: "LaunchOverlayPager", Match: sourceMatchExpr})
	return nil
}

// CallsFor returns the recorded calls whose Op equals op, in order.
func (c *Client) CallsFor(op string) []Call {
	var out []Call
	for _, call := range c.Calls {
		if call.Op == op {
			out = append(out, call)
		}
	}
	return out
}
