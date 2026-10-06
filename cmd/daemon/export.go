package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/sglavoie/agentmux/cmd/debug"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/eventlog"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/spf13/cobra"
)

var (
	daemonExportEventsWindow        int
	daemonExportEventsCurrentWindow bool
	daemonExportEventsLast          int
	daemonExportEventsOutput        string
	daemonExportEventsPretty        bool
	daemonExportEventsSchema        int
	// newDaemonExportClient is also how `daemon status` reads health: the
	// export client is this family's one daemon-query seam.
	newDaemonExportClient = cli.NewDaemonExportClient
	newDaemonExportKitty  = func() cli.KittyWindowLister { return cli.NewKittyClient() }
	daemonExportNow       = time.Now
)

func buildDaemonExportPayload(snapshot protocol.EventsExport, health ipc.Health, osWindows []kitty.OSWindow, requestedWindowID int, last int, exportedAt time.Time) protocol.ExportEventsPayload {
	liveByID := debug.ExportLiveMetadata(osWindows, snapshot, requestedWindowID)
	ids := debug.ExportWindowIDs(snapshot, requestedWindowID)
	windows := make([]protocol.ExportWindow, 0, len(ids))
	for _, id := range ids {
		window := protocol.ExportWindow{
			WindowID: id,
			Live:     liveByID[id],
			Events:   []protocol.SessionEvent{},
		}
		if durable, ok := snapshot.WindowMetadata[id]; ok {
			window.Stats = durable.Stats
			window.Durable = durable.Durable
			window.Renderer = durable.Renderer
			window.Shadow = durable.Shadow
			window.Watchdog = durable.Watchdog
		}
		if events, ok := snapshot.EventsByWindow[id]; ok {
			window.Events = eventlog.TrimLast(events, last)
		}
		windows = append(windows, window)
	}

	cliBuild := buildinfo.Current()
	return protocol.ExportEventsPayload{
		SchemaVersion: protocol.EventsExportSchemaVersion,
		ExportedAt:    exportedAt.Unix(),
		Daemon: protocol.ExportDaemonMetadata{
			PID:           health.PID,
			UptimeSeconds: int64(health.Uptime / time.Second),
			Ready:         health.Ready,
			Build:         health.Build,
			Metrics:       health.Metrics,
		},
		CLIBuild: &cliBuild,
		Windows:  windows,
	}
}

func daemonExportCurrentWindowID() (int, error) {
	s := os.Getenv(cli.WindowIDEnv)
	if s == "" {
		return 0, fmt.Errorf("--current-window requires KITTY_WINDOW_ID to be set")
	}
	id, err := strconv.Atoi(s)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("--current-window requires a valid KITTY_WINDOW_ID, got %q", s)
	}
	return id, nil
}

func resolveDaemonExportWindowID(cmd *cobra.Command) (int, error) {
	if daemonExportEventsCurrentWindow && (daemonExportEventsWindow != 0 || cmd.Flags().Changed("window")) {
		return 0, fmt.Errorf("--window and --current-window cannot be used together")
	}
	if daemonExportEventsWindow < 0 {
		return 0, fmt.Errorf("--window must be >= 0")
	}
	if daemonExportEventsCurrentWindow {
		return daemonExportCurrentWindowID()
	}
	return daemonExportEventsWindow, nil
}

var daemonExportEventsCmd = &cobra.Command{
	Use:   "export-events",
	Short: "Dump hook event history as JSON for investigation",
	Long: `Export the daemon's schema-v13 investigation snapshot as JSON.

The payload is organized as a per-window array so each entry carries its own
event list, durable daemon metadata, and best-effort live Kitty/session
metadata without paired map lookups. Use --schema 12 for the pre-screen-decline
shape, --schema 11 for the pre-busy-anchor shape, --schema 10 for the pre-stop-hold shape, --schema 9 for the previous
complete causal shape, or --schema 8 for pre-causal consumers.`,
	Example: `  amx daemon export-events
  amx daemon export-events --current-window
  amx daemon export-events --current-window --last 25
  amx daemon export-events --window 42
  amx daemon export-events --pretty=false --output events.json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if daemonExportEventsLast < 0 {
			return fmt.Errorf("--last must be >= 0")
		}
		if daemonExportEventsSchema != protocol.EventsExportSchemaV8 &&
			daemonExportEventsSchema != protocol.EventsExportSchemaV9 &&
			daemonExportEventsSchema != protocol.EventsExportSchemaV10 &&
			daemonExportEventsSchema != protocol.EventsExportSchemaV11 &&
			daemonExportEventsSchema != protocol.EventsExportSchemaV12 &&
			daemonExportEventsSchema != protocol.EventsExportSchemaVersion {
			return fmt.Errorf("--schema must be 8, 9, 10, 11, 12, or 13")
		}
		requestedWindowID, err := resolveDaemonExportWindowID(cmd)
		if err != nil {
			return err
		}

		c := newDaemonExportClient()
		snapshot, err := c.ExportEvents(requestedWindowID)
		if err != nil {
			if !c.Health().Alive {
				return fmt.Errorf("daemon not running: %w", err)
			}
			return fmt.Errorf("export events: %w", err)
		}

		var osWindows []kitty.OSWindow
		if ids := debug.ExportWindowIDs(snapshot, requestedWindowID); len(ids) > 0 {
			if liveWindows, liveErr := newDaemonExportKitty().ListWindows(); liveErr == nil {
				osWindows = liveWindows
			}
		}

		payload := buildDaemonExportPayload(snapshot, c.Health(), osWindows, requestedWindowID, daemonExportEventsLast, daemonExportNow())
		projected, err := protocol.ProjectExportEventsPayload(payload, daemonExportEventsSchema)
		if err != nil {
			return err
		}

		var (
			data      []byte
			encodeErr error
		)
		if daemonExportEventsPretty {
			data, encodeErr = json.MarshalIndent(projected, "", "  ")
		} else {
			data, encodeErr = json.Marshal(projected)
		}
		if encodeErr != nil {
			return fmt.Errorf("encode events: %w", encodeErr)
		}
		data = append(data, '\n')

		if daemonExportEventsOutput == "" {
			_, err = cmd.OutOrStdout().Write(data)
			return err
		}
		// Expand a leading ~/ so users can pass shell-style paths via flags.
		// Without this, --output ~/events.json would write a literal
		// "~/events.json" file in the current working directory instead of the
		// user's home directory.
		return os.WriteFile(config.ExpandTilde(daemonExportEventsOutput), data, 0o600)
	},
}
