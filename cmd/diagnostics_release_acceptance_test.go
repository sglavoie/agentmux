package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/indicator"
)

// TestAcceptanceDiagnosticPipeline exercises the release diagnostic path with
// deterministic local substitutes only. The real hook command adapter writes
// to the real journal and crosses a Unix socket into an in-process daemon. A
// fake kitten executable supplies one managed window and records renderer
// writes; no harness provider, model, notification backend, or live Kitty
// process is contacted.
func TestAcceptanceDiagnosticPipeline(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "amx-acceptance-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	stateBase := filepath.Join(home, "state")
	kittyLog := filepath.Join(home, "kitten-commands.log")
	installAcceptanceKitten(t, home, kittyLog)
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", stateBase)
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/agent-mux-acceptance-kitty")
	t.Setenv("KITTY_WINDOW_ID", "42")

	cfg := &config.Config{Defaults: config.Defaults{
		GuardDuration: 5,
		Diagnostics: config.DiagnosticsConfig{
			JournalKeepDays: 14,
			JournalMaxMiB:   16,
		},
		Watchdog: config.WatchdogConfig{
			Active: 600, Subagent: 1800, Compact: 180, HookSilence: 14400,
		},
	}}
	daemonCtx, cancelDaemon := context.WithCancel(context.Background())
	daemonDone := make(chan error, 1)
	go func() { daemonDone <- daemon.Run(daemonCtx, cfg) }()
	t.Cleanup(func() {
		cancelDaemon()
		select {
		case err := <-daemonDone:
			if err != nil {
				t.Errorf("daemon shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	waitForAcceptance(t, "daemon readiness", func() bool {
		health := daemon.NewClient().Health()
		return health.Alive && health.Ready
	})

	aslPayload := `{"run_id":"acceptance-run","cwd":"/workspace","harness":"claude","source_sequence":"2","source_event_id":"acceptance-run:2"}`
	accepted := runAcceptanceHookCommand(t, "AgentSessionLoopTurnStarted", "acceptance-asl-request", aslPayload)
	if accepted.Status != indicator.DeliveryAccepted || !accepted.Successful() {
		t.Fatalf("ASL delivery = %+v, want accepted", accepted)
	}
	duplicate := runAcceptanceHookCommand(t, "AgentSessionLoopTurnStarted", "acceptance-asl-request", aslPayload)
	if duplicate.Status != indicator.DeliveryDuplicate || !duplicate.Successful() {
		t.Fatalf("duplicate ASL delivery = %+v, want duplicate", duplicate)
	}

	largeEventBase := time.Now().Unix()
	largeClaudeActive := runAcceptanceHookCommandForHarness(t, "PreToolUse", "acceptance-large-claude-active", "claude", acceptancePaddedPayload(t, 300<<10, map[string]any{
		"session_id": "acceptance-large-claude", "turn_id": "claude-turn", "tool_use_id": "claude-tool",
		"source_sequence": "11", "source_event_id": "claude-large-active", "emitted_time": fmt.Sprint(largeEventBase), "tool_name": "Bash",
	}))
	largeCodexActive := runAcceptanceHookCommandForHarness(t, "PreToolUse", "acceptance-large-codex-active", "codex", acceptancePaddedPayload(t, 300<<10, map[string]any{
		"sessionId": "acceptance-large-codex", "turnId": "codex-turn", "toolUseId": "codex-tool",
		"sourceSequence": "21", "sourceEventId": "codex-large-active", "emittedTime": fmt.Sprint(largeEventBase + 2), "toolName": "Bash",
	}))
	largeClaudeStop := runAcceptanceHookCommandForHarness(t, "SessionEnd", "acceptance-large-claude-stop", "claude", acceptancePaddedPayload(t, 300<<10, map[string]any{
		"session_id": "acceptance-large-claude", "turn_id": "claude-turn", "source_sequence": "12",
		"source_event_id": "claude-large-stop", "emitted_time": fmt.Sprint(largeEventBase + 1), "reason": "user_exit",
	}))
	for name, result := range map[string]indicator.DeliveryResult{
		"claude active": largeClaudeActive, "codex active": largeCodexActive, "claude stop": largeClaudeStop,
	} {
		if result.Status != indicator.DeliveryAccepted || result.DaemonAcknowledgement == nil {
			t.Fatalf("large %s delivery = %+v, want accepted", name, result)
		}
	}
	client := daemon.NewClient()
	var exported daemon.EventsExport
	waitForAcceptance(t, "large-payload reducer source isolation", func() bool {
		var exportErr error
		exported, exportErr = client.ExportEvents(42)
		if exportErr != nil {
			return false
		}
		facts := acceptanceFactsByKey(exported.WindowMetadata[42].Shadow.Facts)
		claudeFact, claudeOK := facts["claude:acceptance-large-claude"]
		codexFact, codexOK := facts["codex:acceptance-large-codex"]
		return claudeOK && codexOK && claudeFact.Action == "stop" && claudeFact.Terminated &&
			codexFact.Action == "active" && !codexFact.Acked && !codexFact.Terminated
	})

	// Elicitation is a real attention transition. The hook runs beneath the test
	// harness (which may itself be a provider pipe-mode ancestor), and permission
	// notifications are disabled, so the same causal trace must record either
	// pipe-mode or config suppression without invoking a platform backend.
	// It follows the mixed-source sequence so its pending guard cannot suppress
	// the active transitions whose isolation this acceptance test verifies.
	elicitationPayload := `{"session_id":"acceptance-session","turn_id":"acceptance-turn","cwd":"/workspace","source_sequence":"3","source_event_id":"acceptance-source-3","prompt":"PRIVATE-PROMPT-CANARY"}`
	elicitation := runAcceptanceHookCommand(t, "Elicitation", "acceptance-elicitation-request", elicitationPayload)
	if elicitation.Status != indicator.DeliveryAccepted {
		t.Fatalf("elicitation delivery = %+v, want accepted", elicitation)
	}

	paths := diagnostics.PathsForRoot(filepath.Join(stateBase, "agent-mux"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{LockTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var records []diagnostics.TraceEnvelope
	waitForAcceptance(t, "causal journal and async effects", func() bool {
		if _, promoteErr := journal.PromoteSpool(t.Context()); promoteErr != nil {
			return false
		}
		read, readErr := journal.Read(t.Context(), diagnostics.Selector{})
		if readErr != nil {
			return false
		}
		records = read
		return hasAcceptanceStage(records, "hook.delivery", "accepted", "") &&
			hasAcceptanceStage(records, "hook.delivery", "duplicate", "") &&
			hasAcceptanceStage(records, "daemon.decision", "", "") &&
			hasAcceptanceStage(records, "daemon.renderer.rendered", "success", "render_complete") &&
			hasAcceptanceStage(records, "daemon.notification.decision", "suppressed", "")
	})
	if !hasAcceptanceStage(records, "daemon.notification.decision", "suppressed", "config_disabled") &&
		!hasAcceptanceStage(records, "daemon.notification.decision", "suppressed", "pipe_mode") {
		t.Fatalf("notification suppression did not use a safe reason")
	}

	largeRequests := map[string]string{
		"acceptance-large-claude-active": "claude:acceptance-large-claude",
		"acceptance-large-codex-active":  "codex:acceptance-large-codex",
		"acceptance-large-claude-stop":   "claude:acceptance-large-claude",
	}
	seenLargeRequests := make(map[string]bool, len(largeRequests))
	for _, event := range exported.EventsByWindow[42] {
		wantSource, ok := largeRequests[event.RequestID]
		if !ok {
			continue
		}
		if event.PayloadEncoding != "json_compact" || event.SourceKey != wantSource {
			t.Fatalf("large event %q = encoding %q source %q, want json_compact/%q", event.RequestID, event.PayloadEncoding, event.SourceKey, wantSource)
		}
		seenLargeRequests[event.RequestID] = true
	}
	if len(seenLargeRequests) != len(largeRequests) {
		t.Fatalf("large request receipts = %v, want %v", seenLargeRequests, largeRequests)
	}

	beforeOverLimit, err := client.ExportEvents(42)
	if err != nil {
		t.Fatal(err)
	}
	eventsBeforeOverLimit := len(beforeOverLimit.EventsByWindow[42])
	overLimitPayload := acceptancePaddedPayload(t, (1<<20)+1, map[string]any{"session_id": "must-not-reach-daemon"})
	strictResult, strictErr := runAcceptanceHookCommandMode(t, "PreToolUse", "acceptance-over-limit-strict", "claude", overLimitPayload, true)
	var exitErr *ExitError
	if !errors.As(strictErr, &exitErr) || exitErr.Code != 2 || strictResult.Status != indicator.DeliveryPermanentFailure || strictResult.Reason != "payload_oversized" {
		t.Fatalf("strict over-limit result = %+v error=%#v, want permanent failure exit 2", strictResult, strictErr)
	}
	legacyResult, legacyErr := runAcceptanceHookCommandMode(t, "PreToolUse", "acceptance-over-limit-legacy", "claude", overLimitPayload, false)
	if legacyErr != nil || legacyResult.Status != indicator.DeliveryPermanentFailure || legacyResult.Reason != "payload_oversized" {
		t.Fatalf("legacy over-limit result = %+v error=%v, want fail-open nil error", legacyResult, legacyErr)
	}
	afterOverLimit, err := client.ExportEvents(42)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(afterOverLimit.EventsByWindow[42]); got != eventsBeforeOverLimit {
		t.Fatalf("over-limit payloads reached daemon reducer: events %d -> %d", eventsBeforeOverLimit, got)
	}

	commands, err := os.ReadFile(kittyLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"set-user-vars", "set-tab-title"} {
		if !bytes.Contains(commands, []byte(operation)) {
			t.Errorf("fake Kitty command log does not contain %q:\n%s", operation, commands)
		}
	}

	explanation, err := debugger.ExplainRecords(debugger.ExplainOptions{
		Selector: debugger.Selector{TraceID: accepted.TraceID},
		Recent:   10,
		Now:      time.Now().UTC(),
	}, records)
	if err != nil {
		t.Fatal(err)
	}
	if explanation.Cause.Classification != debugger.CauseDuplicate {
		t.Fatalf("ASL explanation cause = %+v", explanation.Cause)
	}
	relations := make(map[string]bool)
	for _, edge := range explanation.Graph.Edges {
		relations[edge.Relation] = true
	}
	for _, relation := range []string{"trace_id", "request_id", "source_event_id"} {
		if !relations[relation] {
			t.Errorf("ASL explanation has no %s edge: %+v", relation, explanation.Graph.Edges)
		}
	}

	bundlePath := filepath.Join(home, "acceptance-share.tar.gz")
	bundle, err := debugger.CreateBundle(t.Context(), debugger.BundleOptions{
		Paths: paths, OutputPath: bundlePath, Mode: debugger.BundleModeShare,
		Now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC), Journal: journal, Config: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Manifest.LocalOnly || bundle.Manifest.RawSources != "raw hook captures and raw native logs are always excluded" {
		t.Fatalf("share bundle policy = %+v", bundle.Manifest)
	}
	if info, statErr := os.Stat(bundlePath); statErr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("bundle mode: info=%v err=%v", info, statErr)
	}

	replay, err := debugger.Replay(t.Context(), bundlePath, debugger.ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Success || !replay.Complete || replay.Summary.Mismatched != 0 || replay.Summary.Incomplete != 0 {
		t.Fatalf("share-bundle replay = %+v", replay)
	}
}

func installAcceptanceKitten(t *testing.T, home, commandLog string) {
	t.Helper()
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(home, "kitty.json")
	kittyJSON := `[{"id":1,"is_active":true,"is_focused":true,"tabs":[{"id":7,"is_active":true,"title":"C:acceptance","windows":[{"id":42,"is_active":true,"title":"C:acceptance","cwd":"/workspace","cmdline":["claude"],"user_vars":{"amx_profile":"acceptance","amx_harness":"claude","amx_project":"acceptance","amx_directory":"/workspace","amx_harness_session":"acceptance-session"}}]}]}]`
	if err := os.WriteFile(fixture, []byte(kittyJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$AMX_ACCEPTANCE_KITTY_LOG\"\n" +
		"case \" $* \" in\n" +
		"  *\" ls \"*) exec /bin/cat \"$AMX_ACCEPTANCE_KITTY_FIXTURE\" ;;\n" +
		"  *) exit 0 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(binDir, "kitten"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMX_ACCEPTANCE_KITTY_FIXTURE", fixture)
	t.Setenv("AMX_ACCEPTANCE_KITTY_LOG", commandLog)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func runAcceptanceHookCommand(t *testing.T, event, requestID, payload string) indicator.DeliveryResult {
	return runAcceptanceHookCommandForHarness(t, event, requestID, "claude", payload)
}

func runAcceptanceHookCommandForHarness(t *testing.T, event, requestID, harnessName, payload string) indicator.DeliveryResult {
	t.Helper()
	result, err := runAcceptanceHookCommandMode(t, event, requestID, harnessName, payload, true)
	if err != nil {
		t.Fatalf("hook-indicator %s: %v", event, err)
	}
	return result
}

func runAcceptanceHookCommandMode(t *testing.T, event, requestID, harnessName, payload string, strict bool) (indicator.DeliveryResult, error) {
	t.Helper()
	stdin, err := os.CreateTemp(t.TempDir(), "hook-payload-*.json")
	if err != nil {
		return indicator.DeliveryResult{}, err
	}
	if _, err := stdin.WriteString(payload); err != nil {
		return indicator.DeliveryResult{}, err
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		return indicator.DeliveryResult{}, err
	}
	defer func() { _ = stdin.Close() }()

	oldStdin := os.Stdin
	oldWindowID, oldHarness, oldRequestID := hookWindowID, hookHarness, hookRequestID
	oldJSON, oldStrict := hookJSON, hookStrict
	oldOut := hookIndicatorCmd.OutOrStdout()
	defer func() {
		os.Stdin = oldStdin
		hookWindowID, hookHarness, hookRequestID = oldWindowID, oldHarness, oldRequestID
		hookJSON, hookStrict = oldJSON, oldStrict
		hookIndicatorCmd.SetOut(oldOut)
	}()
	os.Stdin = stdin
	hookWindowID, hookHarness, hookRequestID = 42, harnessName, requestID
	hookJSON, hookStrict = true, strict
	var output bytes.Buffer
	hookIndicatorCmd.SetOut(&output)
	runErr := hookIndicatorCmd.RunE(hookIndicatorCmd, []string{event})
	var result indicator.DeliveryResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		return indicator.DeliveryResult{}, fmt.Errorf("decode hook-indicator %s result: %w (%s)", event, err, output.String())
	}
	return result, runErr
}

func acceptancePaddedPayload(t *testing.T, paddingBytes int, fields map[string]any) string {
	t.Helper()
	fields["bulk_transcript"] = strings.Repeat("x", paddingBytes)
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func acceptanceFactsByKey(facts []daemon.SourceFactSnapshot) map[string]daemon.SourceFactSnapshot {
	result := make(map[string]daemon.SourceFactSnapshot, len(facts))
	for _, fact := range facts {
		if fact.Kind == daemon.SourceHarness {
			result[fact.Key] = fact
		}
	}
	return result
}

func waitForAcceptance(t *testing.T, description string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func hasAcceptanceStage(records []diagnostics.TraceEnvelope, stage, outcome, reason string) bool {
	for _, record := range records {
		if record.Stage != stage {
			continue
		}
		if outcome != "" && record.Outcome != outcome {
			continue
		}
		if reason != "" && record.Reason != reason {
			continue
		}
		return true
	}
	return false
}

func TestReleaseDocumentationPinsVersionAndDiagnosticWorkflow(t *testing.T) {
	if version != "0.7.0" {
		t.Fatalf("version = %q, want 0.7.0", version)
	}
	for _, path := range []string{"../docs/daemon.md", "../docs/debugging.md", "../docs/acceptance.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, required := range []string{
			"amx debug status", "amx debug explain", "amx debug bundle", "schema v12",
			"schema v2", "journal_snapshot", "event-time", "--graph-depth",
			"notification_failure", "v1 and v2", "causal-record wrappers",
		} {
			if !strings.Contains(text, required) {
				t.Errorf("%s does not document %q", path, required)
			}
		}
	}
	failureMatrix, err := os.ReadFile("../docs/debugging.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{
		"Disabled or untrusted hook", "Invalid payload", "Missing Kitty", "Daemon unavailable",
		"Retry exhaustion", "Duplicate delivery", "Queue saturation", "Route mismatch",
		"Quarantined identity", "Reducer suppression", "Render failure", "Notification dispatch failure", "Notification suppression",
		"Quota exhaustion", "Restart recovery",
	} {
		if !bytes.Contains(failureMatrix, []byte(failure)) {
			t.Errorf("failure matrix does not cover %q", failure)
		}
	}
	if !bytes.Contains(failureMatrix, []byte("no model usage")) {
		t.Error("provider smoke documentation does not state the automated no-model-usage boundary")
	}
	for _, required := range []string{
		"one journal traversal", "payload_encoding=json_compact", "payload_oversized",
		"compaction_input_files", "compaction_duplicates_omitted",
	} {
		if !bytes.Contains(failureMatrix, []byte(required)) {
			t.Errorf("debugging documentation does not pin %q", required)
		}
	}
	acceptanceDoc, err := os.ReadFile("../docs/acceptance.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"2,000-to-1", "json_compact", "strict exit 2", "informational"} {
		if !bytes.Contains(acceptanceDoc, []byte(required)) {
			t.Errorf("acceptance documentation does not pin %q", required)
		}
	}
}

func Example_diagnosticReleaseWorkflow() {
	fmt.Println("amx debug status")
	fmt.Println("amx debug explain --source-event <id>")
	fmt.Println("amx debug bundle --mode share -o agent-mux-debug.tar.gz")
	fmt.Println("amx debug replay agent-mux-debug.tar.gz")
	// Output:
	// amx debug status
	// amx debug explain --source-event <id>
	// amx debug bundle --mode share -o agent-mux-debug.tar.gz
	// amx debug replay agent-mux-debug.tar.gz
}
