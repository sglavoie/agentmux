package hookevent

import (
	"encoding/json"
	"strings"
)

// compactPayload is the allowlisted wire representation used when a valid hook
// payload is too large to forward verbatim. It deliberately contains only the
// fields consumed by hook normalization, routing, lifecycle handling, and
// source selection. Unknown fields and bulk prompt/tool/transcript content are
// never copied into it.
type compactPayload struct {
	// The compatibility block survives compaction. An agent-session-loop payload
	// is capped well below the size that triggers this path, so in practice it is
	// never compacted — but the daemon refuses a lifecycle event whose contract
	// identity is absent, and a forwarding step that silently stripped it would
	// turn a size limit into a compatibility failure.
	SchemaVersion int    `json:"schema_version,omitempty"`
	ContractSetID string `json:"contract_set_id,omitempty"`

	HookEventName    string `json:"hook_event_name,omitempty"`
	NotificationType string `json:"notification_type,omitempty"`
	StopReason       string `json:"stop_reason,omitempty"`
	Reason           string `json:"reason,omitempty"`
	PauseReason      string `json:"pause_reason,omitempty"`
	Source           string `json:"source,omitempty"`
	Trigger          string `json:"trigger,omitempty"`
	CWD              string `json:"cwd,omitempty"`
	Harness          string `json:"harness,omitempty"`
	KittyWindowID    int    `json:"kitty_window_id,omitempty"`
	PlanDir          string `json:"plan_dir,omitempty"`
	RunID            string `json:"run_id,omitempty"`
	StatePath        string `json:"state_path,omitempty"`
	RunFinishedAt    string `json:"run_finished_at,omitempty"`
	Prompt           string `json:"prompt,omitempty"`
	Message          string `json:"message,omitempty"`
	ToolName         string `json:"tool_name,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
	TurnID           string `json:"turn_id,omitempty"`
	ToolUseID        string `json:"tool_use_id,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	AgentType        string `json:"agent_type,omitempty"`
	SourceSequence   string `json:"source_sequence,omitempty"`
	SourceEventID    string `json:"source_event_id,omitempty"`
	EmittedTime      string `json:"emitted_time,omitempty"`
}

// CompactHookPayload serializes the semantic subset of a decoded hook payload.
// Callers must decode and validate the original bytes first. Effective accessors
// canonicalize snake/camel aliases so the daemon observes the same identity in
// either harness dialect.
func CompactHookPayload(payload HookPayload) ([]byte, error) {
	compact := compactPayload{
		SchemaVersion:    payload.SchemaVersion,
		ContractSetID:    payload.ContractSetID,
		HookEventName:    payload.HookEventName,
		NotificationType: payload.EffectiveNotificationType(),
		StopReason:       payload.EffectiveStopReason(),
		Reason:           payload.Reason,
		PauseReason:      payload.EffectivePauseReason(),
		Source:           payload.Source,
		Trigger:          payload.Trigger,
		CWD:              payload.CWD,
		Harness:          payload.Harness,
		KittyWindowID:    payload.EffectiveKittyWindowID(),
		PlanDir:          payload.PlanDir,
		RunID:            payload.EffectiveRunID(),
		StatePath:        payload.StatePath,
		RunFinishedAt:    payload.RunFinishedAt,
		Prompt:           resetPromptCommand(payload.EffectivePrompt()),
		ToolName:         payload.EffectiveToolName(),
		SessionID:        payload.EffectiveSessionID(),
		TurnID:           payload.EffectiveTurnID(),
		ToolUseID:        payload.EffectiveToolUseID(),
		AgentID:          payload.EffectiveAgentID(),
		AgentType:        payload.EffectiveAgentType(),
		SourceSequence:   payload.EffectiveSourceSequence(),
		SourceEventID:    payload.EffectiveSourceEventID(),
		EmittedTime:      payload.EffectiveEmittedTime(),
	}
	if PayloadHasContextLimit(payload) {
		// Preserve only the classification marker, never the original free-text
		// title/message. This canonical spelling satisfies the shared detector.
		compact.Message = "Context limit reached. Run /compact to continue."
	}
	return json.Marshal(compact)
}

func resetPromptCommand(prompt string) string {
	fields := strings.Fields(strings.TrimSpace(prompt))
	if len(fields) == 0 {
		return ""
	}
	command := strings.ToLower(fields[0])
	switch command {
	case "/clear", "/reset", "/new":
		return command
	default:
		return ""
	}
}
