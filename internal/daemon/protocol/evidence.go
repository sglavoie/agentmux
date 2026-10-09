package protocol

// Decision reason constants are the stable vocabulary shared by daemon traces
// and the explanation service. Keep these values machine-oriented: callers
// must branch on them rather than parsing human log messages.
const (
	DecisionReasonAccepted                  = "accepted"
	DecisionReasonMissingEvent              = "missing_event"
	DecisionReasonInvalidPayload            = "invalid_payload"
	DecisionReasonUnknownEvent              = "unknown_event"
	DecisionReasonIgnoredEvent              = "ignored_event"
	DecisionReasonGuardBlocked              = "guard_blocked"
	DecisionReasonSubagentStillRunning      = "subagent_still_running"
	DecisionReasonCopilotMidSubagent        = "copilot_mid_subagent"
	DecisionReasonCopilotPendingPrompt      = "copilot_pending_prompt"
	DecisionReasonActiveAfterTerminal       = "active_after_terminal"
	DecisionReasonElicitationPending        = "elicitation_pending"
	DecisionReasonAutoApprovedPermission    = "auto_approved_permission"
	DecisionReasonOwnerMismatch             = "owner_mismatch"
	DecisionReasonUnmanagedPipeSource       = "unmanaged_pipe_source"
	DecisionReasonPreservesIcon             = "preserves_icon"
	DecisionReasonReducerBypassed           = "reducer_bypassed"
	DecisionReasonUntrustedIdentityFallback = "untrusted_identity_fallback"
	DecisionReasonRequestReplayed           = "request_replayed"
	DecisionReasonDuplicateSequence         = "duplicate_sequence"
	DecisionReasonReorderedSequence         = "reordered_sequence"
	DecisionReasonIdlePreservesTerminal     = "idle_preserves_terminal"
	DecisionReasonTerminalTurnSuppression   = "terminal_turn_suppression"
	DecisionReasonTerminalGraceSuppression  = "terminal_grace_suppression"
	DecisionReasonUserAcknowledged          = "user_acknowledged"
	DecisionReasonSessionAgentsCleared      = "session_agents_cleared"
	DecisionReasonNotSelected               = "not_selected"
	DecisionReasonTerminalStopHeld          = "terminal_stop_held"
	DecisionReasonTerminalStopSuperseded    = "terminal_stop_superseded"
	DecisionReasonTerminalStopBypassed      = "terminal_stop_bypassed"
	// DecisionReasonRuntimeSessionMismatch: the event belongs to a runtime
	// session other than the one bound to the reducer slot.
	DecisionReasonRuntimeSessionMismatch = "runtime_session_mismatch"
	// DecisionReasonRuntimeSessionReplaced: a new runtime session claimed the
	// slot, so the previous session's facts were discarded.
	DecisionReasonRuntimeSessionReplaced = "runtime_session_replaced"
)

const (
	ParseOutcomeDecoded      = "decoded"
	ParseOutcomeInvalid      = "invalid"
	ParseOutcomeMissingEvent = "missing_event"
	ParseOutcomeSynthetic    = "synthetic"

	NormalizationOutcomeNormalized        = "normalized"
	NormalizationOutcomeIgnored           = "ignored"
	NormalizationOutcomeUnknown           = "unknown"
	NormalizationOutcomeQuarantined       = "quarantined"
	NormalizationOutcomeLegacySynthesized = "legacy_synthesized"
	NormalizationOutcomeUntrustedFallback = "quarantined_legacy_fallback"
	NormalizationOutcomeReducerRewrite    = "reducer_rewrite"
	NormalizationOutcomeNotApplicable     = "not_applicable"

	IdentityConfidenceDeclared          = "declared"
	IdentityConfidenceDaemon            = "daemon"
	IdentityConfidenceLegacySynthesized = "legacy_synthesized"
	IdentityConfidenceUntrustedFallback = "untrusted_synthesized"
	IdentityConfidenceInvalid           = "invalid"
	IdentityConfidenceUnresolved        = "unresolved"
)

// ReducerDecision labels how Apply handled one event, for audit traces.
type ReducerDecision string

const (
	DecisionApplied      ReducerDecision = "applied"      // fact advanced
	DecisionDuplicate    ReducerDecision = "duplicate"    // same Seq already seen
	DecisionReordered    ReducerDecision = "reordered"    // older Seq arrived late
	DecisionSuppressed   ReducerDecision = "suppressed"   // terminal suppression blocked activity
	DecisionAcknowledged ReducerDecision = "acknowledged" // user_clear acked visible facts
	DecisionQuarantined  ReducerDecision = "quarantined"  // malformed/inert event never reached the facts
)

// DecisionBypassed is used when the harness-quirk layer intentionally handles
// an event without admitting a fact to the reducer (for example a
// preserve-current child completion). It is distinct from a reducer
// suppression, duplicate, or quarantine.
const DecisionBypassed ReducerDecision = "bypassed"

// EffectiveStateEvidence is the sanitized before/after reducer projection.
// It contains only state-machine identity and never carries hook payload text.
type EffectiveStateEvidence struct {
	Icon       string `json:"icon"`
	Looping    bool   `json:"looping"`
	SourceKind string `json:"source_kind,omitempty"`
	SourceKey  string `json:"source_key,omitempty"`
}

// RoutingWindowEvidence describes one Kitty pane considered by routing. Only
// stable identity and management metadata are retained; titles, directories,
// commands, prompts, and tool arguments are intentionally excluded.
type RoutingWindowEvidence struct {
	WindowID int    `json:"window_id"`
	Harness  string `json:"harness,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Managed  bool   `json:"managed"`
	Active   bool   `json:"active"`
	Source   bool   `json:"source,omitempty"`
	Owner    bool   `json:"owner,omitempty"`
}

// RejectedRouteEvidence identifies an alternative target and the stable reason
// it was not selected.
type RejectedRouteEvidence struct {
	WindowID int    `json:"window_id,omitempty"`
	Reason   string `json:"reason"`
}

// RoutingEvidence is the complete sanitized route decision attached to a
// SessionEvent. The candidate list is bounded so one event cannot make the
// in-memory ring or journal record grow with an unbounded Kitty snapshot.
type RoutingEvidence struct {
	SnapshotReady         bool                    `json:"snapshot_ready"`
	LookupOutcome         string                  `json:"lookup_outcome"`
	SourceWindowID        int                     `json:"source_window_id,omitempty"`
	TargetWindowID        int                     `json:"target_window_id,omitempty"`
	EffectiveHarness      string                  `json:"effective_harness,omitempty"`
	EffectiveProfile      string                  `json:"effective_profile,omitempty"`
	SourceMatched         bool                    `json:"source_matched"`
	OwnerMatched          bool                    `json:"owner_matched"`
	FocusCandidate        bool                    `json:"focus_candidate"`
	PromptCandidate       bool                    `json:"prompt_candidate"`
	StickyMatches         int                     `json:"sticky_matches,omitempty"`
	ASLMatches            int                     `json:"asl_matches,omitempty"`
	SelectionReason       string                  `json:"selection_reason,omitempty"`
	ChosenRoute           string                  `json:"chosen_route,omitempty"`
	ConsideredWindowCount int                     `json:"considered_window_count"`
	ConsideredWindows     []RoutingWindowEvidence `json:"considered_windows,omitempty"`
	RejectedAlternatives  []RejectedRouteEvidence `json:"rejected_alternatives,omitempty"`
}

// SessionEventReference links one in-flight request to the exact SessionEvent
// the daemon appended for it, so later finalization does not have to rely on
// "last event" ordering. It is process-local and never serialized.
type SessionEventReference struct {
	DaemonSeq uint64
}
