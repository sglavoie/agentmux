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
