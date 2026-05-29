package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
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
		if err := daemonPing(); err != nil {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "daemon: not running (%s)\n", daemonSocketPath())
			return err
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "daemon: running (%s)\n", daemonSocketPath())
		return err
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
	daemonExportEventsWindow        int
	daemonExportEventsCurrentWindow bool
	daemonExportEventsLast          int
	daemonExportEventsOutput        string
	daemonExportEventsPretty        bool
	daemonPing                      = daemon.Ping
	daemonSocketPath                = daemon.SocketPath
	newDaemonExportClient           = func() daemonExportClient { return daemon.NewClient() }
	newDaemonExportKitty            = func() kittyWindowLister { return kitty.NewClient(kitty.NewRealRunner()) }
	daemonExportNow                 = time.Now
)

type daemonExportClient interface {
	ExportEvents(windowID int) (daemon.EventsExport, error)
	Health() daemon.Health
}

type kittyWindowLister interface {
	ListWindows() ([]kitty.OSWindow, error)
}

func daemonExportWindowIDs(snapshot daemon.EventsExport, requestedWindowID int) []int {
	seen := make(map[int]struct{}, len(snapshot.EventsByWindow)+len(snapshot.WindowMetadata)+1)
	ids := make([]int, 0, len(snapshot.EventsByWindow)+len(snapshot.WindowMetadata)+1)
	add := func(id int) {
		if id == 0 {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for id := range snapshot.EventsByWindow {
		add(id)
	}
	for id := range snapshot.WindowMetadata {
		add(id)
	}
	add(requestedWindowID)
	sort.Ints(ids)
	return ids
}

func daemonExportLiveMetadata(osWindows []kitty.OSWindow, snapshot daemon.EventsExport, requestedWindowID int) map[int]daemon.WindowLiveMetadata {
	ids := daemonExportWindowIDs(snapshot, requestedWindowID)
	if len(ids) == 0 {
		return map[int]daemon.WindowLiveMetadata{}
	}

	sessions := session.ListFromOSWindows(osWindows)
	out := make(map[int]daemon.WindowLiveMetadata, len(ids))
	for _, id := range ids {
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

func daemonExportTrimEvents(events []daemon.SessionEvent, last int) []daemon.SessionEvent {
	if last > 0 && len(events) > last {
		events = events[len(events)-last:]
	}
	out := make([]daemon.SessionEvent, len(events))
	copy(out, events)
	return out
}

func buildDaemonExportPayload(snapshot daemon.EventsExport, health daemon.Health, osWindows []kitty.OSWindow, requestedWindowID int, last int, exportedAt time.Time) daemon.ExportEventsPayload {
	liveByID := daemonExportLiveMetadata(osWindows, snapshot, requestedWindowID)
	ids := daemonExportWindowIDs(snapshot, requestedWindowID)
	windows := make([]daemon.ExportWindow, 0, len(ids))
	for _, id := range ids {
		window := daemon.ExportWindow{
			WindowID: id,
			Live:     liveByID[id],
			Events:   []daemon.SessionEvent{},
		}
		if durable, ok := snapshot.WindowMetadata[id]; ok {
			window.Stats = durable.Stats
			window.Durable = durable.Durable
		}
		if events, ok := snapshot.EventsByWindow[id]; ok {
			window.Events = daemonExportTrimEvents(events, last)
		}
		windows = append(windows, window)
	}

	return daemon.ExportEventsPayload{
		SchemaVersion: daemon.EventsExportSchemaVersion,
		ExportedAt:    exportedAt.Unix(),
		Daemon: daemon.ExportDaemonMetadata{
			PID:           health.PID,
			UptimeSeconds: int64(health.Uptime / time.Second),
		},
		Windows: windows,
	}
}

func daemonExportCurrentWindowID() (int, error) {
	s := os.Getenv("KITTY_WINDOW_ID")
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
	Short: "Dump schema-v4 hook event history as JSON for investigation",
	Long: `Export the daemon's schema-v4 investigation snapshot as JSON.

The payload is organized as a per-window array so each entry carries its own
event list, durable daemon metadata, and best-effort live Kitty/session
metadata without paired map lookups.`,
	Example: `  amx daemon export-events
  amx daemon export-events --current-window
  amx daemon export-events --current-window --last 25
  amx daemon export-events --window 42
  amx daemon export-events --pretty=false --output events.json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if daemonExportEventsLast < 0 {
			return fmt.Errorf("--last must be >= 0")
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
		if ids := daemonExportWindowIDs(snapshot, requestedWindowID); len(ids) > 0 {
			if liveWindows, liveErr := newDaemonExportKitty().ListWindows(); liveErr == nil {
				osWindows = liveWindows
			}
		}

		payload := buildDaemonExportPayload(snapshot, c.Health(), osWindows, requestedWindowID, daemonExportEventsLast, daemonExportNow())

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
		// Expand a leading ~/ so users can pass shell-style paths via flags.
		// Without this, --output ~/events.json would write a literal
		// "~/events.json" file in the current working directory instead of the
		// user's home directory.
		return os.WriteFile(config.ExpandTilde(daemonExportEventsOutput), data, 0o600)
	},
}

func init() {
	daemonExportEventsCmd.Flags().IntVar(&daemonExportEventsWindow, "window", 0, "Restrict export to a single Kitty window ID")
	daemonExportEventsCmd.Flags().BoolVar(&daemonExportEventsCurrentWindow, "current-window", false, "Resolve the target window from $KITTY_WINDOW_ID")
	daemonExportEventsCmd.Flags().IntVar(&daemonExportEventsLast, "last", 0, "Keep only the last N events per exported window (0 = no limit)")
	daemonExportEventsCmd.Flags().StringVar(&daemonExportEventsOutput, "output", "", "Write JSON to this file instead of stdout")
	daemonExportEventsCmd.Flags().BoolVar(&daemonExportEventsPretty, "pretty", true, "Pretty-print JSON with 2-space indent")

	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonStatusCmd, daemonRestartCmd, daemonExportEventsCmd)
}
