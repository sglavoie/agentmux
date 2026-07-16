package indicator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/diagnostics"
)

func TestDeliverCapturesExplicitlyAuthorizedMalformedPayloadBeforeDecode(t *testing.T) {
	stateBase := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateBase)
	paths := diagnostics.PathsForRoot(filepath.Join(stateBase, "agent-mux"))
	store, err := diagnostics.NewCaptureStore(paths, diagnostics.CaptureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(t.Context(), diagnostics.CaptureStartRequest{
		Scope: diagnostics.CaptureScope{Harness: "claude"}, Duration: time.Minute, IncludeHookPayloads: true,
	}); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(`{"malformed"`); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	oldStdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = reader.Close()
	})
	result := Deliver("PreToolUse", 42, "claude", "/tmp/amx", DeliveryOptions{RequestID: "malformed-capture"})
	if result.Status != DeliveryPermanentFailure || result.Reason != "invalid_payload" {
		t.Fatalf("delivery result = %+v", result)
	}
	status, err := store.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Descriptor == nil || status.Descriptor.CapturedPayloads != 1 {
		t.Fatalf("capture status = %+v", status)
	}
}

func TestDeliverOversizedPayloadCaptureIsMarkedTruncated(t *testing.T) {
	stateBase := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateBase)
	t.Setenv("AMX_CONFIG", filepath.Join(t.TempDir(), "missing-config.toml"))
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	paths := diagnostics.PathsForRoot(filepath.Join(stateBase, "agent-mux"))
	store, err := diagnostics.NewCaptureStore(paths, diagnostics.CaptureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(t.Context(), diagnostics.CaptureStartRequest{
		Scope: diagnostics.CaptureScope{Harness: "claude"}, Duration: time.Minute, IncludeHookPayloads: true,
	}); err != nil {
		t.Fatal(err)
	}

	stdin, err := os.CreateTemp(t.TempDir(), "oversized-hook-*.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	payload := `{"tool_response":"` + strings.Repeat("x", maxHookPayloadBytes) + `"}`
	if _, err := stdin.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	client := &fakeDaemonClient{resp: daemon.Response{OK: true, Delivery: &daemon.DeliveryAcknowledgement{
		SchemaVersion: 1, Status: daemon.DeliveryAcknowledgementAccepted, Reason: "accepted",
	}}}
	result := deliverFromStdinWithDeps(
		"PostToolUse", 42, "claude", "/tmp/amx", DeliveryOptions{RequestID: "oversized-payload"}, stdin, client,
	)
	if result.Status != DeliveryAccepted || result.Reason != "accepted" {
		t.Fatalf("delivery result = %+v, want accepted empty-payload fallback", result)
	}
	if client.calls != 1 {
		t.Fatalf("daemon calls = %d, want 1", client.calls)
	}
	req := client.reqs[0]
	if req.PayloadJSON != "" || req.PayloadSize != maxHookPayloadBytes+1 || req.PayloadHMAC == "" || !req.PayloadCaptured {
		t.Fatalf("degraded request = %+v, want empty wire payload with bounded captured evidence", req)
	}
	status, err := store.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Descriptor == nil || status.Descriptor.CapturedPayloads != 1 ||
		status.Descriptor.CapturedBytes <= maxHookPayloadBytes || status.Descriptor.CapturedBytes > maxHookPayloadBytes+4096 {
		t.Fatalf("capture status = %+v", status)
	}
	files, err := filepath.Glob(filepath.Join(paths.CaptureDir, status.Descriptor.ID, "*.raw.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("capture files = %v, want one raw capture", files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var entry struct {
		Payload   string `json:"payload"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatal(err)
	}
	if !entry.Truncated || len(entry.Payload) != maxHookPayloadBytes+1 {
		t.Fatalf("capture entry = truncated:%t payload-bytes:%d, want marked bounded prefix", entry.Truncated, len(entry.Payload))
	}
}

func TestDeliverPayloadWithDepsStructuredOutcomes(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")

	for _, tc := range []struct {
		name   string
		resp   daemon.Response
		err    error
		status DeliveryStatus
		reason string
	}{
		{
			name: "accepted",
			resp: daemon.Response{OK: true, Delivery: &daemon.DeliveryAcknowledgement{
				SchemaVersion: 1, Status: daemon.DeliveryAcknowledgementAccepted, Reason: "accepted",
			}},
			status: DeliveryAccepted, reason: "accepted",
		},
		{
			name: "duplicate",
			resp: daemon.Response{OK: true, Delivery: &daemon.DeliveryAcknowledgement{
				SchemaVersion: 1, Status: daemon.DeliveryAcknowledgementDuplicate, Reason: "request_replayed",
			}},
			status: DeliveryDuplicate, reason: "duplicate",
		},
		{
			name: "permanent rejection",
			resp: daemon.Response{OK: true, Delivery: &daemon.DeliveryAcknowledgement{
				SchemaVersion: 1, Status: daemon.DeliveryAcknowledgementRejected, Reason: "unknown_event",
			}},
			status: DeliveryPermanentFailure, reason: "unknown_event",
		},
		{
			name:   "retryable rejection",
			resp:   daemon.Response{OK: false, Error: "busy", Retryable: true},
			status: DeliveryRetryableFailure, reason: "daemon_rejected",
		},
		{
			name:   "transport failure",
			err:    errors.New("socket unavailable"),
			status: DeliveryRetryableFailure, reason: "daemon_unavailable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeDaemonClient{resp: tc.resp, err: tc.err}
			recorder := &deliveryRecorder{traceID: "trace-1", requestID: "request-1", event: "Stop"}
			result := deliverPayloadWithDeps(
				"Stop", 42, "claude", "/tmp/amx", `{"source_event_id":"event-7"}`,
				false, "trace-1", "request-1", "event-7", 29, "hmac-1",
				client, recorder, time.Now(),
			)
			if result.Status != tc.status || result.Reason != tc.reason || result.Retryable != (tc.status == DeliveryRetryableFailure) {
				t.Fatalf("result = %+v; want status %q reason %q", result, tc.status, tc.reason)
			}
			if client.calls != 1 {
				t.Fatalf("client calls = %d, want 1", client.calls)
			}
			req := client.reqs[0]
			if req.TraceID != "trace-1" || req.RequestID != "request-1" || req.SourceEventID != "event-7" ||
				req.Attempt != 1 || req.PayloadSize != 29 || req.PayloadHMAC != "hmac-1" ||
				req.PayloadEncoding != "json" || req.ProducerHarness != "claude" || req.Harness != "claude" {
				t.Fatalf("structured request = %+v", req)
			}
		})
	}
}

func TestDeliverPersistsEveryOutsideKittyExitStage(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("AMX_CONFIG", t.TempDir()+"/missing-config.toml")
	t.Setenv("KITTY_LISTEN_ON", "")

	stdin, err := os.CreateTemp(t.TempDir(), "hook-payload-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.WriteString(`{"source_event_id":"source-1"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = stdin.Close()
	})

	result := Deliver("Stop", 42, "claude", "/tmp/amx", DeliveryOptions{})
	if result.Status != DeliveryPermanentFailure || result.Reason != "outside_kitty" ||
		len(result.TraceID) != 36 || result.RequestID != result.TraceID {
		t.Fatalf("result = %+v", result)
	}

	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	records, err := journal.Read(context.Background(), diagnostics.Selector{RequestID: result.RequestID})
	if err != nil {
		t.Fatal(err)
	}
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
			t.Errorf("missing stage %q in %+v", stage, records)
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
