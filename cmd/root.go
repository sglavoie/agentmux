package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/sglavoie/cc-manager-kitty/internal/config"
	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
	"github.com/sglavoie/cc-manager-kitty/tui"
)

const version = "0.3.0"

var debug bool

var rootCmd = &cobra.Command{
	Use:   "ccm",
	Short: "Claude Code session manager for Kitty terminal",
	Long:  "ccm manages Claude Code sessions as Kitty tabs with color-coded accounts and an interactive dashboard.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(config.DefaultConfigPath())
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
	fmt.Fprintln(os.Stderr, "Dashboard already running — focused existing tab.")
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
