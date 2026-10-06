package debug

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/spf13/cobra"
)

var (
	debugLoadConfig  = cli.LoadRunnableConfig
	debugPaths       = diagnostics.DefaultPaths
	debugExecutable  = os.Executable
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
var debugStatusJSON bool
var debugTailJSON bool
var debugTailFollow bool
var debugTailLimit int
var debugProbeJSON bool
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
}

var debugStatusCmd = &cobra.Command{
	Use:          "status",
	Short:        "Report contracts, activation, journal, native-log, and daemon health",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runDebugStatus,
}

var debugTailCmd = &cobra.Command{
	Use:          "tail",
	Short:        "Read or follow persisted diagnostic records without requiring the daemon",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runDebugTail,
}

var debugProbeCmd = &cobra.Command{
	Use:          "probe",
	Short:        "Probe Agent Mux transport and routing without changing session state",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runDebugProbe,
}

var debugExplainCmd = &cobra.Command{
	Use:          "explain",
	Short:        "Reconstruct an evidence-linked causal timeline and supported root cause",
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

	debugStatusCmd.Flags().BoolVar(&debugStatusJSON, "json", false, "write schema-versioned JSON")
	debugTailCmd.Flags().BoolVar(&debugTailJSON, "json", false, "write schema-versioned JSON")
	debugTailCmd.Flags().BoolVarP(&debugTailFollow, "follow", "f", false, "continue following new persisted records")
	debugTailCmd.Flags().IntVarP(&debugTailLimit, "last", "n", 50, "show the last N matching records (0 means all)")
	debugProbeCmd.Flags().BoolVar(&debugProbeJSON, "json", false, "write schema-versioned JSON")
	debugExplainCmd.Flags().BoolVar(&debugExplainJSON, "json", false, "write the complete schema-versioned evidence graph as JSON")
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
		if err := writeKittySnapshot(out, report.Daemon.KittySnapshot, true); err != nil {
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
			lastSeen = profile.LastSeen.Format(time.RFC3339)
		}
		if _, err := fmt.Fprintf(out, "hooks: %s/%s verdict=%s sync=%s activation=%s cli=%s contract=%s last_seen=%s silence=%ds\n",
			profile.Profile, profile.Harness, profile.Verdict, profile.Sync, profile.Activation,
			emptyDash(profile.CLIVersion), emptyDash(profile.VerifiedCLIVersion), lastSeen, profile.HookSilenceSeconds); err != nil {
			return err
		}
		for _, issue := range profile.Issues {
			if _, err := fmt.Fprintf(out, "  - [%s] %s\n", issue.Code, issue.Message); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintf(out, "journal: %s enabled=%t path=%s files=%d records=%d bytes=%d spool=%d spool_bytes=%d total_bytes=%d corrupt=%d quarantined=%d quota=%d\n",
		report.Journal.Verdict, report.Journal.Enabled, report.Journal.Path,
		report.Journal.Health.Files, report.Journal.Health.Records, report.Journal.Health.Bytes,
		report.Journal.Health.SpoolFiles, report.Journal.Health.SpoolBytes,
		report.Journal.Health.Bytes+report.Journal.Health.SpoolBytes, report.Journal.Health.CorruptLines,
		report.Journal.Health.QuarantinedFiles, report.Journal.MaxBytes); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "native logs: %s enabled=%t path=%s %s bytes=%d quota=%d active_excess=%d\n",
		report.Native.Verdict, report.Native.Enabled, report.Native.Path, report.Native.Summary(),
		report.Native.Storage.TotalBytes, report.Native.MaxBytes, report.Native.Storage.ActiveExcessBytes); err != nil {
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
