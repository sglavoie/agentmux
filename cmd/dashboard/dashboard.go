// Package dashboard holds the interactive surfaces `amx` opens inside Kitty:
// the live-session dashboard itself, which is what the bare `amx` invocation
// runs, the live session overview behind `amx overview` and its F2 binding,
// and the three customization overlays (color picker, harness icon picker,
// keybinding reference).
//
// What makes them one family is not that they all draw something. It is that
// each of them is a singleton in a Kitty session: before launching, every one
// asks Kitty whether its surface is already open somewhere and, if it is,
// focuses that tab and window instead of opening a second copy. The dashboard
// additionally hands the already-running instance the tab the caller came
// from, so focusing an existing dashboard still selects the right row. The
// overview is where that rule is strictest. It deliberately hands a running
// instance nothing, because it shows every session at once and a repeat
// invocation must not disturb the selection or page the operator left. And it
// REPORTS a Kitty listing or focus failure as an error rather than degrading
// to "nothing is open" (see focusExistingOverview), because the degraded
// answer is a second full-screen surface polling Kitty beside the first.
//
// `amx open-dashboard` is the dashboard's out-of-terminal entry point, for a
// caller (the companion app, a script) that wants the dashboard brought
// forward rather than drawn where it runs. It applies the same singleton rule
// through the shared focusDashboardIn, and only when no dashboard is running
// launches one in a new tab, reporting {window_id, tab_id, opened}. It is
// strict the way the overview is — a failed listing or a failed focus is an
// error rather than a reason to open a second dashboard — and it validates the
// configuration before launching, because a tab that opens only to show a
// config error and close is worse than refusing.
//
// `amx workspace-view` (workspace_view.go, hidden) is what the desktop
// workspace runs in the terminals it embeds beside the agents: the dashboard
// and the overview hosted by the workspace, Past Sessions opened on the
// conversation of an agent that stopped (`history`), and the shell, editor,
// and pager utility views. It strips every session and Kitty variable first, so a view
// never needs Kitty and never speaks as a managed session.
//
// Outside Kitty there are no Kitty tabs, and the rule that each surface is
// open once is a rule about them: the dashboard, the overview, and the
// keybinding reference open in the terminal they are typed in and look for no
// other instance. For a terminal attached to a tmux session the rule comes
// back in another form (popup.go): the keys bound in the private tmux
// configuration, and `open-dashboard`, open the dashboard and the overview as
// a popup over that terminal, a terminal shows one popup at a time, and
// choosing a session there switches the terminal to it and closes the popup. The two pickers become portable views there
// (picker_portable.go): they open in this terminal and edit a tmux session,
// its title for the icon and its metadata for the color, where inside Kitty
// they stay overlays over the tab they were opened on.
//
// There is deliberately no `dashboard` subcommand. The dashboard is the root
// command's default execution path, so this package exposes Run for the root
// to call rather than a *cobra.Command of its own.
package dashboard

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui"
	"github.com/sglavoie/agentmux/tui/commands"
)

// Run performs the workflow behind a bare `amx`: focus an already-running
// dashboard if there is one, and otherwise start the Bubble Tea program in this
// window. sourceWindowID is the Kitty window whose tab should be selected
// initially; a non-positive value falls back to the ambient KITTY_WINDOW_ID.
//
// backendName is --backend. Outside Kitty the dashboard runs on the tmux
// backend with no Kitty client at all: there is no other dashboard to find,
// so the singleton rule does not apply, and every Kitty tab action is replaced
// by its in-terminal counterpart. view says how it opens there: on which
// session's row, and whether as a popup over an attached terminal
// (popup.go), where the one-dashboard rule is the terminal's one popup.
func Run(sourceWindowID int, backendName string, view View) error {
	cfg, err := loadRunnableConfig()
	if err != nil {
		return err
	}
	configured := backendName == "" && cfg.Defaults.LaunchBackend != ""
	if configured {
		backendName = cfg.Defaults.LaunchBackend
	}
	client, err := selectTerminals(backendName)
	if err != nil {
		return err
	}
	if configured {
		commands.Portable.LaunchSource = "config.toml"
	}
	if client == nil {
		selected, err := terminalView(view)
		if err != nil {
			return err
		}
		return runDashboard(cfg, nil, selected)
	}
	if sourceWindowID <= 0 {
		sourceWindowID = cli.WindowIDFromEnv()
	}
	if focusExistingDashboard(client, sourceWindowID) {
		return nil
	}
	return runDashboard(cfg, client, sourceWindowID)
}

// runDashboard starts the dashboard program in this terminal.
var (
	runDashboard       = tui.RunFromWindow
	loadRunnableConfig = cli.LoadRunnableConfig
)

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
