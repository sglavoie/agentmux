package debug

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

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/hooks"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/spf13/cobra"
)

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
	windowID, runtimeSession, err := debugSessionTarget(selector.WindowID)
	if err != nil {
		return err
	}
	bin, _ := debugExecutable()
	socket := debugProbeKittySocket
	if socket == "" {
		socket = kitty.NormalizeListenAddress(debugGetenv("KITTY_LISTEN_ON"), debugGetenv("KITTY_PID"))
	}
	report := debugger.Probe(debugProbeClient(), debugger.ProbeOptions{
		WindowID: windowID, KittyListenOn: socket, RuntimeSessionID: runtimeSession, Harness: selector.Harness, TraceID: selector.TraceID, RequestID: selector.RequestID, AMXBin: bin,
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
			if report.Delivery.RuntimeSessionID != "" {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "runtime session: %s\n", report.Delivery.RuntimeSessionID); err != nil {
					return err
				}
			}
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
		return &cli.ExitError{Code: cli.ExitHealthFailure, Err: errors.New(report.Error), Quiet: debugProbeJSON}
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
			bin, _ = hooks.ExpectedBinary(bin, debugLookPath)
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
