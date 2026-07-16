package daemon

import (
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/conversation"
)

// MessageType identifies the kind of IPC request.
type MessageType string

const (
	TypeStateUpdate     MessageType = "state_update"
	TypeHookEvent       MessageType = "hook_event"
	TypeDiagnosticProbe MessageType = "diagnostic_probe"
	TypeStateQuery      MessageType = "state_query"
	TypeSessionDetail   MessageType = "session_detail"
	TypeEventsExport    MessageType = "events_export"
	TypeShutdown        MessageType = "shutdown"
	TypeHealth          MessageType = "health"

	// IconStatePreserve signals the client to keep the current icon unchanged.
	IconStatePreserve = "preserve"
)

// Delivery acknowledgement statuses are additive to the legacy OK/Error
// response pair. Hook clients use them to distinguish an original acceptance
// from an idempotent replay or a permanent ingress rejection without parsing
// stderr or daemon error text.
const (
	DeliveryAcknowledgementAccepted  = "accepted"
	DeliveryAcknowledgementDuplicate = "duplicate"
	DeliveryAcknowledgementRejected  = "rejected"
)

const DeliveryAcknowledgementSchemaVersion = 1

// DeliveryAcknowledgement describes what the daemon did with one structured
// hook delivery. It contains identifiers and routing metadata only; raw hook
// payloads never appear in acknowledgements.
type DeliveryAcknowledgement struct {
	SchemaVersion   int    `json:"schema_version"`
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
	TraceID         string `json:"trace_id,omitempty"`
	RequestID       string `json:"request_id,omitempty"`
	SourceEventID   string `json:"source_event_id,omitempty"`
	Attempt         int    `json:"attempt,omitempty"`
	ProducerHarness string `json:"producer_harness,omitempty"`
	TargetHarness   string `json:"target_harness,omitempty"`
	Profile         string `json:"profile,omitempty"`
	SourceWindowID  int    `json:"source_window_id,omitempty"`
	TargetWindowID  int    `json:"target_window_id,omitempty"`
	RouteDecision   string `json:"route_decision,omitempty"`
	SnapshotReady   bool   `json:"snapshot_ready,omitempty"`
	WindowFound     bool   `json:"window_found,omitempty"`
}

// Request is a JSON message sent by a client to the daemon over the Unix socket.
// One request per connection; the connection is closed after the response.
type Request struct {
	// currentIconState is daemon-internal snapshot context. It is deliberately
	// not serialized: hook clients must never make the title an input to the
	// state machine, but event exports can still record the live icon observed
	// by daemon-side probes and snapshot enrichment.
	currentIconState string
	// normalized is the daemon-internal, source-scoped view of the underlying
	// hook event, stamped by paths that hold the raw payload (hook routing,
	// loop lifecycle fan-out, probe promotion). The shadow reducer consumes it;
	// requests without it (legacy clients, sweeps) fall back to a synthesized
	// legacy-source event. Never serialized: clients cannot inject identity.
	normalized *NormalizedEvent
	// routeDecision records how the daemon routed a hook event to its target
	// window ("source", "owner", "source_fallback") for the event log. Daemon-
	// internal: routing is a daemon decision, never client input.
	routeDecision string
	// targetHarness is daemon-internal managed-window metadata. Harness is the
	// event producer; targetHarness identifies the pane whose durable title
	// metadata is updated after routing. Direct legacy state_update requests
	// leave targetHarnessSet false and use Harness for both roles.
	targetHarness    string
	targetHarnessSet bool
	// parseOutcome, normalizationOutcome, and routeEvidence are daemon-owned
	// causal context. Hook clients cannot inject them; routing copies them onto
	// the internal state-update request after decoding the raw delivery.
	parseOutcome         string
	normalizationOutcome string
	routeEvidence        *RoutingEvidence
	// eventRef links the legacy event-log append to the reducer finalization for
	// this request without relying on "last event" ordering. It is process-local
	// and never serialized.
	eventRef *sessionEventReference
	// parentTraceID links daemon-synthesized async work to its originating trace.
	// It is daemon-internal so clients cannot forge causal ancestry.
	parentTraceID string
	// saturationReason attributes bounded-capacity degradation to this request.
	// It is daemon-owned and appears only in sanitized decision evidence.
	saturationReason string
	Type             MessageType `json:"type"`
	// RequestID is a client-generated UUID identifying one logical request.
	// It is reused verbatim across retries so the daemon can deduplicate an
	// ambiguous-timeout resend and apply the underlying event exactly once.
	// Legacy clients omit it; empty-ID requests are never deduplicated.
	RequestID string `json:"request_id,omitempty"`
	// TraceID correlates every client, IPC, and daemon stage for one delivery.
	// RequestID remains the idempotency key and may be reused by an acknowledged
	// retry; TraceID identifies the current diagnostic trace.
	TraceID         string `json:"trace_id,omitempty"`
	SourceEventID   string `json:"source_event_id,omitempty"`
	Attempt         int    `json:"attempt,omitempty"`
	PayloadSize     int64  `json:"payload_size,omitempty"`
	PayloadHMAC     string `json:"payload_hmac,omitempty"`
	PayloadEncoding string `json:"payload_encoding,omitempty"`
	// PayloadCaptured tells a new daemon that the hook client already wrote
	// this raw payload into the explicitly authorized temporary capture. Legacy
	// clients omit it, allowing the daemon to perform the same scoped check.
	PayloadCaptured bool `json:"payload_captured,omitempty"`
	// ProducerHarness is the explicit producer identity used by new clients.
	// Harness remains populated for backward compatibility with older daemons.
	ProducerHarness  string `json:"producer_harness,omitempty"`
	WindowID         int    `json:"window_id,omitempty"`         // KITTY_WINDOW_ID
	Action           string `json:"action,omitempty"`            // active|stop|permission|failure|stop_failure|idle|clear|user_clear
	Event            string `json:"event,omitempty"`             // raw Claude hook event name (e.g. UserPromptSubmit)
	PayloadJSON      string `json:"payload_json,omitempty"`      // raw hook stdin payload for TypeHookEvent
	ToolName         string `json:"tool_name,omitempty"`         // tool name from PreToolUse/PostToolUse hook payloads
	Source           string `json:"source,omitempty"`            // raw source payload field
	IsPrompt         bool   `json:"is_prompt,omitempty"`         // true if event is a user prompt submission
	PreservesIcon    bool   `json:"preserves_icon,omitempty"`    // true if a non-terminal child-complete event should preserve current icon/guard semantics
	Account          string `json:"account,omitempty"`           // profile name from amx_profile or cc_account
	Harness          string `json:"harness,omitempty"`           // event-producing harness; unknown/legacy producers may be empty
	SessionTitle     string `json:"session_title,omitempty"`     // tab base title (e.g. "W:api")
	PipeMode         bool   `json:"pipe_mode,omitempty"`         // suppress notifications when claude -p
	KittyListenOn    string `json:"kitty_listen_on,omitempty"`   // KITTY_LISTEN_ON for daemon-side Kitty writes
	NotificationType string `json:"notification_type,omitempty"` // normalized notification_type / notificationType payload field
	StopReason       string `json:"stop_reason,omitempty"`       // normalized stop_reason / stopReason payload field
	Reason           string `json:"reason,omitempty"`            // normalized reason payload field
	ContextLimit     bool   `json:"context_limit,omitempty"`     // true when indicator detected a context-limit stop/failure
	ResetFlow        bool   `json:"reset_flow,omitempty"`        // true when the event belongs to a user-initiated session reset flow
}

// Response is a JSON message sent by the daemon back to the client.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Retryable classifies a rejected request without requiring clients to
	// inspect Error. Delivery is populated for hook and diagnostic-probe ingress.
	Retryable bool                     `json:"retryable,omitempty"`
	Delivery  *DeliveryAcknowledgement `json:"delivery,omitempty"`
	// Fields populated for TypeStateUpdate:
	Allowed   bool   `json:"allowed,omitempty"`    // whether guard permitted the transition
	IconState string `json:"icon_state,omitempty"` // icon name to show ("bolt","stop","key","warning","error","")
	// Fields populated for TypeStateQuery:
	Activity map[int]int64               `json:"activity,omitempty"` // window_id → unix seconds of last activity
	Watchdog map[int]WatchdogState       `json:"watchdog,omitempty"` // window_id → watchdog state for age-aware TUI rendering
	States   map[int]WindowStateSnapshot `json:"states,omitempty"`   // window_id → daemon-owned visible state
	// Fields populated for TypeSessionDetail:
	Events []SessionEvent `json:"events,omitempty"` // per-window hook event ring buffer (oldest first)
	// Fields populated for TypeEventsExport. Mutually exclusive with Events:
	// TypeSessionDetail returns one window's buffer in Events, TypeEventsExport
	// returns every exported window's buffer keyed by window ID. When
	// Request.WindowID is non-zero, the daemon filters both maps to that single
	// window instead of returning every window.
	EventsByWindow map[int][]SessionEvent `json:"events_by_window,omitempty"`
	// WindowMetadata carries durable debug state for export consumers. It is
	// returned by TypeEventsExport alongside EventsByWindow so future exports can
	// identify windows even after live Kitty lookups stop working.
	WindowMetadata map[int]WindowDebugMetadata `json:"window_metadata,omitempty"`
	// Fields populated for TypeHealth:
	PID           int   `json:"pid,omitempty"`
	UptimeSeconds int64 `json:"uptime_seconds,omitempty"`
	// Build identifies the artifact the daemon is actually running (computed
	// at daemon startup, before any on-disk replacement can happen), so a
	// client can detect a CLI/daemon build mismatch. Metrics carries the
	// server's request-outcome counters, including queue saturation and queue
	// latency, for diagnostics surfaces.
	Build   *buildinfo.Info  `json:"build,omitempty"`
	Metrics *MetricsSnapshot `json:"metrics,omitempty"`
	// Ready reports whether the daemon completed its bounded initial restore
	// before binding the public socket, or — after a degraded start — later
	// recovered when a maintenance tick restored durable state from a
	// successful snapshot and marked the daemon ready. DegradedReason explains
	// why it started (or remains) degraded when Ready is false.
	Ready          bool   `json:"ready"`
	DegradedReason string `json:"degraded_reason,omitempty"`
}

// WindowStateSnapshot is the daemon-owned state needed by dashboard clients.
// It is deliberately a value copy: callers must never observe or mutate the
// handler's live WindowState entries.
type WindowStateSnapshot struct {
	IconState   string `json:"icon_state"`
	Looping     bool   `json:"looping"`
	LastAction  string `json:"last_action"`
	LastEventAt int64  `json:"last_event_at"`
	GuardAction string `json:"guard_action"`
	// Conversation is derived from live parent-harness reducer facts. It is
	// absent for identity-less state, preserving the former response shape for
	// clients that do not use live-conversation continuity.
	Conversation *conversation.Identity `json:"conversation,omitempty"`
}

// WindowDebugMetadata is the daemon's per-window export snapshot. The live
// Kitty-joined section is added later; for now exports can rely on the durable
// fields below even after a session ends or the tab disappears.
type WindowDebugMetadata struct {
	Stats    WindowDebugStats    `json:"stats"`
	Durable  WindowDebugDurable  `json:"durable"`
	Renderer WindowDebugRenderer `json:"renderer"`
	Shadow   WindowDebugShadow   `json:"shadow"`
	Watchdog WindowDebugWatchdog `json:"watchdog"`
}

// WindowDebugWatchdog records the daemon's own synthetic interventions for a
// window: watchdog expiries, ASL reconciliation, and window-close GC. The
// fields are updated atomically (under the same lock) with the per-window
// event ring so the durable export and the live event log can never disagree
// about why the daemon changed state on its own.
type WindowDebugWatchdog struct {
	// LastEvent/LastReason/LastAt describe the most recent synthetic event
	// (WatchdogStale, SourceExpired, ASLReconciled, WindowClosed).
	LastEvent  string `json:"last_event,omitempty"`
	LastReason string `json:"last_reason,omitempty"`
	LastAt     int64  `json:"last_at,omitempty"`
	// SourceExpiries counts individual source-fact expiries; StaleSweeps
	// counts whole-window stale clears; Reconciles counts ASL lease
	// seeds/releases performed by reconciliation.
	SourceExpiries int `json:"source_expiries,omitempty"`
	StaleSweeps    int `json:"stale_sweeps,omitempty"`
	Reconciles     int `json:"reconciles,omitempty"`
	// ClosedAt is non-zero while the window is a tombstone: its live state was
	// garbage-collected and this metadata is retained (bounded) for
	// post-mortem export only. Tombstones never count as renderer drift.
	ClosedAt int64 `json:"closed_at,omitempty"`
}

// WindowDebugShadow reports the shadow reducer's view of a window: its current
// effective state/generation and every recorded divergence from the legacy
// state machine. The reducer's effective state is the authoritative visible
// icon and loop state (published by reduceStateUpdate through
// publishEffectiveLocked); the recorded mismatches are diagnostic evidence of
// where the retired legacy scalar machine would have differed, never a separate
// visible-state writer or a behavior change.
type WindowDebugShadow struct {
	EffectiveIcon    string           `json:"effective_icon"`
	EffectiveLooping bool             `json:"effective_looping"`
	EffectiveGen     uint64           `json:"effective_gen"`
	MismatchCount    int              `json:"mismatch_count"`
	Mismatches       []ShadowMismatch `json:"mismatches,omitempty"`
	// Facts is the reducer's live source-fact set, stamped at export time from
	// the window's reducer (schema v8). Diagnostics use it to hold the visible
	// icon accountable to the authoritative facts: an active ASL lease showing
	// stop/empty, or a live subagent without 🤖, is a health failure.
	Facts []SourceFactSnapshot `json:"facts,omitempty"`
}

// ShadowMismatch is one recorded divergence between the legacy render decision
// and the shadow reducer's effective state, captured after both saw the same
// event. The ring is bounded (maxShadowMismatches); MismatchCount stays
// cumulative.
type ShadowMismatch struct {
	At            int64  `json:"at"`
	Event         string `json:"event,omitempty"`
	Action        string `json:"action,omitempty"`
	LegacyIcon    string `json:"legacy_icon"`
	ShadowIcon    string `json:"shadow_icon"`
	LegacyLooping bool   `json:"legacy_looping"`
	ShadowLooping bool   `json:"shadow_looping"`
	SourceKind    string `json:"source_kind,omitempty"`
	SourceKey     string `json:"source_key,omitempty"`
}

// WindowDebugRenderer is the daemon-owned rendering state for a window. It
// makes drift and repeated Kitty write failures visible without asking callers
// to infer desired state from the mutable tab title.
type WindowDebugRenderer struct {
	DesiredIcon     string `json:"desired_icon"`
	StateSet        bool   `json:"state_set"`
	Looping         bool   `json:"looping"`
	LoopingSet      bool   `json:"looping_set"`
	RenderFailures  int    `json:"render_failures"`
	LastRenderError string `json:"last_render_error"`
	KittyListenOn   string `json:"kitty_listen_on,omitempty"`

	// Render-generation pipeline diagnostics. The window's Seq counter is its
	// generation; each stage records the highest generation it has reached:
	// queued (handed to the render queue), attempted (candidate built),
	// persisted (amx_state landed), rendered (title landed or was already
	// converged), failed (a Kitty write for that generation errored).
	QueuedGen    uint64 `json:"queued_gen,omitempty"`
	AttemptedGen uint64 `json:"attempted_gen,omitempty"`
	PersistedGen uint64 `json:"persisted_gen,omitempty"`
	RenderedGen  uint64 `json:"rendered_gen,omitempty"`
	FailedGen    uint64 `json:"failed_gen,omitempty"`
	// DirtySince is when the current dirty stretch began (unix seconds, 0 when
	// clean); DirtyAgeSeconds is computed at export time from the live state.
	// LastRenderLatencyMs is mark-dirty → fully-rendered for the most recent
	// successful pass.
	DirtySince          int64 `json:"dirty_since,omitempty"`
	DirtyAgeSeconds     int64 `json:"dirty_age_seconds,omitempty"`
	LastRenderLatencyMs int64 `json:"last_render_latency_ms,omitempty"`

	// Generations is the bounded schema-v9 causal history for asynchronous
	// rendering. A latest-generation pass may contain several source generations
	// and request IDs when queue coalescing folded a burst into one Kitty write.
	Generations []RenderGenerationTrace `json:"generations,omitempty"`
}

// MaxRenderCausalIDs bounds the number of request/trace identities retained by
// one coalesced render generation. The renderer never waits on an unbounded
// amount of diagnostic work, even under a sustained hook burst.
const MaxRenderCausalIDs = 32

// RenderGenerationTrace describes one queued render generation and the bounded
// many-to-one causal set that produced it. SourceGenerations retains the
// generations folded into Generation; the ID slices are de-duplicated and
// capped at MaxRenderCausalIDs.
type RenderGenerationTrace struct {
	Generation        uint64   `json:"generation"`
	SourceGenerations []uint64 `json:"source_generations,omitempty"`
	TraceIDs          []string `json:"trace_ids,omitempty"`
	RequestIDs        []string `json:"request_ids,omitempty"`
	SourceEventIDs    []string `json:"source_event_ids,omitempty"`
	Stage             string   `json:"stage"`
	Outcome           string   `json:"outcome,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	UpdatedAt         int64    `json:"updated_at,omitempty"`
}

// EventsExport is the typed daemon snapshot returned by Client.ExportEvents.
// The CLI enriches this with top-level health data and live Kitty metadata
// before writing the final JSON payload.
type EventsExport struct {
	EventsByWindow map[int][]SessionEvent
	WindowMetadata map[int]WindowDebugMetadata
}

// EventsExportSchemaVersion is the current JSON schema version emitted by
// `amx daemon export-events`. Version 9 adds causal decision evidence plus
// asynchronous render, notification, and probe links. Version 8 retains every v7 field and adds durable
// fact acknowledgement/origin evidence plus each event's producer pane origin.
// Version 7 added the interactive-ownership `pm` flag on exported source facts
// (a fact from a non-interactive `claude -p` child), so diagnostics can tell an
// interactive harness owner apart from a pipe-mode child. Version 6 added: the per-window
// Watchdog section (synthetic daemon interventions + tombstone marker), the
// reducer's live source facts, per-event daemon sequence / nanosecond receipt
// time / normalized source identity / route decision, and daemon-level
// readiness, build identity, and request metrics (queue saturation and
// latency).
const (
	EventsExportSchemaVersion = 9
	EventsExportSchemaV8      = 8
)

// ExportEventsPayload is the CLI JSON envelope written by
// `amx daemon export-events`. CLIBuild identifies the exporting CLI binary so
// an investigation bundle records both artifacts (the daemon's own identity
// lives under Daemon.Build).
type ExportEventsPayload struct {
	SchemaVersion int                  `json:"schema_version"`
	ExportedAt    int64                `json:"exported_at"`
	Daemon        ExportDaemonMetadata `json:"daemon"`
	CLIBuild      *buildinfo.Info      `json:"cli_build,omitempty"`
	Windows       []ExportWindow       `json:"windows"`
}

// ExportDaemonMetadata describes the daemon that produced this export.
type ExportDaemonMetadata struct {
	PID           int              `json:"pid"`
	UptimeSeconds int64            `json:"uptime_seconds"`
	Ready         bool             `json:"ready"`
	Build         *buildinfo.Info  `json:"build,omitempty"`
	Metrics       *MetricsSnapshot `json:"metrics,omitempty"`
}

// ExportWindow is one per-window investigation entry in the current payload.
// Durable fields come from the daemon; Live is joined at export time from
// Kitty/session lookups on a best-effort basis.
type ExportWindow struct {
	WindowID int                 `json:"window_id"`
	Stats    WindowDebugStats    `json:"stats"`
	Durable  WindowDebugDurable  `json:"durable"`
	Renderer WindowDebugRenderer `json:"renderer"`
	Shadow   WindowDebugShadow   `json:"shadow"`
	Watchdog WindowDebugWatchdog `json:"watchdog"`
	Live     WindowLiveMetadata  `json:"live"`
	Events   []SessionEvent      `json:"events"`
}

// WindowLiveMetadata is the best-effort Kitty/session join captured at export
// time. Found is false when the window no longer exists or Kitty is
// unreachable; the remaining fields are then left at their zero values.
type WindowLiveMetadata struct {
	Found       bool   `json:"found"`
	TabID       int    `json:"tab_id"`
	OSWindowID  int    `json:"os_window_id"`
	TabTitle    string `json:"tab_title"`
	BaseTitle   string `json:"base_title"`
	IconState   string `json:"icon_state"`
	Looping     bool   `json:"looping"`
	Starred     bool   `json:"starred"`
	WindowTitle string `json:"window_title"`
	Project     string `json:"project"`
	Profile     string `json:"profile"`
	Harness     string `json:"harness"`
	Directory   string `json:"directory"`
}

// WindowDebugStats summarizes the daemon's observed event history for a window.
// event_count is cumulative, not bounded by the ring buffer size.
type WindowDebugStats struct {
	EventCount         int            `json:"event_count"`
	FirstEventAt       int64          `json:"first_event_at"`
	LastEventAt        int64          `json:"last_event_at"`
	SuppressionReasons map[string]int `json:"suppression_reasons,omitempty"`
}

// WindowDebugDurable holds the per-window/session identity that must survive
// terminal cleanup so post-mortem exports still show where events came from.
type WindowDebugDurable struct {
	SessionTitle    string `json:"session_title"`
	Profile         string `json:"profile"`
	Harness         string `json:"harness"`
	PipeMode        bool   `json:"pipe_mode"`
	ActivityAt      int64  `json:"activity_at"`
	LastAction      string `json:"last_action"`
	LastActiveEvent string `json:"last_active_event"`
	SubagentCount   int    `json:"subagent_count"`
}

// WatchdogState describes a single window's last-known active-class action
// and the resolved sweep threshold the daemon would apply to it. The TUI
// uses these to render an age column with a warning style as the window
// approaches its threshold.
type WatchdogState struct {
	LastAction   string `json:"last_action"`             // "active", "subagent", or "compact"
	LastEventAt  int64  `json:"last_event_at"`           // unix seconds of last activity for this window
	ThresholdSec int    `json:"threshold_sec,omitempty"` // resolved sweep threshold in seconds (0 if no threshold configured)
}

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
	// emitted by schema v9 and intentionally exclude raw payloads,
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

	// eventRef exists only while appendEventLocked stamps the daemon sequence.
	// Stored/exported events always clear it.
	eventRef *sessionEventReference
}

// ReducerInputEvidence is the complete sanitized input needed to replay one
// reducer admission without the original hook payload, daemon, or Kitty. It is
// emitted only by schema v9. Every field is state-machine metadata; prompt
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
	p, ok := actionPriority[action]
	if !ok {
		return 0
	}
	return p
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
