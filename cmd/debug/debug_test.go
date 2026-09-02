package debug

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
)

type debugTestDaemon struct{ health ipc.Health }

func (d debugTestDaemon) Health() ipc.Health { return d.health }

type debugTestKitty struct{ err error }

func (k debugTestKitty) ListWindows() ([]kitty.OSWindow, error) { return nil, k.err }

type debugTestProbe struct{}

type cancelBuffer struct {
	bytes.Buffer
	cancel context.CancelFunc
	once   sync.Once
}

func (w *cancelBuffer) Write(data []byte) (int, error) {
	written, err := w.Buffer.Write(data)
	w.once.Do(w.cancel)
	return written, err
}

func (debugTestProbe) SendWithAutoStartObserved(request protocol.Request, _ string, observer ipc.DeliveryObserver) (protocol.Response, error) {
	observer(ipc.DeliveryStageEvent{Stage: ipc.DeliveryStageIPCAttempt, Attempt: 1, Outcome: "success"})
	return protocol.Response{OK: true, Delivery: &protocol.DeliveryAcknowledgement{
		SchemaVersion: protocol.DeliveryAcknowledgementSchemaVersion,
		Status:        protocol.DeliveryAcknowledgementAccepted, Reason: "routing_discovered",
		TraceID: request.TraceID, RequestID: request.RequestID,
		SourceWindowID: request.WindowID, TargetWindowID: request.WindowID,
		RouteDecision: "source", SnapshotReady: true, WindowFound: true,
	}}, nil
}

func resetDebugTestState(t *testing.T) {
	t.Helper()
	oldLoadConfig := debugLoadConfig
	oldPaths := debugPaths
	oldExecutable := debugExecutable
	oldNow := debugNow
	oldDaemon := debugDaemon
	oldKitty := debugKitty
	oldProbe := debugProbeClient
	oldNewJournal := debugNewJournal
	oldSelector := debugSelectorFlags
	oldStatusJSON := debugStatusJSON
	oldTailJSON := debugTailJSON
	oldTailFollow := debugTailFollow
	oldTailLimit := debugTailLimit
	oldProbeJSON := debugProbeJSON
	oldExplainJSON := debugExplainJSON
	oldExplainLast := debugExplainLast
	oldExplainGraphDepth := debugExplainGraphDepth
	oldExplainMaxNodes := debugExplainMaxNodes
	oldExplainMaxEdges := debugExplainMaxEdges
	oldReplayJSON := debugReplayJSON
	oldStatusOut := debugStatusCmd.OutOrStdout()
	oldTailOut := debugTailCmd.OutOrStdout()
	oldProbeOut := debugProbeCmd.OutOrStdout()
	oldExplainOut := debugExplainCmd.OutOrStdout()
	oldReplayOut := debugReplayCmd.OutOrStdout()
	t.Cleanup(func() {
		debugLoadConfig = oldLoadConfig
		debugPaths = oldPaths
		debugExecutable = oldExecutable
		debugNow = oldNow
		debugDaemon = oldDaemon
		debugKitty = oldKitty
		debugProbeClient = oldProbe
		debugNewJournal = oldNewJournal
		debugSelectorFlags = oldSelector
		debugStatusJSON = oldStatusJSON
		debugTailJSON = oldTailJSON
		debugTailFollow = oldTailFollow
		debugTailLimit = oldTailLimit
		debugProbeJSON = oldProbeJSON
		debugExplainJSON = oldExplainJSON
		debugExplainLast = oldExplainLast
		debugExplainGraphDepth = oldExplainGraphDepth
		debugExplainMaxNodes = oldExplainMaxNodes
		debugExplainMaxEdges = oldExplainMaxEdges
		debugReplayJSON = oldReplayJSON
		debugStatusCmd.SetOut(oldStatusOut)
		debugTailCmd.SetOut(oldTailOut)
		debugProbeCmd.SetOut(oldProbeOut)
		debugExplainCmd.SetOut(oldExplainOut)
		debugReplayCmd.SetOut(oldReplayOut)
	})
	debugSelectorFlags = debugSelectorFlagValues{}
	debugStatusJSON = false
	debugTailJSON = false
	debugTailFollow = false
	debugTailLimit = 50
	debugProbeJSON = false
	debugExplainJSON = false
	debugExplainLast = 10
	debugExplainGraphDepth = debugger.DefaultExplainGraphDepth
	debugExplainMaxNodes = debugger.DefaultExplainMaxNodes
	debugExplainMaxEdges = debugger.DefaultExplainMaxEdges
	debugReplayJSON = false
}

func TestDebugStatusJSONIsVersionedAndUsefulOffline(t *testing.T) {
	resetDebugTestState(t)
	root := filepath.Join(t.TempDir(), "state")
	paths := diagnostics.PathsForRoot(root)
	enabled := true
	debugLoadConfig = func() (*config.Config, error) {
		return &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
			JournalEnabled: &enabled, JournalMaxMiB: 1, NativeLogging: &enabled, NativeMaxMiB: 1, NativeSessionMaxMiB: 1,
		}}}, nil
	}
	debugPaths = func() (diagnostics.Paths, error) { return paths, nil }
	debugExecutable = func() (string, error) { return "/tmp/amx", nil }
	debugNow = func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) }
	debugDaemon = func() debugger.DaemonHealthClient { return debugTestDaemon{} }
	debugKitty = func() debugger.KittyWindowLister { return debugTestKitty{err: errors.New("offline")} }
	debugStatusJSON = true

	var out bytes.Buffer
	debugStatusCmd.SetOut(&out)
	if err := debugStatusCmd.RunE(debugStatusCmd, nil); err != nil {
		t.Fatal(err)
	}
	var report debugger.StatusReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if report.SchemaVersion != debugger.StatusSchemaVersion || debugger.StatusSchemaVersion != 2 || report.Daemon.Alive || report.Journal.Path != paths.JournalDir {
		t.Fatalf("report = %+v", report)
	}
}

func TestDebugTailJSONAndFollowWrapperAreV2Snapshots(t *testing.T) {
	for _, follow := range []bool{false, true} {
		t.Run(map[bool]string{false: "tail", true: "follow"}[follow], func(t *testing.T) {
			resetDebugTestState(t)
			paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
			journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
			if err != nil {
				t.Fatal(err)
			}
			record := diagnostics.NewEnvelope("cmd.tail")
			record.TraceID = "trace-tail-v2"
			if err := journal.Append(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			debugPaths = func() (diagnostics.Paths, error) { return paths, nil }
			debugLoadConfig = func() (*config.Config, error) { return nil, errors.New("offline config") }
			debugTailJSON = true
			debugTailFollow = follow
			debugTailLimit = 1

			if follow {
				ctx, cancel := context.WithCancel(t.Context())
				oldContext := debugTailCmd.Context()
				debugTailCmd.SetContext(ctx)
				t.Cleanup(func() { debugTailCmd.SetContext(oldContext) })
				out := &cancelBuffer{cancel: cancel}
				debugTailCmd.SetOut(out)
				if err := debugTailCmd.RunE(debugTailCmd, nil); err != nil {
					t.Fatal(err)
				}
				var wrapper debugger.TailFollowRecord
				if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &wrapper); err != nil {
					t.Fatalf("decode follow wrapper: %v\n%s", err, out.String())
				}
				if wrapper.SchemaVersion != 2 || wrapper.Record.TraceID != "trace-tail-v2" || wrapper.JournalSnapshot.RecordsMatched != 1 {
					t.Fatalf("follow wrapper = %+v", wrapper)
				}
				return
			}

			var out bytes.Buffer
			debugTailCmd.SetOut(&out)
			if err := debugTailCmd.RunE(debugTailCmd, nil); err != nil {
				t.Fatal(err)
			}
			var report debugger.TailReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("decode tail report: %v\n%s", err, out.String())
			}
			if report.SchemaVersion != 2 || len(report.Records) != 1 || report.JournalSnapshot.RecordsMatched != 1 {
				t.Fatalf("tail report = %+v", report)
			}
		})
	}
}

func TestDebugProbeJSONStatesScopeAndStages(t *testing.T) {
	resetDebugTestState(t)
	debugSelectorFlags.windowID = 77
	debugSelectorFlags.harness = "codex"
	debugSelectorFlags.traceID = "trace-probe"
	debugProbeClient = func() debugger.ProbeClient { return debugTestProbe{} }
	debugExecutable = func() (string, error) { return "/tmp/amx", nil }
	debugProbeJSON = true

	var out bytes.Buffer
	debugProbeCmd.SetOut(&out)
	if err := debugProbeCmd.RunE(debugProbeCmd, nil); err != nil {
		t.Fatal(err)
	}
	var report debugger.ProbeReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.TraceID != "trace-probe" || len(report.Stages) != 2 || !strings.Contains(report.Scope, "does not prove") {
		t.Fatalf("report = %+v", report)
	}
}

func TestDebugExplainJSONCitesOfflineEvidence(t *testing.T) {
	resetDebugTestState(t)
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	record := diagnostics.NewEnvelope("daemon.renderer.failed")
	record.TraceID = "trace-explain"
	record.RequestID = "request-explain"
	record.WindowID = 42
	record.Outcome = "failed"
	record.Reason = "render_title"
	if err := journal.Append(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	debugPaths = func() (diagnostics.Paths, error) { return paths, nil }
	debugLoadConfig = func() (*config.Config, error) { return nil, errors.New("offline config") }
	debugSelectorFlags.requestID = "request-explain"
	debugExplainJSON = true
	debugExplainLast = 10

	var out bytes.Buffer
	debugExplainCmd.SetOut(&out)
	if err := debugExplainCmd.RunE(debugExplainCmd, nil); err != nil {
		t.Fatal(err)
	}
	var report debugger.ExplanationReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if report.SchemaVersion != debugger.ExplainSchemaVersion || debugger.ExplainSchemaVersion != 2 ||
		report.Cause.Classification != debugger.CauseRenderFailure || len(report.Cause.EvidenceIDs) != 1 ||
		len(report.Graph.Nodes) != 1 || report.Limits.GraphDepth != debugger.DefaultExplainGraphDepth ||
		report.JournalSnapshot.RecordsMatched != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestDebugExplainValidatesGraphLimitsBeforeStorage(t *testing.T) {
	resetDebugTestState(t)
	storageCalled := false
	debugPaths = func() (diagnostics.Paths, error) {
		storageCalled = true
		return diagnostics.Paths{}, errors.New("storage should not be reached")
	}
	debugExplainMaxNodes = 0
	err := debugExplainCmd.RunE(debugExplainCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "--max-nodes") {
		t.Fatalf("limit error = %v", err)
	}
	if storageCalled {
		t.Fatal("invalid graph limits reached diagnostic storage")
	}
}

func TestDebugExplainTextWarnsWhenGraphIsTruncated(t *testing.T) {
	report := debugger.ExplanationReport{
		SchemaVersion: debugger.ExplainSchemaVersion,
		Cause:         debugger.ExplanationCause{Classification: debugger.CauseInsufficientEvidence, Certainty: debugger.CertaintyUncertain},
		Limits:        debugger.ExplanationLimits{GraphDepth: 1, MaxNodes: 1, MaxEdges: 1},
		Truncation: debugger.ExplanationTruncation{
			Truncated: true, Reasons: []string{debugger.TruncationMaxNodes}, PrunedNodeCandidates: 3,
		},
	}
	var out bytes.Buffer
	if err := writeDebugExplainText(&out, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "warning: evidence graph truncated (max_nodes; pruned node candidates=3 edge candidates=0)") {
		t.Fatalf("text output omitted truncation warning:\n%s", out.String())
	}
}

func TestDebugCommandRegistersSharedSelectorsAndSubcommands(t *testing.T) {
	for _, name := range []string{"harness", "profile", "window", "session", "asl-run", "event", "trace", "request", "source-event", "outcome", "after", "before"} {
		if debugCmd.PersistentFlags().Lookup(name) == nil {
			t.Errorf("missing shared --%s selector", name)
		}
	}
	for _, name := range []string{"status", "tail", "probe", "explain", "capture", "bundle", "replay"} {
		if command, _, err := debugCmd.Find([]string{name}); err != nil || command == nil || command.Name() != name {
			t.Errorf("debug subcommand %s missing: command=%v err=%v", name, command, err)
		}
	}
	for _, name := range []string{"json", "follow", "last"} {
		if debugTailCmd.Flags().Lookup(name) == nil {
			t.Errorf("debug tail is missing --%s", name)
		}
	}
	for _, name := range []string{"json", "last", "graph-depth", "max-nodes", "max-edges"} {
		if debugExplainCmd.Flags().Lookup(name) == nil {
			t.Errorf("debug explain is missing --%s", name)
		}
	}
	if buildinfo.Current().Version == "" {
		t.Fatal("build info unexpectedly empty")
	}
}

func TestDebugReplayJSONRunsOfflineAndHasNoLiveApplyFlags(t *testing.T) {
	resetDebugTestState(t)
	input := filepath.Join(t.TempDir(), "events-v8.json")
	data := []byte(`{"schema_version":8,"windows":[{"window_id":42,"events":[{"at":100,"event":"PreToolUse","action":"active","result_icon_state":"bolt","source_kind":"harness","source_key":"claude:offline","source_seq":1}]}]}`)
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	debugSelectorFlags.windowID = 42
	debugReplayJSON = true
	var out bytes.Buffer
	debugReplayCmd.SetOut(&out)
	if err := debugReplayCmd.RunE(debugReplayCmd, []string{input}); err != nil {
		t.Fatal(err)
	}
	var report debugger.ReplayReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode replay: %v\n%s", err, out.String())
	}
	if report.SchemaVersion != debugger.ReplaySchemaVersion || report.Input.SchemaVersion != protocol.EventsExportSchemaV8 ||
		!report.Success || report.Complete || len(report.Comparisons) != 1 {
		t.Fatalf("replay report = %+v", report)
	}
	for _, name := range []string{"apply", "live", "socket", "kitty"} {
		if debugReplayCmd.Flags().Lookup(name) != nil {
			t.Fatalf("replay unexpectedly exposes --%s", name)
		}
	}
}

func TestDebugCaptureRequiresNonInteractiveYesAndPersistsStatus(t *testing.T) {
	resetDebugTestState(t)
	oldDuration, oldInclude, oldYes, oldJSON := debugCaptureDuration, debugCaptureInclude, debugCaptureYes, debugCaptureJSON
	oldInteractive := debugInputInteractive
	oldStartOut, oldStatusOut, oldStopOut := debugCaptureStartCmd.OutOrStdout(), debugCaptureStatusCmd.OutOrStdout(), debugCaptureStopCmd.OutOrStdout()
	t.Cleanup(func() {
		debugCaptureDuration, debugCaptureInclude, debugCaptureYes, debugCaptureJSON = oldDuration, oldInclude, oldYes, oldJSON
		debugInputInteractive = oldInteractive
		debugCaptureStartCmd.SetOut(oldStartOut)
		debugCaptureStatusCmd.SetOut(oldStatusOut)
		debugCaptureStopCmd.SetOut(oldStopOut)
	})
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	debugPaths = func() (diagnostics.Paths, error) { return paths, nil }
	debugNow = func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) }
	debugSelectorFlags.windowID = 42
	debugSelectorFlags.harness = "codex"
	debugCaptureDuration = time.Minute
	debugCaptureInclude = true
	debugCaptureYes = false
	debugInputInteractive = func(io.Reader) bool { return false }
	if err := debugCaptureStartCmd.RunE(debugCaptureStartCmd, nil); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("non-interactive start error = %v", err)
	}
	debugCaptureYes = true
	debugCaptureJSON = true
	var start bytes.Buffer
	debugCaptureStartCmd.SetOut(&start)
	if err := debugCaptureStartCmd.RunE(debugCaptureStartCmd, nil); err != nil {
		t.Fatal(err)
	}
	var report debugger.CaptureReport
	if err := json.Unmarshal(start.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Descriptor == nil || report.Descriptor.Status != "active" || report.Descriptor.Scope.WindowID != 42 {
		t.Fatalf("start report = %+v", report)
	}
	var status bytes.Buffer
	debugCaptureStatusCmd.SetOut(&status)
	if err := debugCaptureStatusCmd.RunE(debugCaptureStatusCmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status.String(), `"status": "active"`) {
		t.Fatalf("status = %s", status.String())
	}
	var stop bytes.Buffer
	debugCaptureStopCmd.SetOut(&stop)
	if err := debugCaptureStopCmd.RunE(debugCaptureStopCmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stop.String(), `"status": "stopped"`) {
		t.Fatalf("stop = %s", stop.String())
	}
}

func TestTerminalDetectionRejectsDevNull(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if isTerminalInput(file) {
		t.Fatal("/dev/null was classified as an interactive terminal")
	}
}
