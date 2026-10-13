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
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/spf13/cobra"
)

var (
	daemonExportEventsWindow        int
	daemonExportEventsCurrentWindow bool
	daemonExportEventsRuntime       string
	daemonExportEventsLast          int
	daemonExportEventsOutput        string
	daemonExportEventsPretty        bool
	daemonExportEventsSchema        int
	// newDaemonExportClient is also how `daemon status` reads health: the
	// export client is this family's one daemon-query seam.
	newDaemonExportClient = cli.NewDaemonExportClient
	newDaemonExportKitty  = func() cli.KittyWindowLister { return cli.NewKittyClient() }
	// newDaemonExportRuntime lists what runs on the private tmux server now,
	// for the live half of the slots runtime sessions hold. It starts no
	// server: none running lists nothing.
	newDaemonExportRuntime = func() ([]session.RunningSession, error) {
		server, err := cli.NewTmuxBackend()
		if err != nil {
			return nil, err
		}
		panes, err := server.Discover()
		if err != nil {
			return nil, err
		}
		return session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, nil), nil
	}
	daemonExportNow = time.Now
)

func buildDaemonExportPayload(snapshot protocol.EventsExport, health ipc.Health, osWindows []kitty.OSWindow, running []session.RunningSession, requestedWindowID int, last int, exportedAt time.Time) protocol.ExportEventsPayload {
	liveByID := debug.ExportLiveMetadata(osWindows, snapshot, requestedWindowID)
	debug.ExportRuntimeLiveMetadata(liveByID, running, snapshot)
	ids := debug.ExportWindowIDs(snapshot, requestedWindowID)
	windows := make([]protocol.ExportWindow, 0, len(ids))
	for _, id := range ids {
		window := protocol.ExportWindow{
			WindowID: id,
			Live:     liveByID[id],
			Events:   []protocol.SessionEvent{},
		}
		if bound, ok := snapshot.RuntimeSlots[id]; ok {
			window.RuntimeSessionID = bound.RuntimeSessionID
			window.Backend = bound.Backend
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

// daemonExportTarget is what one export is restricted to: a Kitty window, a
// runtime session, or neither (everything).
type daemonExportTarget struct {
	windowID int
	runtime  string
}

// runtimeSessionExporter is the part of the daemon client that exports by
// runtime session. It is asked for separately so the three families sharing
// cli.DaemonExportClient do not all have to grow a method only this one uses.
type runtimeSessionExporter interface {
	ExportRuntimeSessionEvents(runtimeSessionID string) (protocol.EventsExport, error)
}

func daemonExportRuntimeSession(raw, source string) (daemonExportTarget, error) {
	id, err := runtimesession.ParseID(raw)
	if err != nil {
		return daemonExportTarget{}, fmt.Errorf("%s requires a valid runtime session ID, got %q", source, raw)
	}
	return daemonExportTarget{runtime: string(id)}, nil
}

// daemonExportCurrentTarget names the session this command was typed in. A
// managed session says so itself (AMX_RUNTIME_SESSION_ID) and that wins: a
// tmux pane inherits whatever KITTY_WINDOW_ID the launching terminal had, so
// the Kitty variable is only believed when no runtime session is named. This
// is the rule the hook client follows.
func daemonExportCurrentTarget() (daemonExportTarget, error) {
	if raw := os.Getenv(runtimesession.EnvVar); raw != "" {
		return daemonExportRuntimeSession(raw, "--current-window with "+runtimesession.EnvVar)
	}
	s := os.Getenv(cli.WindowIDEnv)
	if s == "" {
		return daemonExportTarget{}, fmt.Errorf("--current-window requires %s or KITTY_WINDOW_ID to be set", runtimesession.EnvVar)
	}
	id, err := strconv.Atoi(s)
	if err != nil || id <= 0 {
		return daemonExportTarget{}, fmt.Errorf("--current-window requires a valid KITTY_WINDOW_ID, got %q", s)
	}
	return daemonExportTarget{windowID: id}, nil
}

func resolveDaemonExportTarget(cmd *cobra.Command) (daemonExportTarget, error) {
	window := daemonExportEventsWindow != 0 || cmd.Flags().Changed("window")
	runtime := daemonExportEventsRuntime != "" || cmd.Flags().Changed("runtime-session")
	if daemonExportEventsCurrentWindow && window {
		return daemonExportTarget{}, fmt.Errorf("--window and --current-window cannot be used together")
	}
	if runtime && (window || daemonExportEventsCurrentWindow) {
		return daemonExportTarget{}, fmt.Errorf("--runtime-session cannot be used with --window or --current-window")
	}
	if daemonExportEventsWindow < 0 {
		return daemonExportTarget{}, fmt.Errorf("--window must be >= 0")
	}
	if runtime {
		return daemonExportRuntimeSession(daemonExportEventsRuntime, "--runtime-session")
	}
	if daemonExportEventsCurrentWindow {
		return daemonExportCurrentTarget()
	}
	return daemonExportTarget{windowID: daemonExportEventsWindow}, nil
}

// exportDaemonEvents asks the daemon for the target's snapshot and returns
// the slot the export is filed under: the Kitty window asked for, or the slot
// the daemon holds for the runtime session.
func exportDaemonEvents(c cli.DaemonExportClient, target daemonExportTarget) (protocol.EventsExport, int, error) {
	if target.runtime == "" {
		snapshot, err := c.ExportEvents(target.windowID)
		return snapshot, target.windowID, err
	}
	exporter, ok := c.(runtimeSessionExporter)
	if !ok {
		return protocol.EventsExport{}, 0, fmt.Errorf("this daemon client cannot export by runtime session")
	}
	snapshot, err := exporter.ExportRuntimeSessionEvents(target.runtime)
	if err != nil {
		return protocol.EventsExport{}, 0, err
	}
	for slot, bound := range snapshot.RuntimeSlots {
		if bound.RuntimeSessionID == target.runtime {
			return snapshot, slot, nil
		}
	}
	return protocol.EventsExport{}, 0, fmt.Errorf("the daemon named no slot for runtime session %s", target.runtime)
}

var daemonExportEventsCmd = &cobra.Command{
	Use:   "export-events",
	Short: "Dump hook event history as JSON for investigation",
	Long: `Export the daemon's schema-v13 investigation snapshot as JSON.

The payload is organized as a per-window array so each entry carries its own
event list, durable daemon metadata, and best-effort live Kitty/session
metadata without paired map lookups. An entry held by a runtime session names
it (runtime_session_id, backend); for a tmux session window_id is the daemon's
own slot for it, not a Kitty window.

--current-window selects the session the command is typed in: the runtime
session named by AMX_RUNTIME_SESSION_ID when there is one, else the Kitty
window named by KITTY_WINDOW_ID. --runtime-session selects one by ID. Use --schema 12 for the pre-screen-decline
shape, --schema 11 for the pre-busy-anchor shape, --schema 10 for the pre-stop-hold shape, --schema 9 for the previous
complete causal shape, or --schema 8 for pre-causal consumers.`,
	Example: `  amx daemon export-events
  amx daemon export-events --current-window
  amx daemon export-events --current-window --last 25
  amx daemon export-events --window 42
  amx daemon export-events --runtime-session rs-0123456789abcdef0123456789abcdef
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
		target, err := resolveDaemonExportTarget(cmd)
		if err != nil {
			return err
		}

		c := newDaemonExportClient()
		snapshot, requestedWindowID, err := exportDaemonEvents(c, target)
		if err != nil {
			if !c.Health().Alive {
				return fmt.Errorf("daemon not running: %w", err)
			}
			return fmt.Errorf("export events: %w", err)
		}

		// The live half of an entry comes from where its session runs: a
		// Kitty window from Kitty, a runtime session on another backend from
		// that backend. Neither is asked when no exported entry is its own,
		// so an export of tmux sessions runs with no Kitty at all. Both are
		// best effort: an entry whose terminal cannot be read is not-found.
		var osWindows []kitty.OSWindow
		var running []session.RunningSession
		inKitty, offKitty := false, false
		for _, id := range debug.ExportWindowIDs(snapshot, requestedWindowID) {
			if slot, ok := snapshot.RuntimeSlots[id]; ok && debug.RuntimeSlotOffKitty(slot) {
				offKitty = true
			} else {
				inKitty = true
			}
		}
		if inKitty {
			if liveWindows, liveErr := newDaemonExportKitty().ListWindows(); liveErr == nil {
				osWindows = liveWindows
			}
		}
		if offKitty {
			if listed, liveErr := newDaemonExportRuntime(); liveErr == nil {
				running = listed
			}
		}

		payload := buildDaemonExportPayload(snapshot, c.Health(), osWindows, running, requestedWindowID, daemonExportEventsLast, daemonExportNow())
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
