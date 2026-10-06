// Package daemon holds the `amx daemon` command family: the always-on local
// daemon's lifecycle from the CLI side (start, stop, restart), the status
// surface that reports whether it is running and ready, and the
// export-events investigation snapshot. `amx daemon audit` is registered here
// but implemented in cmd/debug, because it shares that family's invariant
// evaluator.
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
	daemonExportEventsCmd.Flags().BoolVar(&daemonExportEventsCurrentWindow, "current-window", false, "resolve the target window from $KITTY_WINDOW_ID")
	daemonExportEventsCmd.Flags().IntVar(&daemonExportEventsLast, "last", 0, "keep only the last N events per exported window (0 = no limit)")
	daemonExportEventsCmd.Flags().StringVar(&daemonExportEventsOutput, "output", "", "write JSON to this file instead of stdout")
	daemonExportEventsCmd.Flags().BoolVar(&daemonExportEventsPretty, "pretty", true, "pretty-print JSON with 2-space indent")
	daemonExportEventsCmd.Flags().IntVar(&daemonExportEventsSchema, "schema", protocol.EventsExportSchemaVersion, "export schema version (8, 9, 10, 11, 12, or 13)")

	daemonCmd.AddCommand(debug.AuditCommand())
	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonStatusCmd, daemonRestartCmd, daemonExportEventsCmd)
}
