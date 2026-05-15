package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui"
)

const version = "0.4.0"

var debug bool

var rootCmd = &cobra.Command{
	Use:   "amx",
	Short: "Agent Mux multi-harness session manager for Kitty",
	Long:  "amx manages Claude Code, Copilot, and other supported harness sessions as Kitty tabs with color-coded profiles and an interactive dashboard.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDefault()
		if err != nil {
			return err
		}
		runner := kitty.NewRealRunner()
		client := kitty.NewClient(runner)
		if focusExistingDashboard(client) {
			return nil
		}
		return tui.Run(cfg, client)
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func focusExistingDashboard(client kitty.KittyClient) bool {
	osWindows, err := client.ListWindows()
	if err != nil {
		return false
	}
	loc, found := kitty.FindDashboardWindow(osWindows, windowIDFromEnv())
	if !found {
		return false
	}
	if err := client.FocusTab(kitty.MatchTabByID(loc.TabID)); err != nil {
		return false
	}
	_ = client.FocusWindow(kitty.MatchWindowByID(loc.WindowID))
	fmt.Fprintln(os.Stderr, "Agent Mux dashboard already running — focused existing tab.")
	return true
}

func windowIDFromEnv() int {
	s := os.Getenv("KITTY_WINDOW_ID")
	if s == "" {
		return 0
	}
	id, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return id
}

func init() {
	rootCmd.Version = version
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "enable verbose debug output")
	rootCmd.AddCommand(configCmd, daemonCmd, hookIndicatorCmd)
}
