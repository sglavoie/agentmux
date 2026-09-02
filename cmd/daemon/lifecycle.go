package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/process"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	ccerrors "github.com/sglavoie/agentmux/internal/errors"
	"github.com/spf13/cobra"
)

// daemonLifecycleClient is the seam start/stop/restart drive. It is
// deliberately narrower than the full IPC client: this family only shuts a
// daemon down and re-execs a new one.
type daemonLifecycleClient interface {
	Send(protocol.Request) (protocol.Response, error)
	RestartDetached(amxBin string) error
}

var newDaemonLifecycleClient = func() daemonLifecycleClient { return ipc.NewClient() }

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the daemon (blocks until shutdown)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := cli.LoadRunnableConfig()
		if err != nil {
			ccerrors.Print(err)
			return err
		}
		logger, closer, logDir, err := daemon.NewOperationalLogger(
			cfg.Defaults.Diagnostics,
			cli.DebugEnabled(),
			os.Getenv(process.DetachedEnv) != "1",
		)
		if err != nil {
			return fmt.Errorf("initializing daemon logging: %w", err)
		}
		previousLogger := slog.Default()
		slog.SetDefault(logger)
		defer func() {
			slog.SetDefault(previousLogger)
			_ = closer.Close()
		}()
		slog.Info("daemon: structured logging initialized", "directory", logDir)

		ctx, cancel := signal.NotifyContext(context.Background(),
			os.Interrupt, syscall.SIGTERM)
		defer cancel()

		err = daemon.Run(ctx, cfg)
		if err != nil {
			slog.Error("daemon: stopped with failure", "error", err)
		}
		return err
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newDaemonLifecycleClient()
		resp, err := c.Send(protocol.Request{Type: protocol.TypeShutdown})
		if err != nil {
			return fmt.Errorf("daemon not running: %w", err)
		}
		if !resp.OK {
			return fmt.Errorf("daemon error: %s", resp.Error)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "daemon stopped")
		return err
	},
}

var daemonRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the daemon detached in the background",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newDaemonLifecycleClient()
		// A daemon that cannot be located re-execs by name off PATH; the
		// resolution failure is not itself a reason to refuse the restart.
		amxBin, _ := cli.AmxBinary()
		if err := c.RestartDetached(amxBin); err != nil {
			return err
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "daemon restarted")
		return err
	},
}
