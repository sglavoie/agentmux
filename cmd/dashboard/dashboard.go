// Package dashboard holds the interactive surfaces `amx` opens inside Kitty:
// the live-session dashboard itself, which is what the bare `amx` invocation
// runs, and the three customization overlays (color picker, harness icon
// picker, keybinding reference).
//
// What makes them one family is not that they all draw something. It is that
// each of them is a singleton in a Kitty session: before launching, every one
// asks Kitty whether its surface is already open somewhere and, if it is,
// focuses that tab and window instead of opening a second copy. The dashboard
// additionally hands the already-running instance the tab the caller came from,
// so focusing an existing dashboard still selects the right row.
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
	loc, found := kitty.FindDashboardWindow(osWindows, cli.WindowIDFromEnv())
	if !found {
		return false
	}
	var sourceTabID int
	if sourceWindowID > 0 {
		if sourceTab, _, ok := kitty.FindTabByWindowID(osWindows, sourceWindowID); ok {
			sourceTabID = sourceTab.ID
		}
	}
	if err := client.FocusTab(kitty.MatchTabByID(loc.TabID)); err != nil {
		return false
	}
	if sourceTabID > 0 {
		request := strconv.Itoa(sourceTabID) + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)
		_ = client.SetUserVars(kitty.MatchWindowByID(loc.WindowID), map[string]string{
			kitty.DashboardSelectionVar: request,
		})
	}
	_ = client.FocusWindow(kitty.MatchWindowByID(loc.WindowID))
	fmt.Fprintln(os.Stderr, "Agent Mux dashboard already running — focused existing tab.")
	return true
}
