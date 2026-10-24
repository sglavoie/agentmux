package daemon

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/cmd/debug"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/spf13/cobra"
)

var daemonSocketPath = ipc.SocketPath

// errDaemonNotRunning is the quiet nonzero exit for a status that found no
// daemon, so a script can test `amx daemon status` itself instead of grepping
// its first line. The report has already been written in either output mode.
var errDaemonNotRunning = &cli.ExitError{Code: 1, Err: errors.New("daemon not running"), Quiet: true}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status, readiness, and build identity",
	Long: `Reports whether the daemon answers on its socket and, when it does, its PID,
uptime, readiness, build, notification backend, agent-session-loop bridge,
and connected companion clients. It never starts a daemon. A daemon that is
not running is reported and exits 1, quietly, so a script can test the
command itself.

With --json it reports {alive, socket, pid, uptime_seconds, ready,
degraded_reason, build, notification, asl, asl_reconcile, companion}.`,
	Example: `  amx daemon status
  amx daemon status --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		health := newDaemonExportClient().Health()
		if jsonOutput, _ := cmd.Flags().GetBool("json"); jsonOutput {
			if err := cli.WriteJSON(cmd, daemonStatusDataFrom(health, daemonSocketPath())); err != nil {
				return err
			}
			if !health.Alive {
				return errDaemonNotRunning
			}
			return nil
		}
		if !health.Alive {
			if _, err := fmt.Fprintf(out, "daemon: not running (%s)\n", daemonSocketPath()); err != nil {
				return err
			}
			return errDaemonNotRunning
		}
		if _, err := fmt.Fprintf(out, "daemon: running (%s)\n", runningDetail(health, daemonSocketPath())); err != nil {
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
			if _, err := fmt.Fprintf(out, "notifications: %s (%s)%s\n", state, detail, snoozeStatusSuffix(n)); err != nil {
				return err
			}
		}
		if health.ASL != nil {
			if _, err := fmt.Fprintf(out, "asl: %s\n", debug.ASLStatusLine(health.ASL)); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(out, "companion: %s\n", companionStatusLine(health.Companion)); err != nil {
			return err
		}
		return nil
	},
}

// snoozeStatusSuffix names the snooze in force, so a quiet daemon whose
// backend is ready does not read as one that should be delivering. The daemon
// reports an expired timed snooze as no snooze at all.
// runningDetail is what the running line says about the process: its pid and
// uptime, the way doctor reports them, ahead of the socket. Uptime is the
// quickest way to see that a daemon just restarted. A daemon too old to report
// a pid gets the socket alone.
func runningDetail(health ipc.Health, socket string) string {
	if health.PID <= 0 {
		return socket
	}
	return fmt.Sprintf("pid %d, uptime %s, %s", health.PID, health.Uptime.Round(time.Second), socket)
}

func snoozeStatusSuffix(n *protocol.NotificationCapability) string {
	switch {
	case n.SnoozeIndefinite:
		return "; snoozed until turned off"
	case n.SnoozedUntil != 0:
		return "; snoozed until " + time.Unix(n.SnoozedUntil, 0).Local().Format("2006-01-02 15:04")
	default:
		return ""
	}
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
	Companion      *protocol.CompanionHealth        `json:"companion,omitempty"`
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
	data.Companion = health.Companion
	return data
}

// companionStatusLine summarizes the connected companion subscribers: how
// many, who they said they are, and whether one holds the notification role.
// Nil means a daemon that predates the companion protocol, which is an
// unknown rather than zero subscribers.
func companionStatusLine(c *protocol.CompanionHealth) string {
	if c == nil {
		return "unknown (restart older daemon)"
	}
	if c.Subscribers == 0 {
		return "no subscribers"
	}
	noun := "subscribers"
	if c.Subscribers == 1 {
		noun = "subscriber"
	}
	line := fmt.Sprintf("%d %s", c.Subscribers, noun)
	var names []string
	for _, client := range c.Clients {
		name := client.Client
		if name == "" {
			name = client.SubscriberID
		}
		if client.ClientVersion != "" {
			name += " " + client.ClientVersion
		}
		names = append(names, name)
	}
	if len(names) > 0 {
		line += " (" + strings.Join(names, ", ") + ")"
	}
	if c.NotificationBackendConnected {
		line += "; notification backend connected"
	}
	return line
}

func init() {
	cli.AddJSONFlag(daemonStatusCmd, nil)
}
