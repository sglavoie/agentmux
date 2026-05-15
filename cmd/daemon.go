package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	ccerrors "github.com/sglavoie/agentmux/internal/errors"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the amx daemon",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the daemon (blocks until shutdown)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDefault()
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

var daemonRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the daemon detached in the background",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := daemon.NewClient()
		if resp, err := c.Send(daemon.Request{Type: daemon.TypeShutdown}); err == nil && !resp.OK {
			return fmt.Errorf("daemon error: %s", resp.Error)
		}

		socket := daemon.SocketPath()
		for i := 0; i < 20; i++ {
			if _, err := os.Stat(socket); os.IsNotExist(err) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		amxBin, err := os.Executable()
		if err != nil {
			amxBin = "amx"
		}
		if err := daemon.SpawnDetached(amxBin); err != nil {
			return fmt.Errorf("spawn daemon: %w", err)
		}
		fmt.Println("daemon restarted")
		return nil
	},
}

func init() {
	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonStatusCmd, daemonRestartCmd)
}
