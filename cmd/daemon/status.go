package daemon

import (
	"fmt"

	"github.com/sglavoie/agentmux/cmd/debug"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/spf13/cobra"
)

var daemonSocketPath = ipc.SocketPath

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status, readiness, and build identity",
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		health := newDaemonExportClient().Health()
		if !health.Alive {
			_, err := fmt.Fprintf(out, "daemon: not running (%s)\n", daemonSocketPath())
			return err
		}
		if _, err := fmt.Fprintf(out, "daemon: running (%s)\n", daemonSocketPath()); err != nil {
			return err
		}
		readiness := "true"
		if !health.Ready {
			readiness = "false"
			if health.DegradedReason != "" {
				readiness += " (" + health.DegradedReason + ")"
			}
		}
		if _, err := fmt.Fprintf(out, "ready: %s\n", readiness); err != nil {
			return err
		}
		if health.Build != nil {
			if _, err := fmt.Fprintf(out, "build: %s\n", health.Build.String()); err != nil {
				return err
			}
		}
		if health.Notification == nil {
			if _, err := fmt.Fprintln(out, "notifications: unknown (restart older daemon)"); err != nil {
				return err
			}
		} else {
			n := health.Notification
			state := "unavailable"
			if !n.Enabled {
				state = "disabled"
			} else if n.Available && n.Interactive {
				state = "ready"
			}
			detail := n.Backend
			if n.Version != "" {
				detail += " " + n.Version
			}
			if n.Executable != "" {
				detail += " at " + n.Executable
			}
			if n.Reason != "" && n.Reason != "disabled" {
				detail += "; " + n.Reason
			}
			if _, err := fmt.Fprintf(out, "notifications: %s (%s)\n", state, detail); err != nil {
				return err
			}
		}
		if health.ASL != nil {
			if _, err := fmt.Fprintf(out, "asl: %s\n", debug.ASLStatusLine(health.ASL)); err != nil {
				return err
			}
		}
		return nil
	},
}
