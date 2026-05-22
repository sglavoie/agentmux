package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	ccerrors "github.com/sglavoie/agentmux/internal/errors"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/spf13/cobra"
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
		amxBin, err := os.Executable()
		if err != nil {
			amxBin = "amx"
		}
		if err := c.RestartDetached(amxBin); err != nil {
			return err
		}
		fmt.Println("daemon restarted")
		return nil
	},
}

var (
	daemonExportEventsWindow int
	daemonExportEventsOutput string
	daemonExportEventsPretty bool
	newDaemonExportClient    = func() daemonExportClient { return daemon.NewClient() }
	newDaemonExportKitty     = func() kittyWindowLister { return kitty.NewClient(kitty.NewRealRunner()) }
	daemonExportNow          = time.Now
)

type daemonExportClient interface {
	ExportEvents(windowID int) (daemon.EventsExport, error)
	Health() daemon.Health
}

type kittyWindowLister interface {
	ListWindows() ([]kitty.OSWindow, error)
}

func daemonExportWindowIDs(snapshot daemon.EventsExport, requestedWindowID int) map[int]struct{} {
	ids := make(map[int]struct{}, len(snapshot.EventsByWindow)+len(snapshot.WindowMetadata)+1)
	for id := range snapshot.EventsByWindow {
		ids[id] = struct{}{}
	}
	for id := range snapshot.WindowMetadata {
		ids[id] = struct{}{}
	}
	if requestedWindowID != 0 {
		ids[requestedWindowID] = struct{}{}
	}
	return ids
}

func daemonExportLiveMetadata(osWindows []kitty.OSWindow, snapshot daemon.EventsExport, requestedWindowID int) map[int]daemon.WindowLiveMetadata {
	ids := daemonExportWindowIDs(snapshot, requestedWindowID)
	if len(ids) == 0 {
		return map[int]daemon.WindowLiveMetadata{}
	}

	sessions := session.ListFromOSWindows(osWindows)
	out := make(map[int]daemon.WindowLiveMetadata, len(ids))
	for id := range ids {
		live := daemon.WindowLiveMetadata{}
		osw, tab, win, ok := kitty.FindWindowLocationByID(osWindows, id)
		if !ok {
			out[id] = live
			continue
		}

		live.Found = true
		live.TabID = tab.ID
		live.OSWindowID = osw.ID
		live.TabTitle = tab.Title
		live.WindowTitle = win.Title
		if rs, ok := session.FindByWindowID(sessions, id); ok {
			live.BaseTitle = rs.Base
			live.IconState = rs.State
			live.Starred = rs.Starred
			live.Project = rs.Project
			live.Profile = rs.Profile
			live.Harness = rs.Harness
			live.Directory = rs.Directory
		} else {
			live.BaseTitle, live.IconState, live.Starred = palette.ParseTitle(tab.Title)
			live.Profile = win.UserVars.EffectiveProfile()
			live.Harness = win.UserVars.EffectiveHarness()
			live.Directory = win.UserVars.EffectiveDirectory()
			if live.Directory == "" {
				live.Directory = win.CWD
			}
		}
		out[id] = live
	}
	return out
}

func buildDaemonExportPayload(snapshot daemon.EventsExport, health daemon.Health, osWindows []kitty.OSWindow, requestedWindowID int, exportedAt time.Time) daemon.ExportEventsPayload {
	windows := make(map[string][]daemon.SessionEvent, len(snapshot.EventsByWindow)+1)
	for id, evs := range snapshot.EventsByWindow {
		cp := make([]daemon.SessionEvent, len(evs))
		copy(cp, evs)
		windows[strconv.Itoa(id)] = cp
	}
	if requestedWindowID != 0 {
		key := strconv.Itoa(requestedWindowID)
		if _, ok := windows[key]; !ok {
			windows[key] = []daemon.SessionEvent{}
		}
	}

	liveByID := daemonExportLiveMetadata(osWindows, snapshot, requestedWindowID)
	metaIDs := daemonExportWindowIDs(snapshot, requestedWindowID)
	windowMeta := make(map[string]daemon.ExportWindowMetadata, len(metaIDs))
	for id := range metaIDs {
		meta := daemon.ExportWindowMetadata{
			Live: liveByID[id],
		}
		if durable, ok := snapshot.WindowMetadata[id]; ok {
			meta.Stats = durable.Stats
			meta.Durable = durable.Durable
		}
		windowMeta[strconv.Itoa(id)] = meta
	}

	return daemon.ExportEventsPayload{
		SchemaVersion:       daemon.EventsExportSchemaVersion,
		ExportedAt:          exportedAt.Unix(),
		DaemonPID:           health.PID,
		DaemonUptimeSeconds: int64(health.Uptime / time.Second),
		Windows:             windows,
		WindowMeta:          windowMeta,
	}
}

var daemonExportEventsCmd = &cobra.Command{
	Use:   "export-events",
	Short: "Dump schema-v2 hook event history as JSON for investigation",
	Long: `Export the daemon's schema-v2 investigation snapshot as JSON.

The payload includes top-level export metadata, the legacy windows map of
SessionEvent arrays, and window_meta entries with event stats, durable session
identity, and best-effort live Kitty/session metadata.`,
	Example: `  amx daemon export-events
  amx daemon export-events --window 42
  amx daemon export-events --pretty=false --output events.json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		c := newDaemonExportClient()
		snapshot, err := c.ExportEvents(daemonExportEventsWindow)
		if err != nil {
			return fmt.Errorf("daemon not running: %w", err)
		}

		var osWindows []kitty.OSWindow
		if ids := daemonExportWindowIDs(snapshot, daemonExportEventsWindow); len(ids) > 0 {
			if liveWindows, liveErr := newDaemonExportKitty().ListWindows(); liveErr == nil {
				osWindows = liveWindows
			}
		}

		payload := buildDaemonExportPayload(snapshot, c.Health(), osWindows, daemonExportEventsWindow, daemonExportNow())

		var (
			data      []byte
			encodeErr error
		)
		if daemonExportEventsPretty {
			data, encodeErr = json.MarshalIndent(payload, "", "  ")
		} else {
			data, encodeErr = json.Marshal(payload)
		}
		if encodeErr != nil {
			return fmt.Errorf("encode events: %w", encodeErr)
		}
		data = append(data, '\n')

		if daemonExportEventsOutput == "" {
			_, err = cmd.OutOrStdout().Write(data)
			return err
		}
		return os.WriteFile(daemonExportEventsOutput, data, 0o600)
	},
}

func init() {
	daemonExportEventsCmd.Flags().IntVar(&daemonExportEventsWindow, "window", 0, "Restrict export to a single Kitty window ID")
	daemonExportEventsCmd.Flags().StringVar(&daemonExportEventsOutput, "output", "", "Write JSON to this file instead of stdout")
	daemonExportEventsCmd.Flags().BoolVar(&daemonExportEventsPretty, "pretty", true, "Pretty-print JSON with 2-space indent")

	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonStatusCmd, daemonRestartCmd, daemonExportEventsCmd)
}
