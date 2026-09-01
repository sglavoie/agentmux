package aslexport

// The typed shape of ASL's sanitized diagnostics export. Decoding into these
// rather than into a raw map is the boundary: a field a newer ASL adds cannot
// reach an Agent Mux bundle unreviewed.

// Build is ASL's build identity: which release it claims to be, and which bytes
// actually ran.
type Build struct {
	Product                 string `json:"product"`
	Version                 string `json:"version"`
	Executable              string `json:"executable"`
	BinaryHash              string `json:"binary_hash"`
	ContractSetID           string `json:"contract_set_id"`
	StateSchemaVersion      int    `json:"state_schema_version"`
	ProjectionSchemaVersion int    `json:"projection_schema_version"`
	HookSchemaVersion       int    `json:"hook_schema_version"`
	BridgeProtocol          int    `json:"bridge_protocol"`
}

// Omission is one entry of the export's own withholding ledger: what was left
// out, why, how much of it, and a digest identifying the withheld payloads.
type Omission struct {
	Section string `json:"section"`
	Reason  string `json:"reason"`
	Count   int    `json:"count"`
	SHA256  string `json:"sha256"`
}

// StatusTally, ActionTally, KindTally and EventTally are ASL's counted
// breakdowns. The export ships each breakdown as a list of labelled records
// rather than a map keyed by the tallied value, so that a redactor working from
// a key allowlist sees two known keys instead of one unknown key per value.
type StatusTally struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// ActionTally counts control-queue actions.
type ActionTally struct {
	Action string `json:"action"`
	Count  int    `json:"count"`
}

// KindTally counts mailbox receipt kinds.
type KindTally struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// EventTally counts lifecycle event names.
type EventTally struct {
	Event string `json:"event"`
	Count int    `json:"count"`
}

// Projection is the run position ASL publishes for the cockpit, copied through
// the projection contract's own field list.
type Projection struct {
	SchemaVersion           int    `json:"schema_version"`
	ContractSetID           string `json:"contract_set_id"`
	RunID                   string `json:"run_id"`
	Harness                 string `json:"harness"`
	Phase                   string `json:"phase"`
	SessionID               string `json:"session_id"`
	TurnIndex               *int   `json:"turn_index"`
	QuestionID              string `json:"question_id"`
	LifecycleSourceSequence int    `json:"lifecycle_source_sequence"`
	ProjectionGeneration    int    `json:"projection_generation"`
	StartedAt               string `json:"started_at"`
	UpdatedAt               string `json:"updated_at"`
	FinishedAt              string `json:"finished_at"`
}

// Delivery is one lifecycle notification ASL attempted to deliver. SourceEventID
// and RequestID are the join to the daemon journal's record for the same
// notification.
type Delivery struct {
	RunID               string `json:"run_id"`
	AgentMuxEvent       string `json:"agent_mux_event"`
	Harness             string `json:"harness"`
	SourceEventID       string `json:"source_event_id"`
	SourceSequence      *int   `json:"source_sequence"`
	RequestID           string `json:"request_id"`
	TraceID             string `json:"trace_id"`
	AttemptCount        *int   `json:"attempt_count"`
	FinalStatus         string `json:"final_status"`
	Reason              string `json:"reason"`
	ErrorClassification string `json:"error_classification"`
	Acknowledged        bool   `json:"acknowledged"`
}

// DeliverySection bounds the delivery records with the totals they were drawn
// from, so a reader never mistakes a cap for an absence.
type DeliverySection struct {
	Total        int           `json:"total"`
	Exported     int           `json:"exported"`
	Acknowledged int           `json:"acknowledged"`
	ByStatus     []StatusTally `json:"by_status"`
	Records      []Delivery    `json:"records"`
}

// Control is one durable control-queue observation. CommandID is the bridge
// operation ID, which is what joins a bridge call to the run that observed it.
type Control struct {
	Action           string `json:"action"`
	CommandID        string `json:"command_id"`
	QueueCommand     string `json:"queue_command"`
	StopAfterCurrent bool   `json:"stop_after_current"`
	Session          string `json:"session"`
	TurnIndex        *int   `json:"turn_index"`
	TotalSessions    *int   `json:"total_sessions"`
}

// ControlSection bounds the control records and lists the operation IDs found.
type ControlSection struct {
	Total        int           `json:"total"`
	Exported     int           `json:"exported"`
	ByAction     []ActionTally `json:"by_action"`
	OperationIDs []string      `json:"operation_ids"`
	Records      []Control     `json:"records"`
}

// Receipt is one mailbox record reduced to its identities and its body
// *lengths*. TextChars and NoteChars are what let a reader tell "no reply" from
// "a reply this export will not show you"; the bodies themselves are never
// exported by ASL and have no field here to arrive in.
type Receipt struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Seq       *int   `json:"seq"`
	To        string `json:"to"`
	Action    string `json:"action"`
	ClosureID string `json:"closure_id"`
	RunID     string `json:"run_id"`
	SessionID string `json:"session_id"`
	TurnIndex *int   `json:"turn_index"`
	CreatedAt string `json:"created_at"`
	TextChars int    `json:"text_chars"`
	NoteChars int    `json:"note_chars"`
}

// ReceiptSection bounds the mailbox receipts and lists the closure IDs found.
type ReceiptSection struct {
	Available  bool        `json:"available"`
	Reason     string      `json:"reason"`
	Total      int         `json:"total"`
	Exported   int         `json:"exported"`
	ByKind     []KindTally `json:"by_kind"`
	ClosureIDs []string    `json:"closure_ids"`
	Records    []Receipt   `json:"records"`
}

// EventSection is the run's event log reduced to counts.
type EventSection struct {
	Available bool         `json:"available"`
	Reason    string       `json:"reason"`
	Scanned   int          `json:"scanned"`
	Rejected  int          `json:"rejected"`
	Truncated bool         `json:"truncated"`
	Total     int          `json:"total"`
	ByName    []EventTally `json:"by_name"`
}

// Assurance is the run's terminal audit verdict without any finding body.
type Assurance struct {
	State               string   `json:"state"`
	Eligible            bool     `json:"eligible"`
	Reason              string   `json:"reason"`
	IssueIDs            []string `json:"issue_ids"`
	IssueCount          int      `json:"issue_count"`
	NewIssueCount       int      `json:"new_issue_count"`
	RecurringIssueCount int      `json:"recurring_issue_count"`
	AttemptCount        int      `json:"attempt_count"`
	ArtifactCount       int      `json:"artifact_count"`
	StartedAt           string   `json:"started_at"`
	CompletedAt         string   `json:"completed_at"`
	DurationSeconds     *float64 `json:"duration_seconds"`
}

// Run is one plan's whole correlated record.
type Run struct {
	// PlanDigest is sha256(plan_dir). It is the correlation key that survives a
	// share-mode bundle's path aliasing, because both sides can compute it from
	// the same plan without either publishing the path.
	PlanDigest string `json:"plan_digest"`

	RunID                   string          `json:"run_id"`
	StateSchemaVersion      *int            `json:"state_schema_version"`
	StateAvailable          bool            `json:"state_available"`
	StateReason             string          `json:"state_reason"`
	ProjectionStatus        string          `json:"projection_status"`
	Projection              *Projection     `json:"projection"`
	Phase                   string          `json:"phase"`
	ProjectionGeneration    *int            `json:"projection_generation"`
	LifecycleSourceSequence *int            `json:"lifecycle_source_sequence"`
	Assurance               *Assurance      `json:"assurance"`
	Events                  EventSection    `json:"events"`
	Delivery                DeliverySection `json:"delivery"`
	Control                 ControlSection  `json:"control"`
	Receipts                ReceiptSection  `json:"receipts"`
}

// Limits is the export's published bounds. Carried through so a bundle reader
// can tell a cap that bit from evidence that was never there.
type Limits struct {
	MaxRuns            int `json:"max_runs"`
	MaxDeliveryRecords int `json:"max_delivery_records"`
	MaxControlRecords  int `json:"max_control_records"`
	MaxReceiptRecords  int `json:"max_receipt_records"`
	MaxLogLines        int `json:"max_log_lines"`
	MaxExportBytes     int `json:"max_export_bytes"`
}

// Counts is the export's own tally of what it scanned and emitted.
type Counts struct {
	PlansScanned    int `json:"plans_scanned"`
	RunsMatched     int `json:"runs_matched"`
	RunsExported    int `json:"runs_exported"`
	DeliveryRecords int `json:"delivery_records"`
	ControlRecords  int `json:"control_records"`
	ReceiptRecords  int `json:"receipt_records"`
}

// RedactionPolicyEntry is one standing statement of what the export refuses to
// carry, shipped inside the document so an auditor reading one bundle member
// need not find ASL's source to know what it could not have contained.
type RedactionPolicyEntry struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// Document is one decoded export.
type Document struct {
	SchemaVersion    int                    `json:"schema_version"`
	Kind             string                 `json:"kind"`
	ContractSetID    string                 `json:"contract_set_id"`
	Build            Build                  `json:"build"`
	StateRoot        string                 `json:"state_root"`
	StateRootPresent bool                   `json:"state_root_present"`
	Limits           Limits                 `json:"limits"`
	Counts           Counts                 `json:"counts"`
	RedactionPolicy  []RedactionPolicyEntry `json:"redaction_policy"`
	Truncated        bool                   `json:"truncated"`
	Runs             []Run                  `json:"runs"`
	Omissions        []Omission             `json:"omissions"`

	// Digest is ASL's SHA-256 over the rest of its document. Recorded so a
	// bundle can prove two captures read the same evidence.
	Digest string `json:"digest"`

	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
	Error  string `json:"error"`
}

// Result pairs a document with the reason it may be absent. Document is nil for
// every unavailable outcome; Availability is always populated.
type Result struct {
	Availability Availability
	Document     *Document
	// Bytes is what the child actually wrote, recorded even for a malformed
	// document because "it wrote 3 MB of something" is itself a diagnosis.
	Bytes int
}
