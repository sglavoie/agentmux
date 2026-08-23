package tui

// This file is the harness's stand-in for Kitty. Reads answer from canned
// fixture data and every operation that would mutate a live terminal or move
// focus is recorded instead of executed, so a scenario can assert on what the
// model attempted without a subprocess ever running.

import "github.com/sglavoie/agentmux/internal/kitty"

// UITestKittyCall records one Kitty operation the harness client received.
// Reads (ListWindows/GetText) are not recorded; every call that would mutate a
// live terminal or move focus is captured here instead of being executed, so
// scenarios can assert on the side effects the model attempted.
type UITestKittyCall struct {
	Op    string            // operation name, e.g. "LaunchTab", "CloseTab", "FocusTab"
	Match string            // match expression, when the op takes one
	Title string            // for SetTabTitle / launch titles
	Vars  map[string]string // for SetUserVars
	Opts  *kitty.LaunchOpts // for LaunchTab / LaunchOverlay
	Delta int               // for MoveTab
}

// UITestKittyClient is a kitty.KittyClient that never talks to a live Kitty
// instance. ListWindows and GetText return canned fixture data; every mutating
// or focus operation is appended to Calls. It is the client every harness run
// uses so a scenario can drive the real model without a subprocess ever
// executing.
type UITestKittyClient struct {
	windows []kitty.OSWindow
	text    string
	// Calls is the ordered log of trapped mutating/focus/overlay operations.
	Calls []UITestKittyCall
}

// compile-time assertion that the no-op client satisfies the full interface.
var _ kitty.KittyClient = (*UITestKittyClient)(nil)

// NewUITestKittyClient builds a recording client that reports the given windows
// from ListWindows and traps every mutating call.
func NewUITestKittyClient(windows []kitty.OSWindow) *UITestKittyClient {
	return &UITestKittyClient{windows: windows}
}

func (c *UITestKittyClient) ListWindows() ([]kitty.OSWindow, error) { return c.windows, nil }

func (c *UITestKittyClient) LaunchTab(opts kitty.LaunchOpts) error {
	o := opts
	c.Calls = append(c.Calls, UITestKittyCall{Op: "LaunchTab", Title: opts.Title, Opts: &o})
	return nil
}

func (c *UITestKittyClient) LaunchOverlay(opts kitty.LaunchOpts) error {
	o := opts
	c.Calls = append(c.Calls, UITestKittyCall{Op: "LaunchOverlay", Title: opts.Title, Opts: &o})
	return nil
}

func (c *UITestKittyClient) CloseTab(matchExpr string) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "CloseTab", Match: matchExpr})
	return nil
}

func (c *UITestKittyClient) MoveTab(matchExpr string, delta int) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "MoveTab", Match: matchExpr, Delta: delta})
	return nil
}

func (c *UITestKittyClient) SetTabColor(matchExpr, _, _, _, _ string) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "SetTabColor", Match: matchExpr})
	return nil
}

func (c *UITestKittyClient) SetTabTitle(matchExpr, title string) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "SetTabTitle", Match: matchExpr, Title: title})
	return nil
}

func (c *UITestKittyClient) SetUserVars(matchExpr string, vars map[string]string) error {
	cp := make(map[string]string, len(vars))
	for k, v := range vars {
		cp[k] = v
	}
	c.Calls = append(c.Calls, UITestKittyCall{Op: "SetUserVars", Match: matchExpr, Vars: cp})
	return nil
}

func (c *UITestKittyClient) FocusTab(matchExpr string) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "FocusTab", Match: matchExpr})
	return nil
}

func (c *UITestKittyClient) FocusWindow(matchExpr string) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "FocusWindow", Match: matchExpr})
	return nil
}

func (c *UITestKittyClient) GetText(_, _ string) (string, error) { return c.text, nil }

func (c *UITestKittyClient) LaunchOverlayPager(sourceMatchExpr string) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "LaunchOverlayPager", Match: sourceMatchExpr})
	return nil
}

// CallsFor returns the recorded calls whose Op equals op, in order.
func (c *UITestKittyClient) CallsFor(op string) []UITestKittyCall {
	var out []UITestKittyCall
	for _, call := range c.Calls {
		if call.Op == op {
			out = append(out, call)
		}
	}
	return out
}
