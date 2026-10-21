package dashboard

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/spf13/cobra"
)

// dashboardTabTitle is the title a dashboard tab opened by open-dashboard
// carries, and the fallback evidence for which new tab is the dashboard when
// its foreground process has not been observed yet.
const dashboardTabTitle = "Agent Mux"

// The seams the tests replace. open-dashboard writes to the operator's real
// Kitty and waits for a process to appear in it; a test must do neither.
var (
	newOpenDashboardClient  = func() kitty.KittyClient { return cli.NewKittyClient() }
	loadOpenDashboardConfig = func() (*config.Config, error) { return cli.LoadRunnableConfig() }
	openDashboardBinary     = cli.AmxBinary
	openDashboardSleep      = time.Sleep
	openDashboardWait       = 3 * time.Second
	openDashboardPoll       = 100 * time.Millisecond
	openDashboardStartPopup = startPopup
	openDashboardHere       = func() error { return Run(0, "", View{}) }
)

var (
	openDashboardJSON           bool
	openDashboardSourceWindowID int
)

// OpenDashboardCommand returns the `open-dashboard` command so the root command
// can register it. Root assembly is the only consumer.
func OpenDashboardCommand() *cobra.Command { return openDashboardCmd }

var openDashboardCmd = &cobra.Command{
	Use:   "open-dashboard",
	Short: "Focus the Agent Mux dashboard, opening it in a new tab if none is running",
	Long: `Brings the Agent Mux dashboard forward without drawing it in this terminal.

The dashboard is a singleton: when one is already running, its tab and window
are focused and nothing new is opened. Otherwise a new Kitty tab runs the
dashboard. Either way the command returns as soon as the dashboard is located,
which is what makes it usable from outside Kitty — set KITTY_LISTEN_ON to the
instance's remote-control socket.

--json reports {window_id, tab_id, opened}, where opened says whether a new
dashboard tab was launched.

Outside Kitty the dashboard of a terminal attached to a tmux session is a
popup over it. The terminal used last gets that popup, opened on the session
it is showing, and --json reports it as client; a terminal that already shows
one keeps it. With no terminal attached, the dashboard starts in the terminal
this command is typed in.`,
	Args: cobra.NoArgs,
	RunE: runOpenDashboard,
}

func init() {
	cli.AddJSONFlag(openDashboardCmd, &openDashboardJSON)
	openDashboardCmd.Flags().IntVar(&openDashboardSourceWindowID, "window-id", 0, "Kitty window ID whose tab the dashboard should select")
}

// openDashboardData is the `open-dashboard --json` payload.
type openDashboardData struct {
	WindowID int  `json:"window_id"`
	TabID    int  `json:"tab_id"`
	Opened   bool `json:"opened"`
	// Client is the terminal the dashboard was opened over as a popup,
	// outside Kitty; window_id and tab_id are then zero.
	Client string `json:"client,omitempty"`
}

// runOpenDashboard applies the singleton rule strictly, the way the overview
// does rather than the way a bare `amx` does: a failed listing is not evidence
// that no dashboard is running, and a failed focus is not a reason to open a
// second one, so both are reported instead of degrading into a launch. As with
// the overview, the running instance is looked for before the configuration is
// loaded, because focusing one needs none.
func runOpenDashboard(cmd *cobra.Command, _ []string) error {
	if !insideKitty() {
		return openDashboardInTerminal(cmd)
	}
	client := newOpenDashboardClient()
	sourceWindowID := cli.WindowIDOrEnv(openDashboardSourceWindowID)

	osWindows, err := client.ListWindows()
	if err != nil {
		return cli.CommandFailure(cmd, openDashboardJSON, "kitty_unavailable", fmt.Errorf("list Kitty windows: %w", err))
	}
	loc, found, err := focusDashboardIn(client, osWindows, sourceWindowID)
	if err != nil {
		return cli.CommandFailure(cmd, openDashboardJSON, "focus_failed", err)
	}
	if found {
		return reportOpenDashboard(cmd, openDashboardData{WindowID: loc.WindowID, TabID: loc.TabID})
	}

	// The configuration is checked here, before a tab exists, because the
	// dashboard refuses an invalid one and a tab that opens only to show an
	// error and close is worse than an error from this command.
	if _, err := loadOpenDashboardConfig(); err != nil {
		return cli.CommandFailure(cmd, openDashboardJSON, "config_invalid", err)
	}
	bin, err := openDashboardBinary()
	if err != nil {
		return cli.CommandFailure(cmd, openDashboardJSON, "binary_unavailable", err)
	}
	cmdline := []string{bin}
	if sourceWindowID > 0 {
		cmdline = append(cmdline, "--window-id", strconv.Itoa(sourceWindowID))
	}
	before := make(map[int]bool)
	for _, osWindow := range osWindows {
		for _, tab := range osWindow.Tabs {
			before[tab.ID] = true
		}
	}
	if err := client.LaunchTab(kitty.LaunchOpts{Title: dashboardTabTitle, Cmdline: cmdline}); err != nil {
		return cli.CommandFailure(cmd, openDashboardJSON, "launch_failed", fmt.Errorf("launch dashboard tab: %w", err))
	}
	loc, err = awaitLaunchedDashboard(client, before)
	if err != nil {
		return cli.CommandFailure(cmd, openDashboardJSON, "dashboard_not_observed", err)
	}
	return reportOpenDashboard(cmd, openDashboardData{WindowID: loc.WindowID, TabID: loc.TabID, Opened: true})
}

// openDashboardInTerminal is open-dashboard outside Kitty. The dashboard of a
// terminal attached to a tmux session is a popup over it, so the terminal the
// operator used last gets one, opened on the session it is showing; tmux
// leaves a popup that is already there alone, which is the singleton rule.
// With no terminal attached anywhere there is nothing to bring forward, and
// the dashboard starts in the calling terminal instead. --json cannot do
// that, since its output is the JSON document, and says so.
func openDashboardInTerminal(cmd *cobra.Command) error {
	if _, err := loadOpenDashboardConfig(); err != nil {
		return cli.CommandFailure(cmd, openDashboardJSON, "config_invalid", err)
	}
	popupErr := errNoAttachedTerminal
	if opened, err := openTmux(); err == nil {
		server, ok := opened.(tmuxPopups)
		if !ok {
			return cli.CommandFailure(cmd, openDashboardJSON, "backend_unavailable", errors.New("tmux backend cannot open popups"))
		}
		bin, err := openDashboardBinary()
		if err != nil {
			return cli.CommandFailure(cmd, openDashboardJSON, "binary_unavailable", err)
		}
		var client string
		if client, popupErr = openDashboardPopup(server, bin, openDashboardStartPopup); popupErr == nil {
			return reportOpenDashboard(cmd, openDashboardData{Opened: true, Client: client})
		}
	}
	if !errors.Is(popupErr, errNoAttachedTerminal) {
		return cli.CommandFailure(cmd, openDashboardJSON, "launch_failed", popupErr)
	}
	if openDashboardJSON {
		return cli.CommandFailure(cmd, openDashboardJSON, "no_attached_terminal",
			errors.New("no terminal is attached to a tmux session, so there is no dashboard to bring forward; run `amx` in a terminal"))
	}
	return openDashboardHere()
}

// awaitLaunchedDashboard waits, for at most openDashboardWait, for the
// dashboard just launched to appear in the window tree. Its foreground process
// is the evidence the singleton rule itself reads; when the wait ends without
// it, the one new tab carrying the dashboard title is accepted instead, since
// the launch is what put it there.
func awaitLaunchedDashboard(client kitty.KittyClient, before map[int]bool) (*kitty.DashboardLocation, error) {
	var fallback *kitty.DashboardLocation
	for waited := time.Duration(0); ; waited += openDashboardPoll {
		osWindows, err := client.ListWindows()
		if err == nil {
			if loc, found := kitty.FindDashboardWindow(osWindows, cli.WindowIDFromEnv()); found {
				return loc, nil
			}
			fallback = newDashboardTab(osWindows, before)
		}
		if waited >= openDashboardWait {
			break
		}
		openDashboardSleep(openDashboardPoll)
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, errors.New("launched a dashboard tab, but it did not appear in Kitty")
}

func newDashboardTab(osWindows []kitty.OSWindow, before map[int]bool) *kitty.DashboardLocation {
	var found *kitty.DashboardLocation
	for _, osWindow := range osWindows {
		for _, tab := range osWindow.Tabs {
			if before[tab.ID] || tab.Title != dashboardTabTitle || len(tab.Windows) == 0 {
				continue
			}
			if found != nil {
				return nil
			}
			found = &kitty.DashboardLocation{TabID: tab.ID, WindowID: tab.Windows[0].ID}
		}
	}
	return found
}

func reportOpenDashboard(cmd *cobra.Command, data openDashboardData) error {
	if openDashboardJSON {
		return cli.WriteJSON(cmd, data)
	}
	if data.Client != "" {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "Opened the Agent Mux dashboard over %s.\n", data.Client)
		return err
	}
	verb := "Focused the running"
	if data.Opened {
		verb = "Opened the"
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s Agent Mux dashboard (tab %d, window %d).\n", verb, data.TabID, data.WindowID)
	return err
}
