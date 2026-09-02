package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui"
	"github.com/spf13/cobra"
)

const version = "0.8.0"

var dashboardSourceWindowID int

var rootCmd = &cobra.Command{
	Use:   "amx",
	Short: "Agent Mux multi-harness session manager for Kitty",
	Long:  "Agent Mux is a multi-harness session manager for Kitty. It manages Claude Code, Copilot, Codex, and other supported harness sessions as color-coded tabs with an interactive dashboard.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cli.ConfigureLogging(cli.DebugEnabled())
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := cli.LoadRunnableConfig()
		if err != nil {
			return err
		}
		client := cli.NewKittyClient()
		sourceWindowID := dashboardSourceWindowID
		if sourceWindowID <= 0 {
			sourceWindowID = cli.WindowIDFromEnv()
		}
		if focusExistingDashboard(client, sourceWindowID) {
			return nil
		}
		return tui.RunFromWindow(cfg, client, sourceWindowID)
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		var exitErr *cli.ExitError
		if errors.As(err, &exitErr) {
			// The command already emitted its own output (and mapped the
			// failure to a specific code). Only print when it did not present a
			// complete structured result, so JSON runs are not followed by a
			// redundant stderr line.
			if !exitErr.Quiet && exitErr.Err != nil {
				fmt.Fprintln(os.Stderr, exitErr.Error())
			}
			os.Exit(exitErr.Code)
		}
		os.Exit(1)
	}
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

func init() {
	// Version output carries the full artifact identity (VCS revision, dirty
	// marker, build time, Go version, binary hash) so an installed CLI can be
	// matched against the daemon's reported build.
	buildinfo.SetVersion(version)
	rootCmd.Version = buildinfo.Current().String()
	rootCmd.PersistentFlags().BoolVar(&cli.DebugFlag, "debug", false, "enable verbose debug output")
	rootCmd.Flags().IntVar(&dashboardSourceWindowID, "window-id", 0, "Kitty window ID whose tab should be selected initially")
	rootCmd.AddCommand(colorPickerCmd, configCmd, daemonCmd, debugCmd, hookIndicatorCmd, iconPickerCmd, keybindingsCmd, notificationsCmd, releaseGateCmd, resortCmd, sessionCmd, transcriptsCmd, tuiTestCmd, worktreeCmd)
}
