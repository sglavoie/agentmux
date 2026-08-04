package daemon

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/buildinfo"
)

// ProjectExportEventsPayload returns the requested public wire schema. Schemas
// 8, 9, and 10 are explicit field projections so fields added to the current
// schema can never leak into compatibility output through shared nested
// structs.
func ProjectExportEventsPayload(payload ExportEventsPayload, schema int) (any, error) {
	switch schema {
	case EventsExportSchemaVersion:
		payload.SchemaVersion = EventsExportSchemaVersion
		return payload, nil
	case EventsExportSchemaV10:
		return projectExportEventsPayloadV10(payload), nil
	case EventsExportSchemaV9:
		return projectExportEventsPayloadV9(payload), nil
	case EventsExportSchemaV8:
		return projectExportEventsPayloadV8(payload), nil
	default:
		return nil, fmt.Errorf("unsupported export schema %d (want 8, 9, 10, or 11)", schema)
	}
}

// omitFromCompatibilityExport reports whether a row must be dropped from a
// pre-v11 projection entirely. Rows are otherwise field-projected, so a
// synthetic event introduced by schema v11 would appear in legacy output as a
// row a v10 consumer has never seen, with its evidence silently stripped.
func omitFromCompatibilityExport(event SessionEvent) bool {
	return event.Event == EventTerminalStopHold
}

// stopHoldSuppressionReasons is the suppression vocabulary schema v11
// introduced. Every other compatibility section is pinned by a struct, but
// WindowDebugStats counts suppressions in an open map keyed by decision reason,
// so a pre-v11 projection has to strip these keys by name or it reports
// aggregates for the very rows it just dropped.
var stopHoldSuppressionReasons = map[string]struct{}{
	DecisionReasonTerminalStopHeld:       {},
	DecisionReasonTerminalStopSuperseded: {},
	DecisionReasonTerminalStopBypassed:   {},
}

func projectCompatibilityWindowDebugStats(stats WindowDebugStats) WindowDebugStats {
	out := stats
	out.SuppressionReasons = nil
	for reason, count := range stats.SuppressionReasons {
		if _, ok := stopHoldSuppressionReasons[reason]; ok {
			continue
		}
		if out.SuppressionReasons == nil {
			out.SuppressionReasons = make(map[string]int, len(stats.SuppressionReasons))
		}
		out.SuppressionReasons[reason] = count
	}
	return out
}

type exportEventsPayloadV10 struct {
	SchemaVersion int                  `json:"schema_version"`
	ExportedAt    int64                `json:"exported_at"`
	Daemon        ExportDaemonMetadata `json:"daemon"`
	CLIBuild      *buildinfo.Info      `json:"cli_build,omitempty"`
	Windows       []exportWindowV10    `json:"windows"`
}

// exportWindowV10 keeps the nested sections schema v11 did not change. A later
// schema that adds fields to the renderer, watchdog, or source facts must pin
// them here the way windowDebugShadowV9 pins the pre-v10 fact shape.
type exportWindowV10 struct {
	WindowID int                  `json:"window_id"`
	Stats    WindowDebugStats     `json:"stats"`
	Durable  WindowDebugDurable   `json:"durable"`
	Renderer WindowDebugRenderer  `json:"renderer"`
	Shadow   windowDebugShadowV10 `json:"shadow"`
	Watchdog WindowDebugWatchdog  `json:"watchdog"`
	Live     WindowLiveMetadata   `json:"live"`
	Events   []sessionEventV10    `json:"events"`
}

// windowDebugShadowV10 pins the schema-v10 shadow shape: everything the current
// shadow carries except the v11 pending terminal stop.
type windowDebugShadowV10 struct {
	EffectiveIcon    string               `json:"effective_icon"`
	EffectiveLooping bool                 `json:"effective_looping"`
	EffectiveGen     uint64               `json:"effective_gen"`
	MismatchCount    int                  `json:"mismatch_count"`
	Mismatches       []ShadowMismatch     `json:"mismatches,omitempty"`
	Facts            []SourceFactSnapshot `json:"facts,omitempty"`
}

// sessionEventV10 pins the complete schema-v10 event shape: the v9 causal
// contract plus harness-process lifecycle evidence, the producer config
// directory, and the bounded ASL question preview. The v11 stop-hold fields are
// deliberately absent.
type sessionEventV10 struct {
	sessionEventV9
	ProducerConfigDir    string                           `json:"producer_config_dir,omitempty"`
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
	QuestionPreview      string                           `json:"question_preview,omitempty"`
}

type exportEventsPayloadV9 struct {
	SchemaVersion int                  `json:"schema_version"`
	ExportedAt    int64                `json:"exported_at"`
	Daemon        ExportDaemonMetadata `json:"daemon"`
	CLIBuild      *buildinfo.Info      `json:"cli_build,omitempty"`
	Windows       []exportWindowV9     `json:"windows"`
}

type exportWindowV9 struct {
	WindowID int                   `json:"window_id"`
	Stats    WindowDebugStats      `json:"stats"`
	Durable  WindowDebugDurable    `json:"durable"`
	Renderer WindowDebugRenderer   `json:"renderer"`
	Shadow   windowDebugShadowV9   `json:"shadow"`
	Watchdog windowDebugWatchdogV9 `json:"watchdog"`
	Live     WindowLiveMetadata    `json:"live"`
	Events   []sessionEventV9      `json:"events"`
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
	Shadow   windowDebugShadowV9   `json:"shadow"`
	Watchdog windowDebugWatchdogV9 `json:"watchdog"`
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

// sessionEventV9 pins the complete pre-process causal shape. Embedding the v8
// projection keeps its established fields flat on the wire while ensuring
// schema-v10 lifecycle identity and settlement fields cannot leak backward.
type sessionEventV9 struct {
	sessionEventV8
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
	RenderGeneration     uint64                  `json:"render_generation,omitempty"`
	RenderOutcome        string                  `json:"render_outcome,omitempty"`
	RenderReason         string                  `json:"render_reason,omitempty"`
	NotificationTrace    string                  `json:"notification_trace_id,omitempty"`
	NotificationOutcome  string                  `json:"notification_outcome,omitempty"`
	NotificationReason   string                  `json:"notification_reason,omitempty"`
	ProbeTraceIDs        []string                `json:"probe_trace_ids,omitempty"`
}

type windowDebugWatchdogV9 struct {
	LastEvent      string `json:"last_event,omitempty"`
	LastReason     string `json:"last_reason,omitempty"`
	LastAt         int64  `json:"last_at,omitempty"`
	SourceExpiries int    `json:"source_expiries,omitempty"`
	StaleSweeps    int    `json:"stale_sweeps,omitempty"`
	Reconciles     int    `json:"reconciles,omitempty"`
	ClosedAt       int64  `json:"closed_at,omitempty"`
}

// windowDebugShadowV9 and sourceFactSnapshotV9 pin the complete schema-v9
// shadow shape. SourceFactSnapshot is also the current schema's nested fact
// type, so using it here would leak schema-v10 process ownership into both
// legacy projections as soon as lifecycle fields are populated.
type windowDebugShadowV9 struct {
	EffectiveIcon    string                 `json:"effective_icon"`
	EffectiveLooping bool                   `json:"effective_looping"`
	EffectiveGen     uint64                 `json:"effective_gen"`
	MismatchCount    int                    `json:"mismatch_count"`
	Mismatches       []ShadowMismatch       `json:"mismatches,omitempty"`
	Facts            []sourceFactSnapshotV9 `json:"facts,omitempty"`
}

type sourceFactSnapshotV9 struct {
	Kind            SourceKind `json:"k"`
	Key             string     `json:"y,omitempty"`
	Action          string     `json:"a,omitempty"`
	Icon            string     `json:"i,omitempty"`
	Seq             uint64     `json:"q,omitempty"`
	CausalOrder     uint64     `json:"o,omitempty"`
	TurnID          string     `json:"tn,omitempty"`
	At              int64      `json:"t,omitempty"`
	Acked           bool       `json:"ak,omitempty"`
	AckedAt         int64      `json:"aa,omitempty"`
	OriginWindowID  int        `json:"ow,omitempty"`
	ToolUse         string     `json:"tu,omitempty"`
	PipeMode        bool       `json:"pm,omitempty"`
	SessionID       string     `json:"sid,omitempty"`
	Agents          int        `json:"ag,omitempty"`
	RunID           string     `json:"r,omitempty"`
	Phase           string     `json:"p,omitempty"`
	LeaseLive       bool       `json:"ll,omitempty"`
	Terminated      bool       `json:"tm,omitempty"`
	TerminalTurn    string     `json:"tt,omitempty"`
	TerminalToolUse string     `json:"ttu,omitempty"`
	TerminalAt      int64      `json:"ta,omitempty"`
}

func projectExportEventsPayloadV10(payload ExportEventsPayload) exportEventsPayloadV10 {
	out := exportEventsPayloadV10{
		SchemaVersion: EventsExportSchemaV10,
		ExportedAt:    payload.ExportedAt,
		Daemon:        payload.Daemon,
		CLIBuild:      payload.CLIBuild,
	}
	if payload.Windows != nil {
		out.Windows = make([]exportWindowV10, 0, len(payload.Windows))
	}
	for _, window := range payload.Windows {
		events := make([]sessionEventV10, 0, len(window.Events))
		for _, event := range window.Events {
			if omitFromCompatibilityExport(event) {
				continue
			}
			events = append(events, projectSessionEventV10(event))
		}
		out.Windows = append(out.Windows, exportWindowV10{
			WindowID: window.WindowID, Stats: projectCompatibilityWindowDebugStats(window.Stats), Durable: window.Durable,
			Renderer: window.Renderer, Shadow: projectWindowDebugShadowV10(window.Shadow),
			Watchdog: window.Watchdog, Live: window.Live, Events: events,
		})
	}
	return out
}

func projectExportEventsPayloadV9(payload ExportEventsPayload) exportEventsPayloadV9 {
	out := exportEventsPayloadV9{
		SchemaVersion: EventsExportSchemaV9,
		ExportedAt:    payload.ExportedAt,
		Daemon:        payload.Daemon,
		CLIBuild:      payload.CLIBuild,
	}
	if payload.Windows != nil {
		out.Windows = make([]exportWindowV9, 0, len(payload.Windows))
	}
	for _, window := range payload.Windows {
		events := make([]sessionEventV9, 0, len(window.Events))
		for _, event := range window.Events {
			if omitFromCompatibilityExport(event) {
				continue
			}
			events = append(events, projectSessionEventV9(event))
		}
		out.Windows = append(out.Windows, exportWindowV9{
			WindowID: window.WindowID, Stats: projectCompatibilityWindowDebugStats(window.Stats), Durable: window.Durable,
			Renderer: window.Renderer, Shadow: projectWindowDebugShadowV9(window.Shadow),
			Watchdog: projectWindowDebugWatchdogV9(window.Watchdog), Live: window.Live, Events: events,
		})
	}
	return out
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
		events := make([]sessionEventV8, 0, len(window.Events))
		for _, event := range window.Events {
			if omitFromCompatibilityExport(event) {
				continue
			}
			events = append(events, projectSessionEventV8(event))
		}
		r := window.Renderer
		out.Windows = append(out.Windows, exportWindowV8{
			WindowID: window.WindowID, Stats: projectCompatibilityWindowDebugStats(window.Stats), Durable: window.Durable,
			Renderer: windowDebugRendererV8{
				DesiredIcon: r.DesiredIcon, StateSet: r.StateSet, Looping: r.Looping, LoopingSet: r.LoopingSet,
				RenderFailures: r.RenderFailures, LastRenderError: r.LastRenderError, KittyListenOn: r.KittyListenOn,
				QueuedGen: r.QueuedGen, AttemptedGen: r.AttemptedGen, PersistedGen: r.PersistedGen,
				RenderedGen: r.RenderedGen, FailedGen: r.FailedGen, DirtySince: r.DirtySince,
				DirtyAgeSeconds: r.DirtyAgeSeconds, LastRenderLatencyMs: r.LastRenderLatencyMs,
			},
			Shadow: projectWindowDebugShadowV9(window.Shadow), Watchdog: projectWindowDebugWatchdogV9(window.Watchdog), Live: window.Live, Events: events,
		})
	}
	return out
}

func projectSessionEventV8(event SessionEvent) sessionEventV8 {
	return sessionEventV8{
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

func projectSessionEventV9(event SessionEvent) sessionEventV9 {
	return sessionEventV9{
		sessionEventV8: projectSessionEventV8(event),
		TraceID:        event.TraceID, ParentTraceID: event.ParentTraceID, RequestID: event.RequestID,
		SourceEventID: event.SourceEventID, Attempt: event.Attempt, PayloadSize: event.PayloadSize,
		PayloadHMAC: event.PayloadHMAC, PayloadEncoding: event.PayloadEncoding, ParseOutcome: event.ParseOutcome,
		NormalizationOutcome: event.NormalizationOutcome, IdentityConfidence: event.IdentityConfidence,
		UsedFallbackIdentity: event.UsedFallbackIdentity, ProducerHarness: event.ProducerHarness,
		TargetHarness: event.TargetHarness, Profile: event.Profile, SessionID: event.SessionID,
		RunID: event.RunID, TurnID: event.TurnID, ToolUseID: event.ToolUseID,
		SaturationReason: event.SaturationReason, TargetWindowID: event.TargetWindowID,
		ReducerInputAction: event.ReducerInputAction, ReducerInput: event.ReducerInput,
		ReducerDecision: event.ReducerDecision, ReducerReason: event.ReducerReason,
		DecisionReason: event.DecisionReason, Quarantined: event.Quarantined,
		EffectiveBefore: event.EffectiveBefore, EffectiveAfter: event.EffectiveAfter,
		RouteEvidence: event.RouteEvidence, RenderGeneration: event.RenderGeneration,
		RenderOutcome: event.RenderOutcome, RenderReason: event.RenderReason,
		NotificationTrace: event.NotificationTrace, NotificationOutcome: event.NotificationOutcome,
		NotificationReason: event.NotificationReason, ProbeTraceIDs: event.ProbeTraceIDs,
	}
}

func projectSessionEventV10(event SessionEvent) sessionEventV10 {
	return sessionEventV10{
		sessionEventV9:    projectSessionEventV9(event),
		ProducerConfigDir: event.ProducerConfigDir,
		ProcessHarness:    event.ProcessHarness, ProcessPID: event.ProcessPID,
		ProcessStartToken: event.ProcessStartToken, ProcessObservation: event.ProcessObservation,
		ProcessMode: event.ProcessMode, ProcessSources: event.ProcessSources,
		WithdrawnFacts: event.WithdrawnFacts, ClearedSubagents: event.ClearedSubagents,
		SynthesizedStop: event.SynthesizedStop, TerminalHookEvidence: event.TerminalHookEvidence,
		QuestionPreview: event.QuestionPreview,
	}
}

func projectWindowDebugShadowV10(shadow WindowDebugShadow) windowDebugShadowV10 {
	return windowDebugShadowV10{
		EffectiveIcon:    shadow.EffectiveIcon,
		EffectiveLooping: shadow.EffectiveLooping,
		EffectiveGen:     shadow.EffectiveGen,
		MismatchCount:    shadow.MismatchCount,
		Mismatches:       shadow.Mismatches,
		Facts:            shadow.Facts,
	}
}

func projectWindowDebugWatchdogV9(watchdog WindowDebugWatchdog) windowDebugWatchdogV9 {
	return windowDebugWatchdogV9{
		LastEvent: watchdog.LastEvent, LastReason: watchdog.LastReason, LastAt: watchdog.LastAt,
		SourceExpiries: watchdog.SourceExpiries, StaleSweeps: watchdog.StaleSweeps,
		Reconciles: watchdog.Reconciles, ClosedAt: watchdog.ClosedAt,
	}
}

func projectWindowDebugShadowV9(shadow WindowDebugShadow) windowDebugShadowV9 {
	out := windowDebugShadowV9{
		EffectiveIcon:    shadow.EffectiveIcon,
		EffectiveLooping: shadow.EffectiveLooping,
		EffectiveGen:     shadow.EffectiveGen,
		MismatchCount:    shadow.MismatchCount,
		Mismatches:       shadow.Mismatches,
	}
	if shadow.Facts != nil {
		out.Facts = make([]sourceFactSnapshotV9, len(shadow.Facts))
	}
	for i, fact := range shadow.Facts {
		out.Facts[i] = sourceFactSnapshotV9{
			Kind: fact.Kind, Key: fact.Key, Action: fact.Action, Icon: fact.Icon,
			Seq: fact.Seq, CausalOrder: fact.CausalOrder, TurnID: fact.TurnID, At: fact.At,
			Acked: fact.Acked, AckedAt: fact.AckedAt, OriginWindowID: fact.OriginWindowID,
			ToolUse: fact.ToolUse, PipeMode: fact.PipeMode, SessionID: fact.SessionID,
			Agents: fact.Agents, RunID: fact.RunID, Phase: fact.Phase, LeaseLive: fact.LeaseLive,
			Terminated: fact.Terminated, TerminalTurn: fact.TerminalTurn,
			TerminalToolUse: fact.TerminalToolUse, TerminalAt: fact.TerminalAt,
		}
	}
	return out
}
