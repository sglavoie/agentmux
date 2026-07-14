package daemon

import (
	"github.com/sglavoie/agentmux/internal/buildinfo"
	"github.com/sglavoie/agentmux/internal/conversation"
)

// MessageType identifies the kind of IPC request.
type MessageType string

const (
	TypeStateUpdate   MessageType = "state_update"
	TypeHookEvent     MessageType = "hook_event"
	TypeStateQuery    MessageType = "state_query"
	TypeSessionDetail MessageType = "session_detail"
	TypeEventsExport  MessageType = "events_export"
	TypeShutdown      MessageType = "shutdown"
	TypeHealth        MessageType = "health"

	// IconStatePreserve signals the client to keep the current icon unchanged.
	IconStatePreserve = "preserve"
)

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
	Type          MessageType `json:"type"`
	// RequestID is a client-generated UUID identifying one logical request.
	// It is reused verbatim across retries so the daemon can deduplicate an
	// ambiguous-timeout resend and apply the underlying event exactly once.
	// Legacy clients omit it; empty-ID requests are never deduplicated.
	RequestID        string `json:"request_id,omitempty"`
	WindowID         int    `json:"window_id,omitempty"`         // KITTY_WINDOW_ID
	Action           string `json:"action,omitempty"`            // active|stop|permission|failure|stop_failure|idle|clear|user_clear
	Event            string `json:"event,omitempty"`             // raw Claude hook event name (e.g. UserPromptSubmit)
	PayloadJSON      string `json:"payload_json,omitempty"`      // raw hook stdin payload for TypeHookEvent
	ToolName         string `json:"tool_name,omitempty"`         // tool name from PreToolUse/PostToolUse hook payloads
	Source           string `json:"source,omitempty"`            // raw source payload field
	IsPrompt         bool   `json:"is_prompt,omitempty"`         // true if event is a user prompt submission
	PreservesIcon    bool   `json:"preserves_icon,omitempty"`    // true if a non-terminal child-complete event should preserve current icon/guard semantics
	Account          string `json:"account,omitempty"`           // profile name from amx_profile or cc_account
	Harness          string `json:"harness,omitempty"`           // normalized harness name from amx_harness
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
	// the window's reducer (schema v7). Diagnostics use it to hold the visible
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
}

// EventsExport is the typed daemon snapshot returned by Client.ExportEvents.
// The CLI enriches this with top-level health data and live Kitty metadata
// before writing the final JSON payload.
type EventsExport struct {
	EventsByWindow map[int][]SessionEvent
	WindowMetadata map[int]WindowDebugMetadata
}

// EventsExportSchemaVersion is the current JSON schema version emitted by
// `amx daemon export-events`. Version 7 retains every v6 field and adds the
// interactive-ownership `pm` flag on exported source facts (a fact from a
// non-interactive `claude -p` child), so diagnostics can tell an interactive
// harness owner apart from a pipe-mode child. Version 6 added: the per-window
// Watchdog section (synthetic daemon interventions + tombstone marker), the
// reducer's live source facts, per-event daemon sequence / nanosecond receipt
// time / normalized source identity / route decision, and daemon-level
// readiness, build identity, and request metrics (queue saturation and
// latency).
const EventsExportSchemaVersion = 7

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

// ExportWindow is one per-window investigation entry in the schema-v7 payload.
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
