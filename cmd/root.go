package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/sglavoie/agentmux/cmd/config"
	"github.com/sglavoie/agentmux/cmd/daemon"
	"github.com/sglavoie/agentmux/cmd/dashboard"
	"github.com/sglavoie/agentmux/cmd/debug"
	"github.com/sglavoie/agentmux/cmd/hooks"
	"github.com/sglavoie/agentmux/cmd/release"
	"github.com/sglavoie/agentmux/cmd/tuitest"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
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
		return dashboard.Run(dashboardSourceWindowID)
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

func init() {
	// Version output carries the full artifact identity (VCS revision, dirty
	// marker, build time, Go version, binary hash) so an installed CLI can be
	// matched against the daemon's reported build.
	buildinfo.SetVersion(version)
	rootCmd.Version = buildinfo.Current().String()
	rootCmd.PersistentFlags().BoolVar(&cli.DebugFlag, "debug", false, "enable verbose debug output")
	rootCmd.Flags().IntVar(&dashboardSourceWindowID, "window-id", 0, "Kitty window ID whose tab should be selected initially")
	rootCmd.AddCommand(dashboard.ColorPickerCommand(), config.Command(), daemon.Command(), debug.Command(), debug.DoctorCommand(), hooks.IndicatorCommand(), dashboard.IconPickerCommand(), dashboard.KeybindingsCommand(), notificationsCmd, release.Command(), resortCmd, sessionCmd, transcriptsCmd, tuitest.Command(), worktreeCmd)
}
