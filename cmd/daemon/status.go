package daemon

import (
	"fmt"

	"github.com/sglavoie/agentmux/cmd/debug"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/spf13/cobra"
)

var daemonSocketPath = ipc.SocketPath

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status, readiness, and build identity",
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		health := newDaemonExportClient().Health()
		if jsonOutput, _ := cmd.Flags().GetBool("json"); jsonOutput {
			return cli.WriteJSON(cmd, daemonStatusDataFrom(health, daemonSocketPath()))
		}
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

// daemonStatusData is the `daemon status --json` payload: the same facts the
// text lines carry, as fields rather than prose. A daemon that is not running
// is a successful answer with Alive false, exactly as the text form exits 0
// after saying so. The nested blocks reuse the daemon's own wire shapes, and a
// nil block means the daemon did not report it — an older daemon, or one with
// no ASL integration — never that the value was zero.
type daemonStatusData struct {
	Alive          bool                             `json:"alive"`
	Socket         string                           `json:"socket"`
	PID            int                              `json:"pid,omitempty"`
	UptimeSeconds  float64                          `json:"uptime_seconds,omitempty"`
	Ready          bool                             `json:"ready"`
	DegradedReason string                           `json:"degraded_reason,omitempty"`
	Build          *buildinfo.Info                  `json:"build,omitempty"`
	Notification   *protocol.NotificationCapability `json:"notification,omitempty"`
	ASL            *aslbridge.Status                `json:"asl,omitempty"`
	ASLReconcile   *protocol.ASLReconcileSnapshot   `json:"asl_reconcile,omitempty"`
}

func daemonStatusDataFrom(health ipc.Health, socket string) daemonStatusData {
	data := daemonStatusData{Alive: health.Alive, Socket: socket}
	if !health.Alive {
		return data
	}
	data.PID = health.PID
	data.UptimeSeconds = health.Uptime.Seconds()
	data.Ready = health.Ready
	data.DegradedReason = health.DegradedReason
	data.Build = health.Build
	data.Notification = health.Notification
	data.ASL = health.ASL
	data.ASLReconcile = health.ASLReconcile
	return data
}

func init() {
	daemonStatusCmd.Flags().Bool("json", false, "emit schema-v1 JSON")
}
