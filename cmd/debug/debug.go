// Package debug holds the daemon-state diagnostic surfaces and the vocabulary
// they share: `amx debug` (status, tail, probe, explain, capture, bundle,
// replay, and `subscribe`, which opens a companion stream exactly as an app
// does and prints each frame, one schema-v1 record per line under --json),
// `amx doctor` with its stable finding identifiers and versioned report, the
// invariant evaluator both run, and `amx daemon audit`, which is a doctor
// surface wearing a daemon subcommand name.
//
// It also owns the export projections (ExportWindowIDs, ExportLiveMetadata,
// ASLStatusLine) that doctor and the cmd/daemon command family must agree on,
// so a doctor cannot report drift that `daemon export-events` does not see.
package debug

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/spf13/cobra"
)

var (
	debugLoadConfig  = cli.LoadRunnableConfig
	debugPaths       = diagnostics.DefaultPaths
	debugExecutable  = os.Executable
	debugLookPath    = exec.LookPath
	debugNow         = time.Now
	debugDaemon      = func() debugger.DaemonHealthClient { return ipc.NewClient() }
	debugKitty       = func() debugger.KittyWindowLister { return cli.NewKittyClient() }
	debugProbeClient = func() debugger.ProbeClient { return ipc.NewClient() }
	debugNewJournal  = diagnostics.NewJournal
)

type debugSelectorFlagValues struct {
	harness       string
	profile       string
	windowID      int
	sessionID     string
	aslRunID      string
	event         string
	traceID       string
	requestID     string
	sourceEventID string
	outcomes      []string
	stages        []string
	after         string
	before        string
}

var debugSelectorFlags debugSelectorFlagValues

// debugStageNames and debugOutcomeNames are what <Tab> offers for --stage and
// --outcome: the stages and outcomes the journal records most, not a closed
// vocabulary. A selector still matches any value.
var (
	debugStageNames = []string{
		"hook.delivery", "hook.failure", "hook.ipc_attempt", "hook.rejection", "hook.validation",
		"hook.window_resolution", "hook.runtime_session", "hook.acknowledgement",
		"daemon.decision", "daemon.lifecycle", "daemon.process_lifecycle", "daemon.renderer.rendered",
		"daemon.renderer.failed", "daemon.runtime_reconcile", "daemon.kitty_snapshot",
		"session.launch", "asl.lifecycle.delivered", "maintenance.contention", "snapshot.consumer",
	}
	debugOutcomeNames = []string{
		"accepted", "success", "error", "failed", "pending", "suppressed", "sent", "delivered",
		"retryable_failure", "permanent_failure", "exhausted", "stale", "superseded",
	}
)

// debugRuntimeSession is --runtime-session on the two commands that act on
// one live session, probe and capture start.
var debugRuntimeSession string

// debugGetenv is the environment a command reads its current session from.
var debugGetenv = os.Getenv

const debugRuntimeSessionFlag = "runtime-session"

func addDebugRuntimeSessionFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&debugRuntimeSession, debugRuntimeSessionFlag, "",
		"select a runtime session ID (default: $"+runtimesession.EnvVar+" when set, else the Kitty window of --window or $KITTY_WINDOW_ID)")
}

// debugSessionTarget is the one live session probe and capture act on: the
// runtime session or the Kitty window that was named, and otherwise the
// session the command is typed in — its runtime session when it has one, else
// its Kitty window, the rule `daemon export-events --current-window` follows.
// A pane of a tmux session started from a Kitty window inherits that window's
// ID, so the Kitty variable is believed only when no runtime session is named.
// Both results are zero when nothing names a session.
func debugSessionTarget(windowID int) (int, string, error) {
	named := strings.TrimSpace(debugRuntimeSession)
	if named != "" && windowID != 0 {
		return 0, "", fmt.Errorf("--%s cannot be used with --window", debugRuntimeSessionFlag)
	}
	if windowID != 0 {
		return windowID, "", nil
	}
	source := "--" + debugRuntimeSessionFlag
	if named == "" {
		named, source = strings.TrimSpace(debugGetenv(runtimesession.EnvVar)), runtimesession.EnvVar
	}
	if named == "" {
		return cli.WindowIDFromEnv(), "", nil
	}
	id, err := runtimesession.ParseID(named)
	if err != nil {
		return 0, "", fmt.Errorf("%s requires a valid runtime session ID, got %q; %s", source, named, cli.RuntimeSessionHint)
	}
	return 0, string(id), nil
}

var debugStatusJSON bool
var debugTailJSON bool
var debugTailFollow bool
var debugTailLimit int
var debugProbeJSON bool
var debugProbeKittySocket string
var debugExplainJSON bool
var debugExplainLast int
var debugExplainGraphDepth int
var debugExplainMaxNodes int
var debugExplainMaxEdges int

// Command returns the `debug` command tree so the root command can register
// it. Root assembly is the only consumer.
func Command() *cobra.Command { return debugCmd }

var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Inspect Agent Mux delivery, health, and persisted diagnostic traces",
	Long: `Investigate an icon, notification, or hook problem after the fact.

Agent Mux keeps a private causal journal and native harness logs. tail and
explain read them without the daemon; status asks the daemon for its health
without starting it; probe sends one side-effect-free diagnostic request.
Nothing here changes a session, the reducer, or Kitty. bundle packs the
evidence into a redacted archive and replay recomputes it offline.

The commands that read records take the same selectors (--harness, --trace,
--stage, --after, ...), listed under Selectors in their help. Their --json
output is a report with its own schema version, not the common envelope.
docs/debugging.md walks through the workflow.`,
	Example: `  amx debug status
  amx debug tail --last 50 --harness claude
  amx debug explain --request <request-id>
  amx debug bundle --mode share -o agent-mux-debug.tar.gz`,
}

var debugStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report contracts, activation, journal, native-log, and daemon health",
	Long: `Report the health of everything diagnostics depend on: the daemon (asked
read-only, never started), the tmux backend, hook installation and last-seen
hook evidence per profile, the journal, and native logs, followed by findings
with stable identifiers. Selectors narrow the last-seen events it lists.`,
	Example: `  amx debug status
  amx debug status --profile work --json`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runDebugStatus,
}

var debugTailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Read or follow persisted diagnostic records without requiring the daemon",
	Long: `Print the newest journal records that match the selectors, oldest first, in
event-time order. --follow keeps printing new records as they are persisted
and needs no running daemon.

-n is short for --last here, as in tail(1); in ` + "`amx session list`" + ` -n is --limit.`,
	Example: `  amx debug tail -n 20
  amx debug tail --stage hook.delivery --outcome error --after 30m
  amx debug tail --follow --json`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runDebugTail,
}

var debugProbeCmd = &cobra.Command{
	Use:   "probe",
	Short: "Probe Agent Mux transport and routing without changing session state",
	Long: `Send one diagnostic request through the daemon the way a hook would, and
report each stage it passed, the route it took, and whether it succeeded. The
target is --runtime-session, or --window with --kitty-socket outside that
Kitty, and otherwise the session this is typed in. Nothing is delivered to the
session. It exits 2 when the probe fails.`,
	Example: `  amx debug probe
  amx debug probe --runtime-session rs-0123456789abcdef0123456789abcdef
  amx debug probe --window 12 --kitty-socket unix:/tmp/kitty-1234`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runDebugProbe,
}

var debugExplainCmd = &cobra.Command{
	Use:   "explain",
	Short: "Reconstruct an evidence-linked causal timeline and supported root cause",
	Long: `Rebuild what happened to the matching records from the journal alone: a
causal timeline per history, the root cause the evidence supports and how
certain it is, and with --json the evidence graph behind it. An exact ID
selector (--request, --trace, --source-event) seeds the graph, which follows
links up to --graph-depth hops within --max-nodes and --max-edges.

-n is short for --last here; in ` + "`amx session list`" + ` -n is --limit.`,
	Example: `  amx debug explain --source-event <source-event-id>
  amx debug explain --harness codex --after 1h -n 3
  amx debug explain --request <request-id> --json`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runDebugExplain,
}

func init() {
	flags := debugCmd.PersistentFlags()
	flags.StringVar(&debugSelectorFlags.harness, "harness", "", "select a producer harness")
	flags.StringVar(&debugSelectorFlags.profile, "profile", "", "select a configured profile")
	flags.IntVar(&debugSelectorFlags.windowID, "window", 0, "select a Kitty window ID")
	flags.StringVar(&debugSelectorFlags.sessionID, "session", "", "select a native harness session ID")
	flags.StringVar(&debugSelectorFlags.aslRunID, "asl-run", "", "select an ASL run ID")
	flags.StringVar(&debugSelectorFlags.event, "event", "", "select a harness event")
	flags.StringVar(&debugSelectorFlags.traceID, "trace", "", "select a diagnostic trace ID")
	flags.StringVar(&debugSelectorFlags.requestID, "request", "", "select a request ID")
	flags.StringVar(&debugSelectorFlags.sourceEventID, "source-event", "", "select an upstream source event ID")
	flags.StringSliceVar(&debugSelectorFlags.outcomes, "outcome", nil, "select one or more outcomes")
	flags.StringSliceVar(&debugSelectorFlags.stages, "stage", nil, "select one or more diagnostic stages")
	flags.StringVar(&debugSelectorFlags.after, "after", "", "lower time bound (RFC3339, Unix seconds, or duration such as 30m)")
	flags.StringVar(&debugSelectorFlags.before, "before", "", "upper time bound (RFC3339, Unix seconds, or duration such as 30m)")
	_ = debugCmd.RegisterFlagCompletionFunc("harness", cli.CompleteHarnesses)
	_ = debugCmd.RegisterFlagCompletionFunc("profile", cli.CompleteProfiles)
	_ = debugCmd.RegisterFlagCompletionFunc("stage", cli.FixedCompletions(debugStageNames...))
	_ = debugCmd.RegisterFlagCompletionFunc("outcome", cli.FixedCompletions(debugOutcomeNames...))

	cli.AddReportJSONFlag(debugStatusCmd, &debugStatusJSON, "the report")
	cli.AddReportJSONFlag(debugTailCmd, &debugTailJSON, "the report")
	debugTailCmd.Flags().BoolVarP(&debugTailFollow, "follow", "f", false, "continue following new persisted records")
	debugTailCmd.Flags().IntVarP(&debugTailLimit, "last", "n", 50, "show the last N matching records (0 means all)")
	cli.AddReportJSONFlag(debugProbeCmd, &debugProbeJSON, "the report")
	debugProbeCmd.Flags().StringVar(&debugProbeKittySocket, "kitty-socket", "", "Kitty socket for --window (defaults to KITTY_LISTEN_ON)")
	addDebugRuntimeSessionFlag(debugProbeCmd)
	cli.AddReportJSONFlag(debugExplainCmd, &debugExplainJSON, "the complete evidence graph")
	debugExplainCmd.Flags().IntVarP(&debugExplainLast, "last", "n", 10, "explain the last N matching causal histories")
	debugExplainCmd.Flags().IntVar(&debugExplainGraphDepth, "graph-depth", debugger.DefaultExplainGraphDepth, "follow causal links up to N hops (1-4)")
	debugExplainCmd.Flags().IntVar(&debugExplainMaxNodes, "max-nodes", debugger.DefaultExplainMaxNodes, "limit the evidence graph to N nodes (1-2000)")
	debugExplainCmd.Flags().IntVar(&debugExplainMaxEdges, "max-edges", debugger.DefaultExplainMaxEdges, "limit the evidence graph to N edges (1-8000)")

	debugCmd.AddCommand(debugStatusCmd, debugTailCmd, debugProbeCmd, debugExplainCmd)
}

func debugSelector(now time.Time) (debugger.Selector, error) {
	after, err := debugger.ParseTimeBound(debugSelectorFlags.after, now)
	if err != nil {
		return debugger.Selector{}, fmt.Errorf("--after: %w", err)
	}
	before, err := debugger.ParseTimeBound(debugSelectorFlags.before, now)
	if err != nil {
		return debugger.Selector{}, fmt.Errorf("--before: %w", err)
	}
	selector := debugger.Selector{
		Harness: debugSelectorFlags.harness, Profile: debugSelectorFlags.profile,
		WindowID: debugSelectorFlags.windowID, SessionID: debugSelectorFlags.sessionID,
		ASLRunID: debugSelectorFlags.aslRunID, Event: debugSelectorFlags.event,
		TraceID: debugSelectorFlags.traceID, RequestID: debugSelectorFlags.requestID,
		SourceEventID: debugSelectorFlags.sourceEventID, Outcomes: append([]string(nil), debugSelectorFlags.outcomes...),
		Stages: append([]string(nil), debugSelectorFlags.stages...), After: after, Before: before,
	}
	if err := selector.Normalize(); err != nil {
		return debugger.Selector{}, err
	}
	return selector, nil
}

func runDebugStatus(cmd *cobra.Command, _ []string) error {
	now := debugNow().UTC()
	selector, err := debugSelector(now)
	if err != nil {
		return err
	}
	cfg, configErr := debugLoadConfig()
	paths, pathsErr := debugPaths()
	bin, executableErr := debugExecutable()
	if executableErr != nil && configErr == nil {
		configErr = fmt.Errorf("resolving amx binary: %w", executableErr)
	}
	report, err := debugger.CollectStatus(cmd.Context(), debugger.StatusOptions{
		Config: cfg, ConfigError: configErr, Paths: paths, PathsError: pathsErr,
		Daemon: debugDaemon(), Kitty: debugKitty(), AMXBin: bin,
		CLIBuild: buildinfo.Current(), Now: now, Selector: selector,
		Backend: doctorBackendStatus(),
	})
	if err != nil {
		return err
	}
	if debugStatusJSON {
		return writeIndentedJSON(cmd.OutOrStdout(), report)
	}
	return writeDebugStatusText(cmd.OutOrStdout(), report)
}

func writeDebugStatusText(out io.Writer, report debugger.StatusReport) error {
	if _, err := fmt.Fprintf(out, "Agent Mux debug status (schema %d)\noverall: %s\n", report.SchemaVersion, report.Verdict); err != nil {
		return err
	}
	if report.Daemon.Alive {
		if _, err := fmt.Fprintf(out, "daemon: %s (pid %d, ready=%t, uptime=%s)\n", report.Daemon.Verdict, report.Daemon.PID, report.Daemon.Ready, time.Duration(report.Daemon.UptimeSeconds)*time.Second); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintf(out, "daemon: %s (offline; persisted status remains available)\n", report.Daemon.Verdict); err != nil {
		return err
	}
	if report.Daemon.Build != nil {
		if _, err := fmt.Fprintf(out, "daemon build: %s (matches CLI=%t)\n", report.Daemon.Build.String(), report.Daemon.BuildMatches != nil && *report.Daemon.BuildMatches); err != nil {
			return err
		}
	}
	// Only a daemon that answered has a snapshot to report. An offline daemon
	// already printed why it could not be asked, and adding "unreported" under
	// that would read as an old daemon rather than as no daemon.
	if report.Daemon.Alive {
		if err := writeKittySnapshot(out, report.Daemon.KittySnapshot, true, false); err != nil {
			return err
		}
	}
	// The same lines doctor prints, without its findings: status describes.
	discard := func(string, string, string, ...any) {}
	if backend, ok := report.Backend.(cli.BackendStatus); ok {
		if err := writeDoctorBackend(out, backend, discard); err != nil {
			return err
		}
	}
	if report.Daemon.Alive {
		if err := writeDoctorRuntime(out, report.Daemon.Runtime, discard); err != nil {
			return err
		}
	}
	if len(report.Hooks.Profiles) == 0 {
		if _, err := fmt.Fprintf(out, "hooks: %s (no matching configured profiles)\n", report.Hooks.Verdict); err != nil {
			return err
		}
	}
	for _, profile := range report.Hooks.Profiles {
		lastSeen := "never"
		if profile.LastSeen != nil {
			lastSeen = profile.LastSeen.Format(time.RFC3339) + " age=" + statusAge(report.GeneratedAt.Sub(*profile.LastSeen))
		}
		unused := ""
		if profile.Unused {
			unused = " unused=true"
		}
		if _, err := fmt.Fprintf(out, "hooks: %s/%s verdict=%s sync=%s activation=%s%s cli=%s contract=%s last_seen=%s silence_threshold=%s\n",
			profile.Profile, profile.Harness, profile.Verdict, profile.Sync, profile.Activation, unused,
			emptyDash(profile.CLIVersion), emptyDash(profile.VerifiedCLIVersion), lastSeen, secondsText(profile.HookSilenceSeconds)); err != nil {
			return err
		}
		if profile.Unused {
			if _, err := fmt.Fprintf(out, "  - %s\n", debugger.UnusedHookProfileDetail); err != nil {
				return err
			}
			continue
		}
		for _, issue := range profile.Issues {
			if _, err := fmt.Fprintf(out, "  - [%s] %s\n", issue.Code, issue.Message); err != nil {
				return err
			}
		}
	}
	journalTotal := report.Journal.Health.Bytes + report.Journal.Health.SpoolBytes
	if _, err := fmt.Fprintf(out, "journal: %s enabled=%t path=%s files=%d records=%d bytes=%s spool=%d spool_bytes=%s total_bytes=%s corrupt=%d quarantined=%d quota=%s%s\n",
		report.Journal.Verdict, report.Journal.Enabled, report.Journal.Path,
		report.Journal.Health.Files, report.Journal.Health.Records, statusBytes(report.Journal.Health.Bytes),
		report.Journal.Health.SpoolFiles, statusBytes(report.Journal.Health.SpoolBytes),
		statusBytes(journalTotal), report.Journal.Health.CorruptLines,
		report.Journal.Health.QuarantinedFiles, statusBytes(report.Journal.MaxBytes), quotaUsed(journalTotal, report.Journal.MaxBytes)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "native logs: %s enabled=%t path=%s %s bytes=%s quota=%s%s active_excess=%s\n",
		report.Native.Verdict, report.Native.Enabled, report.Native.Path, report.Native.Summary(),
		statusBytes(report.Native.Storage.TotalBytes), statusBytes(report.Native.MaxBytes),
		quotaUsed(report.Native.Storage.TotalBytes, report.Native.MaxBytes), statusBytes(report.Native.Storage.ActiveExcessBytes)); err != nil {
		return err
	}
	if len(report.Native.Sessions) > 0 {
		if _, err := fmt.Fprintln(out, "native sessions:"); err != nil {
			return err
		}
		for _, session := range report.Native.Sessions {
			if _, err := fmt.Fprintf(out, "- window=%d profile=%s harness=%s coverage=%s launch=%s session=%s asl_run=%s\n",
				session.WindowID, emptyDash(session.Profile), emptyDash(session.Harness), session.Coverage,
				emptyDash(session.LaunchID), emptyDash(session.HarnessSession), emptyDash(session.ASLRunID)); err != nil {
				return err
			}
		}
	}
	if len(report.LastSeenEvents) == 0 {
		if _, err := fmt.Fprintln(out, "last-seen events: none"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(out, "last-seen events:"); err != nil {
			return err
		}
		for _, record := range report.LastSeenEvents {
			if err := writeDebugRecord(out, record); err != nil {
				return err
			}
		}
	}
	if len(report.Findings) > 0 {
		if _, err := fmt.Fprintln(out, "findings:"); err != nil {
			return err
		}
		for _, finding := range report.Findings {
			if _, err := fmt.Fprintf(out, "- [%s/%s] %s\n", finding.Verdict, finding.Check, finding.Message); err != nil {
				return err
			}
		}
	}
	return nil
}
