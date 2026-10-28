package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/process"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/spf13/cobra"
)

// daemonLifecycleClient is the seam start/stop/restart drive. It is
// deliberately narrower than the full IPC client: this family only shuts a
// daemon down, re-execs a new one, and asks whether it answers.
type daemonLifecycleClient interface {
	Send(protocol.Request) (protocol.Response, error)
	RestartDetached(amxBin string) error
	Health() ipc.Health
}

var newDaemonLifecycleClient = func() daemonLifecycleClient { return ipc.NewClient() }

// daemonRestartBinary is the seam tests swap to stand in for the running
// executable the restart re-execs.
var daemonRestartBinary = cli.AmxBinary

// daemonRestartWait bounds how long restart waits for the new daemon to
// answer, and daemonRestartPoll is the pause between attempts. A daemon
// normally answers within half a second; the budget covers a slow restore.
var (
	daemonRestartWait = 10 * time.Second
	daemonRestartPoll = 50 * time.Millisecond
	daemonRestartNow  = time.Now
	daemonRestartNap  = time.Sleep
)

// The seams carryKittySocket reads and writes the environment through, and
// the probe that says whether a Kitty still listens at a socket.
var (
	daemonRestartGetenv       = os.Getenv
	daemonRestartSetenv       = os.Setenv
	daemonRestartKittyAnswers = func(socket string) bool {
		return kitty.ListenAddressAnswers(socket, 250*time.Millisecond)
	}
)

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the daemon (blocks until shutdown)",
	Long: `Runs the Agent Mux daemon in the foreground until it is interrupted or asked
to shut down (` + "`amx daemon stop`" + `). The daemon reduces hook events, writes
every rendered session state, and serves the dashboard, the app, and the
CLI over its socket.

The daemon normally runs on its own: commands that need it, such as hooks
and the dashboard, start it detached when it is not running, and
` + "`amx daemon restart`" + ` replaces it in the background. Run start by hand to
watch its log in a terminal (add --debug for verbose logging). It has no
--json: it answers nothing until it stops.`,
	Example: `  amx daemon start
  amx --debug daemon start`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := cli.LoadRunnableConfig()
		if err != nil {
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
	Long: `Asks the running daemon to shut down. Agents keep running: they live in
their Kitty tabs or on Agent Mux's private tmux server, not in the daemon, and
the next command that needs a daemon starts one again. A daemon that is not
running is an error, reported in plain words (daemon_not_running), so a script
can tell a stop that happened from one that had nothing to stop.

With --json it reports {stopped}; a failure is a structured error
(daemon_not_running, daemon_error).`,
	Example: `  amx daemon stop
  amx daemon stop --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		c := newDaemonLifecycleClient()
		resp, err := c.Send(protocol.Request{Type: protocol.TypeShutdown})
		if err != nil {
			if !c.Health().Alive {
				return cli.CommandFailure(cmd, jsonOutput, "daemon_not_running", errors.New("daemon is not running; nothing to stop"))
			}
			return cli.CommandFailure(cmd, jsonOutput, "daemon_error", fmt.Errorf("stopping the daemon: %w", err))
		}
		if !resp.OK {
			return cli.CommandFailure(cmd, jsonOutput, "daemon_error", fmt.Errorf("daemon error: %s", resp.Error))
		}
		if jsonOutput {
			return cli.WriteJSON(cmd, daemonStopData{Stopped: true})
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "daemon stopped")
		return err
	},
}

var daemonRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the daemon detached in the background",
	Long: `Replaces the running daemon with a new one started detached from this
executable, and waits until the new one answers on its socket before
reporting, so the next command does not reach a daemon that is not there yet.
With no daemon running it starts one. Agents keep running throughout. A
` + "`go run`" + ` build is refused, because its executable is deleted when the
command exits.

With --json it reports {pid, ready, degraded_reason}: ready is false, with a
reason, when the new daemon came up degraded. A daemon that never answered
is a structured error (restart_timeout).`,
	Example: `  amx daemon restart
  amx daemon restart --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		c := newDaemonLifecycleClient()
		// A daemon that cannot be located re-execs by name off PATH; the
		// resolution failure is not itself a reason to refuse the restart.
		amxBin, _ := daemonRestartBinary()
		// A `go run` build is deleted when this command exits, so the daemon
		// would come back up from a file that no longer exists.
		if err := cli.RefuseTransientBinary(amxBin, "restart the daemon"); err != nil {
			return cli.CommandFailure(cmd, jsonOutput, "transient_binary", err)
		}
		carryKittySocket(c)
		if err := c.RestartDetached(amxBin); err != nil {
			code := "restart_failed"
			if errors.Is(err, ipc.ErrRestartTimeout) {
				code = "restart_timeout"
			}
			return cli.CommandFailure(cmd, jsonOutput, code, err)
		}
		// RestartDetached holds off competing starters until the replacement
		// answers. Read its health again for the command's readiness report.
		health, ok := waitForRestartedDaemon(c)
		if !ok {
			return cli.CommandFailure(cmd, jsonOutput, "restart_timeout", fmt.Errorf("daemon restart requested, but the new daemon did not answer within %s; run `amx daemon status` or check the bootstrap log", daemonRestartWait))
		}
		if jsonOutput {
			return cli.WriteJSON(cmd, daemonRestartData{PID: health.PID, Ready: health.Ready, DegradedReason: health.DegradedReason})
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "daemon restarted (%s)\n", restartedDetail(health))
		return err
	},
}

// daemonStopData is the `daemon stop --json` data.
type daemonStopData struct {
	Stopped bool `json:"stopped"`
}

// daemonRestartData is the `daemon restart --json` data: the new daemon as it
// first answered. PID is omitted when the daemon did not report one.
type daemonRestartData struct {
	PID            int    `json:"pid,omitempty"`
	Ready          bool   `json:"ready"`
	DegradedReason string `json:"degraded_reason,omitempty"`
}

func init() {
	daemonStopCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
	daemonRestartCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
}

// carryKittySocket hands the Kitty the running daemon reads to its
// replacement when this command cannot name one itself. The new daemon learns
// its Kitty from the environment it is spawned with, so a restart typed
// outside Kitty (the desktop app's terminal, ssh, a tmux session) would
// otherwise come back knowing none and leave every Kitty session unrestored
// until its next hook. A socket nothing listens on any more is not carried:
// the replacement would wait on a Kitty that is gone.
func carryKittySocket(c daemonLifecycleClient) {
	if kitty.NormalizeListenAddress(daemonRestartGetenv("KITTY_LISTEN_ON"), daemonRestartGetenv("KITTY_PID")) != "" {
		return
	}
	snapshot := c.Health().KittySnapshot
	if snapshot == nil || !kitty.IsReusableListenAddress(snapshot.Socket) || !daemonRestartKittyAnswers(snapshot.Socket) {
		return
	}
	_ = daemonRestartSetenv("KITTY_LISTEN_ON", snapshot.Socket)
}

// waitForRestartedDaemon polls health until the spawned daemon answers or the
// budget runs out.
func waitForRestartedDaemon(c daemonLifecycleClient) (ipc.Health, bool) {
	deadline := daemonRestartNow().Add(daemonRestartWait)
	for {
		if health := c.Health(); health.Alive {
			return health, true
		}
		if !daemonRestartNow().Before(deadline) {
			return ipc.Health{}, false
		}
		daemonRestartNap(daemonRestartPoll)
	}
}

// restartedDetail names the new process and, when it came up degraded, why,
// so a restart that only half worked does not read as a clean one.
func restartedDetail(health ipc.Health) string {
	detail := "pid unknown"
	if health.PID > 0 {
		detail = fmt.Sprintf("pid %d", health.PID)
	}
	if !health.Ready && health.DegradedReason != "" {
		detail += "; not ready: " + health.DegradedReason
	}
	return detail
}
