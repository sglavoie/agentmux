package indicator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func TestHookProcessDetectionJournalsCompleteDegradedAndAbsentIdentity(t *testing.T) {
	t.Parallel()

	paths := diagnostics.PathsForRoot(t.TempDir())
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &deliveryRecorder{
		journal: journal, detailFull: true, traceID: "trace-process", requestID: "request-process",
		event: "PreToolUse", harness: "claude", windowID: 42,
	}
	recorder.recordProcessDetection(HarnessProcessDiscovery{
		Harness: "claude", PID: 4242, StartToken: "Thu Jul 16 10:20:30 2026", PipeMode: true, Trackable: true,
	})
	recorder.recordProcessDetection(HarnessProcessDiscovery{
		Harness: "claude", PID: 4243, PipeMode: true,
	})
	recorder.recordProcessDetection(HarnessProcessDiscovery{})

	records, err := journal.Read(context.Background(), diagnostics.Selector{
		RequestID: "request-process", Stages: []string{"hook.process_detection"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("process detection records = %+v", records)
	}
	if records[0].Outcome != "success" || records[0].Reason != "complete_identity" ||
		records[0].Evidence["process_harness"] != "claude" || records[0].Evidence["process_pid"] != "4242" ||
		records[0].Evidence["process_start_token"] != "Thu Jul 16 10:20:30 2026" || records[0].Evidence["pipe_mode"] != "true" {
		t.Fatalf("complete process detection = %+v", records[0])
	}
	if records[1].Outcome != "degraded" || records[1].Reason != "incomplete_identity" ||
		records[1].Evidence["process_pid"] != "4243" || records[1].Evidence["process_start_token"] != "" {
		t.Fatalf("degraded process detection = %+v", records[1])
	}
	if records[2].Outcome != "absent" || records[2].Reason != "identity_absent" {
		t.Fatalf("absent process detection = %+v", records[2])
	}
}

func TestDeliverStandardDetailSamplesSuccessStagesAndStampsDelivery(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("AMX_CONFIG", t.TempDir()+"/missing-config.toml")

	_, records, payload := deliverOutsideKitty(t)
	stages := map[string]diagnostics.TraceEnvelope{}
	for _, record := range records {
		stages[record.Stage] = record
	}
	for _, stage := range []string{"hook.kitty_detection", "hook.delivery"} {
		if _, ok := stages[stage]; !ok {
			t.Errorf("missing always-written stage %q in %+v", stage, records)
		}
	}
	for _, stage := range []string{"hook.payload_read", "hook.validation"} {
		if _, ok := stages[stage]; ok {
			t.Errorf("success-class stage %q not sampled away at standard detail: %+v", stage, records)
		}
	}
	delivery := stages["hook.delivery"]
	if delivery.PayloadHMAC == "" || delivery.PayloadSize != int64(len(payload)) {
		t.Fatalf("hook.delivery payload identity = hmac:%q size:%d, want stamped from sampled payload_read", delivery.PayloadHMAC, delivery.PayloadSize)
	}
}

func TestDeliverFullDetailPersistsEveryOutsideKittyExitStage(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("[defaults.diagnostics]\njournal_detail = \"full\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMX_CONFIG", configPath)

	_, records, _ := deliverOutsideKitty(t)
	wantStages := map[string]bool{
		"hook.payload_read":    false,
		"hook.validation":      false,
		"hook.kitty_detection": false,
		"hook.delivery":        false,
	}
	for _, record := range records {
		if _, ok := wantStages[record.Stage]; ok {
			wantStages[record.Stage] = true
		}
	}
	for stage, found := range wantStages {
		if !found {
			t.Errorf("missing stage %q at full detail in %+v", stage, records)
		}
	}
}

func TestHookRecorderStandardDetailKeepsEveryNonSuccessOutcome(t *testing.T) {
	t.Parallel()

	paths := diagnostics.PathsForRoot(t.TempDir())
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &deliveryRecorder{
		journal: journal, traceID: "trace-sample", requestID: "request-sample",
		event: "PreToolUse", harness: "claude", windowID: 42,
	}
	// Success-class intermediates are suppressed at standard detail.
	recorder.recordProcessDetection(HarnessProcessDiscovery{
		Harness: "claude", PID: 4242, StartToken: "Thu Jul 16 10:20:30 2026", Trackable: true,
	})
	recorder.recordProcessDetection(HarnessProcessDiscovery{})
	recorder.record("hook.kitty_detection", "success", "kitty_detected", nil, 0)
	recorder.record("hook.validation", "success", "payload_valid", nil, 0)
	recorder.record("hook.acknowledgement", "success", "accepted", nil, 0)
	// Non-success-class outcomes are never sampled.
	recorder.recordProcessDetection(HarnessProcessDiscovery{Harness: "claude", PID: 4243})
	recorder.record("hook.validation", "rejected", "invalid_payload", nil, 0)
	recorder.record("hook.ipc_attempt", "error", "dial unix: no such file", nil, 0)

	records, err := journal.Read(context.Background(), diagnostics.Selector{RequestID: "request-sample"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("standard-detail records = %+v, want only the degraded/rejected/error outcomes", records)
	}
	wantOutcomes := map[string]string{
		"hook.process_detection": "degraded",
		"hook.validation":        "rejected",
		"hook.ipc_attempt":       "error",
	}
	for _, record := range records {
		if wantOutcomes[record.Stage] != record.Outcome {
			t.Errorf("persisted record = %s/%s, want %+v", record.Stage, record.Outcome, wantOutcomes)
		}
	}
}

func TestNewDeliveryRecorderReadsJournalDetailFromConfig(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, tc := range []struct {
		detail   string
		wantFull bool
	}{
		{detail: "full", wantFull: true},
		{detail: "standard", wantFull: false},
	} {
		configPath := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(configPath, []byte("[defaults.diagnostics]\njournal_detail = \""+tc.detail+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AMX_CONFIG", configPath)
		recorder := newDeliveryRecorder("trace-detail", "request-detail", "Stop", "claude", 42)
		if recorder.detailFull != tc.wantFull {
			t.Fatalf("journal_detail %q → detailFull = %t, want %t", tc.detail, recorder.detailFull, tc.wantFull)
		}
	}
}

func TestDeliverHonorsExplicitJournalDisable(t *testing.T) {
	stateRoot := t.TempDir()
	configPath := t.TempDir() + "/config.toml"
	if err := os.WriteFile(configPath, []byte("[defaults.diagnostics]\njournal_enabled = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", stateRoot)
	t.Setenv("AMX_CONFIG", configPath)
	t.Setenv("KITTY_LISTEN_ON", "")

	stdin, err := os.CreateTemp(t.TempDir(), "hook-payload-*.json")
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = stdin.Close()
	})

	result := Deliver("Stop", 42, "claude", "/tmp/amx", DeliveryOptions{})
	if result.Status != DeliveryPermanentFailure || result.Reason != "outside_kitty" {
		t.Fatalf("result = %+v", result)
	}
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.JournalFile); !os.IsNotExist(err) {
		t.Fatalf("journal stat error = %v, want not-exist with explicit disable", err)
	}
}
