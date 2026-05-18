package indicator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// maxHookPayloadBytes caps the size of a hook payload we are willing to read
// from stdin. The daemon already rejects requests above 1 MiB, so reading
// more here would be wasted work — and a misbehaving (or malicious) hook
// that piped a multi-GiB JSON blob into stdin would force the hook script to
// buffer the whole stream in memory before we could reject it. Cap at the
// daemon's ceiling so the two limits stay in sync; anything beyond is
// truncated and surfaces as a JSON parse error from the leftover bytes.
const maxHookPayloadBytes = 1 << 20

// HookPayload captures the hook fields Agent Mux currently needs for icon logic
// and debug traces. Unknown fields are ignored.
type HookPayload struct {
	HookEventName         string `json:"hook_event_name"`
	NotificationType      string `json:"notification_type"`
	NotificationTypeCamel string `json:"notificationType"`
	StopReason            string `json:"stop_reason"`
	StopReasonCamel       string `json:"stopReason"`
	Title                 string `json:"title"`
	Message               string `json:"message"`
}

// ReadHookPayload decodes hook JSON from stdin when stdin is piped. Interactive
// invocations (stdin attached to a TTY) return an empty payload.
func ReadHookPayload(stdin *os.File) (HookPayload, error) {
	if stdin == nil {
		return HookPayload{}, nil
	}

	info, err := stdin.Stat()
	if err != nil {
		return HookPayload{}, fmt.Errorf("stat stdin: %w", err)
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return HookPayload{}, nil
	}

	return DecodeHookPayload(stdin)
}

// DecodeHookPayload decodes a hook payload from r. Empty input yields a zero
// payload without error. Reads are bounded by maxHookPayloadBytes so a hook
// upstream cannot force this process to buffer unbounded memory before the
// daemon's own limit would reject the request anyway.
func DecodeHookPayload(r io.Reader) (HookPayload, error) {
	// Read one extra byte past the cap so we can distinguish "exactly at
	// the limit" (allowed) from "exceeded the limit" (rejected) without
	// silently truncating mid-token.
	data, err := io.ReadAll(io.LimitReader(r, maxHookPayloadBytes+1))
	if err != nil {
		return HookPayload{}, fmt.Errorf("read hook payload: %w", err)
	}
	if len(data) > maxHookPayloadBytes {
		return HookPayload{}, fmt.Errorf("hook payload exceeds %d bytes", maxHookPayloadBytes)
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return HookPayload{}, nil
	}

	var payload HookPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return HookPayload{}, fmt.Errorf("parse hook payload JSON: %w", err)
	}
	return payload, nil
}

func (p HookPayload) EffectiveNotificationType() string {
	if p.NotificationType != "" {
		return p.NotificationType
	}
	return p.NotificationTypeCamel
}

func (p HookPayload) EffectiveStopReason() string {
	if p.StopReason != "" {
		return p.StopReason
	}
	return p.StopReasonCamel
}
