// Package dashboard holds the interactive surfaces `amx` opens inside Kitty:
// the live-session dashboard itself, which is what the bare `amx` invocation
// runs, the live session overview behind `amx overview` (F2), and the three
// customization overlays (color picker, harness icon picker, keybinding
// reference). `amx open-dashboard` is the dashboard's entry point for a caller
// that wants it brought forward rather than drawn in its own terminal.
//
// What makes them one family is not that they all draw something. It is that
// each of them is a singleton in a Kitty session: before launching, every one
// asks Kitty whether its surface is already open somewhere and, if it is,
// focuses that tab and window instead of opening a second copy. The dashboard
// additionally hands the already-running instance the tab the caller came from,
// so focusing an existing dashboard still selects the right row; the overview
// deliberately hands it nothing, because it shows every session at once and a
// repeat invocation must not disturb the selection or page the operator left.
// The overview is also the one surface here that REPORTS a Kitty failure rather
// than degrading to "nothing is open": see focusExistingOverview.
//
// There is deliberately no `dashboard` subcommand. The dashboard is the root
// command's default execution path, so this package exposes Run for the root to
// call rather than a *cobra.Command of its own.
package dashboard

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui"
)

// Run performs the workflow behind a bare `amx`: focus an already-running
// dashboard if there is one, and otherwise start the Bubble Tea program in this
// window. sourceWindowID is the Kitty window whose tab should be selected
// initially; a non-positive value falls back to the ambient KITTY_WINDOW_ID.
func Run(sourceWindowID int) error {
	cfg, err := cli.LoadRunnableConfig()
	if err != nil {
		return err
	}
	client := cli.NewKittyClient()
	if sourceWindowID <= 0 {
		sourceWindowID = cli.WindowIDFromEnv()
	}
	if focusExistingDashboard(client, sourceWindowID) {
		return nil
	}
	return tui.RunFromWindow(cfg, client, sourceWindowID)
}

func focusExistingDashboard(client kitty.KittyClient, sourceWindowID int) bool {
	osWindows, err := client.ListWindows()
	if err != nil {
		return false
	}
	_, found, err := focusDashboardIn(client, osWindows, sourceWindowID)
	if !found || err != nil {
		return false
	}
	fmt.Fprintln(os.Stderr, "Agent Mux dashboard already running — focused existing tab.")
	return true
}

// focusDashboardIn is the singleton rule itself, over a window tree the caller
// already listed: find a running dashboard other than the caller's own window,
// focus its tab, hand it the tab sourceWindowID sits in so it selects that
// row, and focus its window. It reports where the dashboard is, whether one was
// found, and a failure to focus its tab — after which the dashboard exists but
// was not brought forward. Handing over the selection and focusing the window
// are best effort, as they always were.
func focusDashboardIn(client kitty.KittyClient, osWindows []kitty.OSWindow, sourceWindowID int) (*kitty.DashboardLocation, bool, error) {
	loc, found := kitty.FindDashboardWindow(osWindows, cli.WindowIDFromEnv())
	if !found {
		return nil, false, nil
	}
	var sourceTabID int
	if sourceWindowID > 0 {
		if sourceTab, _, ok := kitty.FindTabByWindowID(osWindows, sourceWindowID); ok {
			sourceTabID = sourceTab.ID
		}
	}
	if err := client.FocusTab(kitty.MatchTabByID(loc.TabID)); err != nil {
		return loc, true, fmt.Errorf("focus existing dashboard tab %d: %w", loc.TabID, err)
	}
	if sourceTabID > 0 {
		request := strconv.Itoa(sourceTabID) + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)
		_ = client.SetUserVars(kitty.MatchWindowByID(loc.WindowID), map[string]string{
			kitty.DashboardSelectionVar: request,
		})
	}
	_ = client.FocusWindow(kitty.MatchWindowByID(loc.WindowID))
	return loc, true, nil
}
