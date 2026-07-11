package indicator

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// maxHookPayloadBytes leaves room for the TypeHookEvent JSON envelope within
// the daemon's 1 MiB request limit. The hook forwards the raw payload so the
// daemon owns parsing, event resolution, and all Kitty interactions.
const maxHookPayloadBytes = 256 << 10

type indicatorDaemonClient interface {
	SendWithAutoStart(req daemon.Request, amxBin string) (daemon.Response, error)
}

// Run is the hook entry point. Hooks are fire-and-forget: all event-processing
// failures are logged and intentionally reported as success to the harness.
func Run(event string, windowID int, amxBin string) error {
	if strings.TrimSpace(os.Getenv("KITTY_LISTEN_ON")) == "" {
		return nil
	}
	payloadJSON, err := readHookPayloadJSON(os.Stdin)
	if err != nil {
		slog.Warn("indicator: ignoring unreadable hook payload", "event", event, "err", err)
		payloadJSON = ""
	}
	return runWithDeps(event, windowID, amxBin, payloadJSON, IsPipeMode(), daemon.NewClient())
}

func runWithDeps(event string, windowID int, amxBin, payloadJSON string, pipeMode bool, client indicatorDaemonClient) error {
	if strings.TrimSpace(os.Getenv("KITTY_LISTEN_ON")) == "" {
		return nil
	}
	// Forward only a transferable endpoint. A process-local "fd:" descriptor is
	// useless to the detached daemon; when normalization yields nothing the
	// daemon falls back to its own established Kitty client.
	listenOn := kitty.NormalizedListenAddressFromEnv()

	windowID, err := resolveHookWindowID(windowID)
	if err != nil {
		slog.Warn("indicator: ignoring hook with invalid Kitty window", "event", event, "err", err)
		return nil
	}

	resp, err := client.SendWithAutoStart(daemon.Request{
		Type:          daemon.TypeHookEvent,
		RequestID:     newRequestID(),
		WindowID:      windowID,
		Event:         event,
		PayloadJSON:   payloadJSON,
		KittyListenOn: listenOn,
		PipeMode:      pipeMode,
	}, amxBin)
	if err != nil {
		slog.Warn("indicator: daemon unavailable; dropping hook event", "event", event, "window_id", windowID, "err", err)
	} else if !resp.OK {
		slog.Warn("indicator: daemon rejected hook event; dropping it", "event", event, "window_id", windowID, "error", resp.Error)
	}
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
		return "", fmt.Errorf("hook payload exceeds %d bytes", maxHookPayloadBytes)
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
