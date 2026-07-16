package indicator

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

const DeliveryResultSchemaVersion = 1

type DeliveryStatus string

const (
	DeliveryAccepted         DeliveryStatus = "accepted"
	DeliveryDuplicate        DeliveryStatus = "duplicate"
	DeliveryRetryableFailure DeliveryStatus = "retryable_failure"
	DeliveryPermanentFailure DeliveryStatus = "permanent_failure"
)

// DeliveryOptions carries acknowledgement controls supplied by hook-indicator.
// An empty RequestID allocates a fresh UUID before stdin is inspected.
type DeliveryOptions struct {
	RequestID string
}

var captureHookPayload = func(ctx context.Context, request diagnostics.CapturePayloadRequest) (diagnostics.CaptureWriteResult, error) {
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		return diagnostics.CaptureWriteResult{}, err
	}
	store, err := diagnostics.NewCaptureStore(paths, diagnostics.CaptureOptions{})
	if err != nil {
		return diagnostics.CaptureWriteResult{}, err
	}
	return store.Record(ctx, request)
}

// DeliveryResult is the stable hook-client outcome. JSON and strict callers
// branch on Status/Retryable rather than parsing warnings or daemon error text.
type DeliveryResult struct {
	SchemaVersion         int                             `json:"schema_version"`
	Status                DeliveryStatus                  `json:"status"`
	Retryable             bool                            `json:"retryable"`
	Reason                string                          `json:"reason"`
	TraceID               string                          `json:"trace_id"`
	RequestID             string                          `json:"request_id"`
	DaemonAcknowledgement *daemon.DeliveryAcknowledgement `json:"daemon_acknowledgement,omitempty"`
	LatencyMS             float64                         `json:"latency_ms"`
}

func (r DeliveryResult) Successful() bool {
	return r.Status == DeliveryAccepted || r.Status == DeliveryDuplicate
}

type observedDaemonClient interface {
	SendWithAutoStartObserved(daemon.Request, string, daemon.DeliveryObserver) (daemon.Response, error)
}

type deliveryRecorder struct {
	journal                 *diagnostics.Journal
	capture                 bool
	payloadCaptureAttempted bool
	payloadCaptured         bool
	traceID                 string
	requestID               string
	event                   string
	harness                 string
	windowID                int
}

func newDeliveryRecorder(traceID, requestID, event, producerHarness string, windowID int) *deliveryRecorder {
	recorder := &deliveryRecorder{
		traceID: traceID, requestID: requestID, event: strings.TrimSpace(event),
		harness: canonicalHarness(producerHarness), windowID: windowID, capture: true,
	}
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		return recorder
	}
	options := diagnostics.JournalOptions{}
	if cfg, cfgErr := config.LoadDefault(); cfgErr == nil {
		if !cfg.Defaults.Diagnostics.JournalEnabledValue() {
			return recorder
		}
		options.KeepDays = cfg.Defaults.Diagnostics.JournalKeepDays
		options.MaxBytes = int64(cfg.Defaults.Diagnostics.JournalMaxMiB) << 20
	}
	journal, err := diagnostics.NewJournal(paths, options)
	if err == nil {
		recorder.journal = journal
	}
	return recorder
}

func canonicalHarness(value string) string {
	if known := harness.NormalizeKnown(value); known != "" {
		return known
	}
	return strings.TrimSpace(value)
}

func (r *deliveryRecorder) envelope(stage, outcome, reason string, evidence map[string]string, latencyMS float64) diagnostics.TraceEnvelope {
	envelope := diagnostics.NewEnvelope(stage)
	envelope.TraceID = r.traceID
	envelope.RequestID = r.requestID
	envelope.Harness = r.harness
	envelope.WindowID = r.windowID
	envelope.Outcome = outcome
	envelope.Reason = reason
	envelope.LatencyMS = latencyMS
	envelope.Evidence = evidence
	return envelope
}

func (r *deliveryRecorder) append(envelope diagnostics.TraceEnvelope) {
	if r == nil || r.journal == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r.journal.TryAppend(ctx, envelope)
}

func (r *deliveryRecorder) record(stage, outcome, reason string, evidence map[string]string, latencyMS float64) {
	if r == nil {
		return
	}
	if evidence == nil {
		evidence = make(map[string]string)
	}
	if r.event != "" {
		evidence["event"] = r.event
	}
	r.append(r.envelope(stage, outcome, reason, evidence, latencyMS))
}

func (r *deliveryRecorder) recordPayload(payload []byte, outcome, reason string) (size int64, hmac string) {
	size = int64(len(payload))
	if r == nil {
		return size, ""
	}
	envelope := r.envelope("hook.payload_read", outcome, reason, map[string]string{"event": r.event}, 0)
	if r.journal != nil {
		r.journal.ProtectPayload(&envelope, payload)
		hmac = envelope.PayloadHMAC
	}
	r.append(envelope)
	return size, hmac
}

func (r *deliveryRecorder) complete(result DeliveryResult) {
	if r == nil {
		return
	}
	if ack := result.DaemonAcknowledgement; ack != nil {
		if ack.ProducerHarness != "" {
			r.harness = canonicalHarness(ack.ProducerHarness)
		}
		envelope := r.envelope("hook.delivery", string(result.Status), result.Reason, map[string]string{
			"event":     r.event,
			"retryable": fmt.Sprint(result.Retryable),
		}, result.LatencyMS)
		envelope.Profile = ack.Profile
		r.append(envelope)
		return
	}
	r.record("hook.delivery", string(result.Status), result.Reason, map[string]string{
		"retryable": fmt.Sprint(result.Retryable),
	}, result.LatencyMS)
}

func (r *deliveryRecorder) captureRawPayload(event string, windowID int, producerHarness, payloadJSON string, truncated bool) {
	if r == nil || !r.capture || r.payloadCaptureAttempted {
		return
	}
	r.payloadCaptureAttempted = true
	resolvedWindowID := windowID
	if candidate, err := resolveHookWindowID(windowID); err == nil {
		resolvedWindowID = candidate
	}
	rawProducer := strings.TrimSpace(producerHarness)
	if known := harness.NormalizeKnown(rawProducer); known != "" {
		producerHarness = known
	} else {
		producerHarness = rawProducer
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	result, err := captureHookPayload(ctx, diagnostics.CapturePayloadRequest{
		WindowID: resolvedWindowID, Harness: producerHarness, Event: event, TraceID: r.traceID,
		RequestID: r.requestID, Payload: []byte(payloadJSON), Truncated: truncated,
	})
	cancel()
	if err != nil {
		r.record("hook.capture", "error", "capture_unavailable", nil, 0)
		return
	}
	r.payloadCaptured = result.Captured
	if result.Captured {
		r.record("hook.capture", "success", result.Reason, nil, 0)
	}
}

func resultFor(started time.Time, traceID, requestID string, status DeliveryStatus, reason string, ack *daemon.DeliveryAcknowledgement) DeliveryResult {
	return DeliveryResult{
		SchemaVersion:         DeliveryResultSchemaVersion,
		Status:                status,
		Retryable:             status == DeliveryRetryableFailure,
		Reason:                reason,
		TraceID:               traceID,
		RequestID:             requestID,
		DaemonAcknowledgement: ack,
		LatencyMS:             float64(time.Since(started)) / float64(time.Millisecond),
	}
}

func validRequestID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func deliverPayloadWithDeps(
	event string,
	windowID int,
	producerHarness, amxBin, payloadJSON string,
	pipeMode bool,
	traceID, requestID, sourceEventID string,
	payloadSize int64,
	payloadHMAC string,
	client indicatorDaemonClient,
	recorder *deliveryRecorder,
	started time.Time,
) DeliveryResult {
	rawListenOn := strings.TrimSpace(getenv("KITTY_LISTEN_ON"))
	if rawListenOn == "" {
		recorder.record("hook.kitty_detection", "rejected", "outside_kitty", nil, 0)
		return resultFor(started, traceID, requestID, DeliveryPermanentFailure, "outside_kitty", nil)
	}
	recorder.record("hook.kitty_detection", "success", "kitty_detected", nil, 0)

	resolvedWindowID, err := resolveHookWindowID(windowID)
	if err != nil {
		recorder.record("hook.window_resolution", "rejected", "invalid_window", map[string]string{"error": err.Error()}, 0)
		return resultFor(started, traceID, requestID, DeliveryPermanentFailure, "invalid_window", nil)
	}
	recorder.windowID = resolvedWindowID
	recorder.record("hook.window_resolution", "success", "window_resolved", nil, 0)

	listenOn := kitty.NormalizedListenAddressFromEnv()
	rawProducer := strings.TrimSpace(producerHarness)
	producerHarness = harness.NormalizeKnown(rawProducer)
	if rawProducer != "" && producerHarness == "" {
		producerHarness = rawProducer
	}
	recorder.harness = canonicalHarness(producerHarness)
	if recorder.capture && !recorder.payloadCaptureAttempted {
		recorder.captureRawPayload(event, resolvedWindowID, producerHarness, payloadJSON, false)
	}

	req := daemon.Request{
		Type:            daemon.TypeHookEvent,
		RequestID:       requestID,
		TraceID:         traceID,
		SourceEventID:   sourceEventID,
		PayloadSize:     payloadSize,
		PayloadHMAC:     payloadHMAC,
		PayloadEncoding: "json",
		PayloadCaptured: recorder.payloadCaptured,
		ProducerHarness: producerHarness,
		WindowID:        resolvedWindowID,
		Event:           event,
		Harness:         producerHarness,
		PayloadJSON:     payloadJSON,
		KittyListenOn:   listenOn,
		PipeMode:        pipeMode,
	}

	observer := func(stage daemon.DeliveryStageEvent) {
		recorder.record("hook."+stage.Stage, stage.Outcome, stage.Reason, map[string]string{
			"attempt": fmt.Sprint(stage.Attempt),
		}, stage.LatencyMS)
	}
	var resp daemon.Response
	if observed, ok := client.(observedDaemonClient); ok {
		resp, err = observed.SendWithAutoStartObserved(req, amxBin, observer)
	} else {
		req.Attempt = 1
		attemptStarted := time.Now()
		resp, err = client.SendWithAutoStart(req, amxBin)
		outcome, reason := "success", "acknowledged"
		if err != nil {
			outcome, reason = "error", err.Error()
		}
		observer(daemon.DeliveryStageEvent{
			Stage: daemon.DeliveryStageIPCAttempt, Attempt: 1,
			Outcome: outcome, Reason: reason,
			LatencyMS: float64(time.Since(attemptStarted)) / float64(time.Millisecond),
		})
	}
	if err != nil {
		recorder.record("hook.failure", "error", "daemon_unavailable", map[string]string{"error": err.Error()}, 0)
		return resultFor(started, traceID, requestID, DeliveryRetryableFailure, "daemon_unavailable", nil)
	}

	ack := resp.Delivery
	if !resp.OK {
		reason := "daemon_rejected"
		if ack != nil && ack.Reason != "" {
			reason = ack.Reason
		}
		recorder.record("hook.rejection", "rejected", reason, map[string]string{"error": resp.Error}, 0)
		if resp.Retryable {
			return resultFor(started, traceID, requestID, DeliveryRetryableFailure, reason, ack)
		}
		return resultFor(started, traceID, requestID, DeliveryPermanentFailure, reason, ack)
	}
	if ack != nil {
		switch ack.Status {
		case daemon.DeliveryAcknowledgementDuplicate:
			recorder.record("hook.acknowledgement", "success", "duplicate", nil, 0)
			return resultFor(started, traceID, requestID, DeliveryDuplicate, "duplicate", ack)
		case daemon.DeliveryAcknowledgementRejected:
			reason := ack.Reason
			if reason == "" {
				reason = "daemon_rejected"
			}
			recorder.record("hook.rejection", "rejected", reason, nil, 0)
			return resultFor(started, traceID, requestID, DeliveryPermanentFailure, reason, ack)
		}
	}
	recorder.record("hook.acknowledgement", "success", "accepted", nil, 0)
	return resultFor(started, traceID, requestID, DeliveryAccepted, "accepted", ack)
}
