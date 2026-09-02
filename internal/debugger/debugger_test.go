package debugger

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

type testHealthClient struct{ health ipc.Health }

func (c testHealthClient) Health() ipc.Health { return c.health }

type testKittyClient struct {
	windows []kitty.OSWindow
	err     error
}

func (c testKittyClient) ListWindows() ([]kitty.OSWindow, error) { return c.windows, c.err }

type testProbeClient struct {
	request protocol.Request
}

func (c *testProbeClient) SendWithAutoStartObserved(request protocol.Request, _ string, observer ipc.DeliveryObserver) (protocol.Response, error) {
	c.request = request
	observer(ipc.DeliveryStageEvent{Stage: ipc.DeliveryStageIPCAttempt, Attempt: 1, Outcome: "success", Reason: "acknowledged", LatencyMS: 1.25})
	return protocol.Response{OK: true, Delivery: &protocol.DeliveryAcknowledgement{
		SchemaVersion: protocol.DeliveryAcknowledgementSchemaVersion,
		Status:        protocol.DeliveryAcknowledgementAccepted, Reason: "routing_discovered",
		TraceID: request.TraceID, RequestID: request.RequestID,
		ProducerHarness: harness.Claude, TargetHarness: harness.Codex,
		Profile: "work", SourceWindowID: 43, TargetWindowID: 42,
		RouteDecision: "owner", SnapshotReady: true, WindowFound: true,
	}}, nil
}

func TestSelectorNormalizesHarnessAndParsesTimeBounds(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	after, err := ParseTimeBound("30m", now)
	if err != nil || !after.Equal(now.Add(-30*time.Minute)) {
		t.Fatalf("after = %s, err = %v", after, err)
	}
	selector := Selector{Harness: " Claude ", Outcomes: []string{"success", "success", ""}, After: after, Before: now}
	if err := selector.Normalize(); err != nil {
		t.Fatal(err)
	}
	if selector.Harness != harness.Claude || len(selector.Outcomes) != 1 {
		t.Fatalf("selector = %+v", selector)
	}
	selector.After = now.Add(time.Minute)
	if err := selector.Normalize(); err == nil {
		t.Fatal("inverted time range was accepted")
	}
}

func TestTailWorksOfflineReadsSpoolAndToleratesCorruptRecords(t *testing.T) {
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	record := diagnostics.NewEnvelope("hook.delivery")
	record.RequestID = "main"
	record.Harness = harness.Claude
	record.Outcome = "accepted"
	record.Evidence = map[string]string{"event": "Stop"}
	if err := journal.Append(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(paths.JournalFile, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{bad}\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	spooled := diagnostics.NewEnvelope("hook.delivery")
	spooled.RequestID = "spooled"
	spooled.Harness = harness.Claude
	spooled.Outcome = "accepted"
	spooled.Evidence = map[string]string{"event": "Stop"}
	data, _ := json.Marshal(spooled)
	if err := os.WriteFile(filepath.Join(paths.SpoolDir, "journal-123.jsonl"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := Tail(context.Background(), journal, TailOptions{
		Selector: Selector{Harness: "Claude", Event: "Stop", Outcomes: []string{"accepted"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Records) != 2 || report.JournalSnapshot.CorruptLines != 1 ||
		report.JournalSnapshot.SegmentFilesRead != 1 || report.JournalSnapshot.SpoolFilesRead != 1 {
		t.Fatalf("tail report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(paths.SpoolDir, "journal-123.jsonl")); err != nil {
		t.Fatalf("tail mutated spool evidence: %v", err)
	}
}

func TestCollectStatusRemainsUsefulWithDaemonAndKittyUnavailable(t *testing.T) {
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	record := diagnostics.NewEnvelope("hook.delivery")
	record.RequestID = "offline-evidence"
	record.Harness = harness.Codex
	record.Outcome = "accepted"
	if err := journal.Append(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
		JournalEnabled: &enabled, JournalKeepDays: 14, JournalMaxMiB: 256,
		NativeLogging: &enabled, NativeKeepDays: 14, NativeMaxMiB: 1024, NativeSessionMaxMiB: 128,
	}}}
	report, err := CollectStatus(context.Background(), StatusOptions{
		Config: cfg, Paths: paths, Journal: journal,
		Daemon: testHealthClient{}, Kitty: testKittyClient{err: errors.New("Kitty offline")},
		AMXBin: "/tmp/amx", CLIBuild: buildinfo.Info{Version: "test"},
		Now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC), Selector: Selector{RequestID: "offline-evidence"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Daemon.Alive || report.Journal.Health.Records != 1 || len(report.LastSeenEvents) != 1 || report.LastSeenEvents[0].RequestID != "offline-evidence" {
		t.Fatalf("offline status = %+v", report)
	}
	if !strings.Contains(report.Findings[0].Message, "persisted diagnostics") {
		t.Fatalf("findings = %+v", report.Findings)
	}
}

func TestCollectStatusEmitsEmptyLastSeenEventsWhenJournalUnavailable(t *testing.T) {
	disabled := false
	enabled := true
	openFailurePaths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	if err := os.MkdirAll(openFailurePaths.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(openFailurePaths.JournalDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		paths diagnostics.Paths
		cfg   *config.Config
	}{
		{
			name:  "disabled",
			paths: diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state")),
			cfg: &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
				JournalEnabled: &disabled,
				NativeLogging:  &disabled,
			}}},
		},
		{
			name:  "open failure",
			paths: openFailurePaths,
			cfg: &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
				JournalEnabled: &enabled,
				NativeLogging:  &disabled,
			}}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := CollectStatus(t.Context(), StatusOptions{
				Config: test.cfg, Paths: test.paths, Daemon: testHealthClient{},
				AMXBin: "/tmp/amx", CLIBuild: buildinfo.Info{Version: "test"},
				Now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
			})
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				LastSeenEvents json.RawMessage `json:"last_seen_events"`
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatal(err)
			}
			if got := string(payload.LastSeenEvents); got != "[]" {
				t.Fatalf("last_seen_events = %s, want [] in %s", got, data)
			}
		})
	}
}

func TestCollectStatusEmitsEmptyLastSeenEventsWhenJournalHealthyButEmpty(t *testing.T) {
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	disabled := false
	report, err := CollectStatus(t.Context(), StatusOptions{
		Config: &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
			JournalEnabled: &enabled,
			NativeLogging:  &disabled,
		}}},
		Paths: paths, Journal: journal, Daemon: testHealthClient{},
		AMXBin: "/tmp/amx", CLIBuild: buildinfo.Info{Version: "test"},
		Now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Journal.Enabled || report.Journal.Verdict != VerdictHealthy || report.Journal.Error != "" {
		t.Fatalf("journal report = %+v, want enabled healthy empty journal", report.Journal)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		LastSeenEvents json.RawMessage `json:"last_seen_events"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if got := string(payload.LastSeenEvents); got != "[]" {
		t.Fatalf("last_seen_events = %s, want [] in %s", got, data)
	}
}

func TestCollectStatusReportsHookDeliveryFailuresInTrailingHour(t *testing.T) {
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	for _, fixture := range []struct {
		stage, requestID, outcome string
		observedAt                time.Time
	}{
		// req "exhausted": a failed attempt plus a terminal failure delivery and
		// its hook.failure — counts exactly once for the request.
		{"hook.ipc_attempt", "exhausted", "error", now.Add(-10 * time.Minute)},
		{"hook.failure", "exhausted", "error", now.Add(-9 * time.Minute)},
		{"hook.delivery", "exhausted", "retryable_failure", now.Add(-9 * time.Minute)},
		// req "retry-ok": a transient attempt error resolved by a later success —
		// never counts as a dropped delivery.
		{"hook.ipc_attempt", "retry-ok", "error", now.Add(-6 * time.Minute)},
		{"hook.ipc_attempt", "retry-ok", "success", now.Add(-5 * time.Minute)},
		{"hook.delivery", "retry-ok", "accepted", now.Add(-5 * time.Minute)},
		// req "old": a terminal failure older than the trailing hour is excluded
		// from the count but still anchors the journal's earliest horizon.
		{"hook.delivery", "old", "permanent_failure", now.Add(-3 * time.Hour)},
	} {
		record := diagnostics.NewEnvelope(fixture.stage)
		record.Harness = harness.Claude
		record.RequestID = fixture.requestID
		record.Outcome = fixture.outcome
		record.ObservedAt = fixture.observedAt
		if err := journal.Append(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	enabled := true
	disabled := false
	cfg := &config.Config{Defaults: config.Defaults{Diagnostics: config.DiagnosticsConfig{
		JournalEnabled: &enabled, NativeLogging: &disabled,
	}}}
	report, err := CollectStatus(context.Background(), StatusOptions{
		Config: cfg, Paths: paths, Journal: journal, Daemon: testHealthClient{},
		AMXBin: "/tmp/amx", CLIBuild: buildinfo.Info{Version: "test"}, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	failures := report.Hooks.DeliveryFailures
	if failures == nil || failures.Count != 1 || failures.LatestAt == nil || !failures.LatestAt.Equal(now.Add(-9*time.Minute)) {
		t.Fatalf("delivery failures = %+v, want count 1 latest %s", failures, now.Add(-9*time.Minute))
	}
	if !hasFinding(report.Findings, "hook_delivery_failures") {
		t.Fatalf("findings = %+v, want hook_delivery_failures warning", report.Findings)
	}
	for _, finding := range report.Findings {
		if finding.Check == "hook_delivery_failures" && finding.Verdict != VerdictWarning {
			t.Fatalf("hook_delivery_failures verdict = %s, want warning", finding.Verdict)
		}
	}
	if !report.Journal.Health.EarliestObservedAt.Equal(now.Add(-3 * time.Hour)) {
		t.Fatalf("journal earliest observed = %s, want %s", report.Journal.Health.EarliestObservedAt, now.Add(-3*time.Hour))
	}

	// A journal without error-outcome delivery records reports zero and no finding.
	quietPaths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	quietJournal, err := diagnostics.NewJournal(quietPaths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	quiet := diagnostics.NewEnvelope("hook.delivery")
	quiet.Harness = harness.Claude
	quiet.Outcome = "accepted"
	quiet.ObservedAt = now.Add(-5 * time.Minute)
	if err := quietJournal.Append(context.Background(), quiet); err != nil {
		t.Fatal(err)
	}
	report, err = CollectStatus(context.Background(), StatusOptions{
		Config: cfg, Paths: quietPaths, Journal: quietJournal, Daemon: testHealthClient{},
		AMXBin: "/tmp/amx", CLIBuild: buildinfo.Info{Version: "test"}, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if failures := report.Hooks.DeliveryFailures; failures == nil || failures.Count != 0 || failures.LatestAt != nil {
		t.Fatalf("quiet delivery failures = %+v, want count 0 without latest", failures)
	}
	if hasFinding(report.Findings, "hook_delivery_failures") {
		t.Fatalf("findings = %+v, want no hook_delivery_failures finding", report.Findings)
	}
}

func TestInspectNativeFindsManagedAndUnmanagedSessions(t *testing.T) {
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	enabled := true
	policy := config.DiagnosticsConfig{NativeLogging: &enabled, NativeMaxMiB: 1, NativeSessionMaxMiB: 1}
	windows := []kitty.OSWindow{{Tabs: []kitty.Tab{
		{Windows: []kitty.Window{{ID: 1, UserVars: kitty.UserVars{Profile: "work", Harness: harness.Claude, LaunchID: "11111111-1111-4111-8111-111111111111", NativeCoverage: "managed"}}}},
		{Windows: []kitty.Window{{ID: 2, ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"/usr/bin/codex"}}}}}},
	}}}
	report := InspectNative(paths, policy, windows, true, Selector{}, time.Now())
	if report.Coverage.Managed != 1 || report.Coverage.Unmanaged != 1 || len(report.Sessions) != 2 || report.Verdict != VerdictWarning {
		t.Fatalf("native report = %+v", report)
	}
}

func TestProbeReportsEveryObservedTransportStageAndScope(t *testing.T) {
	client := &testProbeClient{}
	report := Probe(client, ProbeOptions{WindowID: 43, Harness: "Claude", RequestID: "request-1", TraceID: "trace-1", AMXBin: "/tmp/amx"})
	if !report.Success || len(report.Stages) != 2 || report.Stages[0].Stage != ipc.DeliveryStageIPCAttempt || report.Stages[1].Stage != "daemon_acknowledgement" {
		t.Fatalf("probe report = %+v", report)
	}
	if client.request.Type != protocol.TypeDiagnosticProbe || client.request.WindowID != 43 || client.request.RequestID != "request-1" {
		t.Fatalf("probe request = %+v", client.request)
	}
	if !strings.Contains(report.Scope, "does not prove") || !strings.Contains(report.Scope, "without changing reducer") {
		t.Fatalf("scope = %q", report.Scope)
	}
}
