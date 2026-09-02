package indicator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
)

func TestDeliverResolvesProducerConfigDirFromEnvAndStampsEvidence(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("AMX_CONFIG", filepath.Join(t.TempDir(), "missing-config.toml"))
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")
	configDir := filepath.Join(t.TempDir(), "claude-personal")
	t.Setenv("CLAUDE_CONFIG_DIR", configDir+string(os.PathSeparator))

	oldDiscovery := discoverHarnessProcessForDelivery
	discoverHarnessProcessForDelivery = func(string) HarnessProcessDiscovery { return HarnessProcessDiscovery{} }
	t.Cleanup(func() { discoverHarnessProcessForDelivery = oldDiscovery })

	client := &fakeDaemonClient{resp: protocol.Response{OK: true, Delivery: &protocol.DeliveryAcknowledgement{
		SchemaVersion: 1, Status: protocol.DeliveryAcknowledgementAccepted, Reason: "accepted", Profile: "personal-codex",
	}}}
	result := deliverFromStdinWithDeps(
		"PreToolUse", 42, "claude", "/tmp/amx", DeliveryOptions{RequestID: "producer-config-dir"},
		hookPayloadFile(t, `{"session_id":"producer-session"}`), client,
	)
	if result.Status != DeliveryAccepted || client.calls != 1 {
		t.Fatalf("delivery result = %+v, calls = %d", result, client.calls)
	}
	if got := client.reqs[0].ProducerConfigDir; got != configDir {
		t.Fatalf("request producer config dir = %q, want cleaned %q", got, configDir)
	}

	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	records, err := journal.Read(context.Background(), diagnostics.Selector{RequestID: "producer-config-dir"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("no journal records for delivery")
	}
	var sawDelivery bool
	for _, record := range records {
		if !strings.HasPrefix(record.Stage, "hook.") {
			continue
		}
		if record.Evidence["config_dir"] != configDir {
			t.Errorf("stage %s config_dir = %q, want %q", record.Stage, record.Evidence["config_dir"], configDir)
		}
		if record.Stage == "hook.delivery" {
			sawDelivery = true
			if record.Profile != "personal-codex" || record.Evidence["routed_profile"] != "personal-codex" {
				t.Errorf("delivery record profile = %q routed_profile = %q, want routed ack profile on both", record.Profile, record.Evidence["routed_profile"])
			}
		}
	}
	if !sawDelivery {
		t.Fatalf("journal records lack hook.delivery: %+v", records)
	}
}

func TestResolveProducerConfigDirFallbacksAndUnknownHarness(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got, want := resolveProducerConfigDir("claude"), filepath.Clean(harness.DefaultHomeDir("claude")); got != want {
		t.Fatalf("default home resolution = %q, want %q", got, want)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "~/claude-home/")
	if got, want := resolveProducerConfigDir("claude"), filepath.Clean(config.ExpandTilde("~/claude-home")); got != want {
		t.Fatalf("tilde env resolution = %q, want %q", got, want)
	}
	if got := resolveProducerConfigDir("future-harness"); got != "" {
		t.Fatalf("unknown harness config dir = %q, want empty", got)
	}
	if got := resolveProducerConfigDir(""); got != "" {
		t.Fatalf("empty harness config dir = %q, want empty", got)
	}
}

func TestDeliverPayloadWithDepsStructuredOutcomes(t *testing.T) {
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/kitty")

	for _, tc := range []struct {
		name   string
		resp   protocol.Response
		err    error
		status DeliveryStatus
		reason string
	}{
		{
			name: "accepted",
			resp: protocol.Response{OK: true, Delivery: &protocol.DeliveryAcknowledgement{
				SchemaVersion: 1, Status: protocol.DeliveryAcknowledgementAccepted, Reason: "accepted",
			}},
			status: DeliveryAccepted, reason: "accepted",
		},
		{
			name: "duplicate",
			resp: protocol.Response{OK: true, Delivery: &protocol.DeliveryAcknowledgement{
				SchemaVersion: 1, Status: protocol.DeliveryAcknowledgementDuplicate, Reason: "request_replayed",
			}},
			status: DeliveryDuplicate, reason: "duplicate",
		},
		{
			name: "permanent rejection",
			resp: protocol.Response{OK: true, Delivery: &protocol.DeliveryAcknowledgement{
				SchemaVersion: 1, Status: protocol.DeliveryAcknowledgementRejected, Reason: "unknown_event",
			}},
			status: DeliveryPermanentFailure, reason: "unknown_event",
		},
		{
			name:   "retryable rejection",
			resp:   protocol.Response{OK: false, Error: "busy", Retryable: true},
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
	client := &fakeDaemonClient{resp: protocol.Response{OK: true}}
	identity := &protocol.HarnessProcessIdentity{
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
