package worktree

import "github.com/sglavoie/agentmux/internal/kitty"

// worktreeCommandKitty is this package's copy of the recording Kitty client the
// session command tests also use. The worktree reopen path launches and focuses
// tabs, so the fake has to record both.
type worktreeCommandKitty struct {
	windows  []kitty.OSWindow
	launched []kitty.LaunchOpts
	focuses  []string
}

func (c *worktreeCommandKitty) ListWindows() ([]kitty.OSWindow, error) { return c.windows, nil }
func (c *worktreeCommandKitty) LaunchTab(opts kitty.LaunchOpts) error {
	c.launched = append(c.launched, opts)
	return nil
}
func (*worktreeCommandKitty) LaunchOverlay(kitty.LaunchOpts) error                     { return nil }
func (*worktreeCommandKitty) CloseTab(string) error                                    { return nil }
func (*worktreeCommandKitty) MoveTab(string, int) error                                { return nil }
func (*worktreeCommandKitty) SetTabColor(string, string, string, string, string) error { return nil }
func (*worktreeCommandKitty) SetTabTitle(string, string) error                         { return nil }
func (*worktreeCommandKitty) SetUserVars(string, map[string]string) error              { return nil }
func (c *worktreeCommandKitty) FocusTab(match string) error {
	c.focuses = append(c.focuses, match)
	return nil
}
func (*worktreeCommandKitty) FocusWindow(string) error               { return nil }
func (*worktreeCommandKitty) GetText(string, string) (string, error) { return "", nil }
func (*worktreeCommandKitty) LaunchOverlayPager(string) error        { return nil }
