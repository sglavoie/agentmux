package hookevent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

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

// maxQuestionPreviewRunes bounds the free-text question preview carried by
// agent_session_loop wait events. The producer already collapses whitespace and
// caps at the same width; re-bounding on decode means a hand-written, replayed,
// or future producer payload cannot widen the only free-text field the daemon
// persists and the dashboard renders.
const maxQuestionPreviewRunes = 160

// HookPayload captures the hook fields Agent Mux currently needs for icon logic
// and debug traces. Unknown fields are ignored.
type HookPayload struct {
	// SchemaVersion and ContractSetID are the agent-session-loop compatibility
	// block. Harness hooks never carry them and are not asked to: they are what a
	// lifecycle payload presents so a consumer can decide whether it understands
	// the message before it reads the rest. Both are zero-valued for every other
	// producer, and the gate that requires them is scoped to the lifecycle
	// vocabulary for exactly that reason.
	SchemaVersion int    `json:"schema_version"`
	ContractSetID string `json:"contract_set_id"`

	HookEventName         string `json:"hook_event_name"`
	NotificationType      string `json:"notification_type"`
	NotificationTypeCamel string `json:"notificationType"`
	StopReason            string `json:"stop_reason"`
	StopReasonCamel       string `json:"stopReason"`
	Reason                string `json:"reason"`
	PauseReason           string `json:"pause_reason"`
	Source                string `json:"source"`
	Trigger               string `json:"trigger"`
	CWD                   string `json:"cwd"`
	Harness               string `json:"harness"`
	KittyWindowID         int    `json:"kitty_window_id"`
	WindowID              int    `json:"window_id"`
	PlanDir               string `json:"plan_dir"`
	RunID                 string `json:"run_id"`
	RunIDCamel            string `json:"runId"`
	StatePath             string `json:"state_path"`
	RunFinishedAt         string `json:"run_finished_at"`
	QuestionID            string `json:"question_id"`
	QuestionCreatedAt     string `json:"question_created_at"`
	QuestionPreview       string `json:"question_preview"`
	UnansweredQuestions   int    `json:"unanswered_questions"`
	DeliveryState         string `json:"delivery_state"`
	ResolutionAction      string `json:"resolution_action"`
	WaitReason            string `json:"wait_reason"`
	Prompt                string `json:"prompt"`
	InitialPrompt         string `json:"initial_prompt"`
	InitialPromptCamel    string `json:"initialPrompt"`
	Title                 string `json:"title"`
	Message               string `json:"message"`
	ToolName              string `json:"tool_name"`
	ToolNameCamel         string `json:"toolName"`

	// Identity fields carried by the normalized event schema. Each supports the
	// snake-case preferred spelling plus a camelCase fallback so both harness
	// dialects normalize to the same source key. source_sequence and emitted_time
	// are wire-encoded as strings (a numeric spelling that fails JSON decoding is
	// caught upstream and quarantined rather than silently zeroed). Prompt bodies
	// are deliberately absent from the identity set: they never key a source.
	SessionID                      string `json:"session_id"`
	SessionIDCamel                 string `json:"sessionId"`
	TurnID                         string `json:"turn_id"`
	TurnIDCamel                    string `json:"turnId"`
	ToolUseID                      string `json:"tool_use_id"`
	ToolUseIDCamel                 string `json:"toolUseId"`
	AgentID                        string `json:"agent_id"`
	AgentIDCamel                   string `json:"agentId"`
	AgentType                      string `json:"agent_type"`
	AgentTypeCamel                 string `json:"agentType"`
	SourceSequence                 string `json:"source_sequence"`
	SourceSequenceCamel            string `json:"sourceSequence"`
	MailboxProjectionSequence      string `json:"mailbox_projection_sequence"`
	MailboxProjectionSequenceCamel string `json:"mailboxProjectionSequence"`
	SourceEventID                  string `json:"source_event_id"`
	SourceEventIDCamel             string `json:"sourceEventId"`
	EmittedTime                    string `json:"emitted_time"`
	EmittedTimeCamel               string `json:"emittedTime"`
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
	return DecodeHookPayloadWithin(r, maxHookPayloadBytes)
}

// DecodeHookPayloadWithin is DecodeHookPayload with the caller's own bound. The
// hook client reads the harness's stdin under a larger one than the daemon's:
// a payload carrying a batch of tool output is decoded there and compacted to
// its identity fields, so what crosses the socket stays within the daemon's
// limit however large the tool output was.
func DecodeHookPayloadWithin(r io.Reader, limit int) (HookPayload, error) {
	// Read one extra byte past the cap so we can distinguish "exactly at
	// the limit" (allowed) from "exceeded the limit" (rejected) without
	// silently truncating mid-token.
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return HookPayload{}, fmt.Errorf("read hook payload: %w", err)
	}
	if len(data) > limit {
		return HookPayload{}, fmt.Errorf("hook payload exceeds %d bytes", limit)
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return HookPayload{}, nil
	}

	var payload HookPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return HookPayload{}, fmt.Errorf("parse hook payload JSON: %w", err)
	}
	payload.QuestionPreview = boundQuestionPreview(payload.QuestionPreview)
	return payload, nil
}

// boundQuestionPreview strips terminal controls, collapses whitespace, and
// truncates to maxQuestionPreviewRunes, matching the producer's own bounding so
// a decoded preview is always a single bounded line of printable text.
func boundQuestionPreview(value string) string {
	collapsed := strings.Join(strings.Fields(stripTerminalControls(value)), " ")
	runes := []rune(collapsed)
	if len(runes) <= maxQuestionPreviewRunes {
		return collapsed
	}
	return string(runes[:maxQuestionPreviewRunes])
}

// stripTerminalControls removes C0/C1 controls from untrusted text. The question
// body is model-controlled and the TUI detail view renders it straight into the
// terminal, so an escape introducer takes its whole sequence with it rather than
// leaving printable parameter residue such as "[2J" behind. Controls that are
// also whitespace become a space so the caller's collapse still separates the
// words they joined.
func stripTerminalControls(value string) string {
	runes := []rune(value)
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(runes); {
		r := runes[i]
		switch {
		case !unicode.IsControl(r):
			b.WriteRune(r)
			i++
		case unicode.IsSpace(r):
			b.WriteRune(' ')
			i++
		default:
			i += controlSequenceLen(runes[i:])
		}
	}
	return b.String()
}

// controlSequenceLen returns how many runes the control sequence starting at
// runes[0] occupies. A bare control is one rune; an ESC or C1 introducer also
// covers its parameters and terminator. An unterminated sequence consumes the
// remainder, which is what a terminal parser would do with it too.
func controlSequenceLen(runes []rune) int {
	switch runes[0] {
	case 0x1b:
		if len(runes) == 1 {
			return 1
		}
		switch runes[1] {
		case '[':
			return 2 + csiTailLen(runes[2:])
		case ']', 'P', 'X', '^', '_':
			return 2 + stringTailLen(runes[2:])
		default:
			return 2
		}
	case 0x9b: // C1 CSI
		return 1 + csiTailLen(runes[1:])
	case 0x90, 0x98, 0x9d, 0x9e, 0x9f: // C1 DCS, SOS, OSC, PM, APC
		return 1 + stringTailLen(runes[1:])
	default:
		return 1
	}
}

// csiTailLen counts the parameter and intermediate runes plus the final rune
// (0x40-0x7e) that closes a CSI sequence.
func csiTailLen(runes []rune) int {
	for i, r := range runes {
		if r >= 0x40 && r <= 0x7e {
			return i + 1
		}
	}
	return len(runes)
}

// stringTailLen counts a string-terminated body plus its terminator: BEL, or ST
// in either its two-rune (ESC \) or single-rune (0x9c) form.
func stringTailLen(runes []rune) int {
	for i, r := range runes {
		switch {
		case r == 0x07 || r == 0x9c:
			return i + 1
		case r == 0x1b && i+1 < len(runes) && runes[i+1] == '\\':
			return i + 2
		}
	}
	return len(runes)
}

func (p HookPayload) EffectiveNotificationType() string {
	return firstNonBlank(p.NotificationType, p.NotificationTypeCamel)
}

func (p HookPayload) EffectiveStopReason() string {
	return firstNonBlank(p.StopReason, p.StopReasonCamel)
}

func (p HookPayload) EffectivePrompt() string {
	return firstNonBlank(p.Prompt, p.InitialPrompt, p.InitialPromptCamel)
}

func (p HookPayload) EffectiveToolName() string {
	return firstNonBlank(p.ToolName, p.ToolNameCamel)
}

// EffectivePauseReason returns the agent-session-loop pause reason, preferring
// the dedicated pause_reason field and falling back to the generic reason the
// loop's hook payload builder always carries.
func (p HookPayload) EffectivePauseReason() string {
	return firstNonBlank(p.PauseReason, p.Reason)
}

// EffectiveSessionID, EffectiveTurnID, and the other identity accessors prefer
// the snake-case spelling and fall back to the camelCase alias, mirroring the
// existing firstNonBlank pattern for tool name and prompt. They let both harness
// dialects resolve to the same normalized identity.
func (p HookPayload) EffectiveSessionID() string {
	return firstNonBlank(p.SessionID, p.SessionIDCamel)
}

func (p HookPayload) EffectiveTurnID() string {
	return firstNonBlank(p.TurnID, p.TurnIDCamel)
}

func (p HookPayload) EffectiveToolUseID() string {
	return firstNonBlank(p.ToolUseID, p.ToolUseIDCamel)
}

func (p HookPayload) EffectiveAgentID() string {
	return firstNonBlank(p.AgentID, p.AgentIDCamel)
}

func (p HookPayload) EffectiveAgentType() string {
	return firstNonBlank(p.AgentType, p.AgentTypeCamel)
}

func (p HookPayload) EffectiveRunID() string {
	return firstNonBlank(p.RunID, p.RunIDCamel)
}

func (p HookPayload) EffectiveSourceSequence() string {
	return firstNonBlank(p.SourceSequence, p.SourceSequenceCamel)
}

func (p HookPayload) EffectiveMailboxProjectionSequence() string {
	return firstNonBlank(
		p.MailboxProjectionSequence,
		p.MailboxProjectionSequenceCamel,
	)
}

func (p HookPayload) EffectiveSourceEventID() string {
	return firstNonBlank(p.SourceEventID, p.SourceEventIDCamel)
}

func (p HookPayload) EffectiveEmittedTime() string {
	return firstNonBlank(p.EmittedTime, p.EmittedTimeCamel)
}

func (p HookPayload) EffectiveKittyWindowID() int {
	if p.KittyWindowID > 0 {
		return p.KittyWindowID
	}
	if p.WindowID > 0 {
		return p.WindowID
	}
	return 0
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// payloadHasResetFlow classifies user-initiated reset flows from the harness
// hook payload. The daemon only needs a boolean marker, but the indicator must
// respect the two harness contracts:
//   - Claude: `/clear` is exposed through lifecycle payloads.
//   - Copilot: documented reset markers are slash-command prompts and
//     sessionStart.source == "new", but the interactive CLI can also terminate
//     the current session with sessionEnd.reason == "user_exit" before the next
//     session starts.
func PayloadHasResetFlow(harnessName, event string, payload HookPayload) bool {
	return payloadHasResetFlow(harnessName, event, payload)
}

func payloadHasResetFlow(harnessName, event string, payload HookPayload) bool {
	switch strings.ToLower(strings.TrimSpace(harnessName)) {
	case harness.Claude:
		return claudePayloadHasResetFlow(event, payload)
	case harness.Copilot:
		return copilotPayloadHasResetFlow(event, payload)
	case harness.Codex:
		return false
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
