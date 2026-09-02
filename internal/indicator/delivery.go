package indicator

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
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
	SchemaVersion         int                               `json:"schema_version"`
	Status                DeliveryStatus                    `json:"status"`
	Retryable             bool                              `json:"retryable"`
	Reason                string                            `json:"reason"`
	TraceID               string                            `json:"trace_id"`
	RequestID             string                            `json:"request_id"`
	DaemonAcknowledgement *protocol.DeliveryAcknowledgement `json:"daemon_acknowledgement,omitempty"`
	LatencyMS             float64                           `json:"latency_ms"`
}

func (r DeliveryResult) Successful() bool {
	return r.Status == DeliveryAccepted || r.Status == DeliveryDuplicate
}

type observedDaemonClient interface {
	SendWithAutoStartObserved(protocol.Request, string, ipc.DeliveryObserver) (protocol.Response, error)
}

type deliveryRecorder struct {
	journal                 *diagnostics.Journal
	capture                 bool
	detailFull              bool
	payloadCaptureAttempted bool
	payloadCaptured         bool
	traceID                 string
	requestID               string
	event                   string
	harness                 string
	configDir               string
	payloadHMAC             string
	payloadSize             int64
	windowID                int
}

func newDeliveryRecorder(traceID, requestID, event, producerHarness string, windowID int) *deliveryRecorder {
	recorder := &deliveryRecorder{
		traceID: traceID, requestID: requestID, event: strings.TrimSpace(event),
		harness: canonicalHarness(producerHarness), windowID: windowID, capture: true,
	}
	recorder.configDir = resolveProducerConfigDir(recorder.harness)
	paths, err := diagnostics.DefaultPaths()
	if err != nil {
		return recorder
	}
	options := diagnostics.JournalOptions{}
	if cfg, cfgErr := config.LoadDefault(); cfgErr == nil {
		if !cfg.Defaults.Diagnostics.JournalEnabledValue() {
			return recorder
		}
		recorder.detailFull = cfg.Defaults.Diagnostics.JournalDetailFullValue()
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

// resolveProducerConfigDir resolves the producing harness's home/config
// directory from its config env var (visible because the hook client runs
// inside the harness process environment), falling back to the harness
// default. Normalization matches the config side: tilde expansion plus
// filepath.Clean, without resolving symlinks.
func resolveProducerConfigDir(harnessName string) string {
	if harness.NormalizeKnown(harnessName) == "" {
		return ""
	}
	dir := strings.TrimSpace(getenv(harness.EnvVar(harnessName)))
	if dir == "" {
		dir = harness.DefaultHomeDir(harnessName)
	}
	if dir == "" {
		return ""
	}
	return filepath.Clean(config.ExpandTilde(dir))
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
	if r.configDir != "" {
		if evidence == nil {
			evidence = make(map[string]string)
		}
		evidence["config_dir"] = r.configDir
	}
	envelope.Evidence = evidence
	return envelope
}

func (r *deliveryRecorder) producerConfigDir() string {
	if r == nil {
		return ""
	}
	return r.configDir
}

func (r *deliveryRecorder) append(envelope diagnostics.TraceEnvelope) {
	if r == nil || r.journal == nil {
		return
	}
	if !r.shouldPersist(envelope) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r.journal.TryAppend(ctx, envelope)
}

// shouldPersist applies journal_detail sampling before the fail-open append.
// hook.delivery is always written: it is the terminal per-event record and the
// activation-evidence anchor. Every non-success-class outcome (error, failed,
// rejected, degraded, retryable_failure, permanent_failure, ...) is always
// written; only healthy intermediate stages are suppressed at standard detail.
func (r *deliveryRecorder) shouldPersist(envelope diagnostics.TraceEnvelope) bool {
	if r.detailFull || envelope.Stage == "hook.delivery" {
		return true
	}
	switch envelope.Outcome {
	case "success", "accepted", "absent":
		return false
	default:
		return true
	}
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

func (r *deliveryRecorder) recordProcessDetection(discovery HarnessProcessDiscovery) {
	evidence := map[string]string{
		"pipe_mode": fmt.Sprint(discovery.PipeMode),
	}
	outcome, reason := "absent", "identity_absent"
	if discovery.Harness != "" {
		evidence["process_harness"] = discovery.Harness
	}
	if discovery.PID > 0 {
		evidence["process_pid"] = fmt.Sprint(discovery.PID)
	}
	switch {
	case discovery.Trackable:
		outcome, reason = "success", "complete_identity"
		evidence["process_start_token"] = discovery.StartToken
	case discovery.Harness != "" && discovery.PID > 0:
		outcome, reason = "degraded", "incomplete_identity"
	}
	r.record("hook.process_detection", outcome, reason, evidence, 0)
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
	// Stash the payload identity so the always-written hook.delivery record
	// retains it even when this success-path stage is sampled away.
	r.payloadHMAC = hmac
	r.payloadSize = size
	r.append(envelope)
	return size, hmac
}

func (r *deliveryRecorder) complete(result DeliveryResult) {
	if r == nil {
		return
	}
	evidence := map[string]string{"retryable": fmt.Sprint(result.Retryable)}
	if r.event != "" {
		evidence["event"] = r.event
	}
	ack := result.DaemonAcknowledgement
	if ack != nil {
		if ack.ProducerHarness != "" {
			r.harness = canonicalHarness(ack.ProducerHarness)
		}
		evidence["routed_profile"] = ack.Profile
	}
	envelope := r.envelope("hook.delivery", string(result.Status), result.Reason, evidence, result.LatencyMS)
	if ack != nil {
		envelope.Profile = ack.Profile
	}
	envelope.PayloadHMAC = r.payloadHMAC
	envelope.PayloadSize = r.payloadSize
	r.append(envelope)
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

func resultFor(started time.Time, traceID, requestID string, status DeliveryStatus, reason string, ack *protocol.DeliveryAcknowledgement) DeliveryResult {
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
	harnessProcess *protocol.HarnessProcessIdentity,
	traceID, requestID, sourceEventID string,
	payloadSize int64,
	payloadHMAC string,
	payloadEncoding string,
	contextLimit bool,
	resetFlow bool,
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

	req := protocol.Request{
		Type:              protocol.TypeHookEvent,
		RequestID:         requestID,
		TraceID:           traceID,
		SourceEventID:     sourceEventID,
		PayloadSize:       payloadSize,
		PayloadHMAC:       payloadHMAC,
		PayloadEncoding:   payloadEncoding,
		PayloadCaptured:   recorder.payloadCaptured,
		ProducerHarness:   producerHarness,
		ProducerConfigDir: recorder.producerConfigDir(),
		HarnessProcess:    harnessProcess,
		WindowID:          resolvedWindowID,
		Event:             event,
		Harness:           producerHarness,
		PayloadJSON:       payloadJSON,
		KittyListenOn:     listenOn,
		PipeMode:          pipeMode,
		ContextLimit:      contextLimit,
		ResetFlow:         resetFlow,
	}

	observer := func(stage ipc.DeliveryStageEvent) {
		recorder.record("hook."+stage.Stage, stage.Outcome, stage.Reason, map[string]string{
			"attempt": fmt.Sprint(stage.Attempt),
		}, stage.LatencyMS)
	}
	var resp protocol.Response
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
		observer(ipc.DeliveryStageEvent{
			Stage: ipc.DeliveryStageIPCAttempt, Attempt: 1,
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
		case protocol.DeliveryAcknowledgementDuplicate:
			recorder.record("hook.acknowledgement", "success", "duplicate", nil, 0)
			return resultFor(started, traceID, requestID, DeliveryDuplicate, "duplicate", ack)
		case protocol.DeliveryAcknowledgementRejected:
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
