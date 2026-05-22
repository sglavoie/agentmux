package daemon

// MessageType identifies the kind of IPC request.
type MessageType string

const (
	TypeStateUpdate   MessageType = "state_update"
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
	Type             MessageType `json:"type"`
	WindowID         int         `json:"window_id,omitempty"`          // KITTY_WINDOW_ID
	Action           string      `json:"action,omitempty"`             // active|stop|permission|failure|stop_failure|idle|clear|user_clear
	Event            string      `json:"event,omitempty"`              // raw Claude hook event name (e.g. UserPromptSubmit)
	ToolName         string      `json:"tool_name,omitempty"`          // tool name from PreToolUse/PostToolUse hook payloads
	IsPrompt         bool        `json:"is_prompt,omitempty"`          // true if event is a user prompt submission
	PreservesIcon    bool        `json:"preserves_icon,omitempty"`     // true if a non-terminal child-complete event should preserve current icon/guard semantics
	Account          string      `json:"account,omitempty"`            // profile name from amx_profile or cc_account
	Harness          string      `json:"harness,omitempty"`            // normalized harness name from amx_harness
	SessionTitle     string      `json:"session_title,omitempty"`      // tab base title (e.g. "W:api")
	PipeMode         bool        `json:"pipe_mode,omitempty"`          // suppress notifications when claude -p
	CurrentIconState string      `json:"current_icon_state,omitempty"` // icon currently shown in the tab title
	NotificationType string      `json:"notification_type,omitempty"`  // normalized notification_type / notificationType payload field
	StopReason       string      `json:"stop_reason,omitempty"`        // normalized stop_reason / stopReason payload field
	Reason           string      `json:"reason,omitempty"`             // normalized reason payload field
	ContextLimit     bool        `json:"context_limit,omitempty"`      // true when indicator detected a context-limit stop/failure
	ResetFlow        bool        `json:"reset_flow,omitempty"`         // true when the event belongs to a user-initiated session reset flow
}

// Response is a JSON message sent by the daemon back to the client.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Fields populated for TypeStateUpdate:
	Allowed   bool   `json:"allowed,omitempty"`    // whether guard permitted the transition
	IconState string `json:"icon_state,omitempty"` // icon name to show ("bolt","stop","key","warning","error","")
	// Fields populated for TypeStateQuery:
	Activity map[int]int64         `json:"activity,omitempty"` // window_id → unix seconds of last activity
	Watchdog map[int]WatchdogState `json:"watchdog,omitempty"` // window_id → watchdog state for age-aware TUI rendering
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
}

// WindowDebugMetadata is the daemon's per-window export snapshot. The live
// Kitty-joined section is added later; for now exports can rely on the durable
// fields below even after a session ends or the tab disappears.
type WindowDebugMetadata struct {
	Stats   WindowDebugStats   `json:"stats"`
	Durable WindowDebugDurable `json:"durable"`
}

// EventsExport is the typed daemon snapshot returned by Client.ExportEvents.
// The CLI enriches this with top-level health data and live Kitty metadata
// before writing the final JSON payload.
type EventsExport struct {
	EventsByWindow map[int][]SessionEvent
	WindowMetadata map[int]WindowDebugMetadata
}

// EventsExportSchemaVersion is the current JSON schema version emitted by
// `amx daemon export-events`.
const EventsExportSchemaVersion = 2

// ExportEventsPayload is the CLI JSON envelope written by
// `amx daemon export-events`. The legacy per-window SessionEvent arrays stay in
// Windows for backward compatibility; richer metadata lives alongside them in
// WindowMeta.
type ExportEventsPayload struct {
	SchemaVersion       int                             `json:"schema_version"`
	ExportedAt          int64                           `json:"exported_at"`
	DaemonPID           int                             `json:"daemon_pid"`
	DaemonUptimeSeconds int64                           `json:"daemon_uptime_seconds"`
	Windows             map[string][]SessionEvent       `json:"windows"`
	WindowMeta          map[string]ExportWindowMetadata `json:"window_meta"`
}

// ExportWindowMetadata is the richer per-window metadata exported alongside the
// legacy SessionEvent arrays. Durable fields come from the daemon; Live is
// joined at export time from Kitty/session lookups on a best-effort basis.
type ExportWindowMetadata struct {
	Stats   WindowDebugStats   `json:"stats"`
	Durable WindowDebugDurable `json:"durable"`
	Live    WindowLiveMetadata `json:"live"`
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
	EventCount   int   `json:"event_count"`
	FirstEventAt int64 `json:"first_event_at"`
	LastEventAt  int64 `json:"last_event_at"`
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
}

// actionPriority maps each action name to its guard priority.
// Higher priority states block lower priority state updates.
var actionPriority = map[string]int{
	"user_clear":   -1,
	"clear":        -1,
	"idle":         0,
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
// the sticky "error" icon.
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
