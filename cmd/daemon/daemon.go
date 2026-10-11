// Package daemon holds the `amx daemon` command family: the always-on local
// daemon seen from the CLI side. It owns the daemon's lifecycle as a command —
// `start`, which blocks and is the process the detached launch re-execs, plus
// `stop` and `restart` over the lifecycle client seam — the `status` surface
// with its readiness, build, notification, ASL, and companion lines, and
// `export-events`, the investigation snapshot with its window resolution,
// per-window trimming, and schema projection. `amx daemon audit` is registered
// here but implemented in cmd/debug, because it shares that family's invariant
// evaluator.
//
// The family exports exactly one identifier, Command, so no daemon internal is
// re-exported through the CLI. Everything it needs from the daemon it takes
// from internal/daemon, internal/daemon/ipc, internal/daemon/eventlog,
// internal/daemon/protocol, and internal/daemon/process (the latter only for
// the detached-launch handshake `start` reads back).
//
// A file in this package may name the imported internal/daemon package
// unqualified as `daemon`: a package's own name is not in scope inside its
// files.
package daemon

import (
	"github.com/sglavoie/agentmux/cmd/debug"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/spf13/cobra"
)

// Command returns the `daemon` command tree so the root command can register
// it. Root assembly is the only consumer.
func Command() *cobra.Command { return daemonCmd }

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the amx daemon",
}

func init() {
	daemonExportEventsCmd.Flags().IntVar(&daemonExportEventsWindow, "window", 0, "restrict export to a single Kitty window ID")
	daemonExportEventsCmd.Flags().BoolVar(&daemonExportEventsCurrentWindow, "current-window", false, "restrict export to the session this is typed in ($AMX_RUNTIME_SESSION_ID, else $KITTY_WINDOW_ID)")
	daemonExportEventsCmd.Flags().StringVar(&daemonExportEventsRuntime, "runtime-session", "", "restrict export to a single runtime session ID")
	daemonExportEventsCmd.Flags().IntVar(&daemonExportEventsLast, "last", 0, "keep only the last N events per exported window (0 = no limit)")
	daemonExportEventsCmd.Flags().StringVar(&daemonExportEventsOutput, "output", "", "write JSON to this file instead of stdout")
	daemonExportEventsCmd.Flags().BoolVar(&daemonExportEventsPretty, "pretty", true, "pretty-print JSON with 2-space indent")
	daemonExportEventsCmd.Flags().IntVar(&daemonExportEventsSchema, "schema", protocol.EventsExportSchemaVersion, "export schema version (8, 9, 10, 11, 12, or 13)")

	daemonCmd.AddCommand(debug.AuditCommand())
	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonStatusCmd, daemonRestartCmd, daemonExportEventsCmd)
}
