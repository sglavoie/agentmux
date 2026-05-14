package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/sglavoie/cc-manager-kitty/internal/config"
	"github.com/sglavoie/cc-manager-kitty/internal/daemon"
	ccerrors "github.com/sglavoie/cc-manager-kitty/internal/errors"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the ccm daemon",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the daemon (blocks until shutdown)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(config.DefaultConfigPath())
		if err != nil {
			ccerrors.Print(err)
			return fmt.Errorf("loading config: %w", err)
		}

		ctx, cancel := signal.NotifyContext(context.Background(),
			os.Interrupt, syscall.SIGTERM)
		defer cancel()

		return daemon.Run(ctx, cfg)
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := daemon.NewClient()
		resp, err := c.Send(daemon.Request{Type: daemon.TypeShutdown})
		if err != nil {
			return fmt.Errorf("daemon not running: %w", err)
		}
		if !resp.OK {
			return fmt.Errorf("daemon error: %s", resp.Error)
		}
		fmt.Println("daemon stopped")
		return nil
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := daemon.Ping(); err != nil {
			fmt.Printf("daemon: not running (%s)\n", daemon.SocketPath())
			return nil
		}
		fmt.Printf("daemon: running (%s)\n", daemon.SocketPath())
		return nil
	},
}

func init() {
	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonStatusCmd)
}
