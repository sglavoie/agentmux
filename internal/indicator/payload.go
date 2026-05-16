package indicator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

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
// payload without error.
func DecodeHookPayload(r io.Reader) (HookPayload, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return HookPayload{}, fmt.Errorf("read hook payload: %w", err)
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
