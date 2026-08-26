package daemon

import (
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/buildinfo"
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

	// ASL bridge requests. They are answered from daemon memory or forwarded to
	// the bridge subprocess; every one of them fails cleanly when the optional
	// integration is disabled or unavailable.
	TypeASLSnapshot  MessageType = "asl_snapshot"
	TypeASLQuestion  MessageType = "asl_question"
	TypeASLAction    MessageType = "asl_action"
	TypeASLWorkbench MessageType = "asl_workbench"

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

// EventHarnessProcessExited is the stable synthetic event emitted when the
// daemon retires state owned by one exact harness PID/start-token fingerprint.
// It is a public diagnostic contract; native hook event names remain unchanged.
const EventHarnessProcessExited = "HarnessProcessExited"

// EventTerminalStopHold is the stable synthetic event emitted when the daemon
// withholds a native terminal stop from the reducer, or drops that hold because
// a newer authoritative event superseded it. The row is diagnostic evidence
// only: no reducer fact is applied and the visible icon is unchanged.
const EventTerminalStopHold = "TerminalStopHold"

// StopHoldOutcome is the bounded vocabulary describing what happened to one
// held terminal stop. Consumers branch on these values; the daemon never writes
// free text into the field.
type StopHoldOutcome string

const (
	// StopHoldOutcomeHeld records that a terminal stop was withheld;
	// StopHoldOutcomeSuperseded that a newer event retired it before it painted.
	StopHoldOutcomeHeld       StopHoldOutcome = "held"
	StopHoldOutcomeSuperseded StopHoldOutcome = "superseded"
	// The release outcomes describe how a held stop reached the reducer: a
	// harness idle probe confirmed it, the hard deadline expired, or a verified
	// harness-process exit bypassed the gate entirely.
	StopHoldOutcomeConfirmed StopHoldOutcome = "confirmed"
	StopHoldOutcomeDeadline  StopHoldOutcome = "deadline"
	StopHoldOutcomeBypassed  StopHoldOutcome = "bypassed"
)

// StopConfirmedBy names the exact evidence that released a held terminal stop.
// Probe values keep the meaning documented for `probe:<harness>` provenance:
// the harness's own idle observation, never a lost-hook substitute.
type StopConfirmedBy string

const (
	StopConfirmedByClaudeScreen      StopConfirmedBy = "probe:claude_screen"
	StopConfirmedByCodexScreen       StopConfirmedBy = "probe:codex_screen"
	StopConfirmedByCopilotTranscript StopConfirmedBy = "probe:copilot_transcript"
	StopConfirmedByDeadline          StopConfirmedBy = "deadline"
)

// HarnessProcessIdentity is the exact, non-sensitive process fingerprint
// discovered by a hook client. StartToken is the normalized opaque `ps lstart`
// value; consumers compare it byte-for-byte and never parse it as a timestamp.
// Command lines and arguments are intentionally absent from this transport.
type HarnessProcessIdentity struct {
	Harness    string `json:"harness"`
	PID        int    `json:"pid"`
	StartToken string `json:"start_token"`
}

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
	// stopHoldOutcome, stopHoldWaitedMs and stopConfirmedBy stamp the release
	// evidence of a terminal stop hold onto the stop the hold finally commits.
	// Only commitStopHold sets them, so a client can never claim its own stop
	// was confirmed idle.
	stopHoldOutcome  StopHoldOutcome
	stopHoldWaitedMs int
	stopConfirmedBy  StopConfirmedBy
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
	ProducerHarness string `json:"producer_harness,omitempty"`
	// ProducerConfigDir is the producing harness's resolved home/config
	// directory (config env var override or harness default), normalized by the
	// hook client. It credits activation evidence to the producer's home even
	// when routing attributes the event to another window's launch profile.
	ProducerConfigDir string `json:"producer_config_dir,omitempty"`
	// HarnessProcess is optional and accepted only when its canonical harness
	// matches ProducerHarness/Harness and its PID/start token are complete.
	HarnessProcess   *HarnessProcessIdentity `json:"harness_process,omitempty"`
	WindowID         int                     `json:"window_id,omitempty"`         // KITTY_WINDOW_ID
	Action           string                  `json:"action,omitempty"`            // active|stop|permission|failure|stop_failure|idle|clear|user_clear
	Event            string                  `json:"event,omitempty"`             // raw Claude hook event name (e.g. UserPromptSubmit)
	PayloadJSON      string                  `json:"payload_json,omitempty"`      // raw hook stdin payload for TypeHookEvent
	ToolName         string                  `json:"tool_name,omitempty"`         // tool name from PreToolUse/PostToolUse hook payloads
	Source           string                  `json:"source,omitempty"`            // raw source payload field
	IsPrompt         bool                    `json:"is_prompt,omitempty"`         // true if event is a user prompt submission
	PreservesIcon    bool                    `json:"preserves_icon,omitempty"`    // true if a non-terminal child-complete event should preserve current icon/guard semantics
	Account          string                  `json:"account,omitempty"`           // profile name from amx_profile or cc_account
	Harness          string                  `json:"harness,omitempty"`           // event-producing harness; unknown/legacy producers may be empty
	SessionTitle     string                  `json:"session_title,omitempty"`     // tab base title (e.g. "W:api")
	PipeMode         bool                    `json:"pipe_mode,omitempty"`         // suppress notifications when claude -p
	KittyListenOn    string                  `json:"kitty_listen_on,omitempty"`   // KITTY_LISTEN_ON for daemon-side Kitty writes
	NotificationType string                  `json:"notification_type,omitempty"` // normalized notification_type / notificationType payload field
	StopReason       string                  `json:"stop_reason,omitempty"`       // normalized stop_reason / stopReason payload field
	Reason           string                  `json:"reason,omitempty"`            // normalized reason payload field
	ContextLimit     bool                    `json:"context_limit,omitempty"`     // true when indicator detected a context-limit stop/failure
	ResetFlow        bool                    `json:"reset_flow,omitempty"`        // true when the event belongs to a user-initiated session reset flow
	// QuestionPreview is the ASL mailbox question summary, already collapsed and
	// bounded by hookevent decoding. It reaches the TUI detail view through
	// SessionEvent only and is deliberately never folded into reducer facts.
	QuestionPreview string `json:"question_preview,omitempty"`
	// ASL carries the typed parameters of an ASL bridge request. It is a
	// separate struct rather than more flat fields so no ASL parameter can be
	// confused with hook-event state by a generic consumer.
	ASL *ASLRequest `json:"asl,omitempty"`
}

// ASLRequest is the typed parameter block for the ASL message types. Text is
// operator-authored reply prose: it is a SecretText so no formatter, log line,
// or journal record can render it.
type ASLRequest struct {
	Method      string               `json:"method,omitempty"`
	PlanDir     string               `json:"plan_dir,omitempty"`
	RowID       string               `json:"row_id,omitempty"`
	RunID       string               `json:"run_id,omitempty"`
	QuestionID  string               `json:"question_id,omitempty"`
	OperationID string               `json:"operation_id,omitempty"`
	ActionToken string               `json:"action_token,omitempty"`
	Resolution  string               `json:"resolution,omitempty"`
	Note        string               `json:"note,omitempty"`
	Focus       string               `json:"focus,omitempty"`
	Text        aslbridge.SecretText `json:"text,omitempty"`
}

// ASLResponse carries whichever typed result the requested ASL method produces.
// Status accompanies every reply so a client can render availability and
// staleness without a second round trip.
type ASLResponse struct {
	Status    aslbridge.Status         `json:"status"`
	Snapshot  *aslbridge.Snapshot      `json:"snapshot,omitempty"`
	Meta      *aslbridge.SnapshotMeta  `json:"meta,omitempty"`
	Receipts  []aslbridge.Operation    `json:"receipts,omitempty"`
	Question  *aslbridge.QuestionBody  `json:"question,omitempty"`
	Operation *aslbridge.Operation     `json:"operation,omitempty"`
	Workbench *aslbridge.WorkbenchLink `json:"workbench,omitempty"`
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
	// Notification is omitted by older daemons. New daemons publish the
	// startup-selected backend capability independently of global readiness.
	Notification *NotificationCapability `json:"notification,omitempty"`
	// ASLCapability is the optional agent-session-loop integration block. It is
	// omitted entirely by daemons built without the integration configured, and
	// its unavailability never affects Ready.
	ASLCapability *aslbridge.Status `json:"asl,omitempty"`
	// ASLReconcile reports projection-discovery health: how far behind the
	// reconciliation cadence has fallen and how many plans the last sweep could
	// not classify. Unlike ASLCapability it is published whether or not a
	// bridge is configured, because discovery reads ASL's durable state
	// directly and can be failing while the bridge looks perfectly healthy.
	ASLReconcile *ASLReconcileSnapshot `json:"asl_reconcile,omitempty"`
	// ASLHookDelivery reports the inbound direction: how many lifecycle hooks
	// were believed and how many were quarantined, under stable daemon-authored
	// reasons. It is published alongside ASLReconcile because a pairing can be
	// refusing every event while both the bridge and the sweep look healthy —
	// the hook path answers OK to a producer it has declined to believe.
	ASLHookDelivery *ASLHookDeliverySnapshot `json:"asl_hook_delivery,omitempty"`
	// ASL is populated for the ASL message types.
	ASL *ASLResponse `json:"asl_result,omitempty"`
	// Ready reports whether the daemon completed its bounded initial restore
	// before binding the public socket, or — after a degraded start — later
	// recovered when a maintenance tick restored durable state from a
	// successful snapshot and marked the daemon ready. DegradedReason explains
	// why it started (or remains) degraded when Ready is false.
	Ready          bool   `json:"ready"`
	DegradedReason string `json:"degraded_reason,omitempty"`
}
