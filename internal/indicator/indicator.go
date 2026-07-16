package indicator

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/hookevent"
)

// maxHookPayloadBytes leaves room for the TypeHookEvent JSON envelope within
// the daemon's 1 MiB request limit. The hook forwards the raw payload so the
// daemon owns parsing, event resolution, and all Kitty interactions.
const maxHookPayloadBytes = 256 << 10

var errHookPayloadOversized = errors.New("hook payload exceeds forwarding limit")

type indicatorDaemonClient interface {
	SendWithAutoStart(req daemon.Request, amxBin string) (daemon.Response, error)
}

var getenv = os.Getenv

// Run is the hook entry point. Hooks are fire-and-forget: all event-processing
// failures are logged and intentionally reported as success to the harness.
func Run(event string, windowID int, producerHarness, amxBin string) error {
	_ = Deliver(event, windowID, producerHarness, amxBin, DeliveryOptions{})
	return nil
}

// Deliver executes one hook delivery and returns its structured outcome. The
// trace/request identity is allocated before stdin is read, so even unreadable
// input can be correlated with a durable final result.
func Deliver(event string, windowID int, producerHarness, amxBin string, options DeliveryOptions) DeliveryResult {
	return deliverFromStdinWithDeps(event, windowID, producerHarness, amxBin, options, os.Stdin, daemon.NewClient())
}

func deliverFromStdinWithDeps(
	event string,
	windowID int,
	producerHarness, amxBin string,
	options DeliveryOptions,
	stdin *os.File,
	client indicatorDaemonClient,
) DeliveryResult {
	started := time.Now()
	traceID := newRequestID()
	requestID := strings.TrimSpace(options.RequestID)
	if requestID == "" {
		requestID = traceID
	}
	recorder := newDeliveryRecorder(traceID, requestID, event, producerHarness, windowID)
	finish := func(result DeliveryResult) DeliveryResult {
		result.LatencyMS = float64(time.Since(started)) / float64(time.Millisecond)
		recorder.complete(result)
		return result
	}

	payloadJSON, err := readHookPayloadJSON(stdin)
	var payloadSize int64
	var payloadHMAC string
	switch {
	case errors.Is(err, errHookPayloadOversized):
		slog.Warn("indicator: degrading oversized hook payload to empty", "event", event, "err", err)
		payloadSize, payloadHMAC = recorder.recordPayload([]byte(payloadJSON), "degraded", "payload_oversized")
		recorder.captureRawPayload(event, windowID, producerHarness, payloadJSON, true)
		payloadJSON = ""
	case err != nil:
		slog.Warn("indicator: ignoring unreadable hook payload", "event", event, "err", err)
		recorder.recordPayload(nil, "error", "payload_unreadable")
		recorder.record("hook.validation", "rejected", "payload_unreadable", map[string]string{"error": err.Error()}, 0)
		return finish(resultFor(started, traceID, requestID, DeliveryPermanentFailure, "payload_unreadable", nil))
	default:
		payloadSize, payloadHMAC = recorder.recordPayload([]byte(payloadJSON), "success", "payload_read")
		recorder.captureRawPayload(event, windowID, producerHarness, payloadJSON, false)
	}
	payload, err := hookevent.DecodeHookPayload(strings.NewReader(payloadJSON))
	if err != nil {
		recorder.record("hook.validation", "rejected", "invalid_payload", map[string]string{"error": err.Error()}, 0)
		return finish(resultFor(started, traceID, requestID, DeliveryPermanentFailure, "invalid_payload", nil))
	}
	if !validRequestID(requestID) {
		recorder.record("hook.validation", "rejected", "invalid_request_id", nil, 0)
		return finish(resultFor(started, traceID, requestID, DeliveryPermanentFailure, "invalid_request_id", nil))
	}
	recorder.record("hook.validation", "success", "payload_valid", map[string]string{
		"source_event_id_present": fmt.Sprint(payload.EffectiveSourceEventID() != ""),
	}, 0)
	discovery := DiscoverHarnessProcess(producerHarness)
	recorder.recordProcessDetection(discovery)
	result := deliverPayloadWithDeps(
		event, windowID, producerHarness, amxBin, payloadJSON, discovery.PipeMode, discovery.Identity(),
		traceID, requestID, payload.EffectiveSourceEventID(), payloadSize, payloadHMAC,
		client, recorder, started,
	)
	return finish(result)
}

func runWithDeps(event string, windowID int, producerHarness, amxBin, payloadJSON string, pipeMode bool, client indicatorDaemonClient) error {
	started := time.Now()
	traceID := newRequestID()
	requestID := traceID
	payload, _ := hookevent.DecodeHookPayload(strings.NewReader(payloadJSON))
	_ = deliverPayloadWithDeps(
		event, windowID, producerHarness, amxBin, payloadJSON, pipeMode, nil,
		traceID, requestID, payload.EffectiveSourceEventID(), int64(len(payloadJSON)), "",
		client, &deliveryRecorder{traceID: traceID, requestID: requestID, event: event}, started,
	)
	return nil
}

// newRequestID returns a random UUIDv4 string identifying one hook-indicator
// invocation. The same ID travels with every auto-start retry of that
// invocation (the client resends the identical Request), so the daemon can
// apply the underlying event exactly once even when an ambiguous response
// timeout forces a resend. A crypto/rand failure is treated as fatal to the ID
// only: we fall back to an empty ID (legacy, non-deduplicated) rather than
// reusing a predictable value that could collide across invocations.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		slog.Warn("indicator: request ID generation failed; sending legacy non-deduplicated request", "err", err)
		return ""
	}
	// RFC 4122 version 4 / variant bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

func readHookPayloadJSON(stdin *os.File) (string, error) {
	if stdin == nil {
		return "", nil
	}
	info, err := stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("stat stdin: %w", err)
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", nil
	}

	data, err := io.ReadAll(io.LimitReader(stdin, maxHookPayloadBytes+1))
	if err != nil {
		return "", fmt.Errorf("read hook payload: %w", err)
	}
	if len(data) > maxHookPayloadBytes {
		// Return the bounded prefix alongside the sentinel error so an explicitly
		// authorized raw capture can preserve evidence without allowing the hook
		// process to buffer an unbounded payload. The daemon receives an empty
		// payload, which DecodeHookPayload intentionally accepts.
		return string(bytes.TrimSpace(data)), fmt.Errorf("%w: %d bytes", errHookPayloadOversized, maxHookPayloadBytes)
	}
	return string(bytes.TrimSpace(data)), nil
}

func resolveHookWindowID(windowID int) (int, error) {
	if windowID < 0 {
		return 0, fmt.Errorf("invalid window ID %d", windowID)
	}
	if windowID > 0 {
		return windowID, nil
	}

	rawID := strings.TrimSpace(os.Getenv("KITTY_WINDOW_ID"))
	if rawID == "" {
		return 0, fmt.Errorf("KITTY_WINDOW_ID is not set")
	}
	windowID, err := strconv.Atoi(rawID)
	if err != nil || windowID <= 0 {
		return 0, fmt.Errorf("invalid KITTY_WINDOW_ID %q", rawID)
	}
	return windowID, nil
}
