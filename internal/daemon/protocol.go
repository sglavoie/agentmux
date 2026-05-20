package daemon

// MessageType identifies the kind of IPC request.
type MessageType string

const (
	TypeStateUpdate   MessageType = "state_update"
	TypeStateQuery    MessageType = "state_query"
	TypeSessionDetail MessageType = "session_detail"
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
	Action           string      `json:"action,omitempty"`             // active|stop|permission|failure|idle|clear
	Event            string      `json:"event,omitempty"`              // raw Claude hook event name (e.g. UserPromptSubmit)
	ToolName         string      `json:"tool_name,omitempty"`          // tool name from PreToolUse/PostToolUse hook payloads
	IsPrompt         bool        `json:"is_prompt,omitempty"`          // true if event is a user prompt submission
	PreservesIcon    bool        `json:"preserves_icon,omitempty"`     // true if event should keep current icon
	Account          string      `json:"account,omitempty"`            // profile name from amx_profile or cc_account
	SessionTitle     string      `json:"session_title,omitempty"`      // tab base title (e.g. "W:api")
	PipeMode         bool        `json:"pipe_mode,omitempty"`          // suppress notifications when claude -p
	CurrentIconState string      `json:"current_icon_state,omitempty"` // icon currently shown in the tab title
}

// Response is a JSON message sent by the daemon back to the client.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Fields populated for TypeStateUpdate:
	Allowed   bool   `json:"allowed,omitempty"`    // whether guard permitted the transition
	IconState string `json:"icon_state,omitempty"` // icon name to show ("bolt","stop","key","error","")
	// Fields populated for TypeStateQuery:
	Activity map[int]int64         `json:"activity,omitempty"` // window_id → unix seconds of last activity
	Watchdog map[int]WatchdogState `json:"watchdog,omitempty"` // window_id → watchdog state for age-aware TUI rendering
	// Fields populated for TypeSessionDetail:
	Events []SessionEvent `json:"events,omitempty"` // per-window hook event ring buffer (oldest first)
	// Fields populated for TypeHealth:
	PID           int   `json:"pid,omitempty"`
	UptimeSeconds int64 `json:"uptime_seconds,omitempty"`
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
	At         int64  `json:"at"`                   // unix seconds when the daemon received the event
	Event      string `json:"event"`                // raw hook event name (e.g. PreToolUse)
	Action     string `json:"action"`               // resolved action (active, stop, …)
	ToolName   string `json:"tool_name,omitempty"`  // tool name when event carries one
	Suppressed bool   `json:"suppressed,omitempty"` // true when the guard blocked the transition
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
// stop_failure deliberately reuses the "error" glyph (shared with failure);
// the two actions differ only in priority transition logic and notification flag.
var actionIconState = map[string]string{
	"active":       "bolt",
	"compact":      "compact",
	"subagent":     "subagent",
	"stop":         "stop",
	"elicitation":  "question",
	"permission":   "key",
	"failure":      "error",
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
