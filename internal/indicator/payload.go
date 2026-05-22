package indicator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
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
	Reason                string `json:"reason"`
	Source                string `json:"source"`
	Prompt                string `json:"prompt"`
	InitialPrompt         string `json:"initial_prompt"`
	InitialPromptCamel    string `json:"initialPrompt"`
	Title                 string `json:"title"`
	Message               string `json:"message"`
	ToolName              string `json:"tool_name"`
	ToolNameCamel         string `json:"toolName"`
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

func (p HookPayload) EffectivePrompt() string {
	if p.Prompt != "" {
		return p.Prompt
	}
	if p.InitialPrompt != "" {
		return p.InitialPrompt
	}
	return p.InitialPromptCamel
}

func (p HookPayload) EffectiveToolName() string {
	if p.ToolName != "" {
		return p.ToolName
	}
	return p.ToolNameCamel
}

// payloadHasResetFlow classifies user-initiated reset flows from the harness
// hook payload. The daemon only needs a boolean marker, but the indicator must
// respect the two harness contracts:
//   - Claude: `/clear` is exposed through lifecycle payloads.
//   - Copilot: documented reset markers are slash-command prompts and
//     sessionStart.source == "new", but the interactive CLI can also terminate
//     the current session with sessionEnd.reason == "user_exit" before the next
//     session starts.
func payloadHasResetFlow(harnessName, event string, payload HookPayload) bool {
	switch strings.ToLower(strings.TrimSpace(harnessName)) {
	case harness.Claude:
		return claudePayloadHasResetFlow(event, payload)
	case harness.Copilot:
		return copilotPayloadHasResetFlow(event, payload)
	default:
		return claudePayloadHasResetFlow(event, payload) || copilotPayloadHasResetFlow(event, payload)
	}
}

func hasResetPromptCommand(prompt string) bool {
	fields := strings.Fields(strings.TrimSpace(prompt))
	if len(fields) == 0 {
		return false
	}
	switch strings.ToLower(fields[0]) {
	case "/clear", "/reset", "/new":
		return true
	default:
		return false
	}
}

func claudePayloadHasResetFlow(event string, payload HookPayload) bool {
	switch event {
	case "SessionEnd":
		return normalizeLifecycleValue(payload.Reason) == "clear"
	case "SessionStart":
		return normalizeLifecycleValue(payload.Source) == "clear"
	default:
		return false
	}
}

func copilotPayloadHasResetFlow(event string, payload HookPayload) bool {
	switch event {
	case "userPromptSubmitted":
		return hasResetPromptCommand(payload.EffectivePrompt())
	case "sessionStart":
		return normalizeLifecycleValue(payload.Source) == "new"
	case "sessionEnd":
		return normalizeLifecycleValue(payload.Reason) == "user_exit"
	default:
		return false
	}
}

func normalizeLifecycleValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
