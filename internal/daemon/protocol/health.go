package protocol

// This file holds the health-response value types carried by the daemon wire
// Response. They are plain serialization shapes: the counters, capability
// probes, and load results that produce them stay with their subsystems.

// MetricsSnapshot is a plain-value copy of Metrics for reporting.
type MetricsSnapshot struct {
	Accepted      uint64 `json:"accepted"`
	Duplicate     uint64 `json:"duplicate"`
	Rejected      uint64 `json:"rejected"`
	QueueFull     uint64 `json:"queue_full"`
	ExpiredDedupe uint64 `json:"expired_dedupe"`
	// Ingress queue latency in nanoseconds (last observation / lifetime max).
	QueueLastWaitNs int64 `json:"queue_last_wait_ns,omitempty"`
	QueueMaxWaitNs  int64 `json:"queue_max_wait_ns,omitempty"`
	// Subscription stream counters: subscribers streaming now, subscribes
	// refused (limit or shutdown), streams ended as slow consumers, frames
	// written, and frames dropped for exceeding CompanionFrameMaxBytes.
	SubscribersActive       int64  `json:"subscribers_active,omitempty"`
	SubscribersRejected     uint64 `json:"subscribers_rejected,omitempty"`
	SubscribersSlowConsumer uint64 `json:"subscribers_slow_consumer,omitempty"`
	StreamFramesWritten     uint64 `json:"stream_frames_written,omitempty"`
	StreamFramesOversize    uint64 `json:"stream_frames_oversize,omitempty"`
}

// NotificationCapability is the daemon-start snapshot of native notification
// support. It is exported through health so operators can distinguish a ready
// daemon from an unavailable optional notification dependency.
type NotificationCapability struct {
	Enabled     bool   `json:"enabled"`
	Available   bool   `json:"available"`
	Interactive bool   `json:"interactive"`
	Backend     string `json:"backend"`
	Executable  string `json:"executable,omitempty"`
	Version     string `json:"version,omitempty"`
	Reason      string `json:"reason,omitempty"`
	// SnoozedUntil is the Unix second a timed snooze ends, and
	// SnoozeIndefinite whether a snooze holds until cleared. Neither is part
	// of the startup probe: they are the in-memory snooze at the moment of
	// the health answer.
	SnoozedUntil     int64 `json:"snoozed_until,omitempty"`
	SnoozeIndefinite bool  `json:"snooze_indefinite,omitempty"`
}

// ASLReconcileSnapshot is the health-response view of reconciliation state.
type ASLReconcileSnapshot struct {
	StateRoot string `json:"state_root"`
	// LagSeconds is how long ago the last successful sweep completed. It is
	// computed at read time rather than stored, because a daemon that has
	// stopped reconciling stops updating stored values too — the useful signal
	// is precisely the one a stalled writer could not have written.
	LagSeconds      float64 `json:"lag_seconds"`
	MaxGapSeconds   float64 `json:"max_gap_seconds"`
	IntervalSeconds float64 `json:"interval_seconds"`
	Sweeps          uint64  `json:"sweeps"`
	SweepErrors     uint64  `json:"sweep_errors"`

	PlansPresent       int `json:"plans_present"`
	PlansAbsent        int `json:"plans_confirmed_absent"`
	PlansIndeterminate int `json:"plans_indeterminate"`

	InvalidProjections   uint64 `json:"invalid_projections"`
	UnsupportedContracts uint64 `json:"unsupported_contracts"`
	UnreadableStates     uint64 `json:"unreadable_states"`
	MissingProjections   uint64 `json:"missing_projections"`
	ProcessUnverified    uint64 `json:"process_unverified"`

	ConfirmedMisses     uint64 `json:"confirmed_misses"`
	IndeterminateLeases uint64 `json:"indeterminate_leases"`
	RetiredLeases       uint64 `json:"retired_leases"`
	StaleObservations   uint64 `json:"stale_observations"`

	ContractSetID string `json:"contract_set_id,omitempty"`
}

// ASLHookDeliverySnapshot is the health-response view of hook ingestion.
type ASLHookDeliverySnapshot struct {
	Accepted    uint64 `json:"accepted"`
	Quarantined uint64 `json:"quarantined"`
	// QuarantinedByReason is keyed by the stable refusal reasons. It is omitted
	// when empty rather than published as an empty object, so a consumer can
	// tell "no refusals" from "refusals this build does not classify".
	QuarantinedByReason map[string]uint64 `json:"quarantined_by_reason,omitempty"`

	// AcceptedAgeSeconds and QuarantinedAgeSeconds are ages rather than
	// timestamps, computed at read time. A daemon that stopped ingesting also
	// stopped writing, so the useful signal is the one a stalled writer could
	// not have produced.
	AcceptedAgeSeconds    *float64 `json:"accepted_age_seconds,omitempty"`
	QuarantinedAgeSeconds *float64 `json:"quarantined_age_seconds,omitempty"`

	LastQuarantineReason string `json:"last_quarantine_reason,omitempty"`
}

// ScreenPatternWarning is one screenstate.LoadWarning in health-response form.
// Kind is the stable machine-readable vocabulary from
// screenstate.LoadWarningKind; consumers key on it rather than on Detail.
type ScreenPatternWarning struct {
	Kind    string `json:"kind"`
	Harness string `json:"harness,omitempty"`
	Path    string `json:"path,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// ScreenPatternsSnapshot is the health-response view of the startup pattern
// load. Sources maps each pattern set's name to where its installed copy came
// from — "embedded" or the absolute override path — so a stale local file is
// visible without reading the operator's config directory. Warnings is
// everything Load wanted a human to know, in Load's deterministic order.
//
// Note that a pattern set's name is not always a harness: internal/contextlimit
// owns one too, and it flows through the same loader under the name
// "contextlimit".
type ScreenPatternsSnapshot struct {
	Sources  map[string]string      `json:"sources"`
	Warnings []ScreenPatternWarning `json:"warnings,omitempty"`
}

// RuntimeHealth is the health-response view of the runtime sessions and the
// backend work the daemon does for them: how current its reconciliation with
// the private servers is, how many sessions it holds and how many have a
// terminal attached, how its screen reads are going, and how many tmux client
// processes it has had outstanding. Every field is a count, a duration, or a
// word from a fixed vocabulary: nothing here is read from a pane, a prompt,
// or a launch environment, so the block is safe to export as it is.
type RuntimeHealth struct {
	Reconcile  RuntimeReconcileHealth `json:"reconcile"`
	Sessions   RuntimeSessionHealth   `json:"sessions"`
	Capture    ScreenCaptureHealth    `json:"capture"`
	TmuxClient TmuxClientHealth       `json:"tmux_client"`
}

// RuntimeReconcileHealth describes the passes that bring the runtime-session
// registry in line with the backends.
type RuntimeReconcileHealth struct {
	// Enabled is false for a daemon running without a registry, which
	// reconciles nothing and whose other fields are zero.
	Enabled bool `json:"enabled"`
	// IntervalSeconds is how long after a pass the next is due: two seconds,
	// and up to thirty while passes keep failing.
	IntervalSeconds float64 `json:"interval_seconds"`
	Passes          uint64  `json:"passes"`
	Failures        uint64  `json:"failures"`
	// ConsecutiveFailures is zero as soon as one pass reads every server.
	ConsecutiveFailures uint64 `json:"consecutive_failures"`
	// AgeSeconds is how long ago the last pass finished, computed at read
	// time; omitted before the first one.
	AgeSeconds     *float64 `json:"age_seconds,omitempty"`
	LastDurationMs float64  `json:"last_duration_ms"`
	MaxDurationMs  float64  `json:"max_duration_ms"`
	// FailureClass is the kind of the most recent failure, from
	// tmux.FailureClass or "registry_unreadable"; empty when the last pass
	// succeeded.
	FailureClass string `json:"failure_class,omitempty"`
	// Ended, Released, and Detached total what the passes changed: agents
	// found exited or lost, ownerships released because the terminal was
	// gone, and competing terminals detached.
	Ended    uint64 `json:"ended"`
	Released uint64 `json:"released"`
	Detached uint64 `json:"detached"`
}

// RuntimeSessionHealth counts the live runtime sessions as of the last pass.
type RuntimeSessionHealth struct {
	// Known is false until a pass has read the registry, and again whenever
	// the last one could not.
	Known bool `json:"known"`
	Live  int  `json:"live"`
	Tmux  int  `json:"tmux"`
	// Servers counts the distinct private servers those sessions run on.
	Servers int `json:"servers"`
	// Attached counts sessions a terminal owns; Unattached the rest, which
	// run with nobody looking.
	Attached   int `json:"attached"`
	Unattached int `json:"unattached"`
}

// ScreenCaptureHealth describes the daemon's own reads of pane screens (the
// screen probes), whichever backend they went to.
type ScreenCaptureHealth struct {
	Reads               uint64 `json:"reads"`
	Failures            uint64 `json:"failures"`
	ConsecutiveFailures uint64 `json:"consecutive_failures"`
	// AgeSeconds is how long ago a read last succeeded; omitted when none
	// has. A daemon with no active agent reads no screen, so an old capture
	// is not by itself a fault.
	AgeSeconds     *float64 `json:"age_seconds,omitempty"`
	LastDurationMs float64  `json:"last_duration_ms"`
	MaxDurationMs  float64  `json:"max_duration_ms"`
}

// TmuxClientHealth is the daemon's tmux client activity (tmux.ClientStats).
type TmuxClientHealth struct {
	Limit         int     `json:"limit"`
	Commands      uint64  `json:"commands"`
	Failures      uint64  `json:"failures"`
	Timeouts      uint64  `json:"timeouts"`
	InFlight      int     `json:"in_flight"`
	MaxInFlight   int     `json:"max_in_flight"`
	Queued        uint64  `json:"queued"`
	MaxQueueMs    float64 `json:"max_queue_ms"`
	MaxDurationMs float64 `json:"max_duration_ms"`
}
