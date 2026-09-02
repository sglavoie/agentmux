package protocol

// SessionEvent is one entry in a window's hook event ring buffer. The daemon
// appends one of these per Request it processes (both allowed and suppressed),
// and serves the buffer back to the TUI on TypeSessionDetail queries.
type SessionEvent struct {
	At                int64  `json:"at"`                           // unix seconds when the daemon received the event
	Event             string `json:"event"`                        // raw hook event name (e.g. PreToolUse)
	Action            string `json:"action"`                       // resolved action (active, stop, …)
	ToolName          string `json:"tool_name,omitempty"`          // tool name when event carries one
	Source            string `json:"source,omitempty"`             // raw source payload field
	PromptKind        string `json:"prompt_kind,omitempty"`        // screen-probe prompt kind for synthetic prompt events
	Suppressed        bool   `json:"suppressed,omitempty"`         // true when the daemon blocked the transition
	PreservesIcon     bool   `json:"preserves_icon,omitempty"`     // true when the event preserved the current icon (child-complete signals)
	ResultIconState   string `json:"result_icon_state"`            // daemon-resolved icon outcome: "", "preserve", or a concrete icon state
	SuppressionReason string `json:"suppression_reason,omitempty"` // normalized daemon-side reason for a blocked transition
	CurrentIconState  string `json:"current_icon_state,omitempty"` // icon shown in the tab title when the daemon received the event
	NotificationType  string `json:"notification_type,omitempty"`  // normalized notification_type / notificationType payload field
	StopReason        string `json:"stop_reason,omitempty"`        // normalized stop_reason / stopReason payload field
	Reason            string `json:"reason,omitempty"`             // normalized reason payload field
	ContextLimit      bool   `json:"context_limit,omitempty"`      // true when indicator detected a context-limit stop/failure
	ResetFlow         bool   `json:"reset_flow,omitempty"`         // true when the daemon treated the event as part of a reset flow

	// Schema v6 receipt/identity fields. DaemonSeq is the daemon's global
	// monotonic receipt sequence (assigned at append, restarts from 1 on a
	// daemon restart); ReceivedAtNs is the nanosecond receipt time. SourceKind/
	// SourceKey/SourceSeq are the normalized source identity carried by the
	// underlying hook payload (empty for legacy clients and synthetic events).
	// RouteDecision records how the daemon routed the event to this window:
	// "source" (the firing pane owns the tab), "owner" (rerouted to the managed
	// title owner), or "source_fallback" (ownership unknown, fell back to the
	// firing pane).
	DaemonSeq     uint64 `json:"daemon_seq,omitempty"`
	ReceivedAtNs  int64  `json:"received_at_ns,omitempty"`
	SourceKind    string `json:"source_kind,omitempty"`
	SourceKey     string `json:"source_key,omitempty"`
	SourceSeq     uint64 `json:"source_seq,omitempty"`
	RouteDecision string `json:"route_decision,omitempty"`

	// Schema v8 producer-route evidence. OriginWindowID is the pane where the
	// event originated, which may differ from the exported target window when an
	// unmanaged sibling borrows a managed title owner.
	OriginWindowID int `json:"origin_window_id,omitempty"`

	// Causal decision fields correlate hook ingress, sanitized normalization,
	// routing, and the authoritative reducer transition. These fields are
	// emitted by schemas v9+ and intentionally exclude raw payloads,
	// prompt bodies, and tool arguments.
	TraceID              string                  `json:"trace_id,omitempty"`
	ParentTraceID        string                  `json:"parent_trace_id,omitempty"`
	RequestID            string                  `json:"request_id,omitempty"`
	SourceEventID        string                  `json:"source_event_id,omitempty"`
	Attempt              int                     `json:"attempt,omitempty"`
	PayloadSize          int64                   `json:"payload_size,omitempty"`
	PayloadHMAC          string                  `json:"payload_hmac,omitempty"`
	PayloadEncoding      string                  `json:"payload_encoding,omitempty"`
	ParseOutcome         string                  `json:"parse_outcome,omitempty"`
	NormalizationOutcome string                  `json:"normalization_outcome,omitempty"`
	IdentityConfidence   string                  `json:"identity_confidence,omitempty"`
	UsedFallbackIdentity bool                    `json:"used_fallback_identity,omitempty"`
	ProducerHarness      string                  `json:"producer_harness,omitempty"`
	ProducerConfigDir    string                  `json:"producer_config_dir,omitempty"`
	TargetHarness        string                  `json:"target_harness,omitempty"`
	Profile              string                  `json:"profile,omitempty"`
	SessionID            string                  `json:"session_id,omitempty"`
	RunID                string                  `json:"run_id,omitempty"`
	TurnID               string                  `json:"turn_id,omitempty"`
	ToolUseID            string                  `json:"tool_use_id,omitempty"`
	SaturationReason     string                  `json:"saturation_reason,omitempty"`
	TargetWindowID       int                     `json:"target_window_id,omitempty"`
	ReducerInputAction   string                  `json:"reducer_input_action,omitempty"`
	ReducerInput         *ReducerInputEvidence   `json:"reducer_input,omitempty"`
	ReducerDecision      ReducerDecision         `json:"reducer_decision,omitempty"`
	ReducerReason        string                  `json:"reducer_reason,omitempty"`
	DecisionReason       string                  `json:"decision_reason,omitempty"`
	Quarantined          bool                    `json:"quarantined,omitempty"`
	EffectiveBefore      *EffectiveStateEvidence `json:"effective_before,omitempty"`
	EffectiveAfter       *EffectiveStateEvidence `json:"effective_after,omitempty"`
	RouteEvidence        *RoutingEvidence        `json:"route_evidence,omitempty"`

	// Schema-v9 asynchronous effect links. RenderGeneration points into the
	// renderer generation history; terminal outcomes are copied back onto the
	// event when available. ProbeTraceIDs contains child traces only, never
	// captured screen or transcript content.
	RenderGeneration    uint64   `json:"render_generation,omitempty"`
	RenderOutcome       string   `json:"render_outcome,omitempty"`
	RenderReason        string   `json:"render_reason,omitempty"`
	NotificationTrace   string   `json:"notification_trace_id,omitempty"`
	NotificationOutcome string   `json:"notification_outcome,omitempty"`
	NotificationReason  string   `json:"notification_reason,omitempty"`
	ProbeTraceIDs       []string `json:"probe_trace_ids,omitempty"`

	// Schema-v10 harness-process lifecycle evidence. These fields occur only on
	// EventHarnessProcessExited rows and contain bounded state-machine metadata,
	// never command lines, arguments, prompts, payloads, or filesystem paths.
	ProcessHarness       string                           `json:"process_harness,omitempty"`
	ProcessPID           int                              `json:"process_pid,omitempty"`
	ProcessStartToken    string                           `json:"process_start_token,omitempty"`
	ProcessObservation   string                           `json:"process_observation,omitempty"`
	ProcessMode          string                           `json:"process_mode,omitempty"`
	ProcessSources       []ProcessLifecycleSourceEvidence `json:"process_sources,omitempty"`
	WithdrawnFacts       *int                             `json:"withdrawn_facts,omitempty"`
	ClearedSubagents     *int                             `json:"cleared_subagents,omitempty"`
	SynthesizedStop      *bool                            `json:"synthesized_stop,omitempty"`
	TerminalHookEvidence string                           `json:"terminal_hook_evidence,omitempty"`

	// Schema-v11 terminal-stop-hold evidence. StopHoldOutcome and
	// StopConfirmedBy carry bounded vocabulary values only; StopHoldWaitedMs is
	// how long the stop was withheld before this row was written. They appear on
	// EventTerminalStopHold rows and on the stop a hold finally releases.
	StopHoldOutcome  StopHoldOutcome `json:"stop_hold_outcome,omitempty"`
	StopHoldWaitedMs int             `json:"stop_hold_waited_ms,omitempty"`
	StopConfirmedBy  StopConfirmedBy `json:"stop_confirmed_by,omitempty"`

	// QuestionPreview is the bounded ASL mailbox question summary carried by
	// AgentSessionLoopWaitStarted. It is the only free-text payload field the
	// daemon retains on an event, and it is absent when the producer sent no
	// text or when payload compaction dropped it.
	QuestionPreview string `json:"question_preview,omitempty"`

	// EventRef exists only while appendEventLocked stamps the daemon sequence.
	// Stored/exported events always clear it.
	EventRef *SessionEventReference `json:"-"`
}

// ProcessLifecycleSourceEvidence identifies a reducer source affected by one
// exact process settlement. OriginWindowID is the producing pane when known;
// it falls back to the owning window for legacy facts.
type ProcessLifecycleSourceEvidence struct {
	SourceKind     string `json:"source_kind"`
	SourceKey      string `json:"source_key,omitempty"`
	OriginWindowID int    `json:"origin_window_id,omitempty"`
}

// ReducerInputEvidence is the complete sanitized input needed to replay one
// reducer admission without the original hook payload, daemon, or Kitty. It is
// emitted only by schemas v9+. Every field is state-machine metadata; prompt
// bodies, tool arguments, filesystem paths, and native-log content are never
// retained here.
type ReducerInputEvidence struct {
	EmittedAt      int64  `json:"emitted_at"`
	OriginWindowID int    `json:"origin_window_id,omitempty"`
	Prompt         bool   `json:"prompt,omitempty"`
	PipeMode       bool   `json:"pipe_mode,omitempty"`
	ASLPhase       string `json:"asl_phase,omitempty"`
	EndsSession    bool   `json:"ends_session,omitempty"`
	SubagentDelta  int    `json:"subagent_delta,omitempty"`
	GuardDuration  int64  `json:"guard_duration,omitempty"`
	StaleActive    int64  `json:"stale_active,omitempty"`
	StaleSubagent  int64  `json:"stale_subagent,omitempty"`
	StaleCompact   int64  `json:"stale_compact,omitempty"`
}

// actionPriority maps each action name to its guard priority.
// Higher priority states block lower priority state updates.
// loop_marker is at priority 0 because it never participates in guard
// transitions; the handler intercepts it before TryUpdate is reached.
var actionPriority = map[string]int{
	"user_clear":   -1,
	"clear":        -1,
	"idle":         0,
	"loop_marker":  0,
	"active":       1,
	"compact":      1,
	"subagent":     2,
	"stop":         2,
	"elicitation":  2,
	"permission":   3,
	"failure":      4,
	"stop_failure": 4,
}

// actionIconState maps each action to the palette icon state name.
// Recoverable tool failures render as "warning"; terminal stop failures stay on
// the sticky "error" icon. loop_marker maps to IconStatePreserve because the
// daemon must never paint a fresh state icon for an ASL lifecycle signal.
var actionIconState = map[string]string{
	"active":       "bolt",
	"compact":      "compact",
	"subagent":     "subagent",
	"stop":         "stop",
	"elicitation":  "question",
	"permission":   "key",
	"failure":      "warning",
	"stop_failure": "error",
	"idle":         "",
	"clear":        "",
	"user_clear":   "",
	"loop_marker":  IconStatePreserve,
}

// Priority returns the priority for an action, or 0 for unknown actions.
func Priority(action string) int {
	p, _ := ActionPriority(action)
	return p
}

// ActionPriority returns the guard priority for an action and whether the
// action is one the wire protocol recognizes at all. Callers that must reject
// unknown actions need the second result: several real actions legitimately
// carry priority 0, so a bare priority cannot distinguish them from a name the
// daemon has never heard of.
func ActionPriority(action string) (int, bool) {
	p, ok := actionPriority[action]
	return p, ok
}

// IconState returns the icon state name for an action.
func IconState(action string) string {
	return actionIconState[action]
}

// IconPrecedence exposes the reducer's target icon precedence for diagnostics
// consumers (doctor/audit): higher outranks lower, idle is 0. It lets an
// invariant like "a live subagent must show 🤖 or higher-priority attention"
// be evaluated outside this package without duplicating the ordering.
func IconPrecedence(icon string) int {
	return reducerIconPrecedence[icon]
}
