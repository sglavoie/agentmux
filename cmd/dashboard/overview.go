package dashboard

import (
	"fmt"
	"os"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui/overview"
	"github.com/spf13/cobra"
)

// The seams the tests replace. They exist for one reason: `amx overview` either
// draws a full-screen Bubble Tea surface or writes to the operator's real Kitty,
// and a test must be able to do neither.
var (
	newOverviewClient  = func() kitty.KittyClient { return cli.NewKittyClient() }
	loadOverviewConfig = func() (*config.Config, error) { return cli.LoadRunnableConfig() }
	runOverview        = func(cfg *config.Config, client kitty.KittyClient, sourceWindowID int) error {
		return overview.Run(cfg, client, sourceWindowID)
	}
)

var overviewSourceWindowID int

// OverviewCommand returns the `overview` command so the root command can
// register it. Root assembly is the only consumer.
func OverviewCommand() *cobra.Command { return overviewCmd }

var overviewCmd = &cobra.Command{
	Use:   "overview",
	Short: "Open or focus the live session overview (overlay grid)",
	Long: "Open the live session overview: a grid of every managed session in this Kitty " +
		"instance with a compact header and a live preview of each pane. The overview is a " +
		"singleton, so invoking it again focuses the one already open rather than drawing a " +
		"second copy.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runOverviewCommand(overviewSourceWindowID)
	},
}

func init() {
	overviewCmd.Flags().IntVar(&overviewSourceWindowID, "window-id", 0, "Kitty window ID whose session should be selected initially")
}

// runOverviewCommand performs the workflow behind `amx overview`: focus an
// already-open overview if there is one, and otherwise start the standalone
// program in this window. A non-positive sourceWindowID falls back to the
// ambient KITTY_WINDOW_ID, which is what makes the command useful from a plain
// shell as well as from the F2 binding.
//
// The singleton question is asked BEFORE the configuration is loaded, because
// focusing a surface that is already running needs no configuration at all: an
// operator who has just broken their config file should still be able to reach
// the overview they already have open.
func runOverviewCommand(sourceWindowID int) error {
	client := newOverviewClient()

	focused, err := focusExistingOverview(client)
	if err != nil {
		return err
	}
	if focused {
		return nil
	}

	cfg, err := loadOverviewConfig()
	if err != nil {
		return err
	}
	if sourceWindowID <= 0 {
		sourceWindowID = cli.WindowIDFromEnv()
	}
	return runOverview(cfg, client, sourceWindowID)
}

// focusExistingOverview focuses an already-open overview and reports whether it
// did.
//
// It is deliberately STRICTER than focusExistingDashboard and the three overlay
// pickers beside it, which all return a bare bool and swallow every Kitty
// failure into "no instance found". That degradation is wrong here in both
// directions. A failed listing is not evidence that no overview is open, so
// treating it as such opens a second full-screen surface that then polls Kitty
// for captures alongside the first; and a failed focus leaves the operator
// staring at a launcher window that silently did nothing. Both are reported.
//
// It also writes NOTHING. The dashboard hands a running instance the tab the
// caller came from through kitty.DashboardSelectionVar, but the overview shows
// every session at once, so a repeat invocation has nothing to tell it: the
// instance already open keeps the selection and the page the operator left it
// on.
func focusExistingOverview(client kitty.KittyClient) (bool, error) {
	osWindows, err := client.ListWindows()
	if err != nil {
		return false, fmt.Errorf("list Kitty windows: %w", err)
	}
	loc, found := kitty.FindOverviewWindow(osWindows, cli.WindowIDFromEnv())
	if !found {
		return false, nil
	}
	if err := client.FocusTab(kitty.MatchTabByID(loc.TabID)); err != nil {
		return false, fmt.Errorf("focus existing overview tab %d: %w", loc.TabID, err)
	}
	if err := client.FocusWindow(kitty.MatchWindowByID(loc.WindowID)); err != nil {
		return false, fmt.Errorf("focus existing overview window %d: %w", loc.WindowID, err)
	}
	fmt.Fprintln(os.Stderr, "Agent Mux overview already open — focused existing overlay.")
	return true, nil
}
