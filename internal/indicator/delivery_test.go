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
	"github.com/sglavoie/agentmux/internal/hookevent"
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

func TestDeliverPayloadBoundariesAndEncoding(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("AMX_CONFIG", filepath.Join(t.TempDir(), "missing-config.toml"))
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")

	oldDiscovery := discoverHarnessProcessForDelivery
	discoveryCalls := 0
	discoverHarnessProcessForDelivery = func(string) HarnessProcessDiscovery {
		discoveryCalls++
		return HarnessProcessDiscovery{}
	}
	t.Cleanup(func() { discoverHarnessProcessForDelivery = oldDiscovery })

	for _, tc := range []struct {
		name         string
		size         int
		wantStatus   DeliveryStatus
		wantReason   string
		wantEncoding string
		wantCalls    int
	}{
		{name: "exactly 256 KiB", size: maxRawHookPayloadBytes, wantStatus: DeliveryAccepted, wantReason: "accepted", wantEncoding: "json", wantCalls: 1},
		{name: "256 KiB plus one", size: maxRawHookPayloadBytes + 1, wantStatus: DeliveryAccepted, wantReason: "accepted", wantEncoding: "json_compact", wantCalls: 1},
		{name: "exactly 1 MiB", size: maxHookPayloadBytes, wantStatus: DeliveryAccepted, wantReason: "accepted", wantEncoding: "json_compact", wantCalls: 1},
		{name: "1 MiB plus one", size: maxHookPayloadBytes + 1, wantStatus: DeliveryPermanentFailure, wantReason: "payload_oversized", wantCalls: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := exactSizedHookPayload(t, tc.size, map[string]any{
				"session_id":      "boundary-session",
				"tool_use_id":     "tool-boundary",
				"source_event_id": "event-boundary",
				"tool_name":       "Bash",
			})
			stdin := hookPayloadFile(t, payload)
			client := &fakeDaemonClient{resp: daemon.Response{OK: true, Delivery: &daemon.DeliveryAcknowledgement{
				SchemaVersion: 1, Status: daemon.DeliveryAcknowledgementAccepted, Reason: "accepted",
			}}}
			beforeDiscovery := discoveryCalls
			result := deliverFromStdinWithDeps(
				"PreToolUse", 42, "claude", "/tmp/amx", DeliveryOptions{RequestID: "boundary-" + strings.ReplaceAll(tc.name, " ", "-")}, stdin, client,
			)
			if result.Status != tc.wantStatus || result.Reason != tc.wantReason {
				t.Fatalf("delivery result = %+v, want status %q reason %q", result, tc.wantStatus, tc.wantReason)
			}
			if client.calls != tc.wantCalls {
				t.Fatalf("daemon calls = %d, want %d", client.calls, tc.wantCalls)
			}
			wantDiscoveryCalls := tc.wantCalls
			if got := discoveryCalls - beforeDiscovery; got != wantDiscoveryCalls {
				t.Fatalf("process discovery calls = %d, want %d", got, wantDiscoveryCalls)
			}
			if tc.wantCalls == 0 {
				return
			}
			req := client.reqs[0]
			if req.PayloadSize != int64(tc.size) || req.PayloadEncoding != tc.wantEncoding || req.PayloadHMAC == "" {
				t.Fatalf("payload metadata = size:%d encoding:%q hmac:%q", req.PayloadSize, req.PayloadEncoding, req.PayloadHMAC)
			}
			if tc.wantEncoding == "json" {
				if req.PayloadJSON != payload {
					t.Fatal("payload at raw boundary was not forwarded byte-for-byte")
				}
				return
			}
			if strings.Contains(req.PayloadJSON, "bulk_transcript") {
				t.Fatalf("compact payload retained unknown bulk field: %s", req.PayloadJSON)
			}
			decoded, err := hookevent.DecodeHookPayload(strings.NewReader(req.PayloadJSON))
			if err != nil {
				t.Fatal(err)
			}
			if decoded.EffectiveSessionID() != "boundary-session" || decoded.EffectiveToolUseID() != "tool-boundary" || decoded.EffectiveSourceEventID() != "event-boundary" {
				t.Fatalf("compact identity = %+v", decoded)
			}
		})
	}
}

func TestDeliverCompactPayloadStillOversizedFailsPermanently(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("AMX_CONFIG", filepath.Join(t.TempDir(), "missing-config.toml"))
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")

	oldDiscovery := discoverHarnessProcessForDelivery
	discoveryCalls := 0
	discoverHarnessProcessForDelivery = func(string) HarnessProcessDiscovery {
		discoveryCalls++
		return HarnessProcessDiscovery{}
	}
	t.Cleanup(func() { discoverHarnessProcessForDelivery = oldDiscovery })

	payload := `{"session_id":"` + strings.Repeat("x", maxRawHookPayloadBytes) + `"}`
	if len(payload) <= maxRawHookPayloadBytes || len(payload) > maxHookPayloadBytes {
		t.Fatalf("test payload size = %d, want compactable input range", len(payload))
	}
	client := &fakeDaemonClient{}
	result := deliverFromStdinWithDeps(
		"PreToolUse", 42, "claude", "/tmp/amx", DeliveryOptions{RequestID: "compact-still-oversized"}, hookPayloadFile(t, payload), client,
	)
	if result.Status != DeliveryPermanentFailure || result.Reason != "payload_oversized" || result.Retryable {
		t.Fatalf("delivery result = %+v, want permanent payload_oversized failure", result)
	}
	if client.calls != 0 || discoveryCalls != 0 {
		t.Fatalf("oversized compact payload reached daemon/discovery: daemon=%d discovery=%d", client.calls, discoveryCalls)
	}
}

func TestDeliverCompactPayloadCaptureAndHMACUseOriginalBytes(t *testing.T) {
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

	payload := exactSizedHookPayload(t, maxRawHookPayloadBytes+1, map[string]any{
		"session_id": "capture-session",
		"tool_name":  "Bash",
	})
	stdin := hookPayloadFile(t, payload)

	client := &fakeDaemonClient{resp: daemon.Response{OK: true, Delivery: &daemon.DeliveryAcknowledgement{
		SchemaVersion: 1, Status: daemon.DeliveryAcknowledgementAccepted, Reason: "accepted",
	}}}
	result := deliverFromStdinWithDeps(
		"PostToolUse", 42, "claude", "/tmp/amx", DeliveryOptions{RequestID: "compact-payload"}, stdin, client,
	)
	if result.Status != DeliveryAccepted || result.Reason != "accepted" {
		t.Fatalf("delivery result = %+v, want accepted compact payload", result)
	}
	if client.calls != 1 {
		t.Fatalf("daemon calls = %d, want 1", client.calls)
	}
	req := client.reqs[0]
	if req.PayloadEncoding != "json_compact" || req.PayloadSize != int64(len(payload)) || req.PayloadHMAC == "" || !req.PayloadCaptured {
		t.Fatalf("compact request metadata = %+v", req)
	}
	status, err := store.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Descriptor == nil || status.Descriptor.CapturedPayloads != 1 ||
		status.Descriptor.CapturedBytes < int64(len(payload)) || status.Descriptor.CapturedBytes > int64(len(payload)+4096) {
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
	if entry.Truncated || entry.Payload != payload {
		t.Fatalf("capture entry = truncated:%t payload-bytes:%d, want complete original", entry.Truncated, len(entry.Payload))
	}

	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rawEnvelope := diagnostics.NewEnvelope("test.raw")
	journal.ProtectPayload(&rawEnvelope, []byte(payload))
	compactEnvelope := diagnostics.NewEnvelope("test.compact")
	journal.ProtectPayload(&compactEnvelope, []byte(req.PayloadJSON))
	if req.PayloadHMAC != rawEnvelope.PayloadHMAC || req.PayloadHMAC == compactEnvelope.PayloadHMAC {
		t.Fatalf("request HMAC = %q, raw = %q, compact = %q", req.PayloadHMAC, rawEnvelope.PayloadHMAC, compactEnvelope.PayloadHMAC)
	}
}

func TestDeliverOverLimitCaptureIsBoundedAndTruncated(t *testing.T) {
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
	payload := exactSizedHookPayload(t, maxHookPayloadBytes+128, map[string]any{"session_id": "too-large"})
	client := &fakeDaemonClient{}
	result := deliverFromStdinWithDeps(
		"PostToolUse", 42, "claude", "/tmp/amx", DeliveryOptions{RequestID: "over-limit-capture"}, hookPayloadFile(t, payload), client,
	)
	if result.Status != DeliveryPermanentFailure || result.Reason != "payload_oversized" || client.calls != 0 {
		t.Fatalf("delivery result = %+v, daemon calls = %d", result, client.calls)
	}
	status, err := store.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(paths.CaptureDir, status.Descriptor.ID, "*.raw.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("capture files = %v, err = %v", files, err)
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
		t.Fatalf("capture entry = truncated:%t payload-bytes:%d", entry.Truncated, len(entry.Payload))
	}
}

func TestDeliverLargeHarnessPayloadsRetainSourceIdentity(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("AMX_CONFIG", filepath.Join(t.TempDir(), "missing-config.toml"))
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")

	oldDiscovery := discoverHarnessProcessForDelivery
	discoverHarnessProcessForDelivery = func(string) HarnessProcessDiscovery { return HarnessProcessDiscovery{} }
	t.Cleanup(func() { discoverHarnessProcessForDelivery = oldDiscovery })

	seenKeys := make(map[string]bool)
	for _, tc := range []struct {
		name      string
		harness   string
		event     string
		fields    map[string]any
		sessionID string
		toolUseID string
		eventID   string
	}{
		{
			name: "claude snake case", harness: "claude", event: "PreToolUse", sessionID: "claude-session", toolUseID: "claude-tool", eventID: "claude-event",
			fields: map[string]any{"session_id": "claude-session", "tool_use_id": "claude-tool", "source_event_id": "claude-event", "tool_name": "Bash"},
		},
		{
			name: "codex camel case", harness: "codex", event: "PreToolUse", sessionID: "codex-session", toolUseID: "codex-tool", eventID: "codex-event",
			fields: map[string]any{"sessionId": "codex-session", "toolUseId": "codex-tool", "sourceEventId": "codex-event", "toolName": "Bash"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := exactSizedHookPayload(t, maxRawHookPayloadBytes+1, tc.fields)
			client := &fakeDaemonClient{resp: daemon.Response{OK: true}}
			result := deliverFromStdinWithDeps(
				tc.event, 42, tc.harness, "/tmp/amx", DeliveryOptions{RequestID: tc.harness + "-large-tool"}, hookPayloadFile(t, payload), client,
			)
			if result.Status != DeliveryAccepted || client.calls != 1 {
				t.Fatalf("delivery result = %+v, calls = %d", result, client.calls)
			}
			req := client.reqs[0]
			if req.PayloadEncoding != "json_compact" || req.SourceEventID != tc.eventID {
				t.Fatalf("request identity = %+v", req)
			}
			normalized := daemon.NormalizedFromHook(tc.event, tc.harness, req.PayloadJSON, 42, false)
			wantKey := tc.harness + ":" + tc.sessionID
			if normalized.Invalid || normalized.Source.Kind != daemon.SourceHarness || normalized.Source.Key != wantKey ||
				normalized.SessionID != tc.sessionID || normalized.ToolUse != tc.toolUseID || normalized.SourceEventID != tc.eventID {
				t.Fatalf("normalized compact event = %+v, want source %q", normalized, wantKey)
			}
			seenKeys[normalized.Source.Key] = true
		})
	}
	if len(seenKeys) != 2 {
		t.Fatalf("large harness payloads collapsed onto source keys: %v", seenKeys)
	}
}

func TestDeliverCompactResetAndContextLimitMatchRawPayload(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("AMX_CONFIG", filepath.Join(t.TempDir(), "missing-config.toml"))
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	oldDiscovery := discoverHarnessProcessForDelivery
	discoverHarnessProcessForDelivery = func(string) HarnessProcessDiscovery { return HarnessProcessDiscovery{} }
	t.Cleanup(func() { discoverHarnessProcessForDelivery = oldDiscovery })

	for _, tc := range []struct {
		name        string
		harness     string
		event       string
		fields      map[string]any
		wantContext bool
		wantReset   bool
	}{
		{
			name: "reset flow", harness: "copilot", event: "userPromptSubmitted", wantReset: true,
			fields: map[string]any{"sessionId": "reset-session", "prompt": " /clear discard this session transcript"},
		},
		{
			name: "context limit", harness: "claude", event: "Stop", wantContext: true,
			fields: map[string]any{"session_id": "context-session", "title": "Context limit reached", "message": "/compact or /clear to continue"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []daemon.Request
			for _, size := range []int{1024, maxRawHookPayloadBytes + 1} {
				payload := exactSizedHookPayload(t, size, tc.fields)
				client := &fakeDaemonClient{resp: daemon.Response{OK: true}}
				requestID := tc.harness + "-semantic-raw"
				if size > maxRawHookPayloadBytes {
					requestID = tc.harness + "-semantic-compact"
				}
				result := deliverFromStdinWithDeps(
					tc.event, 42, tc.harness, "/tmp/amx", DeliveryOptions{RequestID: requestID}, hookPayloadFile(t, payload), client,
				)
				if result.Status != DeliveryAccepted || client.calls != 1 {
					t.Fatalf("size %d delivery result = %+v, calls = %d", size, result, client.calls)
				}
				requests = append(requests, client.reqs[0])
			}
			if requests[0].PayloadEncoding != "json" || requests[1].PayloadEncoding != "json_compact" {
				t.Fatalf("payload encodings = %q/%q", requests[0].PayloadEncoding, requests[1].PayloadEncoding)
			}
			for _, req := range requests {
				payload, err := hookevent.DecodeHookPayload(strings.NewReader(req.PayloadJSON))
				if err != nil {
					t.Fatal(err)
				}
				if got := hookevent.PayloadHasContextLimit(payload); got != tc.wantContext || req.ContextLimit != tc.wantContext {
					t.Fatalf("context-limit classification = payload:%t request:%t, want %t", got, req.ContextLimit, tc.wantContext)
				}
				if got := hookevent.PayloadHasResetFlow(tc.harness, tc.event, payload); got != tc.wantReset || req.ResetFlow != tc.wantReset {
					t.Fatalf("reset classification = payload:%t request:%t, want %t", got, req.ResetFlow, tc.wantReset)
				}
			}
		})
	}
}

func TestDeliverMalformedAndOverLimitCannotReachDaemonOrDiscovery(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("AMX_CONFIG", filepath.Join(t.TempDir(), "missing-config.toml"))
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	oldDiscovery := discoverHarnessProcessForDelivery
	discoveryCalls := 0
	discoverHarnessProcessForDelivery = func(string) HarnessProcessDiscovery {
		discoveryCalls++
		return HarnessProcessDiscovery{}
	}
	t.Cleanup(func() { discoverHarnessProcessForDelivery = oldDiscovery })

	for _, tc := range []struct {
		name    string
		payload string
		reason  string
	}{
		{name: "malformed", payload: `{"session_id":`, reason: "invalid_payload"},
		{name: "over limit", payload: exactSizedHookPayload(t, maxHookPayloadBytes+1, map[string]any{"session_id": "never-delivered"}), reason: "payload_oversized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := daemon.NewHandler(daemon.NewStateGuard(time.Hour), nil)
			client := &handlerDaemonClient{handler: handler}
			beforeDiscovery := discoveryCalls
			result := deliverFromStdinWithDeps(
				"Stop", 42, "claude", "/tmp/amx", DeliveryOptions{RequestID: "reject-" + strings.ReplaceAll(tc.name, " ", "-")}, hookPayloadFile(t, tc.payload), client,
			)
			if result.Status != DeliveryPermanentFailure || result.Reason != tc.reason {
				t.Fatalf("delivery result = %+v", result)
			}
			if client.calls != 0 || discoveryCalls != beforeDiscovery {
				t.Fatalf("rejected payload reached daemon/discovery: daemon=%d discovery=%d", client.calls, discoveryCalls-beforeDiscovery)
			}
			if states := handler.Handle(daemon.Request{Type: daemon.TypeStateQuery}).States; len(states) != 0 {
				t.Fatalf("rejected payload mutated daemon state: %+v", states)
			}
		})
	}
}

type handlerDaemonClient struct {
	handler *daemon.Handler
	calls   int
}

func (c *handlerDaemonClient) SendWithAutoStart(req daemon.Request, _ string) (daemon.Response, error) {
	c.calls++
	return c.handler.Handle(req), nil
}

func exactSizedHookPayload(t *testing.T, size int, fields map[string]any) string {
	t.Helper()
	payload := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		payload[key] = value
	}
	payload["bulk_transcript"] = ""
	base, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	padding := size - len(base)
	if padding < 0 {
		t.Fatalf("requested payload size %d is smaller than semantic base %d", size, len(base))
	}
	payload["bulk_transcript"] = strings.Repeat("x", padding)
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != size {
		t.Fatalf("exact payload size = %d, want %d", len(encoded), size)
	}
	return string(encoded)
}

func hookPayloadFile(t *testing.T, payload string) *os.File {
	t.Helper()
	stdin, err := os.CreateTemp(t.TempDir(), "hook-payload-*.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	if _, err := stdin.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	return stdin
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
				false, nil, "trace-1", "request-1", "event-7", 29, "hmac-1",
				"json", false, false,
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

func TestDeliverPayloadWithDepsForwardsHarnessProcessIdentity(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	client := &fakeDaemonClient{resp: daemon.Response{OK: true}}
	identity := &daemon.HarnessProcessIdentity{
		Harness: "claude", PID: 4242, StartToken: "Thu Jul 16 10:20:30 2026",
	}

	deliverPayloadWithDeps(
		"Stop", 42, "claude", "/tmp/amx", `{}`, true, identity,
		"trace-1", "request-1", "", 2, "", "json", false, false, client,
		&deliveryRecorder{traceID: "trace-1", requestID: "request-1", event: "Stop"}, time.Now(),
	)
	if client.calls != 1 || client.reqs[0].HarnessProcess == nil || *client.reqs[0].HarnessProcess != *identity {
		t.Fatalf("forwarded request = %+v", client.reqs)
	}
}

func TestHookProcessDetectionJournalsCompleteDegradedAndAbsentIdentity(t *testing.T) {
	paths := diagnostics.PathsForRoot(t.TempDir())
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &deliveryRecorder{
		journal: journal, traceID: "trace-process", requestID: "request-process",
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
