package daemon

// Public wire-schema projection. Every compatibility schema is an explicit
// field projection rather than a reuse of the current structs, so a field added
// to the current schema can never leak into older output through a shared
// nested type. This file holds the dispatch, the projection rules shared by all
// compatibility schemas, and the v11/v10 shapes; v9 and v8 live in
// export_schema_legacy.go.

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/buildinfo"
)

// ProjectExportEventsPayload returns the requested public wire schema. Schemas
// 8, 9, 10, and 11 are explicit field projections so fields added to the current
// schema can never leak into compatibility output through shared nested
// structs.
func ProjectExportEventsPayload(payload ExportEventsPayload, schema int) (any, error) {
	switch schema {
	case EventsExportSchemaVersion:
		payload.SchemaVersion = EventsExportSchemaVersion
		return payload, nil
	case EventsExportSchemaV11:
		return projectExportEventsPayloadV11(payload), nil
	case EventsExportSchemaV10:
		return projectExportEventsPayloadV10(payload), nil
	case EventsExportSchemaV9:
		return projectExportEventsPayloadV9(payload), nil
	case EventsExportSchemaV8:
		return projectExportEventsPayloadV8(payload), nil
	default:
		return nil, fmt.Errorf("unsupported export schema %d (want 8, 9, 10, 11, or 12)", schema)
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

type exportEventsPayloadV11 struct {
	SchemaVersion int                  `json:"schema_version"`
	ExportedAt    int64                `json:"exported_at"`
	Daemon        ExportDaemonMetadata `json:"daemon"`
	CLIBuild      *buildinfo.Info      `json:"cli_build,omitempty"`
	Windows       []exportWindowV11    `json:"windows"`
}

type exportWindowV11 struct {
	WindowID int                  `json:"window_id"`
	Stats    WindowDebugStats     `json:"stats"`
	Durable  WindowDebugDurable   `json:"durable"`
	Renderer WindowDebugRenderer  `json:"renderer"`
	Shadow   windowDebugShadowV11 `json:"shadow"`
	Watchdog WindowDebugWatchdog  `json:"watchdog"`
	Live     WindowLiveMetadata   `json:"live"`
	Events   []SessionEvent       `json:"events"`
}

// windowDebugShadowV11 pins the schema-v11 shadow shape: everything the current
// shadow carries through the pending terminal stop. The v12 busy-stretch anchor
// is deliberately absent.
type windowDebugShadowV11 struct {
	EffectiveIcon       string               `json:"effective_icon"`
	EffectiveLooping    bool                 `json:"effective_looping"`
	EffectiveGen        uint64               `json:"effective_gen"`
	MismatchCount       int                  `json:"mismatch_count"`
	Mismatches          []ShadowMismatch     `json:"mismatches,omitempty"`
	Facts               []SourceFactSnapshot `json:"facts,omitempty"`
	PendingTerminalStop *PendingTerminalStop `json:"pending_terminal_stop,omitempty"`
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

func projectExportEventsPayloadV11(payload ExportEventsPayload) exportEventsPayloadV11 {
	out := exportEventsPayloadV11{
		SchemaVersion: EventsExportSchemaV11,
		ExportedAt:    payload.ExportedAt,
		Daemon:        payload.Daemon,
		CLIBuild:      payload.CLIBuild,
	}
	if payload.Windows != nil {
		out.Windows = make([]exportWindowV11, 0, len(payload.Windows))
	}
	for _, window := range payload.Windows {
		out.Windows = append(out.Windows, exportWindowV11{
			WindowID: window.WindowID, Stats: window.Stats, Durable: window.Durable,
			Renderer: window.Renderer, Shadow: projectWindowDebugShadowV11(window.Shadow),
			Watchdog: window.Watchdog, Live: window.Live, Events: window.Events,
		})
	}
	return out
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

func projectWindowDebugShadowV11(shadow WindowDebugShadow) windowDebugShadowV11 {
	return windowDebugShadowV11{
		EffectiveIcon:       shadow.EffectiveIcon,
		EffectiveLooping:    shadow.EffectiveLooping,
		EffectiveGen:        shadow.EffectiveGen,
		MismatchCount:       shadow.MismatchCount,
		Mismatches:          shadow.Mismatches,
		Facts:               shadow.Facts,
		PendingTerminalStop: shadow.PendingTerminalStop,
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
