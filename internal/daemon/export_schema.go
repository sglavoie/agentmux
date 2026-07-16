package daemon

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/buildinfo"
)

// ProjectExportEventsPayload returns the requested public wire schema. Schema 8
// is an explicit field projection so newly added v9 fields can never leak into
// compatibility output through ordinary json tags.
func ProjectExportEventsPayload(payload ExportEventsPayload, schema int) (any, error) {
	switch schema {
	case EventsExportSchemaVersion:
		payload.SchemaVersion = EventsExportSchemaVersion
		return payload, nil
	case EventsExportSchemaV8:
		return projectExportEventsPayloadV8(payload), nil
	default:
		return nil, fmt.Errorf("unsupported export schema %d (want 8 or 9)", schema)
	}
}

type exportEventsPayloadV8 struct {
	SchemaVersion int                  `json:"schema_version"`
	ExportedAt    int64                `json:"exported_at"`
	Daemon        ExportDaemonMetadata `json:"daemon"`
	CLIBuild      *buildinfo.Info      `json:"cli_build,omitempty"`
	Windows       []exportWindowV8     `json:"windows"`
}

type exportWindowV8 struct {
	WindowID int                   `json:"window_id"`
	Stats    WindowDebugStats      `json:"stats"`
	Durable  WindowDebugDurable    `json:"durable"`
	Renderer windowDebugRendererV8 `json:"renderer"`
	Shadow   WindowDebugShadow     `json:"shadow"`
	Watchdog WindowDebugWatchdog   `json:"watchdog"`
	Live     WindowLiveMetadata    `json:"live"`
	Events   []sessionEventV8      `json:"events"`
}

type windowDebugRendererV8 struct {
	DesiredIcon         string `json:"desired_icon"`
	StateSet            bool   `json:"state_set"`
	Looping             bool   `json:"looping"`
	LoopingSet          bool   `json:"looping_set"`
	RenderFailures      int    `json:"render_failures"`
	LastRenderError     string `json:"last_render_error"`
	KittyListenOn       string `json:"kitty_listen_on,omitempty"`
	QueuedGen           uint64 `json:"queued_gen,omitempty"`
	AttemptedGen        uint64 `json:"attempted_gen,omitempty"`
	PersistedGen        uint64 `json:"persisted_gen,omitempty"`
	RenderedGen         uint64 `json:"rendered_gen,omitempty"`
	FailedGen           uint64 `json:"failed_gen,omitempty"`
	DirtySince          int64  `json:"dirty_since,omitempty"`
	DirtyAgeSeconds     int64  `json:"dirty_age_seconds,omitempty"`
	LastRenderLatencyMs int64  `json:"last_render_latency_ms,omitempty"`
}

type sessionEventV8 struct {
	At                int64  `json:"at"`
	Event             string `json:"event"`
	Action            string `json:"action"`
	ToolName          string `json:"tool_name,omitempty"`
	Source            string `json:"source,omitempty"`
	PromptKind        string `json:"prompt_kind,omitempty"`
	Suppressed        bool   `json:"suppressed,omitempty"`
	PreservesIcon     bool   `json:"preserves_icon,omitempty"`
	ResultIconState   string `json:"result_icon_state"`
	SuppressionReason string `json:"suppression_reason,omitempty"`
	CurrentIconState  string `json:"current_icon_state,omitempty"`
	NotificationType  string `json:"notification_type,omitempty"`
	StopReason        string `json:"stop_reason,omitempty"`
	Reason            string `json:"reason,omitempty"`
	ContextLimit      bool   `json:"context_limit,omitempty"`
	ResetFlow         bool   `json:"reset_flow,omitempty"`
	DaemonSeq         uint64 `json:"daemon_seq,omitempty"`
	ReceivedAtNs      int64  `json:"received_at_ns,omitempty"`
	SourceKind        string `json:"source_kind,omitempty"`
	SourceKey         string `json:"source_key,omitempty"`
	SourceSeq         uint64 `json:"source_seq,omitempty"`
	RouteDecision     string `json:"route_decision,omitempty"`
	OriginWindowID    int    `json:"origin_window_id,omitempty"`
}

func projectExportEventsPayloadV8(payload ExportEventsPayload) exportEventsPayloadV8 {
	out := exportEventsPayloadV8{
		SchemaVersion: EventsExportSchemaV8,
		ExportedAt:    payload.ExportedAt,
		Daemon:        payload.Daemon,
		CLIBuild:      payload.CLIBuild,
		Windows:       make([]exportWindowV8, 0, len(payload.Windows)),
	}
	for _, window := range payload.Windows {
		events := make([]sessionEventV8, len(window.Events))
		for i, event := range window.Events {
			events[i] = sessionEventV8{
				At: event.At, Event: event.Event, Action: event.Action,
				ToolName: event.ToolName, Source: event.Source, PromptKind: event.PromptKind,
				Suppressed: event.Suppressed, PreservesIcon: event.PreservesIcon,
				ResultIconState: event.ResultIconState, SuppressionReason: event.SuppressionReason,
				CurrentIconState: event.CurrentIconState, NotificationType: event.NotificationType,
				StopReason: event.StopReason, Reason: event.Reason, ContextLimit: event.ContextLimit,
				ResetFlow: event.ResetFlow, DaemonSeq: event.DaemonSeq, ReceivedAtNs: event.ReceivedAtNs,
				SourceKind: event.SourceKind, SourceKey: event.SourceKey, SourceSeq: event.SourceSeq,
				RouteDecision: event.RouteDecision, OriginWindowID: event.OriginWindowID,
			}
		}
		r := window.Renderer
		out.Windows = append(out.Windows, exportWindowV8{
			WindowID: window.WindowID, Stats: window.Stats, Durable: window.Durable,
			Renderer: windowDebugRendererV8{
				DesiredIcon: r.DesiredIcon, StateSet: r.StateSet, Looping: r.Looping, LoopingSet: r.LoopingSet,
				RenderFailures: r.RenderFailures, LastRenderError: r.LastRenderError, KittyListenOn: r.KittyListenOn,
				QueuedGen: r.QueuedGen, AttemptedGen: r.AttemptedGen, PersistedGen: r.PersistedGen,
				RenderedGen: r.RenderedGen, FailedGen: r.FailedGen, DirtySince: r.DirtySince,
				DirtyAgeSeconds: r.DirtyAgeSeconds, LastRenderLatencyMs: r.LastRenderLatencyMs,
			},
			Shadow: window.Shadow, Watchdog: window.Watchdog, Live: window.Live, Events: events,
		})
	}
	return out
}
