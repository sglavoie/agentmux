package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/daemon/eventlog"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/process"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
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
			if _, err := fmt.Fprintf(out, "asl: %s\n", aslStatusLine(health.ASL)); err != nil {
				return err
			}
		}
		return nil
	},
}

// aslStatusLine renders the optional ASL capability block for text surfaces.
// It reports only availability, identity, and sanitized counters: no plan
// directory, question, or reply text ever reaches this line.
func aslStatusLine(s *aslbridge.Status) string {
	state := "unavailable"
	switch {
	case s.Mode == string(aslbridge.ModeDisabled):
		state = "disabled"
	case s.Available && s.MutationsEnabled:
		state = "ready"
	case s.Available:
		state = "read-only"
	}
	detail := "mode " + s.Mode
	if s.Executable != "" {
		detail += " at " + s.Executable
	} else if s.Command != "" {
		detail += " via " + s.Command
	}
	if s.Version != "" {
		detail += fmt.Sprintf("; asl %s protocol %d", s.Version, s.Protocol)
	}
	if s.SnapshotAgeSeconds != nil {
		age := fmt.Sprintf("; snapshot %ds old", *s.SnapshotAgeSeconds)
		if s.SnapshotStale {
			age += " (stale)"
		}
		detail += age
	}
	if s.RestartCount > 0 {
		detail += fmt.Sprintf("; restarts %d", s.RestartCount)
	}
	if outcomes := aslActionOutcomes(s.Metrics); outcomes != "" {
		detail += "; actions " + outcomes
	}
	if s.Reason != "" {
		detail += "; " + s.Reason
	}
	return fmt.Sprintf("%s (%s)", state, detail)
}

// aslActionOutcomes summarizes how the operator's mutations settled. The status
// vocabulary is fixed by the protocol, so the rendered order is fixed too and a
// counter key can never be bridge-authored text.
func aslActionOutcomes(m *aslbridge.Metrics) string {
	if m == nil {
		return ""
	}
	var parts []string
	for _, status := range []string{
		aslbridge.OperationAccepted,
		aslbridge.OperationConsumed,
		aslbridge.OperationRejected,
		aslbridge.OperationExpired,
		aslbridge.OperationSuperseded,
	} {
		if count := m.OperationsByStatus[status]; count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, status))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "/")
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

var (
	daemonExportEventsWindow        int
	daemonExportEventsCurrentWindow bool
	daemonExportEventsLast          int
	daemonExportEventsOutput        string
	daemonExportEventsPretty        bool
	daemonExportEventsSchema        int
	daemonSocketPath                = ipc.SocketPath
	newDaemonLifecycleClient        = func() daemonLifecycleClient { return ipc.NewClient() }
	newDaemonExportClient           = cli.NewDaemonExportClient
	newDaemonExportKitty            = func() cli.KittyWindowLister { return cli.NewKittyClient() }
	daemonExportNow                 = time.Now
)

type daemonLifecycleClient interface {
	Send(protocol.Request) (protocol.Response, error)
	RestartDetached(amxBin string) error
}

func daemonExportWindowIDs(snapshot protocol.EventsExport, requestedWindowID int) []int {
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

func daemonExportLiveMetadata(osWindows []kitty.OSWindow, snapshot protocol.EventsExport, requestedWindowID int) map[int]protocol.WindowLiveMetadata {
	ids := daemonExportWindowIDs(snapshot, requestedWindowID)
	if len(ids) == 0 {
		return map[int]protocol.WindowLiveMetadata{}
	}

	sessions := session.ListFromOSWindows(osWindows)
	out := make(map[int]protocol.WindowLiveMetadata, len(ids))
	for _, id := range ids {
		live := protocol.WindowLiveMetadata{}
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
			live.Looping = rs.Looping
			live.Starred = rs.Starred
			live.Project = rs.Project
			live.Profile = rs.Profile
			live.Harness = rs.Harness
			live.Directory = rs.Directory
		} else {
			parts := palette.ParseTitleParts(tab.Title)
			live.BaseTitle = parts.Base
			live.IconState = parts.State
			live.Looping = parts.Looping
			live.Starred = parts.Starred
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

func buildDaemonExportPayload(snapshot protocol.EventsExport, health ipc.Health, osWindows []kitty.OSWindow, requestedWindowID int, last int, exportedAt time.Time) protocol.ExportEventsPayload {
	liveByID := daemonExportLiveMetadata(osWindows, snapshot, requestedWindowID)
	ids := daemonExportWindowIDs(snapshot, requestedWindowID)
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
	Long: `Export the daemon's schema-v12 investigation snapshot as JSON.

The payload is organized as a per-window array so each entry carries its own
event list, durable daemon metadata, and best-effort live Kitty/session
metadata without paired map lookups. Use --schema 11 for the pre-busy-anchor
shape, --schema 10 for the pre-stop-hold shape, --schema 9 for the previous
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
		if ids := daemonExportWindowIDs(snapshot, requestedWindowID); len(ids) > 0 {
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

func init() {
	daemonExportEventsCmd.Flags().IntVar(&daemonExportEventsWindow, "window", 0, "Restrict export to a single Kitty window ID")
	daemonExportEventsCmd.Flags().BoolVar(&daemonExportEventsCurrentWindow, "current-window", false, "Resolve the target window from $KITTY_WINDOW_ID")
	daemonExportEventsCmd.Flags().IntVar(&daemonExportEventsLast, "last", 0, "Keep only the last N events per exported window (0 = no limit)")
	daemonExportEventsCmd.Flags().StringVar(&daemonExportEventsOutput, "output", "", "Write JSON to this file instead of stdout")
	daemonExportEventsCmd.Flags().BoolVar(&daemonExportEventsPretty, "pretty", true, "Pretty-print JSON with 2-space indent")
	daemonExportEventsCmd.Flags().IntVar(&daemonExportEventsSchema, "schema", protocol.EventsExportSchemaVersion, "Export schema version (8, 9, 10, 11, 12, or 13)")

	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonStatusCmd, daemonRestartCmd, daemonExportEventsCmd)
}
