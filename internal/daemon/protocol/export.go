package protocol

import (
	"github.com/sglavoie/agentmux/internal/buildinfo"
)

// EventsExport is the typed daemon snapshot returned by Client.ExportEvents.
// The CLI enriches this with top-level health data and live Kitty metadata
// before writing the final JSON payload.
type EventsExport struct {
	EventsByWindow map[int][]SessionEvent
	WindowMetadata map[int]WindowDebugMetadata
	// RuntimeSlots names the runtime session bound to each exported slot
	// that has one. A session on a backend other than Kitty is exported
	// under a slot only the daemon can name; this is what says whose it is.
	RuntimeSlots map[int]RuntimeSlot
	// KittySlots retains instance provenance when two Kitty windows reuse a
	// number. It is omitted by older daemons.
	KittySlots map[int]KittySlot
}

// EventsExportSchemaVersion is the current JSON schema version emitted by
// `amx daemon export-events`. Version 13 adds the watchdog's screen-probe
// decline counters: per probe source, how many consecutive frames the screen
// classifier refused to grade as a settleable terminal, whether those refusals
// carried a stable fingerprint, and the vetoes and status line of the frame
// that last extended the streak.
// Version 12 adds the shadow's busy-stretch anchor
// used to diagnose how long a window has remained active. Version 11 adds
// bounded terminal-stop-hold evidence: synthetic `TerminalStopHold` rows,
// per-event hold outcome, waited duration, and confirming evidence, plus the
// shadow's pending terminal stop.
// Version 10 adds safe harness-process lifecycle
// metadata to source facts, synthetic exit events, and intervention counters.
// Version 9 adds causal decision evidence plus
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
	EventsExportSchemaVersion = 13
	EventsExportSchemaV12     = 12
	EventsExportSchemaV11     = 11
	EventsExportSchemaV10     = 10
	EventsExportSchemaV9      = 9
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
//
// RuntimeSessionID and Backend are set when a runtime session holds the
// entry. For a backend other than Kitty WindowID is then the daemon's slot
// for that session, not a Kitty window, and Live is never found.
type ExportWindow struct {
	WindowID         int                 `json:"window_id"`
	RuntimeSessionID string              `json:"runtime_session_id,omitempty"`
	Backend          string              `json:"backend,omitempty"`
	Stats            WindowDebugStats    `json:"stats"`
	Durable          WindowDebugDurable  `json:"durable"`
	Renderer         WindowDebugRenderer `json:"renderer"`
	Shadow           WindowDebugShadow   `json:"shadow"`
	Watchdog         WindowDebugWatchdog `json:"watchdog"`
	Live             WindowLiveMetadata  `json:"live"`
	Events           []SessionEvent      `json:"events"`
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
