package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/spf13/cobra"
)

var (
	debugLoadConfig  = loadRunnableConfig
	debugPaths       = diagnostics.DefaultPaths
	debugExecutable  = os.Executable
	debugNow         = time.Now
	debugDaemon      = func() debugger.DaemonHealthClient { return daemon.NewClient() }
	debugKitty       = func() debugger.KittyWindowLister { return kitty.NewClient(kitty.NewRealRunner()) }
	debugProbeClient = func() debugger.ProbeClient { return daemon.NewClient() }
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

func runDebugTail(cmd *cobra.Command, _ []string) error {
	now := debugNow().UTC()
	selector, err := debugSelector(now)
	if err != nil {
		return err
	}
	if debugTailLimit < 0 {
		return fmt.Errorf("--last must not be negative")
	}
	paths, err := debugPaths()
	if err != nil {
		return err
	}
	policy := config.DiagnosticsConfig{}
	if cfg, configErr := debugLoadConfig(); configErr == nil && cfg != nil {
		policy = cfg.Defaults.Diagnostics
	}
	journal, err := debugNewJournal(paths, diagnostics.JournalOptions{
		KeepDays: policy.JournalKeepDays, MaxBytes: int64(policy.JournalMaxMiB) << 20,
	})
	if err != nil {
		return err
	}
	options := debugger.TailOptions{Selector: selector, Limit: debugTailLimit}
	if !debugTailFollow {
		report, err := debugger.Tail(cmd.Context(), journal, options)
		if err != nil {
			return err
		}
		if debugTailJSON {
			return writeIndentedJSON(cmd.OutOrStdout(), report)
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Agent Mux debug tail (schema %d; records=%d; scanned=%d; corrupt=%d)\n",
			report.SchemaVersion, len(report.Records), report.JournalSnapshot.RecordsScanned, report.JournalSnapshot.CorruptLines); err != nil {
			return err
		}
		for _, record := range report.Records {
			if err := writeDebugRecord(cmd.OutOrStdout(), record); err != nil {
				return err
			}
		}
		return nil
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !debugTailJSON {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Agent Mux debug tail follow (schema %d)\n", debugger.TailSchemaVersion); err != nil {
			return err
		}
	}
	err = debugger.FollowWithMetadata(ctx, journal, options, func(record debugger.TailFollowRecord) error {
		if debugTailJSON {
			return writeJSONLine(cmd.OutOrStdout(), record)
		}
		return writeDebugRecord(cmd.OutOrStdout(), record.Record)
	})
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func runDebugProbe(cmd *cobra.Command, _ []string) error {
	now := debugNow().UTC()
	selector, err := debugSelector(now)
	if err != nil {
		return err
	}
	windowID := selector.WindowID
	if windowID == 0 {
		windowID = windowIDFromEnv()
	}
	bin, _ := debugExecutable()
	report := debugger.Probe(debugProbeClient(), debugger.ProbeOptions{
		WindowID: windowID, Harness: selector.Harness, TraceID: selector.TraceID, RequestID: selector.RequestID, AMXBin: bin,
	})
	if debugProbeJSON {
		if err := writeIndentedJSON(cmd.OutOrStdout(), report); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Agent Mux debug probe (schema %d)\ntrace: %s\nrequest: %s\n",
			report.SchemaVersion, report.TraceID, report.RequestID); err != nil {
			return err
		}
		for _, stage := range report.Stages {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "- %s attempt=%d outcome=%s reason=%s latency=%.2fms\n",
				stage.Stage, stage.Attempt, stage.Outcome, emptyDash(stage.Reason), stage.LatencyMS); err != nil {
				return err
			}
		}
		if report.Delivery != nil {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "route: %s source=%d target=%d producer=%s target_harness=%s profile=%s snapshot_ready=%t window_found=%t\n",
				report.Delivery.RouteDecision, report.Delivery.SourceWindowID, report.Delivery.TargetWindowID,
				emptyDash(report.Delivery.ProducerHarness), emptyDash(report.Delivery.TargetHarness),
				emptyDash(report.Delivery.Profile), report.Delivery.SnapshotReady, report.Delivery.WindowFound); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "result: success=%t\nscope: %s\n", report.Success, report.Scope); err != nil {
			return err
		}
		if report.Error != "" {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "error: %s\n", report.Error); err != nil {
				return err
			}
		}
	}
	if !report.Success {
		return &ExitError{Code: exitHealthFailure, Err: errors.New(report.Error), Quiet: debugProbeJSON}
	}
	return nil
}

func runDebugExplain(cmd *cobra.Command, _ []string) error {
	now := debugNow().UTC()
	selector, err := debugSelector(now)
	if err != nil {
		return err
	}
	if debugExplainLast < 0 {
		return fmt.Errorf("--last must not be negative")
	}
	if debugExplainGraphDepth < debugger.MinExplainGraphDepth || debugExplainGraphDepth > debugger.MaxExplainGraphDepth {
		return fmt.Errorf("--graph-depth must be between %d and %d", debugger.MinExplainGraphDepth, debugger.MaxExplainGraphDepth)
	}
	if debugExplainMaxNodes < debugger.MinExplainMaxNodes || debugExplainMaxNodes > debugger.MaxExplainMaxNodes {
		return fmt.Errorf("--max-nodes must be between %d and %d", debugger.MinExplainMaxNodes, debugger.MaxExplainMaxNodes)
	}
	if debugExplainMaxEdges < debugger.MinExplainMaxEdges || debugExplainMaxEdges > debugger.MaxExplainMaxEdges {
		return fmt.Errorf("--max-edges must be between %d and %d", debugger.MinExplainMaxEdges, debugger.MaxExplainMaxEdges)
	}
	paths, err := debugPaths()
	if err != nil {
		return err
	}
	cfg, configErr := debugLoadConfig()
	policy := config.DiagnosticsConfig{}
	var hookReport *debugger.HookReport
	if configErr == nil && cfg != nil {
		policy = cfg.Defaults.Diagnostics
		bin, executableErr := debugExecutable()
		if executableErr == nil {
			report := debugger.InspectHooks(cfg, bin, now)
			hookReport = &report
		} else {
			configErr = fmt.Errorf("resolving amx binary: %w", executableErr)
		}
	}
	journal, err := debugNewJournal(paths, diagnostics.JournalOptions{
		KeepDays: policy.JournalKeepDays, MaxBytes: int64(policy.JournalMaxMiB) << 20,
	})
	if err != nil {
		return err
	}
	statusError := ""
	if configErr != nil {
		statusError = "hook activation evidence unavailable: " + configErr.Error()
	}
	report, err := debugger.Explain(cmd.Context(), journal, debugger.ExplainOptions{
		Selector: selector, Recent: debugExplainLast,
		GraphDepth: debugExplainGraphDepth, MaxNodes: debugExplainMaxNodes, MaxEdges: debugExplainMaxEdges,
		Hooks: hookReport, StatusError: statusError, Now: now,
	})
	if err != nil {
		return err
	}
	if debugExplainJSON {
		return writeIndentedJSON(cmd.OutOrStdout(), report)
	}
	return writeDebugExplainText(cmd.OutOrStdout(), report)
}

func writeDebugExplainText(out io.Writer, report debugger.ExplanationReport) error {
	if _, err := fmt.Fprintf(out, "Agent Mux debug explain (schema %d)\nclassification: %s (%s)\nsummary: %s\n",
		report.SchemaVersion, report.Cause.Classification, report.Cause.Certainty, report.Cause.Summary); err != nil {
		return err
	}
	if report.Cause.FirstFailedStage != "" {
		if _, err := fmt.Fprintf(out, "first failed stage: %s\n", report.Cause.FirstFailedStage); err != nil {
			return err
		}
	}
	if report.Cause.FirstMissingStage != "" {
		if _, err := fmt.Fprintf(out, "first missing stage: %s (uncertain)\n", report.Cause.FirstMissingStage); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(out, "evidence graph: nodes=%d edges=%d depth=%d max_nodes=%d max_edges=%d scanned=%d corrupt=%d\n",
		len(report.Graph.Nodes), len(report.Graph.Edges), report.Limits.GraphDepth, report.Limits.MaxNodes, report.Limits.MaxEdges,
		report.JournalSnapshot.RecordsScanned, report.JournalSnapshot.CorruptLines); err != nil {
		return err
	}
	if report.Truncation.Truncated {
		if _, err := fmt.Fprintf(out, "warning: evidence graph truncated (%s; pruned node candidates=%d edge candidates=%d)\n",
			strings.Join(report.Truncation.Reasons, ","), report.Truncation.PrunedNodeCandidates, report.Truncation.PrunedEdgeCandidates); err != nil {
			return err
		}
	}
	for _, node := range report.Graph.Nodes {
		if node.Record == nil {
			continue
		}
		record := node.Record
		event := record.Evidence["event"]
		if _, err := fmt.Fprintf(out, "- [%s] %s stage=%s outcome=%s reason=%s harness=%s profile=%s window=%d session=%s asl_run=%s event=%s request=%s trace=%s\n",
			node.ID, record.ObservedAt.UTC().Format(time.RFC3339Nano), record.Stage, emptyDash(record.Outcome),
			emptyDash(record.Reason), emptyDash(record.Harness), emptyDash(record.Profile), record.WindowID,
			emptyDash(record.SessionID), emptyDash(record.RunID), emptyDash(event), emptyDash(record.RequestID), record.TraceID); err != nil {
			return err
		}
	}
	if len(report.ExpectedSequences) > 0 {
		if _, err := fmt.Fprintln(out, "expected sequences:"); err != nil {
			return err
		}
		for _, sequence := range report.ExpectedSequences {
			if _, err := fmt.Fprintf(out, "- %s scope=%s status=%s observed=%s missing=%s evidence=%s\n",
				sequence.Kind, sequence.Scope, sequence.Status, strings.Join(sequence.ObservedSteps, " -> "),
				emptyDash(strings.Join(sequence.MissingSteps, ",")), strings.Join(sequence.EvidenceIDs, ",")); err != nil {
				return err
			}
		}
	}
	if len(report.SuggestedCommands) > 0 {
		if _, err := fmt.Fprintln(out, "suggested next commands:"); err != nil {
			return err
		}
		for _, command := range report.SuggestedCommands {
			if _, err := fmt.Fprintf(out, "- %s\n", command); err != nil {
				return err
			}
		}
	}
	for _, warning := range report.Warnings {
		if _, err := fmt.Fprintf(out, "warning: %s\n", warning); err != nil {
			return err
		}
	}
	return nil
}

func writeDebugRecord(out io.Writer, record diagnostics.TraceEnvelope) error {
	event := ""
	if record.Evidence != nil {
		event = record.Evidence["event"]
	}
	_, err := fmt.Fprintf(out, "- %s stage=%s outcome=%s reason=%s harness=%s profile=%s window=%d session=%s asl_run=%s event=%s request=%s trace=%s\n",
		record.ObservedAt.UTC().Format(time.RFC3339Nano), record.Stage, emptyDash(record.Outcome),
		emptyDash(record.Reason), emptyDash(record.Harness), emptyDash(record.Profile), record.WindowID,
		emptyDash(record.SessionID), emptyDash(record.RunID), emptyDash(event), emptyDash(record.RequestID), record.TraceID)
	return err
}

func writeIndentedJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func writeJSONLine(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
